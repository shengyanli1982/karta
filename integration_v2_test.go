package karta_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────
// Test helpers & domain types
// ─────────────────────────────────────────────────────────────

// Request represents a simulated business request struct.
type Request struct {
	ID   int
	Body string
}

// Response represents a simulated business response struct.
type Response struct {
	ID     int
	Status string
}

// ─────────────────────────────────────────────────────────────
// Test 1: Group — int → string 同步批处理
// ─────────────────────────────────────────────────────────────

// TestIntegration_Group_IntToString 验证 Group 的基本同步批处理能力：
// int → string 转换，4 workers，验证结果按输入顺序排列。
func TestIntegration_Group_IntToString(t *testing.T) {
	const taskCount = 20

	handler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("result-%d", n), nil
	}

	g := karta.NewGroup[int, string](handler, karta.WithGroupWorkers(4))
	defer g.Stop()

	inputs := make([]int, taskCount)
	for i := 0; i < taskCount; i++ {
		inputs[i] = i + 1
	}

	results := g.Map(context.Background(), inputs)
	require.NotNil(t, results, "Map should return results for non-empty input")
	require.Len(t, results, taskCount)

	for i, r := range results {
		assert.True(t, r.Ok(), "result[%d] should be Ok, got err: %v", i, r.Err)
		assert.Equal(t, fmt.Sprintf("result-%d", i+1), r.Value, "result[%d] value mismatch", i)
	}
}

// ─────────────────────────────────────────────────────────────
// Test 2: Group — struct → struct 模拟真实业务场景
// ─────────────────────────────────────────────────────────────

// TestIntegration_Group_StructToStruct 验证 Group 处理自定义结构体转换：
// Request{ID, Body} → Response{ID, Status}，包含错误路径验证。
func TestIntegration_Group_StructToStruct(t *testing.T) {
	handler := func(ctx context.Context, req Request) (Response, error) {
		if req.Body == "" {
			return Response{}, fmt.Errorf("empty body for request ID %d", req.ID)
		}
		return Response{
			ID:     req.ID,
			Status: fmt.Sprintf("processed: %s", req.Body),
		}, nil
	}

	g := karta.NewGroup[Request, Response](handler, karta.WithGroupWorkers(4))
	defer g.Stop()

	const taskCount = 50
	inputs := make([]Request, taskCount)
	for i := 0; i < taskCount; i++ {
		if i == 25 {
			// 注入一条空 body 请求，验证错误隔离
			inputs[i] = Request{ID: i + 1, Body: ""}
		} else {
			inputs[i] = Request{ID: i + 1, Body: fmt.Sprintf("payload-%d", i+1)}
		}
	}

	results := g.Map(context.Background(), inputs)
	require.Len(t, results, taskCount)

	for i, r := range results {
		if i == 25 {
			// 预期错误
			assert.False(t, r.Ok(), "result[%d] should have error for empty body", i)
			assert.Error(t, r.Err)
		} else {
			assert.True(t, r.Ok(), "result[%d] should be Ok, got err: %v", i, r.Err)
			assert.Equal(t, i+1, r.Value.ID, "result[%d] ID mismatch", i)
			assert.Equal(t, fmt.Sprintf("processed: payload-%d", i+1), r.Value.Status)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Test 3: Pipeline — 提交 100 个任务，全部 Future.Get 验证
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_FullFlow 验证 Pipeline 完整流处理：
// Submit 100 个任务并逐个 Future.Get 验证结果正确性。
func TestIntegration_Pipeline_FullFlow(t *testing.T) {
	const taskCount = 100

	sched := karta.NewSimpleScheduler(512)
	handler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("pipeline-%d", n), nil
	}

	p := karta.NewPipeline[int, string](handler, sched, karta.WithPipelineWorkers(8))
	require.NotNil(t, p, "NewPipeline should not be nil with valid scheduler")
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 提交所有任务并收集 Future
	futures := make([]*karta.Future[string], taskCount)
	for i := 0; i < taskCount; i++ {
		future, err := p.Submit(ctx, i+1)
		require.NoError(t, err, "Submit(%d) should not return error", i)
		require.NotNil(t, future, "Submit(%d) future should not be nil", i)
		futures[i] = future
	}

	// 逐个 Get 验证结果
	for i, future := range futures {
		result := future.Get(ctx)
		assert.True(t, result.Ok(), "future[%d] should be Ok, got err: %v", i, result.Err)
		assert.Equal(t, fmt.Sprintf("pipeline-%d", i+1), result.Value, "future[%d] value mismatch", i)
	}
}

// ─────────────────────────────────────────────────────────────
// Test 4: Pipeline — SubmitWithHandler 覆盖默认 Handler
// ─────────────────────────────────────────────────────────────

// TestIntegration_Pipeline_SubmitWithHandler_Override 验证单任务 Handler 覆盖：
// 默认 handler 返回 n，per-task handler 返回 n*100。
// 验证覆盖生效后，默认 handler 保持不变。
func TestIntegration_Pipeline_SubmitWithHandler_Override(t *testing.T) {
	sched := karta.NewSimpleScheduler(512)

	// 默认 handler: n → n
	defaultHandler := func(ctx context.Context, n int) (int, error) {
		return n, nil
	}

	p := karta.NewPipeline[int, int](defaultHandler, sched)
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. 用 per-task handler 提交，期望 n*100
	customHandler := func(ctx context.Context, n int) (int, error) {
		return n * 100, nil
	}
	f1, err := p.SubmitWithHandler(ctx, customHandler, 5)
	require.NoError(t, err)
	r1 := f1.Get(ctx)
	assert.True(t, r1.Ok(), "per-task handler result should be Ok")
	assert.Equal(t, 500, r1.Value, "per-task handler should return n*100")

	// 2. 用默认 handler 提交，验证未受影响
	f2, err := p.Submit(ctx, 7)
	require.NoError(t, err)
	r2 := f2.Get(ctx)
	assert.True(t, r2.Ok(), "default handler result should be Ok")
	assert.Equal(t, 7, r2.Value, "default handler should return n")

	// 3. per-task handler 返回错误
	errorHandler := func(ctx context.Context, n int) (int, error) {
		return 0, fmt.Errorf("custom error for %d", n)
	}
	f3, err := p.SubmitWithHandler(ctx, errorHandler, 42)
	require.NoError(t, err)
	r3 := f3.Get(ctx)
	assert.False(t, r3.Ok(), "error handler result should not be Ok")
	assert.EqualError(t, r3.Err, "custom error for 42")

	// 4. 再次用默认提交，确保仍然正常
	f4, err := p.Submit(ctx, 99)
	require.NoError(t, err)
	r4 := f4.Get(ctx)
	assert.True(t, r4.Ok())
	assert.Equal(t, 99, r4.Value)
}

// ─────────────────────────────────────────────────────────────
// Test 5: LifecycleManager 托管 Group + Pipeline
// ─────────────────────────────────────────────────────────────

// TestIntegration_LifecycleManager_Manage_Group_Pipeline 验证：
// LifecycleManager 同时管理 Group 和 Pipeline，Shutdown 后两者都被正确停止。
func TestIntegration_LifecycleManager_Manage_Group_Pipeline(t *testing.T) {
	// 创建 Group
	groupHandler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("group-%d", n), nil
	}
	g := karta.NewGroup[int, string](groupHandler, karta.WithGroupWorkers(4))

	// 创建 Pipeline
	pipelineHandler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("pipeline-%d", n), nil
	}
	sched := karta.NewSimpleScheduler(512)
	p := karta.NewPipeline[int, string](pipelineHandler, sched)
	require.NotNil(t, p)

	// LifecycleManager 托管 Group + Pipeline
	lm := karta.NewLifecycleManager(
		karta.WithManaged(g, p),
		karta.WithShutdownTimeout(5*time.Second),
	)
	require.NotNil(t, lm)

	// Shutdown 之前：验证两者正常工作
	t.Run("before shutdown - Group works", func(t *testing.T) {
		results := g.Map(context.Background(), []int{1, 2, 3})
		require.Len(t, results, 3)
		for _, r := range results {
			assert.True(t, r.Ok())
		}
	})

	t.Run("before shutdown - Pipeline works", func(t *testing.T) {
		future, err := p.Submit(context.Background(), 42)
		require.NoError(t, err)
		result := future.Get(context.Background())
		assert.True(t, result.Ok())
		assert.Equal(t, "pipeline-42", result.Value)
	})

	// 执行 Shutdown
	lm.Shutdown()

	// Shutdown 之后：验证两者都已停止
	t.Run("after shutdown - Group.Map returns nil", func(t *testing.T) {
		results := g.Map(context.Background(), []int{1, 2, 3})
		assert.Nil(t, results, "Group.Map should return nil after Stop")
	})

	t.Run("after shutdown - Pipeline.Submit returns error", func(t *testing.T) {
		_, err := p.Submit(context.Background(), 42)
		assert.Error(t, err, "Pipeline.Submit should return error after Stop")
		// 可能是 ErrPipelineClosed 或底层 SubmitError
		var submitErr *karta.SubmitError
		isPipelineClosed := errors.Is(err, karta.ErrPipelineClosed)
		isSubmitErr := errors.As(err, &submitErr)
		assert.True(t, isPipelineClosed || isSubmitErr,
			"expected ErrPipelineClosed or SubmitError, got: %v", err)
	})

	// 幂等性验证：再次调用 Shutdown 不会 panic
	lm.Shutdown()
}

// ─────────────────────────────────────────────────────────────
// Test 6: 并发压力测试 — 500 并发 Submit，-race 通过
// ─────────────────────────────────────────────────────────────

// TestIntegration_ConcurrentPressure 验证在高并发场景下的正确性和 race-safety：
// 500 goroutine 同时向 Pipeline Submit，-race 检测通过且结果正确。
func TestIntegration_ConcurrentPressure(t *testing.T) {
	const (
		concurrency = 500
		workers     = 16
		bufferSize  = 1024 // 足够大，避免 buffer full
	)

	var processedCount atomic.Int64

	handler := func(ctx context.Context, n int) (int, error) {
		// 模拟极少量工作
		processedCount.Add(1)
		return n * 2, nil
	}

	sched := karta.NewSimpleScheduler(bufferSize)
	p := karta.NewPipeline[int, int](handler, sched, karta.WithPipelineWorkers(workers))
	require.NotNil(t, p)
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 并发提交
	var wg sync.WaitGroup
	futures := make([]*karta.Future[int], concurrency)
	submitErrors := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			future, err := p.Submit(ctx, idx+1)
			if err != nil {
				submitErrors[idx] = err
				return
			}
			futures[idx] = future
		}(i)
	}
	wg.Wait()

	// 统计提交阶段错误
	submitErrorCount := 0
	for _, err := range submitErrors {
		if err != nil {
			submitErrorCount++
			t.Logf("submit error: %v", err)
		}
	}
	if submitErrorCount > 0 {
		t.Logf("%d/%d submits failed (buffer contention)", submitErrorCount, concurrency)
	}

	// 验证已成功提交的 Future 结果正确
	successCount := 0
	for i, future := range futures {
		if future == nil {
			continue
		}
		result := future.Get(ctx)
		if result.Ok() {
			successCount++
			assert.Equal(t, (i+1)*2, result.Value,
				"future[%d] expected %d, got %d", i, (i+1)*2, result.Value)
		}
	}

	// 大部分提交应该成功（buffer 足够大）
	assert.Greater(t, successCount, concurrency/2,
		"at least half of concurrent submissions should succeed, got %d/%d", successCount, concurrency)

	// 处理器至少被调用了
	assert.Greater(t, int(processedCount.Load()), 0,
		"handler should have been called at least once")

	t.Logf("Concurrent pressure test: %d submitted, %d succeeded, %d processed",
		concurrency-submitErrorCount, successCount, processedCount.Load())
}
