package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// 编译期接口检查：确保 dlqScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*dlqScheduler)(nil)

// dlqScheduler 将 workqueue.DeadLetterQueue 适配为 karta.Scheduler 接口。
//
// 入队时将 TaskEnvelope 包装为 DeadLetter；出队时从 DeadLetter 中提取原始任务。
// maxRetries 记录死信最大重试次数，作为元数据存储但不影响调度行为。
//
// Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费（委托内层队列，
// 结果还原为 *DeadLetter，与 GetDead 一致）。
type dlqScheduler struct {
	queue workqueue.DeadLetterQueue
	// blocking 构造时一次性断言。deadLetterQueueImpl.GetWithContext 返回的
	// any 动态类型为 *workqueue.DeadLetter（非 *TaskEnvelope）。
	blocking   workqueue.BlockingGetQueue
	closed     atomic.Bool
	pending    sync.Map // map[*karta.TaskEnvelope]*workqueue.DeadLetter
	maxRetries int
}

// NewDLQScheduler 创建基于 workqueue.DeadLetterQueue 的死信调度器。
// maxRetries 指定允许的最大重试次数（元数据，供外部参考）。
func NewDLQScheduler(maxRetries int) karta.Scheduler {
	q := workqueue.NewDeadLetterQueue(workqueue.NewDeadLetterQueueConfig())
	return &dlqScheduler{
		queue:      q,
		blocking:   q.(workqueue.BlockingGetQueue),
		maxRetries: maxRetries,
	}
}

func (s *dlqScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}

	letter := &workqueue.DeadLetter{
		Payload:     task,
		SourceQueue: "dlq-scheduler",
	}

	if err := s.queue.PutDead(letter); err != nil {
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

// Dequeue 阻塞获取任务：取到死信并解出 TaskEnvelope 返回；队列关闭返回
// ErrSchedulerClosed（底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；
// ctx 完成原样返回 ctx.Err()。
func (s *dlqScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	for {
		val, err := s.blocking.GetWithContext(ctx)
		if err != nil {
			if errors.Is(err, workqueue.ErrQueueIsClosed) {
				return nil, karta.ErrSchedulerClosed
			}
			return nil, err
		}
		letter, ok := val.(*workqueue.DeadLetter)
		if !ok || letter == nil {
			// 类型不匹配时继续出队（不应在正常使用中出现）
			continue
		}
		if env, ok := letter.Payload.(*karta.TaskEnvelope); ok {
			// 记录 DeadLetter → TaskEnvelope 映射，供 Done 使用
			s.pending.Store(env, letter)
			return env, nil
		}
	}
}

// Done 确认死信已处理完成。
func (s *dlqScheduler) Done(task *karta.TaskEnvelope) {
	if val, ok := s.pending.LoadAndDelete(task); ok {
		letter := val.(*workqueue.DeadLetter)
		_ = s.queue.AckDead(letter)
	}
}

func (s *dlqScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *dlqScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *dlqScheduler) IsClosed() bool {
	return s.closed.Load()
}

// GetDeadLetters 返回当前死信队列中所有死信的快照。
// 此方法不在 karta.Scheduler 接口中，需通过类型断言调用。
func (s *dlqScheduler) GetDeadLetters() []*workqueue.DeadLetter {
	var letters []*workqueue.DeadLetter
	s.queue.RangeDead(func(letter *workqueue.DeadLetter) bool {
		letters = append(letters, letter)
		return true
	})
	return letters
}
