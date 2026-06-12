package middleware

import (
	"context"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/retry"
)

// RetryOption 配置 Retry middleware
type RetryOption func(*retryConfig)

type retryConfig struct {
	attempts uint64
	delay    time.Duration
	retryIf  func(error) bool
	onRetry  func(attempt int, err error)
}

// WithAttempts 设置最大尝试次数（含首次执行）
func WithAttempts(n int) RetryOption {
	return func(c *retryConfig) { c.attempts = uint64(n) }
}

// WithDelay 设置重试间隔时间
func WithDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) { c.delay = d }
}

// WithRetryIf 设置重试条件判断函数，返回 false 时停止重试
func WithRetryIf(fn func(error) bool) RetryOption {
	return func(c *retryConfig) { c.retryIf = fn }
}

// WithOnRetry 设置每次重试时的回调函数（在重试前调用）
func WithOnRetry(fn func(attempt int, err error)) RetryOption {
	return func(c *retryConfig) { c.onRetry = fn }
}

// retryCallback 适配 retry.Callback 接口到简化签名
type retryCallback struct {
	fn func(attempt int, err error)
}

func (cb *retryCallback) OnRetry(count int64, _ time.Duration, err error) {
	cb.fn(int(count), err)
}

// zeroBackoff 零退避函数，使实际退避间隔仅由 InitDelay 决定
func zeroBackoff(_ int64) time.Duration { return 0 }

// Retry 重试中间件，handler 失败时按配置自动重试
// 内部使用闭包捕获 handler + input，调用 retry.Do，类型断言回 Out
func Retry[In, Out any](opts ...RetryOption) karta.Middleware[In, Out] {
	cfg := &retryConfig{
		attempts: 3,
		delay:    100 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			rc := retry.NewConfig().
				WithAttempts(cfg.attempts).
				WithInitDelay(cfg.delay).
				WithBackOffFunc(zeroBackoff).
				WithJitter(0).
				WithFactor(0).
				WithContext(ctx)

			if cfg.retryIf != nil {
				rc.WithRetryIfFunc(cfg.retryIf)
			}
			if cfg.onRetry != nil {
				rc.WithCallback(&retryCallback{fn: cfg.onRetry})
			}

			var result Out
			var lastErr error

			res := retry.Do(func() (any, error) {
				var err error
				result, err = next(ctx, input)
				lastErr = err
				return result, err
			}, rc)

			if res.IsSuccess() {
				return result, nil
			}
			// 优先返回 handler 的实际错误，而非 retry 库的包装错误
			if lastErr != nil {
				return result, lastErr
			}
			return result, res.TryError()
		}
	}
}
