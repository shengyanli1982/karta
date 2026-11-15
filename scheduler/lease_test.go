package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// 编译期接口断言
var _ karta.Scheduler = NewLeaseScheduler(5 * time.Second)

func TestLease_Basic(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "lease-basic"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-basic", env.Input)
	s.Done(env)
}

func TestLease_AckAfterDone(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "lease-ack"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-ack", env.Input)

	// Done 应成功（内部 Ack）
	s.Done(env)

	// 再次调用 Done 不应 panic（leaseID 已不存在，静默忽略）
	s.Done(env)
}

func TestLease_Shutdown(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	assert.False(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	s.Shutdown()
	assert.True(t, s.IsClosed())

	err := s.Enqueue(&karta.TaskEnvelope{Input: "test"})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestLease_DequeueContextCancel(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	env, err := s.Dequeue(ctx)
	assert.Nil(t, env)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLease_MultipleTasks(t *testing.T) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	for i := 0; i < 5; i++ {
		require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: i}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		env, err := s.Dequeue(ctx)
		require.NoError(t, err)
		assert.Equal(t, i, env.Input)
		s.Done(env)
	}
}

// TestLease_RedeliveryOwnership 验证租约过期重投递的所有权契约：
// 第一个消费者持有任务但不 Done，租约过期后任务被重新投递，
// 第二个消费者拿到的必须是不同的 *TaskEnvelope（浅拷贝），
// 且两者的 Done 互不干扰。
func TestLease_RedeliveryOwnership(t *testing.T) {
	s := NewLeaseScheduler(20 * time.Millisecond)
	defer s.Shutdown()

	task := &karta.TaskEnvelope{Input: "lease-redeliver"}
	require.NoError(t, s.Enqueue(task))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 消费者 A 取走任务但不 Done，等待租约过期触发重投递
	envA, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-redeliver", envA.Input)

	time.Sleep(30 * time.Millisecond)

	// 消费者 B 拿到重投递的任务
	envB, err := s.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "lease-redeliver", envB.Input)

	// 拷贝交付：两个消费者不得持有同一指针
	assert.NotSame(t, envA, envB)

	// A、B 各自安全完成：A 的租约已过期（Ack 失败被静默忽略），
	// B 的 Done 通过拷贝找回 leaseID，正确 Ack 底层对原对象的租约
	s.Done(envA)
	s.Done(envB)

	// B 已完成确认：任务不应再次被投递出来
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer shortCancel()
	envC, err := s.Dequeue(shortCtx)
	assert.Nil(t, envC)
	assert.Error(t, err)
}

// TestLease_DoneCopyAcksUnderlyingLease 验证正常路径下 Done(拷贝)
// 能正确 Ack 底层租约：完成确认后的任务不会被重投递。
func TestLease_DoneCopyAcksUnderlyingLease(t *testing.T) {
	s := NewLeaseScheduler(20 * time.Millisecond)
	defer s.Shutdown()

	require.NoError(t, s.Enqueue(&karta.TaskEnvelope{Input: "ack-check"}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := s.Dequeue(ctx)
	require.NoError(t, err)
	s.Done(env)

	// 等待超过租约时长：若 Ack 未生效，任务会被当作过期租约重新投递
	idleCtx, idleCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer idleCancel()
	env2, err := s.Dequeue(idleCtx)
	assert.Nil(t, env2)
	assert.Error(t, err)
}

// TestLease_ConcurrentEnqueueDequeueDone — T4 并发压力（-race 下运行）：
// 多 goroutine 并发 Enqueue/Dequeue/Done，混入 1/10 故意不 Done 的任务
// 触发租约过期重投递（deliver 的 prev 清理路径），drain 后白盒断言
// leases/owners 双 map 收支平衡（无泄漏）。map+Mutex 实现下验证无数据
// 竞争、无死锁。
func TestLease_ConcurrentEnqueueDequeueDone(t *testing.T) {
	s := NewLeaseScheduler(20 * time.Millisecond)
	defer s.Shutdown()

	const workers = 4
	const iterations = 100
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := s.Enqueue(&karta.TaskEnvelope{Input: seed*iterations + i}); err != nil {
					return
				}
				env, err := s.Dequeue(ctx)
				if err != nil {
					return
				}
				// 1/10 故意不 Done：租约过期触发重投递，
				// 由 drain 阶段消费并 Done，验证 deliver 的 prev 清理
				if (seed+i)%10 == 0 {
					continue
				}
				s.Done(env)
			}
		}(w)
	}
	wg.Wait()

	// drain：消费全部重投递任务并 Done；重投递扫描间隔 5ms，
	// 500ms 空转窗口足以确认队列已排空
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer drainCancel()
	drained := 0
	for {
		env, err := s.Dequeue(drainCtx)
		if err != nil {
			break
		}
		s.Done(env)
		drained++
	}
	assert.Positive(t, drained, "被放弃的任务应经租约过期重投递后被 drain 消费")

	// 白盒：映射收支平衡，无泄漏
	ls := s.(*leaseScheduler)
	ls.mu.Lock()
	defer ls.mu.Unlock()
	assert.Empty(t, ls.leases, "leases 映射不得泄漏")
	assert.Empty(t, ls.owners, "owners 映射不得泄漏")
}

// BenchmarkLeaseScheduler_EnqueueDequeue 测量 Lease 调度器在并发场景下
// Enqueue + Dequeue 的吞吐（单 goroutine 循环：入队后立即出队）。
//
// 卫生约束：每迭代新建 envelope 且出队后必须 Done——旧写法跨迭代复用同一
// 指针且从不 Done，底层租约登记（以 leaseID 为键）随迭代无界增长，过期租约
// 还会被 reaper 重投递回队列，测的是泄漏路径而非真实吞吐。Done 会 Ack 租约，
// 保持底层租约表与适配器 leases/owners 映射收支平衡。
func BenchmarkLeaseScheduler_EnqueueDequeue(b *testing.B) {
	s := NewLeaseScheduler(5 * time.Second)
	defer s.Shutdown()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			env := &karta.TaskEnvelope{Input: 1}
			if err := s.Enqueue(env); err != nil {
				continue
			}
			got, err := s.Dequeue(ctx)
			if err == nil {
				s.Done(got)
			}
		}
	})
}
