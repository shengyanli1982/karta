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

// retryScheduler 将 workqueue.RetryQueue 适配为 karta.Scheduler 接口。
//
// Done 标记任务成功完成并清除重试计数。
// Enqueue 成功时会清除该指针遗留的重试计数：重试计数以指针为键，
// 根包 TaskEnvelope 池复用指针后，旧任务的计数会串到新任务，
// 因此每次新入队都视为新任务生命周期的开始。
// 如需重试，通过匿名接口断言访问 Retry 方法（retryScheduler 未导出，
// 具体类型断言对外部包不可行；断言写法见 Retry 方法的文档示例）。
//
// Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费：重试延迟到期项由
// 底层 scheduler 搬运（委托内层 DelayingQueue），等待内层即正确语义。
type retryScheduler struct {
	queue    workqueue.RetryQueue
	blocking workqueue.BlockingGetQueue // 构造时一次性断言，见 fifoScheduler.blocking
	closed   atomic.Bool
}

// NewRetryScheduler 创建基于 workqueue.RetryQueue 的重试调度器。
// policy 为重试策略，nil 时使用 workqueue 默认的指数退避策略。
func NewRetryScheduler(policy workqueue.RetryPolicy) karta.Scheduler {
	cfg := workqueue.NewRetryQueueConfig().
		WithKeyFunc(retryTaskKeyFunc)
	if policy != nil {
		cfg = cfg.WithPolicy(policy)
	}
	q := workqueue.NewRetryQueue(cfg)
	return &retryScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
	}
}

func (s *retryScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
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
	// 指针复用防护：重试计数以 envelope 指针为键（见 retryTaskKeyFunc），
	// 根包 TaskEnvelope 池归还并复用指针后，旧任务的计数会串到新任务。
	// 入队成功即代表该指针开启新的任务生命周期，必须在此清除遗留计数，
	// 保证记录在指针归还 pool 前已清零。
	// （重试耗尽路径无需处理：workqueue.RetryQueue 在 ErrRetryExhausted
	// 时已内部重置计数。）
	s.queue.Forget(task)
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

// Retry 将任务标记为失败并按策略重新入队。
// 此方法不在 karta.Scheduler 接口中，需通过类型断言调用：
//
//	if rs, ok := sched.(interface {
//		Retry(task *karta.TaskEnvelope, reason error) error
//	}); ok {
//	    rs.Retry(task, reason)
//	}
func (s *retryScheduler) Retry(task *karta.TaskEnvelope, reason error) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	return s.queue.Retry(task, reason)
}

// NumRequeues 返回任务已被重试的次数。
func (s *retryScheduler) NumRequeues(task *karta.TaskEnvelope) int {
	return s.queue.NumRequeues(task)
}

// retryTaskKeyFunc 使用指针地址作为重试 key，确保同一 *TaskEnvelope 的 key 稳定。
var retryTaskKeyFunc workqueue.RetryKeyFunc = func(value interface{}) string {
	return fmt.Sprintf("%p", value)
}
