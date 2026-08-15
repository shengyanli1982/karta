//go:build !windows

package karta

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLifecycleManager_WithSignals — POSIX 信号配置（SIGUSR1/SIGUSR2 在 Windows 不存在，
// 故本测试仅在非 Windows 平台编译运行）
func TestLifecycleManager_WithSignals(t *testing.T) {
	lm := NewLifecycleManager(WithSignals(syscall.SIGUSR1, syscall.SIGUSR2))

	require.Len(t, lm.signals, 2)
	assert.Equal(t, syscall.SIGUSR1, lm.signals[0])
	assert.Equal(t, syscall.SIGUSR2, lm.signals[1])
}
