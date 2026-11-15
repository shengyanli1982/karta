package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// 编译期接口检查：确保 retryScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*retryScheduler)(nil)

// 编译期接口检查：确保 retryScheduler 满足根包 Retryer 可选接口
// （Pipeline 的 WithAutoRetry 经该接口自动驱动重试）
var _ karta.Retryer = (*retryScheduler)(nil)

// retryScheduler 将 workqueue.RetryQueue 适配为 karta.Scheduler 接口。
//
// Done 标记任务成功完成并清除重试计数。
// Enqueue 成功时会清除该指针遗留的重试计数：重试计数以指针为键，
// 根包 TaskEnvelope 池复用指针后，旧任务的计数会串到新任务，
// 因此每次新入队都视为新任务生命周期的开始。
// 重试经 karta.Retryer 接口暴露：Pipeline 开启 WithAutoRetry 后自动驱动；
// 外部也可对 karta.Scheduler 做 karta.Retryer 断言手动驱动。
//
// Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费：重试延迟到期项由
// 底层 scheduler 搬运（委托内层 DelayingQueue），等待内层即正确语义。
type retryScheduler struct {
	queue    workqueue.RetryQueue
	blocking workqueue.BlockingGetQueue // 构造时一次性断言，见 fifoScheduler.blocking
	closed   atomic.Bool
	// dlq 为接通的重试死信队列（仅 NewRetrySchedulerWithDLQ 构造，其余为 nil）：
	// 重试耗尽时底层 retryQueue 自动转发死信（workqueue 的
	// WithDeadLetterQueue 原生桥），本字段仅用于 GetDeadLetters 快照读取
	dlq workqueue.DeadLetterQueue
}

// NewRetryScheduler 创建基于 workqueue.RetryQueue 的重试调度器。
// policy 为重试策略，nil 时使用 workqueue 默认的指数退避策略。
func NewRetryScheduler(policy workqueue.RetryPolicy) karta.Scheduler {
	return newRetryScheduler(policy, nil)
}

// NewRetrySchedulerWithDLQ 创建带死信接通的重试调度器：
// 重试耗尽（policy 拒绝继续重试）时底层自动将任务转发至死信队列，
// Retry 返回 workqueue.ErrRetryExhausted。
// 死信通过类型断言 interface{ GetDeadLetters() []*workqueue.DeadLetter }
// 访问（与 dlqScheduler 同模式）。
// policy 为重试策略，nil 时使用 workqueue 默认的指数退避策略。
//
// Payload 稳定性：耗尽任务的 envelope 不归还 karta 对象池（Pipeline
// executor 在 Retry 失败后跳过归还，由死信独占持有该指针），因此
// DeadLetter.Payload 指向的 *karta.TaskEnvelope 字段（Input 等）可稳定
// 检视，不会与池复用任务构成数据竞争；元数据（Attempts/LastError/
// FailedAt/SourceQueue）同样稳定。
//
// 注意：不要用 Composite(Retry→DLQ) 链替代本构造——Composite 的 pump
// 会把所有任务无条件搬进 DLQ，语义错误；本构造使用 workqueue 原生的
// “重试耗尽 → 死信”桥（RetryQueueConfig.WithDeadLetterQueue），仅耗尽任务转发。
func NewRetrySchedulerWithDLQ(policy workqueue.RetryPolicy) karta.Scheduler {
	dlq := workqueue.NewDeadLetterQueue(workqueue.NewDeadLetterQueueConfig())
	return newRetryScheduler(policy, dlq)
}

// newRetryScheduler 共享构造：dlq 非 nil 时接通“重试耗尽 → 死信”自动桥。
func newRetryScheduler(policy workqueue.RetryPolicy, dlq workqueue.DeadLetterQueue) *retryScheduler {
	cfg := workqueue.NewRetryQueueConfig().
		WithKeyFunc(retryTaskKeyFunc)
	if policy != nil {
		cfg = cfg.WithPolicy(policy)
	}
	if dlq != nil {
		cfg = cfg.WithDeadLetterQueue(dlq, "karta-retry")
	}
	q := workqueue.NewRetryQueue(cfg)
	return &retryScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
		dlq:      dlq,
	}
}

func (s *retryScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	// 指针复用防护：重试计数以 envelope 指针为键（见 retryTaskKeyFunc），
	// 根包 TaskEnvelope 池归还并复用指针后，旧任务的计数会串到新任务，
	// 入队即代表该指针开启新的任务生命周期，必须清除遗留计数。
	// Forget 必须先于 Put（P2-2）：Put 是指针的发布点，发布后消费者可能
	// 立刻 Dequeue→handler 失败→Retry 写入新计数，后置的 Forget 会把这条
	// 新计数误清零，任务获得超出策略一次的尝试；Put 之前消费者不可能见到
	// 该指针，先清残留的防护语义等价且竞态窗口归零。
	// （重试耗尽路径无需处理：workqueue.RetryQueue 在 ErrRetryExhausted
	// 时已内部重置计数。）
	s.queue.Forget(task)
	if err := s.queue.Put(task); err != nil {
		if errors.Is(err, workqueue.ErrQueueIsClosed) {
			return karta.ErrSchedulerClosed
		}
		// double-check: 其他错误也可能由并发 Shutdown 导致
		if s.closed.Load() {
			return karta.ErrSchedulerClosed
		}
		return err
	}
	return nil
}

// Dequeue 阻塞获取任务：取到值返回；队列关闭返回 ErrSchedulerClosed
// （底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；ctx 完成原样返回 ctx.Err()。
func (s *retryScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	for {
		val, err := s.blocking.GetWithContext(ctx)
		if err != nil {
			if errors.Is(err, workqueue.ErrQueueIsClosed) {
				return nil, karta.ErrSchedulerClosed
			}
			return nil, err
		}
		if env, ok := val.(*karta.TaskEnvelope); ok {
			return env, nil
		}
		// 类型不匹配时继续出队（不应在正常使用中出现）
	}
}

// Done 标记任务成功完成，清除重试计数。
func (s *retryScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Forget(task)
	s.queue.Done(task)
}

func (s *retryScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *retryScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *retryScheduler) IsClosed() bool {
	return s.closed.Load()
}

// Retry 将任务标记为失败并按策略重新入队（karta.Retryer 接口方法）。
// Pipeline 开启 karta.WithAutoRetry 后由 executor 自动驱动；外部也可
// 经 karta.Retryer 断言手动调用：
//
//	if r, ok := sched.(karta.Retryer); ok {
//	    r.Retry(task, reason)
//	}
func (s *retryScheduler) Retry(task *karta.TaskEnvelope, reason error) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	return s.queue.Retry(task, reason)
}

// NumRequeues 返回任务已被重试的次数（karta.Retryer 接口方法）。
func (s *retryScheduler) NumRequeues(task *karta.TaskEnvelope) int {
	return s.queue.NumRequeues(task)
}

// GetDeadLetters 返回接通死信队列中所有死信的快照。
// 未经 NewRetrySchedulerWithDLQ 构造（未接通死信）时返回 nil。
// 此方法不在 karta.Scheduler 接口中，需通过类型断言调用（与 dlqScheduler 同模式）。
// 快照中 DeadLetter.Payload 的 *karta.TaskEnvelope 由死信独占持有（耗尽后
// 不归还对象池），其字段（Input 等）与元数据（Attempts/LastError/FailedAt/
// SourceQueue）均可稳定检视（见 NewRetrySchedulerWithDLQ 的 Payload 稳定性说明）。
func (s *retryScheduler) GetDeadLetters() []*workqueue.DeadLetter {
	if s.dlq == nil {
		return nil
	}
	var letters []*workqueue.DeadLetter
	s.dlq.RangeDead(func(letter *workqueue.DeadLetter) bool {
		letters = append(letters, letter)
		return true
	})
	return letters
}

// retryTaskKeyFunc 使用指针地址作为重试 key，确保同一 *TaskEnvelope 的 key 稳定。
var retryTaskKeyFunc workqueue.RetryKeyFunc = func(value interface{}) string {
	return fmt.Sprintf("%p", value)
}
