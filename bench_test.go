package karta

import (
	"context"
	"testing"
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
				b.Fatalf("Submit error: %v", err)
			}
			_ = f.Get(context.Background())
			i++
		}
	})
}

// BenchmarkFutureThen — Pending Future + 异步 goroutine Resolve + Then 回调开销
func BenchmarkFutureThen(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f := NewPendingFuture[int]()
		go f.Resolve(Result[int]{Value: i})
		done := make(chan struct{})
		f.Then(func(Result[int]) { close(done) })
		<-done
	}
}
