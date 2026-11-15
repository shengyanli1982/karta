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
// P1-1 回归测试 — limiter 拒绝路径击穿"至少 1 worker"活性不变量
//
// 根因：trySpawnWorker 的 CAS 预留名额（幻影）会骗过 executor 的 idle 退出
// 守卫（守卫以 running 为存活依据），随后 limiter 拒绝归还名额 → running=0
// 且队列有任务、无任何真实 worker → Future 永久挂起、SubmitEvery 永久停摆。
// 修复：拒绝路径归还名额后检测 running 归零（且未 closed）即绕过 limiter
// 强制补 spawn，恢复无条件最低存活不变量。
// 本文件位于 package karta（内部白盒测试，可直接操纵 p.running）。
// ═══════════════════════════════════════════════════════════════

// TestTrySpawnWorker_LimiterReject_RestoresMinWorker 确定性复现 P1-1 竞态
// aftermath 状态（limiter 干涸 + running=0 + 无真实 worker），验证 Submit
// 后任务仍能完成：
//   - 修复前：trySpawnWorker 被 limiter 拒绝后直接归还名额返回，任务永久
//     搁浅，Get 在超时窗口内必然失败（已在旧代码上实测 FAIL：
//     "context deadline exceeded"）；
//   - 修复后：拒绝路径检测到 running 归零，绕过 limiter 强制补 spawn，
//     任务毫秒级完成。
func TestTrySpawnWorker_LimiterReject_RestoresMinWorker(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n * 2, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(64),
		WithSpawnRate(1),                     // 1 token/s、burst=8：耗尽后短期内（<1s）必然拒绝
		WithIdleTimeout(20*time.Millisecond), // 加速 idle 收敛
		WithScanInterval(5*time.Millisecond),
	)
	defer p.Stop()

	// Phase 1: Tune(10) 经真实路径一次性耗尽全部 8 个 burst 令牌
	//（delta=8 → 8 次 trySpawnWorker 全部 Allow 成功并 spawn），随后调回目标。
	// 从这里开始到 Phase 5 的 Submit 必须落在 1s 内，保证令牌未回补。
	require.NoError(t, p.Tune(10))
	require.Eventually(t, func() bool { return p.GetWorkerNumber() == 10 },
		5*time.Second, time.Millisecond, "Tune 调大后 running 应达到目标")
	require.NoError(t, p.Tune(2))

	// Phase 2: 队列空，全部 worker 经 idle 守卫自然收敛到恰好 1 个
	//（最后一个 worker 的守卫 Add(-1) 得 0 会自行恢复，不会退出）。
	// 收敛期间无 Submit/Tune spawn，running 与真实 worker 数始终一致。
	require.Eventually(t, func() bool { return p.GetWorkerNumber() == 1 },
		5*time.Second, time.Millisecond, "空闲后应收敛到 1 个 worker")

	// Phase 3: 伪造 running=3 欺骗 idle 守卫，让最后一个真实 worker 也退出：
	// 其守卫 Add(-1) 得 2≥1，误判"仍有同伴存活"而退出。退出后 running 停在
	// 伪造值 3-1=2 且不再有真实 worker——该稳定值即退出的确定性探测信号。
	p.running.Store(3)
	require.Eventually(t, func() bool { return p.GetWorkerNumber() == 2 },
		5*time.Second, time.Millisecond, "最后 1 个真实 worker 应被幻影名额骗出")

	// Phase 4: running 归零，完成 P1-1 竞态 aftermath 状态构造：
	// running=0、limiter 干涸、无任何真实 worker。
	p.running.Store(0)

	// Phase 5: Submit → trySpawnWorker CAS 预留后被 limiter 拒绝。
	// 修复前：归还名额直接返回，任务搁浅，Get 超时（牙齿：旧代码必 FAIL）；
	// 修复后：拒绝路径兜底补 spawn，任务正常完成。
	f, err := p.Submit(context.Background(), 21)
	require.NoError(t, err)
	getCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	res := f.Get(getCtx)
	require.NoError(t, res.Err, "limiter 拒绝致 running 归零后必须兜底补 spawn，任务不得搁浅")
	assert.Equal(t, 42, res.Value)
	assert.GreaterOrEqual(t, p.GetWorkerNumber(), int64(1), "兜底 spawn 后应至少有 1 个 worker")
}

// TestTrySpawnWorker_LimiterReject_StressNoStrand 有界压力测试（总时长 ≤10s）：
// spawnRate=1（令牌长期干涸，拒绝为常态）+ idleTimeout=1ms + scanInterval=100µs
// （空闲 worker 以 10k 次/s 做守卫判定，放大幻影名额命中守卫窗口的概率）。
// 提交采用"空闲间隔 + 突发"节奏：每轮先睡 3ms 让 worker 进入 idle 守卫舞蹈，
// 再 Submit 制造 CAS 幻影——持续提交会让 worker 一直忙碌、守卫判定不发生，
// 无法复现竞态（牙齿验证第一轮实测教训）。
//
// 双观察器：
//  1. 语义观察：每笔提交 Get(500ms) 必须完成（搁浅时旧代码要等 ~1s 令牌
//     回补才被动解救，超时率 ~50%/笔）；
//  2. 不变量观察：轮询 "running==0 且队列非空"（间隔 200µs 双采样确认，
//     排除修复后兜底路径 refund→CAS 的 ~100ns 良性瞬态）。
//
// 修复前：幻影与守卫重叠 → 存活 worker 全部退出 → running=0 搁浅态持续
// ~1s（直至令牌回补），两个观察器都会大量报警（牙齿）；
// 修复后：拒绝路径归零兜底立即补 spawn，搁浅态不复存在。
func TestTrySpawnWorker_LimiterReject_StressNoStrand(t *testing.T) {
	handler := func(ctx context.Context, n int) (int, error) { return n, nil }
	p := NewPipeline[int, int](handler, NewSimpleScheduler(256),
		WithPipelineWorkers(2),
		WithSpawnRate(1), // 1 token/s：burst 耗尽后拒绝是常态路径
		WithIdleTimeout(time.Millisecond),
		WithScanInterval(100*time.Microsecond), // 高频守卫判定
	)
	defer p.Stop()

	const submitters = 4
	var timeouts, completed, violations atomic.Int64
	deadline := time.Now().Add(4 * time.Second) // 有界总时长（≤10s 预算内）
	var wg sync.WaitGroup

	for g := 0; g < submitters; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				// 空闲间隔：让 worker 落入 idle 守卫舞蹈（持续提交会喂饱
				// worker，守卫判定不再发生，竞态窗口关闭）
				time.Sleep(3 * time.Millisecond)
				f, err := p.Submit(context.Background(), g*1_000_000+i)
				if err != nil {
					continue // 缓冲满等提交错误不计入搁浅
				}
				getCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				res := f.Get(getCtx)
				cancel()
				switch {
				case res.Err == nil:
					completed.Add(1)
				case errors.Is(res.Err, context.DeadlineExceeded):
					// 搁浅判定：无兜底时任务要等 ~1s 令牌回补才被解救，
					// 远超 500ms 窗口；有兜底时毫秒级完成，不会命中此分支。
					timeouts.Add(1)
				default:
					// 其他错误（如 Stop 竞态的 ErrPipelineClosed）不计入
				}
			}
		}(g)
	}

	// 不变量观察器：直接捕获 "running==0 且队列非空" 的搁浅态。
	// 双采样确认（间隔 200µs）：修复后兜底路径存在 refund→CAS(0,1) 的
	// ~100ns 良性瞬态，单次采样可能误报；搁浅态（旧代码）持续 ~1s，
	// 双采样必然命中。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			if p.GetWorkerNumber() == 0 && p.scheduler.Len() > 0 && !p.closed.Load() {
				time.Sleep(200 * time.Microsecond)
				if p.GetWorkerNumber() == 0 && p.scheduler.Len() > 0 && !p.closed.Load() {
					violations.Add(1)
					time.Sleep(10 * time.Millisecond) // 去抖：同一次搁浅不重复计数
				}
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	wg.Wait()
	assert.Zero(t, violations.Load(), "不变量被击穿：running=0 且队列非空（任务搁浅、无真实 worker）")
	assert.Zero(t, timeouts.Load(), "出现搁浅任务：running 归零后 limiter 拒绝路径未兜底补 spawn")
	assert.Positive(t, completed.Load(), "压力窗口内应有任务正常完成（测试有效性自检）")
}
