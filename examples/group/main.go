// Package main demonstrates karta Group[In, Out] synchronous batch processing.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

func main() {
	// ── 1. Basic batch: int → string with 4 workers ──

	handler := func(ctx context.Context, n int) (string, error) {
		// Simulate variable processing time.
		time.Sleep(time.Duration(n%3) * 10 * time.Millisecond)
		return fmt.Sprintf("processed-%d", n), nil
	}

	g := karta.NewGroup[int, string](handler, karta.WithGroupWorkers(4))
	defer g.Stop()

	inputs := []int{1, 2, 3, 4, 5, 6, 7, 8}
	results := g.Map(context.Background(), inputs)

	fmt.Println("=== Basic Batch ===")
	for i, r := range results {
		val, err := r.Unwrap()
		if err != nil {
			fmt.Printf("  [%d] ERROR: %v\n", i, err)
		} else {
			fmt.Printf("  [%d] %s\n", i, val)
		}
	}

	// ── 2. Error handling: partial failures ──

	divisiveHandler := func(ctx context.Context, n int) (int, error) {
		if n%3 == 0 {
			return 0, fmt.Errorf("cannot process %d: divisible by 3", n)
		}
		return n * 10, nil
	}

	g2 := karta.NewGroup[int, int](divisiveHandler, karta.WithGroupWorkers(3))
	defer g2.Stop()

	fmt.Println("\n=== Error Handling ===")
	results2 := g2.Map(context.Background(), []int{1, 2, 3, 4, 5, 6})
	for i, r := range results2 {
		if r.Ok() {
			fmt.Printf("  [%d] value=%d\n", i, r.Value)
		} else {
			fmt.Printf("  [%d] ERROR: %v\n", i, r.Err)
		}
	}

	// ── 3. Context cancellation: cancel mid-batch ──

	slowHandler := func(ctx context.Context, n int) (string, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return fmt.Sprintf("done-%d", n), nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	g3 := karta.NewGroup[int, string](slowHandler, karta.WithGroupWorkers(2))
	defer g3.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	fmt.Println("\n=== Context Cancellation ===")
	results3 := g3.Map(ctx, []int{1, 2, 3, 4})
	for i, r := range results3 {
		if r.Ok() {
			fmt.Printf("  [%d] %s\n", i, r.Value)
		} else {
			fmt.Printf("  [%d] cancelled: %v\n", i, r.Err)
		}
	}

	// ── 4. Stop the group: subsequent Map returns nil ──

	g.Stop()
	nilResults := g.Map(context.Background(), []int{1, 2, 3})
	fmt.Printf("\n=== After Stop ===\n")
	fmt.Printf("  Map returns nil: %v\n", nilResults == nil)

	log.Println("Group examples completed.")
}
