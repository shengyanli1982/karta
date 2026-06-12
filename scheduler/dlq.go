package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// 编译期接口检查：确保 dlqScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*dlqScheduler)(nil)

// dlqScheduler 将 workqueue.DeadLetterQueue 适配为 karta.Scheduler 接口。
//
// 入队时将 TaskEnvelope 包装为 DeadLetter；出队时从 DeadLetter 中提取原始任务。
// maxRetries 记录死信最大重试次数，作为元数据存储但不影响调度行为。
type dlqScheduler struct {
	queue      workqueue.DeadLetterQueue
	closed     atomic.Bool
	pending    sync.Map // map[*karta.TaskEnvelope]*workqueue.DeadLetter
	notify     chan struct{}
	doneCh     chan struct{}
	maxRetries int
}

// NewDLQScheduler 创建基于 workqueue.DeadLetterQueue 的死信调度器。
// maxRetries 指定允许的最大重试次数（元数据，供外部参考）。
func NewDLQScheduler(maxRetries int) karta.Scheduler {
	return &dlqScheduler{
		queue:      workqueue.NewDeadLetterQueue(workqueue.NewDeadLetterQueueConfig()),
		notify:     make(chan struct{}, notifyChanCapacity()),
		doneCh:     make(chan struct{}),
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
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *dlqScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	backoff := minBackoff
	timer := time.NewTimer(backoff)
	defer stopAndDrainTimer(timer)

	for {
		letter, err := s.queue.GetDead()
		if err == nil && letter != nil {
			if env, ok := letter.Payload.(*karta.TaskEnvelope); ok {
				// 记录 DeadLetter → TaskEnvelope 映射，供 Done 使用
				s.pending.Store(env, letter)
				return env, nil
			}
			continue
		}
		if s.closed.Load() {
			return nil, karta.ErrSchedulerClosed
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.notify:
			backoff = minBackoff
		case <-s.doneCh:
			return nil, karta.ErrSchedulerClosed
		case <-timer.C:
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		stopAndDrainTimer(timer)
		timer.Reset(backoff)
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

func (s *dlqScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
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
