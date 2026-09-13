//go:build !windows

package karta

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// raiseUntilStopped 轮询向自身进程发送 sig，直到 cond 满足或超时。
// 轮询消除与 WaitForSignal 内部 signal.Notify 注册之间的时序竞争：
// 首次发送可能早于注册完成（由测试预注册的 probe 通道兜底，不会触发默认动作），
// 注册完成后的发送必然被 WaitForSignal 接收。
func raiseUntilStopped(t *testing.T, sig os.Signal, cond func() bool) {
	t.Helper()
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_ = proc.Signal(sig)
		return cond()
	}, 5*time.Second, 20*time.Millisecond,
		"signal %v should trigger Shutdown and stop the managed component", sig)
}

// TestLifecycleManager_WithSignals_TriggersShutdown — 行为断言：
// 注册自定义信号 SIGUSR1 → 真实向自身进程发送 → 断言 Shutdown 被触发
// （托管组件 Stop 被调用、WaitForSignal 返回）。
// SIGUSR1/SIGUSR2 在 Windows 不存在，故本测试仅在非 Windows 平台编译运行。
func TestLifecycleManager_WithSignals_TriggersShutdown(t *testing.T) {
	comp := &mockComponent{}
	lm := NewLifecycleManager(
		WithSignals(syscall.SIGUSR1),
		WithManaged(comp),
	)

	// 测试侧预先注册 SIGUSR1，覆盖其默认动作（终止进程），
	// 保证 WaitForSignal 内部注册生效前的提前发送也是安全的
	probe := make(chan os.Signal, 1)
	signal.Notify(probe, syscall.SIGUSR1)
	defer signal.Stop(probe)

	waitReturned := make(chan struct{})
	go func() {
		defer close(waitReturned)
		lm.WaitForSignal()
	}()

	raiseUntilStopped(t, syscall.SIGUSR1, comp.isStopped)

	select {
	case <-waitReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForSignal should return after handling the signal")
	}
	assert.True(t, comp.isStopped(), "managed component should be stopped by signal-triggered Shutdown")
}

// TestLifecycleManager_WaitForSignal_DefaultFallback — WithSignals() 传空集合时，
// WaitForSignal 回退默认信号集 SIGINT/SIGTERM：发送 SIGTERM 应触发 Shutdown。
func TestLifecycleManager_WaitForSignal_DefaultFallback(t *testing.T) {
	comp := &mockComponent{}
	lm := NewLifecycleManager(
		WithSignals(), // 空集合 → WaitForSignal 回退默认 SIGINT/SIGTERM
		WithManaged(comp),
	)

	// 预注册 SIGTERM，覆盖默认动作（终止进程）
	probe := make(chan os.Signal, 1)
	signal.Notify(probe, syscall.SIGTERM)
	defer signal.Stop(probe)

	waitReturned := make(chan struct{})
	go func() {
		defer close(waitReturned)
		lm.WaitForSignal()
	}()

	raiseUntilStopped(t, syscall.SIGTERM, comp.isStopped)

	select {
	case <-waitReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForSignal should return after handling the signal")
	}
}
