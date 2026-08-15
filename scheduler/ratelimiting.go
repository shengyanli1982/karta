package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
	"golang.org/x/time/rate"
)

// rateLimiterAdapter 将 golang.org/x/time/rate.Limiter 适配为 workqueue.Limiter 接口。
type rateLimiterAdapter struct {
	limiter *rate.Limiter
}

func (a *rateLimiterAdapter) When(interface{}) time.Duration {
	return a.limiter.Reserve().Delay()
}

// 编译期接口检查：确保 rateLimitingScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*rateLimitingScheduler)(nil)

// rateLimitingScheduler 将 workqueue.RateLimitingQueue 适配为 karta.Scheduler 接口。
type rateLimitingScheduler struct {
	queue  workqueue.RateLimitingQueue
	closed atomic.Bool
	notify chan struct{}
	doneCh chan struct{}
}

// NewRateLimitingScheduler 创建基于 workqueue.RateLimitingQueue 的限流调度器。
// limiter 为 nil 时使用无等待限流器（NopRateLimiter）。
func NewRateLimitingScheduler(limiter *rate.Limiter) karta.Scheduler {
	cfg := workqueue.NewRateLimitingQueueConfig()
	if limiter != nil {
		cfg.WithLimiter(&rateLimiterAdapter{limiter: limiter})
	}
	return &rateLimitingScheduler{
		queue:  workqueue.NewRateLimitingQueue(cfg),
		notify: make(chan struct{}, notifyChanCapacity()),
		doneCh: make(chan struct{}),
	}
}

func (s *rateLimitingScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	if err := s.queue.PutWithLimited(task); err != nil {
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

func (s *rateLimitingScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
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
		// Go 1.23+ 保证 Reset 返回后不会收到旧定时值，直接重设即可
		timer.Reset(backoff)
	}
}

func (s *rateLimitingScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *rateLimitingScheduler) Len() int {
	return s.queue.Len()
}

func (s *rateLimitingScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
		close(s.doneCh)
	}
}

func (s *rateLimitingScheduler) IsClosed() bool {
	return s.closed.Load()
}
