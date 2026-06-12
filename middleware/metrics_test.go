package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findMetricFamily 从 Gather 结果中按名称查找 MetricFamily
func findMetricFamily(t *testing.T, reg *prometheus.Registry, name string) (uint64, float64) {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			m := mf.GetMetric()[0]
			if h := m.GetHistogram(); h != nil {
				return h.GetSampleCount(), h.GetSampleSum()
			}
			if c := m.GetCounter(); c != nil {
				return 1, c.GetValue()
			}
		}
	}
	t.Fatalf("metric family %q not found", name)
	return 0, 0
}

// getCounterVecValue 从 Gather 结果中获取 CounterVec 特定 label 的值
func getCounterVecValue(t *testing.T, reg *prometheus.Registry, name string, labelValue string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "status" && l.GetValue() == labelValue {
						return m.GetCounter().GetValue()
					}
				}
			}
		}
	}
	return 0
}

// TestMetrics_Success 成功执行 3 次, total[success]=3, errors_total=0
func TestMetrics_Success(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Metrics[int, int](
		WithRegisterer(reg),
		WithNamespace("test"),
		WithSubsystem("h"),
	)

	handler := func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	}

	wrapped := mw(handler)

	for i := 0; i < 3; i++ {
		result, err := wrapped(context.Background(), i)
		require.NoError(t, err)
		assert.Equal(t, i*2, result)
	}

	// total[success] = 3
	assert.Equal(t, float64(3), getCounterVecValue(t, reg, "test_h_total", "success"))
	// total[error] = 0 (未调用过)
	assert.Equal(t, float64(0), getCounterVecValue(t, reg, "test_h_total", "error"))
	// errors_total = 0
	_, errVal := findMetricFamily(t, reg, "test_h_errors_total")
	assert.Equal(t, float64(0), errVal)
}

// TestMetrics_Error 失败执行, errors_total=1, total[error]=1
func TestMetrics_Error(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Metrics[string, string](
		WithRegisterer(reg),
		WithNamespace("test"),
		WithSubsystem("err"),
	)

	expectedErr := errors.New("handler failure")
	handler := func(ctx context.Context, input string) (string, error) {
		return "", expectedErr
	}

	wrapped := mw(handler)

	result, err := wrapped(context.Background(), "hello")
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, result)

	// total[error] = 1
	assert.Equal(t, float64(1), getCounterVecValue(t, reg, "test_err_total", "error"))
	// total[success] = 0
	assert.Equal(t, float64(0), getCounterVecValue(t, reg, "test_err_total", "success"))
	// errors_total = 1
	_, errVal := findMetricFamily(t, reg, "test_err_errors_total")
	assert.Equal(t, float64(1), errVal)
}

// TestMetrics_Duration 验证 histogram 记录正确的耗时（>0）
func TestMetrics_Duration(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Metrics[int, int](
		WithRegisterer(reg),
		WithNamespace("test"),
		WithSubsystem("dur"),
	)

	handler := func(ctx context.Context, input int) (int, error) {
		time.Sleep(15 * time.Millisecond)
		return input + 1, nil
	}

	wrapped := mw(handler)

	_, err := wrapped(context.Background(), 41)
	require.NoError(t, err)

	// 验证 histogram 记录了 1 次观察
	count, sum := findMetricFamily(t, reg, "test_dur_execution_duration_seconds")
	assert.Equal(t, uint64(1), count, "histogram should have 1 observation")
	assert.Greater(t, sum, 0.01, "duration sum should be > 10ms")
}

// TestMetrics_Transparent 输入输出正确传递，不被破坏
func TestMetrics_Transparent(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Metrics[int, string](
		WithRegisterer(reg),
		WithNamespace("test"),
		WithSubsystem("tr"),
	)

	handler := func(ctx context.Context, input int) (string, error) {
		return "result-" + string(rune('0'+input)), nil
	}

	wrapped := mw(handler)

	result, err := wrapped(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, "result-7", result)
}

// TestMetrics_MixedCalls 混合成功和失败调用
func TestMetrics_MixedCalls(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Metrics[int, int](
		WithRegisterer(reg),
		WithNamespace("test"),
		WithSubsystem("mix"),
	)

	handler := func(ctx context.Context, input int) (int, error) {
		if input%2 == 0 {
			return input, nil
		}
		return 0, errors.New("odd")
	}

	wrapped := mw(handler)

	// 5 次调用: 0(success), 1(error), 2(success), 3(error), 4(success)
	for i := 0; i < 5; i++ {
		_, _ = wrapped(context.Background(), i)
	}

	assert.Equal(t, float64(3), getCounterVecValue(t, reg, "test_mix_total", "success"))
	assert.Equal(t, float64(2), getCounterVecValue(t, reg, "test_mix_total", "error"))
	_, errVal := findMetricFamily(t, reg, "test_mix_errors_total")
	assert.Equal(t, float64(2), errVal)

	// histogram 记录了 5 次观察
	count, _ := findMetricFamily(t, reg, "test_mix_execution_duration_seconds")
	assert.Equal(t, uint64(5), count)
}

// TestMetrics_WithLabels 验证 ConstLabels 正确附加
func TestMetrics_WithLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Metrics[int, int](
		WithRegisterer(reg),
		WithNamespace("test"),
		WithSubsystem("lbl"),
		WithLabels(map[string]string{"env": "test"}),
	)

	handler := func(ctx context.Context, input int) (int, error) {
		return input, nil
	}

	wrapped := mw(handler)
	_, err := wrapped(context.Background(), 1)
	require.NoError(t, err)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	// 验证所有指标都包含 env=test 标签
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			found := false
			for _, l := range m.GetLabel() {
				if l.GetName() == "env" && l.GetValue() == "test" {
					found = true
					break
				}
			}
			assert.True(t, found, "metric %s should have env=test label", mf.GetName())
		}
	}
}
