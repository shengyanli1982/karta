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
//
// Dequeue 基于 BlockingGetQueue.GetWithContext 阻塞消费：限流延迟到期项由
// 底层 scheduler 搬运（委托内层 DelayingQueue），等待内层即正确语义。
type rateLimitingScheduler struct {
	queue    workqueue.RateLimitingQueue
	blocking workqueue.BlockingGetQueue // 构造时一次性断言，见 fifoScheduler.blocking
	closed   atomic.Bool
}

// NewRateLimitingScheduler 创建基于 workqueue.RateLimitingQueue 的限流调度器。
// limiter 为 nil 时使用无等待限流器（NopRateLimiter）。
func NewRateLimitingScheduler(limiter *rate.Limiter) karta.Scheduler {
	cfg := workqueue.NewRateLimitingQueueConfig()
	if limiter != nil {
		cfg.WithLimiter(&rateLimiterAdapter{limiter: limiter})
	}
	q := workqueue.NewRateLimitingQueue(cfg)
	return &rateLimitingScheduler{
		queue:    q,
		blocking: q.(workqueue.BlockingGetQueue),
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
	return nil
}

// Dequeue 阻塞获取任务：取到值返回；队列关闭返回 ErrSchedulerClosed
// （底层队列仅经 Shutdown 关闭，关闭前 closed 已置位）；ctx 完成原样返回 ctx.Err()。
func (s *rateLimitingScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
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

func (s *rateLimitingScheduler) Done(task *karta.TaskEnvelope) {
	s.queue.Done(task)
}

func (s *rateLimitingScheduler) Len() int {
	return s.queue.Len()
}

// Shutdown 关闭调度器，幂等。底层队列 Shutdown 会唤醒全部阻塞在
// GetWithContext 上的消费者（返回 ErrQueueIsClosed）。
func (s *rateLimitingScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		s.queue.Shutdown()
	}
}

func (s *rateLimitingScheduler) IsClosed() bool {
	return s.closed.Load()
}
