package middleware

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	karta "github.com/shengyanli1982/karta/v2"
)

// MetricsOption 配置 Metrics 中间件
type MetricsOption func(*metricsConfig)

type metricsConfig struct {
	namespace  string
	subsystem  string
	labels     prometheus.Labels
	registerer prometheus.Registerer
}

// WithNamespace 设置 Prometheus 指标命名空间（默认 "karta"）
func WithNamespace(ns string) MetricsOption {
	return func(c *metricsConfig) { c.namespace = ns }
}

// WithSubsystem 设置 Prometheus 指标子系统（默认 "handler"）
func WithSubsystem(ss string) MetricsOption {
	return func(c *metricsConfig) { c.subsystem = ss }
}

// WithLabels 为所有指标添加常量标签
func WithLabels(labels map[string]string) MetricsOption {
	return func(c *metricsConfig) { c.labels = prometheus.Labels(labels) }
}

// WithRegisterer 设置指标注册器（默认 prometheus.DefaultRegisterer）
func WithRegisterer(r prometheus.Registerer) MetricsOption {
	return func(c *metricsConfig) { c.registerer = r }
}

// registerOrReuse 注册 collector，并在重复注册时复用已注册的实例。
// 当相同配置的 collector 已注册（prometheus.AlreadyRegisteredError）时，
// 返回注册器中已存在的实例，保证多个相同配置的中间件写入同一份指标，
// 避免第二个实例持有未注册的 collector 导致指标静默丢失；
// 复用时的类型不匹配或其他注册错误属于不可恢复的配置错误，直接 panic（fail-fast）。
func registerOrReuse[C prometheus.Collector](r prometheus.Registerer, c C) C {
	if err := r.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			existing, ok := are.ExistingCollector.(C)
			if !ok {
				panic(fmt.Sprintf("karta middleware: already-registered collector type mismatch: want %T, got %T", c, are.ExistingCollector))
			}
			return existing
		}
		panic(fmt.Sprintf("karta middleware: failed to register prometheus collector: %v", err))
	}
	return c
}

// Metrics 指标采集中间件
// 采集: handler 执行耗时 histogram, 成功/失败计数 counter
// 相同配置的中间件重复构建时，复用已注册的 collector，指标在共享实例上累加
func Metrics[In, Out any](opts ...MetricsOption) karta.Middleware[In, Out] {
	cfg := &metricsConfig{
		namespace:  "karta",
		subsystem:  "handler",
		registerer: prometheus.DefaultRegisterer,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	duration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace:   cfg.namespace,
		Subsystem:   cfg.subsystem,
		Name:        "execution_duration_seconds",
		Help:        "Handler execution duration in seconds",
		Buckets:     prometheus.DefBuckets,
		ConstLabels: cfg.labels,
	})

	total := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   cfg.namespace,
		Subsystem:   cfg.subsystem,
		Name:        "total",
		Help:        "Total number of handler executions",
		ConstLabels: cfg.labels,
	}, []string{"status"})

	errCount := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace:   cfg.namespace,
		Subsystem:   cfg.subsystem,
		Name:        "errors_total",
		Help:        "Total number of handler errors",
		ConstLabels: cfg.labels,
	})

	// 已注册的 collector 会被复用，注册失败等不可恢复错误直接 panic
	duration = registerOrReuse(cfg.registerer, duration)
	total = registerOrReuse(cfg.registerer, total)
	errCount = registerOrReuse(cfg.registerer, errCount)

	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			start := time.Now()
			out, err := next(ctx, input)
			elapsed := time.Since(start)

			duration.Observe(elapsed.Seconds())
			if err != nil {
				total.WithLabelValues("error").Inc()
				errCount.Inc()
			} else {
				total.WithLabelValues("success").Inc()
			}
			return out, err
		}
	}
}
