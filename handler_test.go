package karta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_TypeSignature(t *testing.T) {
	var h Handler[int, string] = func(ctx context.Context, input int) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "ok", nil
	}

	result, err := h(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

func TestHandler_ContextCancellation(t *testing.T) {
	h := Handler[int, int](func(ctx context.Context, input int) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			return input * 2, nil
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h(ctx, 5)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMiddleware_TypeSignature(t *testing.T) {
	var mw Middleware[int, string] = func(next Handler[int, string]) Handler[int, string] {
		return func(ctx context.Context, input int) (string, error) {
			return next(ctx, input)
		}
	}

	base := Handler[int, string](func(ctx context.Context, input int) (string, error) {
		return "base", nil
	})
	wrapped := mw(base)

	result, err := wrapped(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "base", result)
}
