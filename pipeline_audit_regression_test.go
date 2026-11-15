package karta

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════
// 2026-11-06 缺陷审计回归测试（根包部分）— P2-3 / P2-4
// 审计报告：.agent-work/reports/2026-11-06-karta-defect-audit.md
// 本文件位于 package karta（内部测试，可直读 p.closed 等私有字段）。
// ═══════════════════════════════════════════════════════════════

// TestStop_FastPath_BufferedTasksResolveClosed P2-3：Stop 后 SimpleScheduler
// 缓冲区中的任务不得再以已取消的 ctx 执行 handler，终态必须与 Stop 清理路径
// 一致（一律 ErrPipelineClosed）。
//
// 构造：8 个 worker 被阻塞 handler 占满 → 50 个任务滞留 ss.ch 缓冲 →
// 发起 Stop（closed 置位、ctx 取消、channel 关闭）→ 释放 worker。
// worker 回到 select 时 ss.ch（closed channel 的剩余缓冲仍可接收）与
// p.ctx.Done() 同时就绪、随机选择：
//   - 修复前：选中 ss.ch 即照常执行 handler（lateCalls>0），对应 future 以
//     handler 结果（nil error）resolve，与同批其余任务的 ErrPipelineClosed
//     终态分叉。8 个 worker 全部立即选中 ctx.Done 的概率仅 1/256——
//     旧代码几乎必然 FAIL（牙齿；第一轮 2-worker 构造以 1/4 概率漏检，
//     已加大 worker 数使牙齿接近确定性）；
//   - 修复后：fast path 取出任务即检查 closed，就地按 Stop 清理语义终结
//     （resolve ErrPipelineClosed + Done + removeDedup + 归还 envelope），
//     handler 零调用，全部缓冲任务终态一致。
func TestStop_FastPath_BufferedTasksResolveClosed(t *testing.T) {
	release := make(chan struct{})
	var blockedCalls, lateCalls atomic.Int64
	handler := func(ctx context.Context, n int) (int, error) {
		if n < 100 {
			blockedCalls.Add(1)
			<-release // 占住 worker，令后续任务滞留 ss.ch 缓冲
			return n, nil
		}
		lateCalls.Add(1) // 缓冲任务计数：Stop 后不得再被调用
		return n, nil
	}
	const workers = 8
	p := NewPipeline[int, int](handler, NewSimpleScheduler(128), WithPipelineWorkers(workers))

	// 8 个阻塞任务占满全部 worker（running=8 已达 target，不会 spawn 新 worker）
	blockedFutures := make([]*Future[int], 0, workers)
	for i := 0; i < workers; i++ {
		f, err := p.Submit(context.Background(), i)
		require.NoError(t, err)
		blockedFutures = append(blockedFutures, f)
	}
	require.Eventually(t, func() bool { return blockedCalls.Load() == workers },
		5*time.Second, time.Millisecond, "全部 worker 应进入阻塞 handler")

	// 50 个缓冲任务滞留 ss.ch（无空闲 worker 消费）
	const buffered = 50
	bufferedFutures := make([]*Future[int], 0, buffered)
	for i := 0; i < buffered; i++ {
		f, err := p.Submit(context.Background(), 100+i)
		require.NoError(t, err)
		bufferedFutures = append(bufferedFutures, f)
	}
	require.Equal(t, buffered, p.scheduler.Len(), "缓冲任务应全部滞留队列")

	// 发起 Stop（内部 wg.Wait 会等待被阻塞的 worker），确认 closed 已置位
	// 后再释放 worker：确保 worker 回到 select 时 ctx 已取消、channel 已关闭，
	// 精确复现"closed 后缓冲任务仍可被 fast path 取出"的时序窗口。
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		p.Stop()
	}()
	require.Eventually(t, p.closed.Load, 5*time.Second, time.Millisecond)
	close(release)
	<-stopDone

	// Stop 前已在执行的任务正常完成（终态不受本次修复影响）
	getCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i, f := range blockedFutures {
		r := f.Get(getCtx)
		require.NoError(t, r.Err, "Stop 前已在执行的任务 %d 应正常完成", i)
		assert.Equal(t, i, r.Value)
	}

	// 核心断言 1：closed 后 handler 零调用
	assert.Zero(t, lateCalls.Load(), "Stop 后缓冲任务不得再执行 handler")
	// 核心断言 2：全部缓冲任务以 ErrPipelineClosed 终态（与 Stop 清理一致，
	// 无论任务是被 fast path 取出就地终结，还是留在队列由 Stop 清理兜底）
	for i, f := range bufferedFutures {
		res := f.Get(getCtx)
		assert.ErrorIs(t, res.Err, ErrPipelineClosed, "缓冲任务 %d 应以 ErrPipelineClosed 终态", i)
	}
}

// TestSubmit_NilInterfaceInput P2-4：In 为接口类型且 input=nil 时，
// handler 应收到 nil 并正常执行，不得被单值类型断言 panic 误判为任务失败。
//
// 修复前：envelope.Input 为 nil any，executor 的单值断言 nil.(error) panic，
// 被 recover 吞为 "handler panic: interface conversion..." 任务错误（牙齿：
// 旧代码必 FAIL）；修复后：双值断言对不可转换值返回 In 零值（接口型 In 的
// 零值即 nil），恰为契约语义。
func TestSubmit_NilInterfaceInput(t *testing.T) {
	var called, gotNil atomic.Int64
	handler := func(ctx context.Context, e error) (int, error) {
		called.Add(1)
		if e == nil {
			gotNil.Add(1)
		}
		return 42, nil
	}
	p := NewPipeline[error, int](handler, NewSimpleScheduler(8), WithPipelineWorkers(2))
	defer p.Stop()

	f, err := p.Submit(context.Background(), nil)
	require.NoError(t, err)

	getCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := f.Get(getCtx)
	require.NoError(t, res.Err, "nil 接口输入不应被误判为 handler panic")
	assert.Equal(t, 42, res.Value)
	assert.Equal(t, int64(1), called.Load(), "handler 应被调用一次")
	assert.Equal(t, int64(1), gotNil.Load(), "handler 应收到 nil 输入")
}
