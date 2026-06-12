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
