// Package main demonstrates karta middleware composition (Chain, Logging, Recovery, custom).
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/karta/v2/middleware"
)

// tracingID middleware adds a simple request ID tag to the context.
// This shows how to write a custom middleware for karta.
func tracingID() karta.Middleware[int, string] {
	return func(next karta.Handler[int, string]) karta.Handler[int, string] {
		return func(ctx context.Context, input int) (string, error) {
			reqID := fmt.Sprintf("req-%d", input)
			ctx = context.WithValue(ctx, ctxKeyReqID, reqID)

			fmt.Printf("  [trace] start %s\n", reqID)
			out, err := next(ctx, input)
			fmt.Printf("  [trace] end   %s, err=%v\n", reqID, err)

			return out, err
		}
	}
}

// ctxKey is an unexported type for context keys to avoid collisions.
type ctxKey struct{}

var ctxKeyReqID = ctxKey{}

func main() {
	// ── 1. Using Chain to compose multiple built-in middleware ──

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Compose: Recovery(outermost) → Logging → Timeout(innermost)
	mw := karta.Chain[int, string](
		middleware.Recovery[int, string](),
		middleware.Logging[int, string](logger),
		middleware.Timeout[int, string](1*time.Second),
	)

	handler := func(ctx context.Context, n int) (string, error) {
		time.Sleep(20 * time.Millisecond)
		return fmt.Sprintf("value-%d", n), nil
	}

	// Wrap the handler with the middleware chain.
	wrapped := mw(handler)

	fmt.Println("=== Chain: Recovery + Logging + Timeout ===")
	val, err := wrapped(context.Background(), 42)
	fmt.Printf("  result: %s, err: %v\n", val, err)

	// ── 2. Recovery middleware catches panics ──

	fmt.Println("\n=== Recovery: panic handling ===")
	panicHandler := func(ctx context.Context, n int) (string, error) {
		panic(fmt.Sprintf("unexpected explosion at item %d", n))
	}
	recovered := middleware.Recovery[int, string]()(panicHandler)
	_, recErr := recovered(context.Background(), 7)
	if recErr != nil {
		// Print only the first line of the error (omit full stack trace for readability).
		msg := recErr.Error()
		for i, c := range msg {
			if c == '\n' {
				msg = msg[:i]
				break
			}
		}
		fmt.Printf("  recovered error: %s...\n", msg)
	}

	// ── 3. Custom middleware: request tracing ──

	fmt.Println("\n=== Custom Middleware: tracing ===")
	customChain := karta.Chain[int, string](
		tracingID(),
		middleware.Logging[int, string](logger),
	)
	traced := customChain(func(ctx context.Context, n int) (string, error) {
		reqID, _ := ctx.Value(ctxKeyReqID).(string)
		return fmt.Sprintf("hello-%s", reqID), nil
	})

	out, err := traced(context.Background(), 100)
	fmt.Printf("  result: %s, err: %v\n", out, err)

	// ── 4. Using middleware with Group (via WithGroupMiddleware) ──

	fmt.Println("\n=== Middleware with Group ===")
	g := karta.NewGroup[int, string](
		func(ctx context.Context, n int) (string, error) {
			return fmt.Sprintf("batch-%d", n), nil
		},
		karta.WithGroupWorkers(2),
		karta.WithGroupMiddleware(middleware.Logging[int, string](logger)),
	)
	defer g.Stop()

	results := g.Map(context.Background(), []int{10, 20, 30})
	for i, r := range results {
		fmt.Printf("  [%d] %s (ok=%v)\n", i, r.Value, r.Ok())
	}

	log.Println("Middleware examples completed.")
}
