package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestExporter 创建用于测试的 InMemoryExporter 和 cleanup 函数。
// 使用 WithSyncer 确保 span 写入 exporter 立即可见。
func newTestExporter(t *testing.T) (*tracetest.InMemoryExporter, func()) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	cleanup := func() {
		_ = provider.Shutdown(context.Background())
	}
	return exporter, cleanup
}

// TestTracing_SpanCreated 成功执行，验证 span 被创建，有 handler.input/output 属性。
func TestTracing_SpanCreated(t *testing.T) {
	exporter, cleanup := newTestExporter(t)
	defer cleanup()

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	tracer := provider.Tracer("test")

	mw := Tracing[int, int](tracer, WithSpanName("my.handler"))
	handler := mw(func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})

	result, err := handler(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, 10, result)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "my.handler", spans[0].Name)

	// 验证 handler.input 和 handler.output 属性
	inputFound, outputFound := false, false
	for _, attr := range spans[0].Attributes {
		switch string(attr.Key) {
		case "handler.input":
			assert.Equal(t, "5", attr.Value.AsString())
			inputFound = true
		case "handler.output":
			assert.Equal(t, "10", attr.Value.AsString())
			outputFound = true
		}
	}
	assert.True(t, inputFound, "handler.input attribute not found")
	assert.True(t, outputFound, "handler.output attribute not found")
}

// TestTracing_Error handler 返回 error，span status=Error 且 RecordError 被调用。
func TestTracing_Error(t *testing.T) {
	exporter, cleanup := newTestExporter(t)
	defer cleanup()

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	tracer := provider.Tracer("test")

	expectedErr := errors.New("something went wrong")
	mw := Tracing[string, string](tracer, WithSpanName("error.handler"))
	handler := mw(func(ctx context.Context, input string) (string, error) {
		return "", expectedErr
	})

	result, err := handler(context.Background(), "input")
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, result)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "error.handler", spans[0].Name)

	// 验证 span 状态为 Error
	assert.Equal(t, codes.Error, spans[0].Status.Code)
	assert.Equal(t, expectedErr.Error(), spans[0].Status.Description)

	// 验证 RecordError 产生了 event
	require.NotEmpty(t, spans[0].Events, "expected at least one event from RecordError")

	// 验证 handler.input 属性仍然存在
	inputFound := false
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "handler.input" {
			assert.Equal(t, "input", attr.Value.AsString())
			inputFound = true
		}
	}
	assert.True(t, inputFound, "handler.input attribute not found")
}

// TestTracing_Transparent 输入输出正确透传，不改变业务逻辑。
func TestTracing_Transparent(t *testing.T) {
	exporter, cleanup := newTestExporter(t)
	defer cleanup()

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	tracer := provider.Tracer("test")

	called := false
	mw := Tracing[int, string](tracer)
	handler := mw(func(ctx context.Context, input int) (string, error) {
		called = true
		return "result-" + string(rune('0'+input)), nil
	})

	result, err := handler(context.Background(), 7)
	require.NoError(t, err)
	assert.True(t, called, "handler should have been called")
	assert.Equal(t, "result-7", result)

	// 确保 span 被创建
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
}

// TestTracing_DefaultSpanName 不设置 WithSpanName 时默认 span 名称为 "karta.handler"。
func TestTracing_DefaultSpanName(t *testing.T) {
	exporter, cleanup := newTestExporter(t)
	defer cleanup()

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	tracer := provider.Tracer("test")

	mw := Tracing[int, int](tracer) // 不传 WithSpanName
	handler := mw(func(ctx context.Context, input int) (int, error) {
		return input, nil
	})

	_, err := handler(context.Background(), 1)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, defaultSpanName, spans[0].Name)
}
