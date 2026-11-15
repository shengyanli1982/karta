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

// TestMain 在运行全部测试前为测试进程注册常驻信号吸收器。
//
// Why（清理侧逃逸窗口）：raiseUntilStopped 轮询自发 SIGTERM/SIGUSR1 直到条件满足，
// 最后一个 in-flight 信号可能在测试侧 defer signal.Stop(probe) 与 WaitForSignal
// 内部 defer signal.Stop(ch) 双双注销之后才被 runtime dispatch——此刻进程对该
// 信号无任何注册 handler，触发默认动作（终止进程），表现为 -count>1 + 全包并行
// 加压下偶发的 "signal: terminated"（启动侧竞争已由 probe 预注册覆盖，此处补上
// 清理侧）。吸收器生命周期 = 整个测试进程，不在任何 defer 中 Stop，保证进程对
// 这两类信号恒有至少一个注册 handler，默认终止动作永不触发——以结构手段消除
// 窗口，而非靠 sleep/加锁掩盖时序。
//
// 正确性不变量（os/signal 官方语义保证）：
//  1. 广播独立性：Notify 允许多个 channel 注册同一信号，"each channel receives
//     copies of incoming signals independently"——常驻吸收 channel 不会阻止被测
//     probe 或 WaitForSignal 内部 channel 收到信号，既有测试语义不变。
//  2. 非阻塞投递："Package signal will not block sending to c"，缓冲满时仅丢弃
//     该 channel 的副本，吸收器不会拖慢或阻塞其他 handler。
//  3. drain goroutine 持续排空缓冲，使吸收能力实际无上限（丢弃对吸收器无害，
//     排空更稳妥）。
//
// 运维影响（吸收器方案的固有代价）：常驻吸收器使本包测试进程不再响应
// SIGTERM 的默认终止动作——CI 以 SIGTERM 取消测试时无法优雅终止本进程，
// 需升级 SIGKILL 才能强制回收。
func TestMain(m *testing.M) {
	absorb := make(chan os.Signal, 64)
	signal.Notify(absorb, syscall.SIGTERM, syscall.SIGUSR1)
	go func() {
		for range absorb {
			// 收到即丢弃；永不 signal.Stop(absorb)——"常驻不注销"正是修复点。
		}
	}()
	os.Exit(m.Run())
}

// raiseUntilStopped 轮询向自身进程发送 sig，直到 cond 满足或超时。
// 轮询消除与 WaitForSignal 内部 signal.Notify 注册之间的时序竞争：
// 首次发送可能早于注册完成（由测试预注册的 probe 通道兜底，不会触发默认动作），
// 注册完成后的发送必然被 WaitForSignal 接收。
// 清理侧（probe 与 WaitForSignal 双双注销后 in-flight 信号逃逸）由 TestMain
// 的常驻吸收器兜底。
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
