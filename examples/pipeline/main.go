// Package main demonstrates karta Pipeline[In, Out] async submission with Futures.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

func main() {
	// ── 1. Basic Pipeline: submit + Future.Get() ──

	handler := func(ctx context.Context, n int) (string, error) {
		time.Sleep(5 * time.Millisecond)
		return fmt.Sprintf("result-%d", n), nil
	}

	sched := karta.NewSimpleScheduler(64)
	p := karta.NewPipeline[int, string](handler, sched,
		karta.WithPipelineWorkers(4),
	)
	if p == nil {
		log.Fatal("failed to create pipeline")
	}
	defer p.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Println("=== Basic Submit ===")
	futures := make([]*karta.Future[string], 5)
	for i := 0; i < 5; i++ {
		f, err := p.Submit(ctx, i+1)
		if err != nil {
			log.Fatalf("submit failed: %v", err)
		}
		futures[i] = f
	}

	for i, f := range futures {
		r := f.Get(ctx)
		if r.Ok() {
			fmt.Printf("  task[%d] = %s\n", i, r.Value)
		} else {
			fmt.Printf("  task[%d] ERROR: %v\n", i, r.Err)
		}
	}

	// ── 2. SubmitWithHandler: per-task handler override ──

	fmt.Println("\n=== SubmitWithHandler ===")
	customHandler := func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("CUSTOM[%d]", n*100), nil
	}

	fc, err := p.SubmitWithHandler(ctx, customHandler, 7)
	if err != nil {
		log.Fatalf("SubmitWithHandler failed: %v", err)
	}
	rc := fc.Get(ctx)
	fmt.Printf("  custom handler: %s (ok=%v)\n", rc.Value, rc.Ok())

	// Default handler still works after override.
	fd, err := p.Submit(ctx, 99)
	if err != nil {
		log.Fatalf("submit after override failed: %v", err)
	}
	rd := fd.Get(ctx)
	fmt.Printf("  default handler: %s (ok=%v)\n", rd.Value, rd.Ok())

	// ── 3. SubmitAfter: delayed submission ──

	fmt.Println("\n=== SubmitAfter ===")
	start := time.Now()
	fa, err := p.SubmitAfter(ctx, 42, 200*time.Millisecond)
	if err != nil {
		log.Fatalf("SubmitAfter failed: %v", err)
	}
	ra := fa.Get(ctx)
	elapsed := time.Since(start)
	fmt.Printf("  delayed task: %s, elapsed=%v\n", ra.Value, elapsed.Round(time.Millisecond))

	// ── 4. Error handler in pipeline ──

	fmt.Println("\n=== Error Handler ===")
	errorHandler := func(ctx context.Context, n int) (string, error) {
		return "", fmt.Errorf("task %d failed intentionally", n)
	}
	fe, err := p.SubmitWithHandler(ctx, errorHandler, 55)
	if err != nil {
		log.Fatalf("submit error handler failed: %v", err)
	}
	re := fe.Get(ctx)
	if !re.Ok() {
		fmt.Printf("  error received: %v\n", re.Err)
	}

	// ── 5. Dynamic worker count ──

	fmt.Printf("\n=== Worker Count ===\n")
	fmt.Printf("  running workers: %d\n", p.GetWorkerNumber())

	// ── 6. Stop the pipeline ──

	p.Stop()
	_, submitErr := p.Submit(ctx, 1)
	fmt.Printf("\n=== After Stop ===\n")
	fmt.Printf("  submit error: %v\n", submitErr)

	log.Println("Pipeline examples completed.")
}
