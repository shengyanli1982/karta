package karta

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
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

// TestSimpleScheduler_Len_NeverNegative — P2-9：并发 Enqueue/Dequeue 下
// Len() 不得出现负值（接口契约"当前队列中的任务数量"非负）。
//
// 竞态根因：旧实现 send 成功后才 len.Add(1)，而接收方（Dequeue / executor
// 快速路径）在收到任务的瞬间即 Add(-1)——直送交接（接收方已阻塞在空队列上）
// 时接收方可能抢先递减，Len() 读到瞬态负值。修复后计数先于 send 递增、
// send 未成功的分支回退：只可能出现瞬态高估（可接受），不可能为负。
// 观察方式为 tight-loop 轮询（窗口纳秒~微秒级，靠高频采样捕获）。
func TestSimpleScheduler_Len_NeverNegative(t *testing.T) {
	s := NewSimpleScheduler(16)
	defer s.Shutdown()

	stop := make(chan struct{})
	var negative atomic.Bool
	var minSeen atomic.Int64
	var wg sync.WaitGroup

	// 生产者：高频入队（满时让出调度，避免纯自旋淹没消费者）
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.Enqueue(&TaskEnvelope{Input: id}); err != nil {
					runtime.Gosched()
				}
			}
		}(i)
	}

	// 消费者：阻塞式出队（制造"接收方已就绪等待直送"的窗口）
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				dqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
				_, err := s.Dequeue(dqCtx)
				cancel()
				_ = err
			}
		}()
	}

	// 观察者：tight-loop 轮询 Len()，捕获任何瞬态负值
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			n := int64(s.Len())
			for {
				old := minSeen.Load()
				if n >= old || minSeen.CompareAndSwap(old, n) {
					break
				}
			}
			if n < 0 {
				negative.Store(true)
				return
			}
		}
	}()

	time.Sleep(time.Second) // 有界采样时长
	close(stop)
	wg.Wait()

	assert.False(t, negative.Load(), "Len() 出现负值（最小观测值 %d）：计数递增必须先于 send", minSeen.Load())
}
