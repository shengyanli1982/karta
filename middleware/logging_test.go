package middleware

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogging_Success 验证 handler 成功时日志有 Info 记录
func TestLogging_Success(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

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
	assert.True(t, strings.Contains(logOut, "INFO") || strings.Contains(logOut, "info"),
		"expected Info level log, got: %s", logOut)
	assert.Contains(t, logOut, "handler completed")
}

// TestLogging_Error 验证 handler 返回 error 时日志有 Error 记录
func TestLogging_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
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
	assert.True(t, strings.Contains(logOut, "ERROR") || strings.Contains(logOut, "error"),
		"expected Error level log, got: %s", logOut)
	assert.Contains(t, logOut, "handler failed")
}

// TestLogging_Transparent 验证输入输出正确传递
func TestLogging_Transparent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

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
