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

// TestClaimChunk — 批量认领大小计算的钳制边界：
// clamp(n/(workers*claimFactor), 1, claimCap)
func TestClaimChunk(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		workers int
		want    int
	}{
		{"下界钳制到1", 8, 4, 1},             // 8/16=0 → 1
		{"整除中间值", 512, 4, 32},           // 512/16=32
		{"上界钳制到cap", 4096, 4, claimCap}, // 4096/16=256 → 64
		{"恰好等于cap", 1024, 4, claimCap},  // 1024/16=64
		{"cap差1", 1020, 4, 63},          // 1020/16=63
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, claimChunk(tc.n, tc.workers))
		})
	}
}

// TestGroup_Map_ConcurrentPath_ChunkBoundary — chunked claim 认领边界回归：
// n 恰为 chunk 整数倍 / 余 1 / 尾部余数小于 chunk 的组合（均 > seqThreshold=128
// 强制并发路径），逐位断言结果完整有序，杜绝「已认领未处理」空洞与越界错写。
func TestGroup_Map_ConcurrentPath_ChunkBoundary(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		workers int
		wantMod int // 期望的 n % chunk（-1 表示不校验）
	}{
		{"w2_n129_余1", 129, 2, 1},             // chunk=16, 129=8×16+1
		{"w2_n130_尾部余2", 130, 2, 2},           // chunk=16, 130=8×16+2
		{"w2_n1000_cap钳制尾部余40", 1000, 2, 40},  // chunk=64(125→cap), 1000=15×64+40
		{"w4_n129_余1", 129, 4, 1},             // chunk=8, 129=16×8+1
		{"w4_n512_整数倍", 512, 4, 0},            // chunk=32, 512=16×32
		{"w4_n1024_cap整数倍", 1024, 4, 0},       // chunk=64(cap), 1024=16×64
		{"w8_n1000_LargeBatch形状", 1000, 8, 8}, // chunk=31, 1000=32×31+8
		{"w8_n2049_cap余1", 2049, 8, 1},        // chunk=64(cap), 2049=32×64+1
		{"w8_n4096_cap整数倍", 4096, 8, 0},       // chunk=128→64(cap), 4096=64×64
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunk := claimChunk(tc.n, tc.workers)
			require.Positive(t, chunk)
			require.LessOrEqual(t, chunk, claimCap)
			if tc.wantMod >= 0 {
				require.Equal(t, tc.wantMod, tc.n%chunk, "用例应命中预期认领边界")
			}

			g := NewGroup[int, int](func(ctx context.Context, v int) (int, error) {
				return v*2 + 7, nil
			}, WithGroupWorkers(tc.workers))
			defer g.Stop()

			inputs := make([]int, tc.n)
			for i := range inputs {
				inputs[i] = i
			}
			results := g.Map(context.Background(), inputs)
			require.Len(t, results, tc.n)
			for i, r := range results {
				require.NoError(t, r.Err, "index %d 应成功（无认领空洞）", i)
				require.Equal(t, i*2+7, r.Value, "index %d 结果应按输入顺序确定", i)
			}
		})
	}
}

// TestGroup_Map_ConcurrentPath_ChunkBoundary_PartialPanic — chunk 认领与
// per-item recover 的交互：panic 项之后同一认领区间内的项必须仍被处理，
// 每个输入恰好产生一个 Result（panic 项 Err，其余正确值）。
func TestGroup_Map_ConcurrentPath_ChunkBoundary_PartialPanic(t *testing.T) {
	const (
		n       = 1000 // chunk=62 (1000/16)，panic 项散布在多个 chunk 内部
		workers = 4
	)
	require.Greater(t, claimChunk(n, workers), 1, "用例必须走批量认领（chunk>1）")

	g := NewGroup[int, int](func(ctx context.Context, v int) (int, error) {
		if v%23 == 0 {
			panic(fmt.Sprintf("boom-%d", v))
		}
		return v * 3, nil
	}, WithGroupWorkers(workers))
	defer g.Stop()

	inputs := make([]int, n)
	for i := range inputs {
		inputs[i] = i
	}
	results := g.Map(context.Background(), inputs)
	require.Len(t, results, n)
	for i, r := range results {
		if i%23 == 0 {
			require.Error(t, r.Err, "index %d 应为 panic 失败", i)
			assert.Contains(t, r.Err.Error(), "handler panic")
		} else {
			require.NoError(t, r.Err, "index %d 应成功（同 chunk 内 panic 不得留下空洞）", i)
			require.Equal(t, i*3, r.Value, "index %d", i)
		}
	}
}
