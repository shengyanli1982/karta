package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogging_Success 验证 handler 成功时日志有 Info 记录
func TestLogging_Success(t *testing.T) {
	var buf bytes.Buffer
	logger := funcr.New(func(prefix, args string) {
		fmt.Fprintln(&buf, prefix, args)
	}, funcr.Options{})

	handler := func(ctx context.Context, input string) (string, error) {
		return input + "-done", nil
	}

	logging := Logging[string, string](logger)
	wrapped := logging(handler)

	result, err := wrapped(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello-done", result)

	logOut := buf.String()
	t.Logf("log output: %s", logOut)
	assert.Contains(t, logOut, `"level"=0`,
		"expected Info level log (funcr Info records carry level=0), got: %s", logOut)
	assert.Contains(t, logOut, "handler completed")
}

// TestLogging_Error 验证 handler 返回 error 时日志有 Error 记录
func TestLogging_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := funcr.New(func(prefix, args string) {
		fmt.Fprintln(&buf, prefix, args)
	}, funcr.Options{})
	expectedErr := errors.New("handler failure")

	handler := func(ctx context.Context, input string) (string, error) {
		return "", expectedErr
	}

	logging := Logging[string, string](logger)
	wrapped := logging(handler)

	result, err := wrapped(context.Background(), "hello")
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Empty(t, result)

	logOut := buf.String()
	t.Logf("log output: %s", logOut)
	assert.Contains(t, logOut, `"error"="handler failure"`,
		"expected Error level log (funcr Error records carry the error key), got: %s", logOut)
	assert.Contains(t, logOut, "handler failed")
}

// TestLogging_Transparent 验证输入输出正确传递
func TestLogging_Transparent(t *testing.T) {
	var buf bytes.Buffer
	logger := funcr.New(func(prefix, args string) {
		fmt.Fprintln(&buf, prefix, args)
	}, funcr.Options{})

	handler := func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	}

	logging := Logging[int, int](logger)
	wrapped := logging(handler)

	result, err := wrapped(context.Background(), 21)
	require.NoError(t, err)
	assert.Equal(t, 42, result)

	// 验证日志中记录了 input 和 output
	logOut := buf.String()
	assert.Contains(t, logOut, "21")
	assert.Contains(t, logOut, "42")
}

// TestLogging_ZeroLogger_Panics — P2-7：零值 logger 必须构造期 fail-fast。
// 旧行为：构造成功，每个任务执行时才 nil 解引用 panic 被 executor recover
// 吞为晦涩的任务错误，配置错误被延迟到运行期。
// 注：上述为 *slog.Logger 时代的因果链；logr 零值 Logger（sink==nil）下
// Info/Error 为静默 no-op，日志被无声丢弃而非 panic，配置错误同样直到
// 运行期都无从发现。
func TestLogging_ZeroLogger_Panics(t *testing.T) {
	assert.PanicsWithValue(t, "karta middleware: logger must not be the zero value",
		func() { Logging[int, int](logr.Logger{}) },
		"零值 logger 应构造期 panic（fail-fast）")
}
