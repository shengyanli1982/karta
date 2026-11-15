package karta

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════
// P1-4 验收测试 — Tune：运行时动态调整目标 worker 数
//
// targetWorkers 是 spawn 上限（atomic，Tune 改写）；调大立即尝试 spawn 差量，
// 调小依赖既有 idle 退出机制自然收敛（至少保留 1 个）。本文件位于 package karta。
// ═══════════════════════════════════════════════════════════════

// TestTune_ScaleUp_IncreasesConcurrency 验证 Tune 调大后并发执行能力上升：
// 初始 2 worker（并发上限 2），Tune(8) 后应能 8 个 handler 同时在执行。
// 用 gate 阻塞 handler，inHandler 达到 8 即证明 8 个 worker 真实并发。
func TestTune_ScaleUp_IncreasesConcurrency(t *testing.T) {
	const target = 8
	var inHandler atomic.Int64
	release := make(chan struct{})
	handler := func(ctx context.Context, n int) (int, error) {
		inHandler.Add(1)
		<-release
		inHandler.Add(-1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(256),
		WithPipelineWorkers(2),
		WithSpawnRate(1000),             // 高 rate 值：加速 spawn，避免限流拖慢到位
		WithIdleTimeout(30*time.Second), // 长 idle：测试期间 spawned worker 不空闲退出
	)
	defer func() {
		close(release)
		p.Stop()
	}()

	require.NoError(t, p.Tune(target))
	assert.Equal(t, target, p.Workers(), "Workers() 应反映最新目标值")

	// 提交 target 个阻塞任务：仅当并发上限升到 target 才能全部同时在执行
	for i := 0; i < target; i++ {
		_, err := p.Submit(context.Background(), i)
		require.NoError(t, err)
	}

	// 并发执行数应升到 target（若 Tune 未生效、上限仍为 2，则永远到不了 8）
	assert.Eventually(t, func() bool {
		return inHandler.Load() == target
	}, 5*time.Second, 10*time.Millisecond, "调大后应能并发执行 target 个任务")
	assert.Eventually(t, func() bool {
		return p.GetWorkerNumber() == target
	}, 5*time.Second, 10*time.Millisecond, "运行 worker 数应升到 target")
}

// TestTune_ScaleDown_ExcessWorkersExit 验证 Tune 调小后多余 worker 优雅退出：
// 8 个忙碌 worker 占满后 Tune(2)，释放任务 → 空闲 worker 经 idle timeout 退出，
// GetWorkerNumber 从 8 下降（收敛到 idle 最小值 1，≤ 目标 2），Stop 不阻塞（无泄漏）。
func TestTune_ScaleDown_ExcessWorkersExit(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int64
	handler := func(ctx context.Context, n int) (int, error) {
		started.Add(1)
		<-release
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(256),
		WithPipelineWorkers(8),
		WithIdleTimeout(80*time.Millisecond),  // 短 idle：加速多余 worker 退出
		WithScanInterval(20*time.Millisecond), // 短 scan：加速 idle 检测
		WithSpawnRate(1000),
	)
	defer p.Stop()

	// 8 个阻塞任务占满 8 个 worker（忙碌态，不会 idle 退出）
	for i := 0; i < 8; i++ {
		_, err := p.Submit(context.Background(), i)
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return started.Load() == 8 },
		5*time.Second, 10*time.Millisecond, "8 个 worker 应全部进入执行")
	require.Equal(t, int64(8), p.GetWorkerNumber(), "初始应有 8 个 worker")

	// 调小目标：不影响当前忙碌 worker，仅降低后续 spawn 上限
	require.NoError(t, p.Tune(2))
	assert.Equal(t, 2, p.Workers())

	// 释放任务 → worker 转为空闲 → 多余 worker 经 idle timeout 优雅退出
	close(release)
	assert.Eventually(t, func() bool {
		return p.GetWorkerNumber() <= 2
	}, 5*time.Second, 10*time.Millisecond, "调小后多余 worker 应退出，GetWorkerNumber 下降到 ≤ 目标")
	assert.Less(t, p.GetWorkerNumber(), int64(8), "worker 数应从 8 下降")
}

// TestTune_One_LastWorkerSurvives 验证 Tune(1) 后最后一个 worker 不因 idle 退出：
// 等待远超 idleTimeout，仍至少保留 1 个 worker 且能继续处理任务。
func TestTune_One_LastWorkerSurvives(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n * 2, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64),
		WithPipelineWorkers(4),
		WithIdleTimeout(50*time.Millisecond),
		WithScanInterval(15*time.Millisecond),
	)
	defer p.Stop()

	require.NoError(t, p.Tune(1))
	assert.Equal(t, 1, p.Workers())

	// 等待远超 idleTimeout：多余 worker 退出，但最后一个必须存活
	time.Sleep(500 * time.Millisecond)
	assert.GreaterOrEqual(t, p.GetWorkerNumber(), int64(1), "Tune(1) 后至少保留 1 个 worker")

	// 仍能正常处理任务
	f, err := p.Submit(context.Background(), 21)
	require.NoError(t, err)
	res := awaitResult(t, f)
	require.NoError(t, res.Err)
	assert.Equal(t, 42, res.Value)
}

// TestTune_Concurrent_NoPanic 验证多 goroutine 并发 Tune/Workers/Submit 不 panic、
// 无数据竞争（在 -race 下压测）。
func TestTune_Concurrent_NoPanic(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(512),
		WithPipelineWorkers(4),
		WithSpawnRate(1000),
		WithIdleTimeout(30*time.Millisecond),
		WithScanInterval(10*time.Millisecond),
	)
	defer p.Stop()

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = p.Tune(1 + (g+i)%8) // 目标在 1..8 间波动
				_ = p.Workers()
				_, _ = p.Submit(context.Background(), i)
				_ = p.GetWorkerNumber()
			}
		}(g)
	}
	wg.Wait()
	// 到达此处即无 panic；-race 下无竞争报告即通过
}

// TestTune_Validation 验证 Tune 参数与状态校验：
// workers < 1 → ErrInvalidWorkerCount（且不改变 targetWorkers）；已关闭 → ErrPipelineClosed。
func TestTune_Validation(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }

	// 未关闭：非法 worker 数返回 ErrInvalidWorkerCount，目标值不变
	p1 := NewPipeline[int, int](handler, NewSimpleScheduler(16), WithPipelineWorkers(2))
	defer p1.Stop()
	assert.ErrorIs(t, p1.Tune(0), ErrInvalidWorkerCount)
	assert.ErrorIs(t, p1.Tune(-1), ErrInvalidWorkerCount)
	assert.Equal(t, 2, p1.Workers(), "非法 Tune 不应改变 targetWorkers")

	// 已关闭：合法 worker 数返回 ErrPipelineClosed
	p2 := NewPipeline[int, int](handler, NewSimpleScheduler(16), WithPipelineWorkers(2))
	p2.Stop()
	assert.ErrorIs(t, p2.Tune(4), ErrPipelineClosed)
}

// TestTune_Workers_ReturnsLatestTarget 验证 Workers() 始终返回最近一次 Tune 的目标值。
func TestTune_Workers_ReturnsLatestTarget(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64),
		WithPipelineWorkers(4),
		WithSpawnRate(1000),
		WithIdleTimeout(30*time.Second),
	)
	defer p.Stop()

	assert.Equal(t, 4, p.Workers(), "初始 Workers() 应为构造配置值")
	require.NoError(t, p.Tune(10))
	assert.Equal(t, 10, p.Workers())
	require.NoError(t, p.Tune(3))
	assert.Equal(t, 3, p.Workers())
	require.NoError(t, p.Tune(1))
	assert.Equal(t, 1, p.Workers())
}

// TestTune_ClampsToUpperBound — P2-5：Tune 超上界钳制到 defaultMaxWorkerNum
// （与 WithPipelineWorkers 共享上界，防止超大值令 spawn 上限失控）；
// 下界保留运行期语义：Tune(1) 合法（idle 机制本就收敛到 1，Tune(1) 即最小
// 稳态，与构造期 [2, max] 域的差异是刻意的，见 Tune 文档）。
func TestTune_ClampsToUpperBound(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(16), WithPipelineWorkers(2))
	defer p.Stop()

	// 最小越界样本：修复前 Workers() 记录越界原值（牙齿：旧代码在此快速失败，
	// require 中止避免后续 1e9 样本在未钳制代码上做 ~1e9 次 spawn 尝试长自旋）
	require.NoError(t, p.Tune(int(defaultMaxWorkerNum)+1))
	require.Equal(t, int(defaultMaxWorkerNum), p.Workers(), "超上界应钳制到 defaultMaxWorkerNum")

	// 极端越界值：修复后同样钳制到上界并快速返回——
	// 本调用能在毫秒~百毫秒级完成本身即钳制生效的证据
	require.NoError(t, p.Tune(1_000_000_000))
	require.Equal(t, int(defaultMaxWorkerNum), p.Workers(), "极端值应钳制到 defaultMaxWorkerNum")

	// 运行期下界：1 合法（非法值 0/-1 已由 TestTune_Validation 覆盖）
	require.NoError(t, p.Tune(1))
	require.Equal(t, 1, p.Workers(), "Tune(1) 应为合法的最小稳态目标")
}
