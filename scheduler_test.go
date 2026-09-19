package karta

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleScheduler_EnqueueDequeue_FIFO(t *testing.T) {
	s := NewSimpleScheduler(64)
	defer s.Shutdown()

	// 入队 5 个任务，携带序号作为输入
	const taskCount = 5
	for i := 0; i < taskCount; i++ {
		err := s.Enqueue(&TaskEnvelope{Input: i, CreatedAt: time.Now()})
		require.NoError(t, err)
	}

	// FIFO 顺序出队并验证
	ctx := context.Background()
	for i := 0; i < taskCount; i++ {
		task, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, task.Input, "第 %d 个任务应为 %d", i, i)
	}
}

func TestSimpleScheduler_Dequeue_ContextCancel(t *testing.T) {
	s := NewSimpleScheduler(64)
	defer s.Shutdown()

	// 空队列上 Dequeue，50ms 超时后应返回 context.DeadlineExceeded
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := s.Dequeue(ctx)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSimpleScheduler_Len(t *testing.T) {
	s := NewSimpleScheduler(64)
	defer s.Shutdown()

	// 初始长度应为 0
	assert.Equal(t, 0, s.Len())

	// 入队 2 个任务
	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: "a"}))
	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: "b"}))
	assert.Equal(t, 2, s.Len())

	// 出队 1 个，长度应减 1
	ctx := context.Background()
	task, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "a", task.Input)
	assert.Equal(t, 1, s.Len())

	// 出队第 2 个
	task, err = s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "b", task.Input)
	assert.Equal(t, 0, s.Len())
}

func TestSimpleScheduler_Shutdown(t *testing.T) {
	s := NewSimpleScheduler(64)

	// 初始未关闭
	assert.False(t, s.IsClosed())

	// Shutdown 后 IsClosed 为 true
	s.Shutdown()
	assert.True(t, s.IsClosed())

	// 再次 Shutdown 应幂等，不 panic
	s.Shutdown()
	assert.True(t, s.IsClosed())

	// Shutdown 后 Enqueue 应返回 ErrSchedulerClosed
	err := s.Enqueue(&TaskEnvelope{Input: "should fail"})
	assert.ErrorIs(t, err, ErrSchedulerClosed)
}

func TestSimpleScheduler_ImplementsInterface(t *testing.T) {
	// 编译期检查：NewSimpleScheduler 返回的类型满足 Scheduler 接口
	var s Scheduler = NewSimpleScheduler(64)
	defer s.Shutdown()
	assert.NotNil(t, s)
}

// TestSimpleScheduler_EnqueueAfterShutdown — 钉住不变量 1：
// Shutdown 后 Enqueue 必须返回 ErrSchedulerClosed（closed.Load fast-path）。
// 多次调用确保 fast-path 稳定，不受缓冲状态影响。
func TestSimpleScheduler_EnqueueAfterShutdown(t *testing.T) {
	s := NewSimpleScheduler(64)
	s.Shutdown()

	for i := 0; i < 10; i++ {
		err := s.Enqueue(&TaskEnvelope{Input: i})
		assert.ErrorIs(t, err, ErrSchedulerClosed, "第 %d 次 Enqueue 后应返回 ErrSchedulerClosed", i)
	}
}

// TestSimpleScheduler_DequeueAfterShutdown_DrainsBuffer — 钉住不变量 2 与 6：
// Shutdown 后 Dequeue 必须先返回剩余缓冲项（FIFO 顺序），缓冲空后返回
// ErrSchedulerClosed（drain 语义）；drain 过程中 len 计数须一致。
func TestSimpleScheduler_DequeueAfterShutdown_DrainsBuffer(t *testing.T) {
	s := NewSimpleScheduler(64)

	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: 1}))
	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: 2}))
	require.NoError(t, s.Enqueue(&TaskEnvelope{Input: 3}))
	assert.Equal(t, 3, s.Len())

	// Shutdown 后不再 close(ch)（新实现），但 drain 语义必须保留
	s.Shutdown()
	assert.True(t, s.IsClosed())

	ctx := context.Background()

	// 必须按 FIFO 顺序排空剩余缓冲项
	for i, expected := range []any{1, 2, 3} {
		task, err := s.Dequeue(ctx)
		require.NoError(t, err, "第 %d 个 drain 应返回缓冲任务而非 ErrSchedulerClosed", i)
		assert.Equal(t, expected, task.Input)
		assert.Equal(t, 2-i, s.Len(), "drain 后 len 应递减")
	}

	// 缓冲空后必须返回 ErrSchedulerClosed
	_, err := s.Dequeue(ctx)
	assert.ErrorIs(t, err, ErrSchedulerClosed)
	assert.Equal(t, 0, s.Len())

	// Shutdown 后 Enqueue 必须返回 ErrSchedulerClosed
	err = s.Enqueue(&TaskEnvelope{Input: 99})
	assert.ErrorIs(t, err, ErrSchedulerClosed)
}

// TestSimpleScheduler_ConcurrentEnqueueShutdown_NoPanic — 钉住不变量 3 与 4：
// 并发 Enqueue + Shutdown 不得 panic（ch 永不关闭，无 send-on-closed 风险；
// 旧实现由 mu 保护）。-race 验证无数据竞争。Shutdown 幂等（once）。
func TestSimpleScheduler_ConcurrentEnqueueShutdown_NoPanic(t *testing.T) {
	s := NewSimpleScheduler(64)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 并发 Enqueue goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				// send 不得 panic：新实现 ch 永不关闭，旧实现 mu 保护
				_ = s.Enqueue(&TaskEnvelope{Input: i})
				i++
			}
		}
	}()

	// 让 Enqueue 跑一会，建立并发窗口
	time.Sleep(10 * time.Millisecond)

	// 并发 Shutdown（幂等，多次调用也安全）
	s.Shutdown()
	s.Shutdown()
	close(stop)
	wg.Wait()

	assert.True(t, s.IsClosed())
}
