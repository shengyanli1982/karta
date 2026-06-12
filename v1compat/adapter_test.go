package v1compat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 1. TestCallbackAdapter — OnBefore/OnAfter 函数被正确调用
// ---------------------------------------------------------------------------

func TestCallbackAdapter(t *testing.T) {
	t.Run("both functions set", func(t *testing.T) {
		var beforeCalled, afterCalled bool
		var capturedInput, capturedOutput any
		var capturedErr error

		adapter := &CallbackAdapter{
			OnBeforeFunc: func(msg any) {
				beforeCalled = true
				capturedInput = msg
			},
			OnAfterFunc: func(msg, result any, err error) {
				afterCalled = true
				capturedInput = msg
				capturedOutput = result
				capturedErr = err
			},
		}

		ctx := context.Background()
		adapter.OnBefore(ctx, "hello")
		assert.True(t, beforeCalled)
		assert.Equal(t, "hello", capturedInput)

		adapter.OnAfter(ctx, "hello", "world", nil)
		assert.True(t, afterCalled)
		assert.Equal(t, "hello", capturedInput)
		assert.Equal(t, "world", capturedOutput)
		assert.NoError(t, capturedErr)
	})

	t.Run("nil functions are no-op", func(t *testing.T) {
		adapter := &CallbackAdapter{} // both funcs are nil
		ctx := context.Background()
		// Should not panic
		adapter.OnBefore(ctx, "input")
		adapter.OnAfter(ctx, "input", "output", errors.New("err"))
	})

	t.Run("with error in OnAfter", func(t *testing.T) {
		var capturedErr error
		adapter := &CallbackAdapter{
			OnAfterFunc: func(msg, result any, err error) {
				capturedErr = err
			},
		}
		expectedErr := errors.New("handler failed")
		adapter.OnAfter(context.Background(), "in", nil, expectedErr)
		assert.Equal(t, expectedErr, capturedErr)
	})
}

// ---------------------------------------------------------------------------
// 2. TestHandlerAdapter — func(any) (any, error) → Handler[any, any] 签名兼容
// ---------------------------------------------------------------------------

func TestHandlerAdapter(t *testing.T) {
	t.Run("success path", func(t *testing.T) {
		v1Fn := func(msg any) (any, error) {
			s, ok := msg.(string)
			if !ok {
				return nil, errors.New("not a string")
			}
			return s + "-processed", nil
		}

		handler := HandlerAdapter(v1Fn)
		result, err := handler(context.Background(), "input")
		require.NoError(t, err)
		assert.Equal(t, "input-processed", result)
	})

	t.Run("error path", func(t *testing.T) {
		expectedErr := errors.New("boom")
		v1Fn := func(msg any) (any, error) {
			return nil, expectedErr
		}

		handler := HandlerAdapter(v1Fn)
		result, err := handler(context.Background(), "anything")
		assert.Equal(t, expectedErr, err)
		assert.Nil(t, result)
	})

	t.Run("context is passed but ignored by v1 func", func(t *testing.T) {
		var receivedCtx context.Context
		v1Fn := func(msg any) (any, error) {
			return msg, nil
		}
		// Wrap to capture ctx usage
		handler := HandlerAdapter(v1Fn)
		ctx := context.WithValue(context.Background(), "testkey", "testval")
		_, err := handler(ctx, "data")
		// The v1 func itself never sees ctx, so just verify no panic/error
		require.NoError(t, err)
		_ = receivedCtx
	})
}

// ---------------------------------------------------------------------------
// 3. TestV1Group_Map — 3 个 any 输入 → 3 个 any 输出
// ---------------------------------------------------------------------------

func TestV1Group_Map(t *testing.T) {
	t.Run("map processes all inputs", func(t *testing.T) {
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) {
				n, ok := msg.(int)
				if !ok {
					return nil, errors.New("not int")
				}
				return n * 2, nil
			})

		group := NewV1Group(config)
		defer group.Stop()

		inputs := []any{1, 2, 3}
		results := group.Map(inputs)

		require.Len(t, results, 3)
		assert.Equal(t, 2, results[0])
		assert.Equal(t, 4, results[1])
		assert.Equal(t, 6, results[2])
	})

	t.Run("map with errors returns error in result", func(t *testing.T) {
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) {
				n, ok := msg.(int)
				if !ok {
					return nil, errors.New("not int")
				}
				if n == 2 {
					return nil, errors.New("fail on 2")
				}
				return n * 10, nil
			})

		group := NewV1Group(config)
		defer group.Stop()

		results := group.Map([]any{1, 2, 3})
		require.Len(t, results, 3)
		assert.Equal(t, 10, results[0])
		assert.Error(t, results[1].(error)) // v1: error 作为 result
		assert.Equal(t, "fail on 2", results[1].(error).Error())
		assert.Equal(t, 30, results[2])
	})

	t.Run("map with callback config is accepted", func(t *testing.T) {
		// 注意: v2 Group 当前不在执行路径中调用 callback（callback 已存储但未触发）。
		// 此测试验证 V1Config 正确接受 CallbackAdapter 并传递给 v2 Group，
		// callback 的实际触发由 v2 核心层负责（将在未来版本接入）。
		var cbCreated atomic.Bool
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) {
				return msg, nil
			}).
			WithCallback(CallbackAdapter{
				OnBeforeFunc: func(msg any) { cbCreated.Store(true) },
				OnAfterFunc:  func(msg, result any, err error) {},
			})

		group := NewV1Group(config)
		defer group.Stop()

		// V1Group 能正常创建并执行（callback 被传递给 v2）
		results := group.Map([]any{"a", "b", "c"})
		require.Len(t, results, 3)
		// callback 由 v2 Group 在任务执行路径中触发
		assert.True(t, cbCreated.Load(), "v2 Group should invoke callbacks in execution path")
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) { return msg, nil })

		group := NewV1Group(config)
		defer group.Stop()

		results := group.Map(nil)
		assert.Nil(t, results)
	})
}

// ---------------------------------------------------------------------------
// 4. TestV1Pipeline_Submit — Submit any → 无 error
// ---------------------------------------------------------------------------

func TestV1Pipeline_Submit(t *testing.T) {
	t.Run("submit returns no error", func(t *testing.T) {
		var processed atomic.Int32
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) {
				processed.Add(1)
				return msg, nil
			})

		pipeline := NewV1Pipeline(nil, config)
		defer pipeline.Stop()

		err := pipeline.Submit("task1")
		require.NoError(t, err)

		// Pipeline 是异步的，等待 handler 执行完毕
		require.Eventually(t, func() bool {
			return processed.Load() == 1
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("submit after delay", func(t *testing.T) {
		var processed atomic.Int32
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) {
				processed.Add(1)
				return msg, nil
			})

		pipeline := NewV1Pipeline(nil, config)
		defer pipeline.Stop()

		err := pipeline.SubmitAfter("task-delayed", 50*time.Millisecond)
		require.NoError(t, err)

		// 应该还没有被处理（延迟 50ms）
		time.Sleep(20 * time.Millisecond)
		assert.Equal(t, int32(0), processed.Load())

		// 等待延迟过后处理完成
		require.Eventually(t, func() bool {
			return processed.Load() == 1
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("submit with func override", func(t *testing.T) {
		var defaultCalled, overrideCalled atomic.Int32
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) {
				defaultCalled.Add(1)
				return "default", nil
			})

		pipeline := NewV1Pipeline(nil, config)
		defer pipeline.Stop()

		override := func(msg any) (any, error) {
			overrideCalled.Add(1)
			return "override", nil
		}

		err := pipeline.SubmitWithFunc(override, "task-override")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return overrideCalled.Load() == 1
		}, 2*time.Second, 10*time.Millisecond)
		assert.Equal(t, int32(0), defaultCalled.Load())
	})

	t.Run("GetWorkerNumber returns positive", func(t *testing.T) {
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) { return msg, nil })

		pipeline := NewV1Pipeline(nil, config)
		defer pipeline.Stop()

		// executor goroutine 已在 NewPipeline 时启动
		assert.Greater(t, pipeline.GetWorkerNumber(), int64(0))
	})

	t.Run("queue parameter is ignored", func(t *testing.T) {
		config := NewV1Config().
			WithWorkerNumber(2).
			WithHandleFunc(func(msg any) (any, error) { return msg, nil })

		// v1 传入任意 queue 参数，v2 应该忽略
		pipeline := NewV1Pipeline("ignored-queue", config)
		require.NotNil(t, pipeline)
		defer pipeline.Stop()
	})
}

// ---------------------------------------------------------------------------
// 5. TestV1Config_Builder — builder pattern 验证 workers/handler/callback 设置正确
// ---------------------------------------------------------------------------

func TestV1Config_Builder(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		cfg := NewV1Config()
		assert.Equal(t, 2, cfg.workers)
		assert.Nil(t, cfg.callback)
		assert.Nil(t, cfg.handlerFunc)
		assert.False(t, cfg.withResult)
	})

	t.Run("full builder chain", func(t *testing.T) {
		handler := func(msg any) (any, error) { return "handled", nil }
		cb := CallbackAdapter{
			OnBeforeFunc: func(msg any) {},
			OnAfterFunc:  func(msg, result any, err error) {},
		}

		cfg := NewV1Config().
			WithWorkerNumber(8).
			WithHandleFunc(handler).
			WithCallback(cb).
			WithResult()

		assert.Equal(t, 8, cfg.workers)
		assert.NotNil(t, cfg.callback)
		assert.NotNil(t, cfg.handlerFunc)
		assert.True(t, cfg.withResult)
	})

	t.Run("WithWorkerNumber rejects non-positive", func(t *testing.T) {
		cfg := NewV1Config().
			WithWorkerNumber(0).
			WithWorkerNumber(-5)

		assert.Equal(t, 2, cfg.workers) // remains default
	})

	t.Run("WithWorkerNumber accepts positive", func(t *testing.T) {
		cfg := NewV1Config().WithWorkerNumber(1)
		assert.Equal(t, 1, cfg.workers)
	})

	t.Run("chaining returns same pointer", func(t *testing.T) {
		cfg := NewV1Config()
		cfg2 := cfg.WithWorkerNumber(4)
		// Builder pattern: 同一个指针
		assert.Same(t, cfg, cfg2)

		cfg3 := cfg.WithHandleFunc(func(any) (any, error) { return nil, nil })
		assert.Same(t, cfg, cfg3)

		cfg4 := cfg.WithResult()
		assert.Same(t, cfg, cfg4)

		cfg5 := cfg.WithCallback(CallbackAdapter{})
		assert.Same(t, cfg, cfg5)
	})
}
