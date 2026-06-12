package scheduler

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// Dequeue 退避参数（包级别共享，供所有调度器使用）
const (
	minBackoff = 10 * time.Millisecond
	maxBackoff = 500 * time.Millisecond
)

// stopAndDrainTimer 安全地停止 timer 并排空其 channel，
// 使得后续的 timer.Reset 不会发生竞态。
// 适用于所有 time.Timer（无论是否已触发）。
func stopAndDrainTimer(timer *time.Timer) {
	if !timer.Stop() {
		// timer 已触发：排空 channel（若非 select 已消费则为空）
		select {
		case <-timer.C:
		default:
		}
	}
}

// notifyChanCapacity 根据 CPU 数量计算 notify channel 容量，
// 避免高吞吐下非阻塞发送丢通知；下限为 4。
func notifyChanCapacity() int {
	cap := runtime.NumCPU() * 2
	if cap < 4 {
		cap = 4
	}
	return cap
}

// fifoScheduler 将 workqueue.Queue 适配为 karta.Scheduler 接口。
type fifoScheduler struct {
	queue  workqueue.Queue
	closed atomic.Bool
	notify chan struct{}
	doneCh chan struct{}
}

// NewFIFOScheduler 创建基于 workqueue.Queue 的 FIFO 调度器。
func NewFIFOScheduler() karta.Scheduler {
	return &fifoScheduler{
		queue:  workqueue.NewQueue(workqueue.NewQueueConfig()),
		notify: make(chan struct{}, notifyChanCapacity()),
		doneCh: make(chan struct{}),
	}
}

func (s *fifoScheduler) Enqueue(task *karta.TaskEnvelope) error {
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
	// 非阻塞通知：唤醒可能正在等待的 Dequeue
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *fifoScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	backoff := minBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	for {
		val, err := s.queue.Get()
		if err == nil {
			if env, ok := val.(*karta.TaskEnvelope); ok {
				return env, nil
			}
			continue
		}
		// 队列空或已关闭
		if s.closed.Load() {
			return nil, karta.ErrSchedulerClosed
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.notify:
			// 收到入队通知，重置退避以尽快取到任务
			backoff = minBackoff
		case <-s.doneCh:
			return nil, karta.ErrSchedulerClosed
		case <-timer.C:
			// timer 触发，指数退避（channel 已在 select 中被消费）
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		// 安全重设：先 Stop+drain（处理所有分支），再 Reset
		stopAndDrainTimer(timer)
		timer.Reset(backoff)
	}
}

func (s *fifoScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *fifoScheduler) Len() int {
	return s.queue.Len()
}

func (s *fifoScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
	}
}

func (s *fifoScheduler) IsClosed() bool {
	return s.closed.Load()
}
