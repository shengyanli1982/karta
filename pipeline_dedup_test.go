package karta

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════
// P1-3 验收测试 — WithDedupKey：任务去重 / singleflight 语义
//
// 采用 Pipeline 层方案（非 Decorator Scheduler）：Pipeline 在 Enqueue 前已
// 创建 future，decorator 拒绝入队会导致第二个 future 无人 Resolve 而永久阻塞；
// 故 dedup 登记直接存 future，同 key 在途提交共享首个 future。
// 本文件位于 package karta（内部测试），可直接断言私有 p.dedup 是否清空。
// ═══════════════════════════════════════════════════════════════

// awaitResult 带超时的 Future.Get，超时视为测试失败（内部测试辅助）
func awaitResult[T any](t *testing.T, f *Future[T]) Result[T] {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return f.Get(ctx)
}

// neverDeliverScheduler 的 Enqueue 成功但任务永不交付（Dequeue 阻塞至 ctx 取消），
// 用于构造"dedup 已登记但任务从不执行"的确定场景，从而触发 Stop 的 clear(p.dedup)
// （任务不执行 → executor 不会走 removeDedup，登记项必然存活到 Stop 清理）。
type neverDeliverScheduler struct {
	closed atomic.Bool
}

var _ Scheduler = (*neverDeliverScheduler)(nil)

func (s *neverDeliverScheduler) Enqueue(*TaskEnvelope) error { return nil }

func (s *neverDeliverScheduler) Dequeue(ctx context.Context) (*TaskEnvelope, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *neverDeliverScheduler) Done(*TaskEnvelope) {}

func (s *neverDeliverScheduler) Len() int { return 0 }

func (s *neverDeliverScheduler) Shutdown() { s.closed.Store(true) }

func (s *neverDeliverScheduler) IsClosed() bool { return s.closed.Load() }

// dedupLen 在锁内读取 p.dedup 长度（内部断言辅助）
func dedupLen[In, Out any](p *Pipeline[In, Out]) int {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	return len(p.dedup)
}

// TestDedup_ConcurrentSameKey_SingleExecution 验证并发提交相同 key N 次：
// handler 调用计数 = 1，所有调用方拿到同一 future、同一结果（Spec P1-3 验收）
func TestDedup_ConcurrentSameKey_SingleExecution(t *testing.T) {
	const N = 16
	const input = 7
	var calls atomic.Int32
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		startOnce.Do(func() { close(handlerStarted) })
		<-release // 阻塞至全部提交完成，确保在途窗口内 dedup 登记不被清除
		return n * 2, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(4))
	defer p.Stop()

	futures := make([]*Future[int], N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // 全部 goroutine 同时开始提交，制造并发竞态
			f, err := p.Submit(context.Background(), input, WithDedupKey("same-key"))
			if err != nil {
				return
			}
			futures[idx] = f
		}(i)
	}
	close(start)
	wg.Wait()

	// 等待首个任务进入执行（确认 handler 已启动且在途）
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("handler 未启动")
	}

	// 所有调用方拿到同一 future 指针
	require.NotNil(t, futures[0])
	for i := 1; i < N; i++ {
		assert.Same(t, futures[0], futures[i], "同 key 并发提交应共享同一 future")
	}
	assert.Equal(t, int32(1), calls.Load(), "同 key 只应真实执行一次")

	close(release)
	// 全部调用方拿到同一结果（input=7 → 14）
	for i := 0; i < N; i++ {
		res := awaitResult(t, futures[i])
		require.NoError(t, res.Err)
		assert.Equal(t, input*2, res.Value)
	}
	assert.Equal(t, int32(1), calls.Load(), "共享 future 不应触发额外执行")
}

// TestDedup_DifferentKeys_Independent 验证不同 key 互不影响，各自独立执行
func TestDedup_DifferentKeys_Independent(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		return n * 10, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(4))
	defer p.Stop()

	fa, err := p.Submit(context.Background(), 1, WithDedupKey("key-a"))
	require.NoError(t, err)
	fb, err := p.Submit(context.Background(), 2, WithDedupKey("key-b"))
	require.NoError(t, err)

	assert.NotSame(t, fa, fb, "不同 key 应得到独立 future")
	ra := awaitResult(t, fa)
	rb := awaitResult(t, fb)
	require.NoError(t, ra.Err)
	require.NoError(t, rb.Err)
	assert.Equal(t, 10, ra.Value)
	assert.Equal(t, 20, rb.Value)
	assert.Equal(t, int32(2), calls.Load(), "不同 key 各自真实执行")
}

// TestDedup_AfterCompletion_ResubmitExecutes 验证任务完成（future Resolve）后
// 同 key 可再次提交并再次真实执行（removeDedup 已在完成路径清除登记）
func TestDedup_AfterCompletion_ResubmitExecutes(t *testing.T) {
	var calls atomic.Int32
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		return n, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	f1, err := p.Submit(context.Background(), 1, WithDedupKey("K"))
	require.NoError(t, err)
	r1 := awaitResult(t, f1) // 等待首次任务完成
	require.NoError(t, r1.Err)
	assert.Equal(t, 1, r1.Value)
	assert.Equal(t, int32(1), calls.Load())
	// 生产路径 Resolve 先于 removeDedup：Get 返回瞬间 dedup 登记可能尚未清除，
	// 用 Eventually 等待异步清理完成后再执行重提交断言（消除理论 flake 窗口）
	assert.Eventually(t, func() bool { return dedupLen(p) == 0 },
		time.Second, 10*time.Millisecond, "完成后 dedup 登记应被清除")

	// 完成后同 key 再次提交 → 新 future、再次真实执行
	f2, err := p.Submit(context.Background(), 2, WithDedupKey("K"))
	require.NoError(t, err)
	assert.NotSame(t, f1, f2, "完成后同 key 应得到全新 future")
	r2 := awaitResult(t, f2)
	require.NoError(t, r2.Err)
	assert.Equal(t, 2, r2.Value)
	assert.Equal(t, int32(2), calls.Load(), "完成后同 key 应再次真实执行")
}

// TestDedup_DuringExecution_StillShares 验证执行期间（handler 阻塞中）同 key
// 提交仍共享首个 future。这是"dedup 存 future 而非查 pending"设计的关键：
// executor 执行 handler 前已把 future 从 pending 摘除，若错误地间接查 pending
// 会 miss 并触发第二次真实执行。
func TestDedup_DuringExecution_StillShares(t *testing.T) {
	var calls atomic.Int32
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		startOnce.Do(func() { close(handlerStarted) })
		<-release
		return n * 100, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	f1, err := p.Submit(context.Background(), 1, WithDedupKey("K"))
	require.NoError(t, err)
	<-handlerStarted // 首个任务已进入 handler（此时 future 已从 pending 摘除）

	// 执行期间提交同 key：dedup 仍持有登记 → 共享 f1（不会因 pending 已空而重复执行）
	f2, err := p.Submit(context.Background(), 2, WithDedupKey("K"))
	require.NoError(t, err)
	assert.Same(t, f1, f2, "执行期间同 key 应共享首个 future")

	close(release)
	r1 := awaitResult(t, f1)
	r2 := awaitResult(t, f2)
	require.NoError(t, r1.Err)
	require.NoError(t, r2.Err)
	assert.Equal(t, 100, r1.Value)
	assert.Equal(t, r1.Value, r2.Value, "共享 future 结果一致")
	assert.Equal(t, int32(1), calls.Load(), "执行期间同 key 不应触发第二次执行")
}

// TestDedup_FirstInputWins_SubsequentIgnored 验证 singleflight 语义：
// 首次登记任务的 input 生效，后续同 key 提交的 input 被忽略
func TestDedup_FirstInputWins_SubsequentIgnored(t *testing.T) {
	var calls atomic.Int32
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	handler := func(ctx context.Context, n int) (int, error) {
		calls.Add(1)
		startOnce.Do(func() { close(handlerStarted) })
		<-release
		return n * 10, nil
	}
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64), WithPipelineWorkers(2))
	defer p.Stop()

	// 首次提交 input=1（登记 dedup[K] 并入队），Submit 同步返回时登记已完成
	f1, err := p.Submit(context.Background(), 1, WithDedupKey("K"))
	require.NoError(t, err)
	<-handlerStarted

	// 后续提交 input=2 同 key：共享 f1，input=2 被丢弃（不入队、不执行）
	f2, err := p.Submit(context.Background(), 2, WithDedupKey("K"))
	require.NoError(t, err)
	require.Same(t, f1, f2)

	close(release)
	r := awaitResult(t, f1)
	require.NoError(t, r.Err)
	assert.Equal(t, 10, r.Value, "应以首次提交的 input(1) 执行，后续 input(2) 被忽略")
	assert.Equal(t, int32(1), calls.Load())
}

// TestDedup_Stop_ClearsDedup 验证 Stop 后 dedup 清空（无泄漏）且 Stop 不阻塞。
// 用 neverDeliverScheduler 保证任务永不执行 → 登记项存活到 Stop 的 clear(p.dedup)。
func TestDedup_Stop_ClearsDedup(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	sched := &neverDeliverScheduler{}
	p := NewPipeline[int, int](handler, sched,
		WithPipelineWorkers(2),
		WithScanInterval(10*time.Millisecond),
	)

	// Enqueue 成功但任务永不交付：dedup[K]、pending[id] 均登记后无法经完成路径清除
	f1, err := p.Submit(context.Background(), 1, WithDedupKey("K"))
	require.NoError(t, err)
	f2, err := p.Submit(context.Background(), 2, WithDedupKey("K"))
	require.NoError(t, err)
	assert.Same(t, f1, f2)

	// Stop 前：dedup 有 1 条登记
	assert.Equal(t, 1, dedupLen(p), "Stop 前应存在 dedup 登记")

	// Stop 必须限时返回（Dequeue 随 p.ctx 取消立即唤醒，wg.Wait 不阻塞）
	stopped := make(chan struct{})
	go func() {
		p.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 阻塞：dedup/pending 残留或 goroutine 泄漏")
	}

	// 未执行的共享 future 被 Stop 清理为 ErrPipelineClosed（不永久阻塞）
	res := f1.Get(context.Background())
	assert.ErrorIs(t, res.Err, ErrPipelineClosed)

	// Stop 后：dedup 清空（无内存泄漏）
	assert.Zero(t, dedupLen(p), "Stop 应清空 dedup")
}

// TestDedup_EnqueueFail_NoResidue 验证 Enqueue 失败路径 dedup 无残留：
// 失败后同 key 再次提交不会误共享一个永不 Resolve 的孤儿 future
func TestDedup_EnqueueFail_NoResidue(t *testing.T) {
	enqueueErr := errors.New("karta test: enqueue refused")
	sched := &enqueueFailScheduler{err: enqueueErr} // 复用 pipeline_test.go 内的失败调度器
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, sched)
	defer p.Stop()

	// 提交 dedup 任务，Enqueue 失败 → 返回 SubmitError，且撤销 dedup/pending 登记
	_, err := p.Submit(context.Background(), 1, WithDedupKey("K"))
	require.Error(t, err)
	var se *SubmitError
	assert.ErrorAs(t, err, &se, "Enqueue 失败应返回 *SubmitError")
	assert.ErrorIs(t, err, enqueueErr)

	assert.Zero(t, dedupLen(p), "Enqueue 失败后 dedup 不应残留")
	p.pendingMu.Lock()
	assert.Zero(t, len(p.pending), "Enqueue 失败后 pending 不应残留")
	p.pendingMu.Unlock()

	// 无残留则同 key 再次提交不会误命中共享：应再次走 Enqueue（仍失败），返回 error
	_, err2 := p.Submit(context.Background(), 2, WithDedupKey("K"))
	require.Error(t, err2, "无残留时同 key 再次提交应重新入队（再次失败），而非共享孤儿 future")
	assert.Zero(t, dedupLen(p))
}

// blockingFailEnqueueScheduler 的 Enqueue 先通知 entered 再阻塞等待 release，
// 随后返回固定错误。用于确定性撑开"dedup/pending 登记完成（锁已释放）"与
// "Enqueue 失败返回"之间的窗口，让第二个提交者在窗口内 dedup 命中共享 future
// （P1-1 回归的实证时序：dedup 命中发生在 Enqueue 失败之前）。
// 本文件测试保证 Enqueue 恰好被调用一次（共享方命中 dedup 后不再入队）。
type blockingFailEnqueueScheduler struct {
	entered chan struct{} // 容量 1，非阻塞通知：Enqueue 已进入（登记已完成）
	release chan struct{} // close 后 Enqueue 返回 err
	err     error
	closed  atomic.Bool
}

var _ Scheduler = (*blockingFailEnqueueScheduler)(nil)

func (s *blockingFailEnqueueScheduler) Enqueue(*TaskEnvelope) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return s.err
}

func (s *blockingFailEnqueueScheduler) Dequeue(ctx context.Context) (*TaskEnvelope, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *blockingFailEnqueueScheduler) Done(*TaskEnvelope) {}

func (s *blockingFailEnqueueScheduler) Len() int { return 0 }

func (s *blockingFailEnqueueScheduler) Shutdown() { s.closed.Store(true) }

func (s *blockingFailEnqueueScheduler) IsClosed() bool { return s.closed.Load() }

// TestDedup_EnqueueFail_SharedFutureResolves — P1-1 回归：提交者 A 登记
// dedup[K]=futureA 后释放锁再 Enqueue（阻塞失败）；窗口内提交者 B dedup 命中
// 拿到 (futureA, nil)。A 的 Enqueue 失败回滚必须 Resolve futureA（包
// SubmitError，与直接提交者同构），否则 B 的 Get 永久挂死——futureA 已不在
// pending，Stop 的清理也覆盖不到，成为永久孤儿。
func TestDedup_EnqueueFail_SharedFutureResolves(t *testing.T) {
	enqueueErr := errors.New("karta test: enqueue refused")
	sched := &blockingFailEnqueueScheduler{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		err:     enqueueErr,
	}
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, sched)
	defer p.Stop()

	// A：goroutine 提交，Enqueue 阻塞在调度器内（此时 dedup[K]/pending 已登记）
	submitErr := make(chan error, 1)
	go func() {
		_, err := p.Submit(context.Background(), 1, WithDedupKey("K"))
		submitErr <- err
	}()
	<-sched.entered // A 已进入 Enqueue：登记完成且锁已释放，窗口已撑开

	// B：窗口内提交同 key → dedup 命中，拿到共享 future（确定性时序）
	shared, err := p.Submit(context.Background(), 2, WithDedupKey("K"))
	require.NoError(t, err)
	require.NotNil(t, shared)

	// 放行 A 的 Enqueue → 返回错误并回滚登记；修复要求返回前 Resolve 共享 future
	close(sched.release)
	errA := <-submitErr
	require.Error(t, errA)
	var seA *SubmitError
	assert.ErrorAs(t, errA, &seA, "直接提交者应收到 *SubmitError")

	// B 的 Get 必须收到 SubmitError 而非挂死（修复前：永久阻塞，
	// awaitResult 的 10s 超时兜底后 ErrorAs 断言失败）
	res := awaitResult(t, shared)
	require.Error(t, res.Err, "共享 future 应在 A 的 Enqueue 失败回滚时被 Resolve")
	var se *SubmitError
	assert.ErrorAs(t, res.Err, &se, "共享方应收到 *SubmitError（与直接提交者同构）")
	assert.ErrorIs(t, res.Err, enqueueErr)

	// 回滚后 dedup/pending 无残留
	assert.Zero(t, dedupLen(p))
	p.pendingMu.Lock()
	assert.Zero(t, len(p.pending))
	p.pendingMu.Unlock()
}
