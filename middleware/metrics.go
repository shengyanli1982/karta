package middleware

import (
	"context"
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

// Metrics 指标采集中间件
// 采集: handler 执行耗时 histogram, 成功/失败计数 counter
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

	errors := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace:   cfg.namespace,
		Subsystem:   cfg.subsystem,
		Name:        "errors_total",
		Help:        "Total number of handler errors",
		ConstLabels: cfg.labels,
	})

	// 使用 Register 而非 MustRegister，忽略已注册错误（测试中可能重复创建）
	_ = cfg.registerer.Register(duration)
	_ = cfg.registerer.Register(total)
	_ = cfg.registerer.Register(errors)

	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (Out, error) {
			start := time.Now()
			out, err := next(ctx, input)
			elapsed := time.Since(start)

			duration.Observe(elapsed.Seconds())
			if err != nil {
				total.WithLabelValues("error").Inc()
				errors.Inc()
			} else {
				total.WithLabelValues("success").Inc()
			}
			return out, err
		}
	}
}
