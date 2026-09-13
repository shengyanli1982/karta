package karta_test

import (
	"context"
	"testing"
	"time"

	"github.com/shengyanli1982/karta/v2/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// ═══════════════════════════════════════════════════════════════
// P0 N1 回归测试 — Pipeline × Lease 任务静默丢失
// ═══════════════════════════════════════════════════════════════
//
// 根因：scheduler.LeaseScheduler 的 Dequeue 返回原始 envelope 的浅拷贝
// （新指针，所有权契约要求），而 Pipeline 的 pending map 曾以原 envelope
// 指针为键 → executor 用拷贝查找 loadAndDeletePending 必然 miss →
// 走 nil-future 分支 Done(Ack 租约) → 任务被静默丢弃：handler 不执行、
// Future 永挂、pending 条目泄漏。
//
// 修复：pending map 改用 TaskEnvelope.id 为键（submitInternal 统一赋值，
// 浅拷贝天然继承 id），本文件钉住修复后的端到端行为。
// ═══════════════════════════════════════════════════════════════

// TestIntegration_Pipeline_LeaseScheduler 验证 Pipeline × LeaseScheduler
// 端到端：Submit → handler 执行 → Future.Get 成功返回结果，无任务丢失。
func TestIntegration_Pipeline_LeaseScheduler(t *testing.T) {
	const taskCount = 20

	handler := func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	}

	p := karta.NewPipeline[int, int](handler,
		scheduler.NewLeaseScheduler(5*time.Second),
		karta.WithPipelineWorkers(4))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	futures := make([]*karta.Future[int], taskCount)
	for i := 0; i < taskCount; i++ {
		f, err := p.Submit(ctx, i+1)
		require.NoError(t, err, "Submit(%d) should succeed", i+1)
		futures[i] = f
	}

	// 修复前：所有 future 永挂，Get 在 ctx 超时后返回错误（100% 任务丢失）
	for i, f := range futures {
		result := f.Get(ctx)
		require.NoError(t, result.Err,
			"future[%d] lost: lease 拷贝交付后 pending 查找 miss，任务被静默丢弃", i)
		assert.Equal(t, (i+1)*2, result.Value, "future[%d] value mismatch", i)
	}
}

// TestIntegration_Pipeline_CompositeScheduler_LeaseFIFO 验证 Pipeline ×
// CompositeScheduler(Lease, FIFO) 端到端：搬运泵从 Lease 级取出的浅拷贝
// 经 FIFO 级交付 executor，pending 仍须按 id 命中，Future 正常完成。
func TestIntegration_Pipeline_CompositeScheduler_LeaseFIFO(t *testing.T) {
	const taskCount = 20

	handler := func(ctx context.Context, n int) (int, error) {
		return n * 3, nil
	}

	composite := scheduler.NewCompositeScheduler(
		scheduler.NewLeaseScheduler(5*time.Second),
		scheduler.NewFIFOScheduler(),
	)

	p := karta.NewPipeline[int, int](handler, composite, karta.WithPipelineWorkers(4))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	futures := make([]*karta.Future[int], taskCount)
	for i := 0; i < taskCount; i++ {
		f, err := p.Submit(ctx, i+1)
		require.NoError(t, err, "Submit(%d) should succeed", i+1)
		futures[i] = f
	}

	// 修复前：泵交付的是 lease 拷贝指针，pending 按原指针查找 miss，全部丢失
	for i, f := range futures {
		result := f.Get(ctx)
		require.NoError(t, result.Err,
			"future[%d] lost: composite(lease→fifo) 拷贝交付后 pending 查找 miss", i)
		assert.Equal(t, (i+1)*3, result.Value, "future[%d] value mismatch", i)
	}
}
