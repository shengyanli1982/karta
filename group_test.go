package karta

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGroup_Map_IntToString — happy path: int → string 转换
func TestGroup_Map_IntToString(t *testing.T) {
	handler := func(ctx context.Context, v int) (string, error) {
		return fmt.Sprintf("result-%d", v), nil
	}
	g := NewGroup[int, string](handler, WithGroupWorkers(4))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3})
	require.Len(t, results, 3)
	for i, r := range results {
		assert.NoError(t, r.Err)
		assert.Equal(t, fmt.Sprintf("result-%d", i+1), r.Value)
	}
}

// TestGroup_Map_OrderPreserved — 不同耗时的任务，结果顺序 = 输入顺序
func TestGroup_Map_OrderPreserved(t *testing.T) {
	handler := func(ctx context.Context, v int) (int, error) {
		// v=1 等 90ms, v=2 等 80ms ... v=10 等 0ms
		// 最先完成的是最后一个输入，但结果仍按输入顺序排列
		delay := time.Duration((10-v)*10) * time.Millisecond
		select {
		case <-time.After(delay):
			return v * 100, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	g := NewGroup[int, int](handler, WithGroupWorkers(4))
	defer g.Stop()

	inputs := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	results := g.Map(context.Background(), inputs)
	require.Len(t, results, len(inputs))
	for i, r := range results {
		assert.NoError(t, r.Err)
		assert.Equal(t, inputs[i]*100, r.Value, "results[%d] mismatch", i)
	}
}

// TestGroup_Map_HandlerError — 单个 handler error 不影响其他任务
func TestGroup_Map_HandlerError(t *testing.T) {
	handler := func(ctx context.Context, v int) (string, error) {
		if v == 2 {
			return "", fmt.Errorf("error on %d", v)
		}
		return fmt.Sprintf("ok-%d", v), nil
	}
	g := NewGroup[int, string](handler, WithGroupWorkers(4))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3})
	require.Len(t, results, 3)

	assert.NoError(t, results[0].Err)
	assert.Equal(t, "ok-1", results[0].Value)

	assert.Error(t, results[1].Err)
	assert.Contains(t, results[1].Err.Error(), "error on 2")

	assert.NoError(t, results[2].Err)
	assert.Equal(t, "ok-3", results[2].Value)
}

// TestGroup_Map_ContextCancel — 外部 context 超时导致部分结果 error
func TestGroup_Map_ContextCancel(t *testing.T) {
	handler := func(ctx context.Context, v int) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return v, nil
		}
	}
	g := NewGroup[int, int](handler, WithGroupWorkers(2))
	defer g.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	results := g.Map(ctx, []int{1, 2, 3, 4})
	require.NotNil(t, results)
	require.Len(t, results, 4)

	errCount := 0
	for _, r := range results {
		if r.Err != nil {
			errCount++
		}
	}
	assert.True(t, errCount > 0, "expected at least 1 error due to context cancel, got %d", errCount)
}

// TestGroup_Map_PanicRecovery — panic 被捕获为 error，Group 不崩溃
func TestGroup_Map_PanicRecovery(t *testing.T) {
	handler := func(ctx context.Context, v int) (string, error) {
		if v == 2 {
			panic("boom on 2")
		}
		return fmt.Sprintf("ok-%d", v), nil
	}
	g := NewGroup[int, string](handler, WithGroupWorkers(2))
	defer g.Stop()

	results := g.Map(context.Background(), []int{1, 2, 3})
	require.Len(t, results, 3)

	assert.NoError(t, results[0].Err)
	assert.Equal(t, "ok-1", results[0].Value)

	assert.Error(t, results[1].Err)
	assert.Contains(t, results[1].Err.Error(), "panic")
	assert.Contains(t, results[1].Err.Error(), "boom on 2")

	assert.NoError(t, results[2].Err)
	assert.Equal(t, "ok-3", results[2].Value)
}

// TestGroup_Map_EmptyInput — nil 和空切片均返回 nil
func TestGroup_Map_EmptyInput(t *testing.T) {
	handler := func(ctx context.Context, v int) (string, error) {
		return "x", nil
	}
	g := NewGroup[int, string](handler)
	defer g.Stop()

	// nil input
	assert.Nil(t, g.Map(context.Background(), nil))

	// empty slice
	assert.Nil(t, g.Map(context.Background(), []int{}))
}

// TestGroup_Stop_Idempotent — 多次 Stop 不 panic
func TestGroup_Stop_Idempotent(t *testing.T) {
	g := NewGroup[int, string](func(ctx context.Context, v int) (string, error) {
		return "", nil
	})

	// 首次 Stop
	assert.NotPanics(t, func() { g.Stop() })
	// 第二次 Stop
	assert.NotPanics(t, func() { g.Stop() })
	// 第三次 Stop
	assert.NotPanics(t, func() { g.Stop() })

	// Stop 后 Map 返回 nil
	assert.Nil(t, g.Map(context.Background(), []int{1, 2, 3}))
}

// TestGroup_Map_ConcurrentSafe — 多 goroutine 并发调用 Map, 配合 -race 检测
func TestGroup_Map_ConcurrentSafe(t *testing.T) {
	handler := func(ctx context.Context, v int) (int, error) {
		return v * 2, nil
	}
	g := NewGroup[int, int](handler, WithGroupWorkers(8))
	defer g.Stop()

	inputs := make([]int, 100)
	for i := range inputs {
		inputs[i] = i
	}

	var wg sync.WaitGroup
	const rounds = 10
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := g.Map(context.Background(), inputs)
			require.Len(t, results, 100)
			for j, r := range results {
				assert.NoError(t, r.Err)
				assert.Equal(t, j*2, r.Value)
			}
		}()
	}
	wg.Wait()
}

// TestGroup_Map_ConcurrentPath_AllPanic — P1 #5: 并发路径（n > seqThreshold=128）
// 所有输入 panic 时，每个输入恰好产生一个失败 Result，
// 不得出现零值 Result（Err==nil）假成功
func TestGroup_Map_ConcurrentPath_AllPanic(t *testing.T) {
	g := NewGroup[int, int](func(ctx context.Context, v int) (int, error) {
		panic("boom")
	}, WithGroupWorkers(4))
	defer g.Stop()

	const n = 200 // 必须 > seqThreshold(128) 才走并发路径
	inputs := make([]int, n)
	for i := range inputs {
		inputs[i] = i
	}

	results := g.Map(context.Background(), inputs)
	require.Len(t, results, n)
	for i, r := range results {
		require.Error(t, r.Err, "index %d 应为失败结果，禁止零值假成功", i)
		assert.Contains(t, r.Err.Error(), "handler panic")
	}
}

// TestGroup_Map_ConcurrentPath_PartialPanic — 并发路径交替 panic（偶数项 panic），
// 逐位验证：panic 项为 Err，其余项结果正确
func TestGroup_Map_ConcurrentPath_PartialPanic(t *testing.T) {
	g := NewGroup[int, int](func(ctx context.Context, v int) (int, error) {
		if v%2 == 0 {
			panic("even boom")
		}
		return v * 10, nil
	}, WithGroupWorkers(4))
	defer g.Stop()

	const n = 300 // 必须 > seqThreshold(128) 才走并发路径
	inputs := make([]int, n)
	for i := range inputs {
		inputs[i] = i
	}

	results := g.Map(context.Background(), inputs)
	require.Len(t, results, n)
	for i, r := range results {
		if i%2 == 0 {
			require.Error(t, r.Err, "index %d 应为 panic 失败", i)
			assert.Contains(t, r.Err.Error(), "handler panic")
		} else {
			require.NoError(t, r.Err, "index %d 应成功", i)
			assert.Equal(t, i*10, r.Value)
		}
	}
}

// TestGroup_Map_ConcurrentLargeBatch_Race — P1-1 回归测试：并发复用同一 Group 时，
// 大量输入强制走并发路径（n > seqThreshold），多个调用方的 mapWorkCtx 经 sync.Pool
// 复用。修复前 worker 在 run() 内 doneCount.Add 之后普通读池化字段 sh.targetCount，
// 而另一调用方在 pool.Put 归还后立即普通写 targetCount，二者无同步边 → 数据竞态。
// 本测试以 ≥8 goroutine 并发调用 Map，逐位断言确定性结果，配合 go test -race -count=10 验证。
func TestGroup_Map_ConcurrentLargeBatch_Race(t *testing.T) {
	// 确定性计算：平方 + 偏移，便于逐位断言
	handler := func(ctx context.Context, v int) (int, error) {
		return v*v + 3, nil
	}
	g := NewGroup[int, int](handler, WithGroupWorkers(4))
	defer g.Stop()

	const n = 500 // 必须 > seqThreshold(128) 强制并发路径
	inputs := make([]int, n)
	for i := range inputs {
		inputs[i] = i
	}

	const goroutines = 16 // ≥8，放大 sync.Pool 复用碰撞概率
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := g.Map(context.Background(), inputs)
			require.Len(t, results, n)
			for j, r := range results {
				require.NoError(t, r.Err, "index %d 应成功", j)
				require.Equal(t, j*j+3, r.Value, "index %d 结果应确定", j)
			}
		}()
	}
	wg.Wait()
}
