package karta

import (
	"context"
	"sync"
	"testing"
	"time"
)

// BenchmarkGroupMap — Group.Map 吞吐量（100 个 int→int 转换, 8 workers）
func BenchmarkGroupMap(b *testing.B) {
	handler := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	g := NewGroup[int, int](handler, WithGroupWorkers(8))
	defer g.Stop()

	inputs := make([]int, 100)
	for i := range inputs {
		inputs[i] = i
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g.Map(context.Background(), inputs)
	}
}

// BenchmarkGroupMap_Parallel — Group.Map 并发版
func BenchmarkGroupMap_Parallel(b *testing.B) {
	handler := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	g := NewGroup[int, int](handler, WithGroupWorkers(8))
	defer g.Stop()

	inputs := make([]int, 100)
	for i := range inputs {
		inputs[i] = i
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = g.Map(context.Background(), inputs)
		}
	})
}

// BenchmarkPipelineSubmit — Pipeline.Submit + Future.Get 吞吐量
func BenchmarkPipelineSubmit(b *testing.B) {
	handler := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	sched := NewSimpleScheduler(4096)
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(8))
	defer p.Stop()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f, err := p.Submit(context.Background(), i)
		if err != nil {
			b.Fatalf("Submit error: %v", err)
		}
		_ = f.Get(context.Background())
	}
}

// BenchmarkFutureGet — Future 创建 + 同步 Get 开销
func BenchmarkFutureGet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewResolvedFuture[int](Result[int]{Value: i})
		_ = f.Get(context.Background())
	}
}

// BenchmarkFutureResolve — Future Resolve 开销（创建 pending → resolve → get）
func BenchmarkFutureResolve(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewPendingFuture[int]()
		f.Resolve(Result[int]{Value: i})
		_ = f.Get(context.Background())
	}
}

// BenchmarkMiddlewareChain — Chain 组合开销（3 个 pass-through middleware）
func BenchmarkMiddlewareChain(b *testing.B) {
	mw1 := Middleware[int, int](func(next Handler[int, int]) Handler[int, int] { return next })
	mw2 := Middleware[int, int](func(next Handler[int, int]) Handler[int, int] { return next })
	mw3 := Middleware[int, int](func(next Handler[int, int]) Handler[int, int] { return next })

	identity := Handler[int, int](func(ctx context.Context, input int) (int, error) { return input, nil })
	wrapped := Chain[int, int](mw1, mw2, mw3)(identity)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = wrapped(context.Background(), i)
	}
}

// BenchmarkScheduler_SimpleScheduler — SimpleScheduler Enqueue+Dequeue 吞吐量
func BenchmarkScheduler_SimpleScheduler(b *testing.B) {
	s := NewSimpleScheduler(4096)
	defer s.Shutdown()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		env := &TaskEnvelope{Input: 1}
		for pb.Next() {
			_ = s.Enqueue(env)
			_, _ = s.Dequeue(context.Background())
		}
	})
}

// BenchmarkGroupMap_LargeBatch — Group.Map 大批量吞吐（1000 项, 8 workers，覆盖并发等待路径）
func BenchmarkGroupMap_LargeBatch(b *testing.B) {
	handler := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	g := NewGroup[int, int](handler, WithGroupWorkers(8))
	defer g.Stop()

	inputs := make([]int, 1000)
	for i := range inputs {
		inputs[i] = i
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g.Map(context.Background(), inputs)
	}
}

// BenchmarkPipelineSubmit_Parallel — Pipeline.Submit + Future.Get 并发版
func BenchmarkPipelineSubmit_Parallel(b *testing.B) {
	handler := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	sched := NewSimpleScheduler(4096)
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(8))
	defer p.Stop()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			f, err := p.Submit(context.Background(), i)
			if err != nil {
				// RunParallel 的回调运行在并行 goroutine 中，不得调用
				// b.Fatalf（FailNow→runtime.Goexit 仅允许在基准主
				// goroutine 中调用）；记录错误并跳过本次迭代
				b.Error("Submit error:", err)
				continue
			}
			_ = f.Get(context.Background())
			i++
		}
	})
}

// BenchmarkFutureThen — 已 resolved Future 上 Then 的开销（注册 + 回调
// goroutine 派发）。旧版每迭代 NewPendingFuture + go Resolve + channel
// 同步，测量被 goroutine 创建与跨 goroutine 交接成本主导；改为对已
// resolved Future 直接调用 Then（命中 Then 的已决议快速路径），用 WaitGroup
// 批量协调排空，摊薄同步开销。基准名保持不变以维持历史对比。
func BenchmarkFutureThen(b *testing.B) {
	const drainBatch = 1024 // 批量排空，限制同时在飞的回调 goroutine 数量

	var wg sync.WaitGroup
	cb := func(Result[int]) { wg.Done() }
	f := NewResolvedFuture[int](Result[int]{Value: 42})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		f.Then(cb)
		if (i+1)%drainBatch == 0 {
			wg.Wait()
		}
	}
	wg.Wait()
}

// BenchmarkPipelineSubmitAfter — SubmitAfter 延迟提交吞吐（delay=1ms，
// 串行提交并等待每次执行完成，覆盖延迟 goroutine + timer + 入队全路径）
func BenchmarkPipelineSubmitAfter(b *testing.B) {
	handler := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	sched := NewSimpleScheduler(4096)
	p := NewPipeline[int, int](handler, sched, WithPipelineWorkers(8))
	defer p.Stop()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f, err := p.SubmitAfter(ctx, i, time.Millisecond)
		if err != nil {
			b.Fatalf("SubmitAfter error: %v", err)
		}
		_ = f.Get(ctx) // 等待执行完成后再进入下一迭代（延迟 goroutine 已退出）
	}
}
