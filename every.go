package karta

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SubmitEvery 以固定间隔周期性提交任务到 Pipeline（P2-5 周期触发薄封装）。
//
// 语义：
//   - 非阻塞：本函数立即返回，周期提交由内部 goroutine + time.Ticker 驱动；
//   - skip-if-busy：若上次提交的 Future 尚未完成，跳过本次 tick——慢任务
//     不堆积（规避 gocron 式 piled-up executions 痛点）；
//   - Submit 返回 ErrPipelineClosed 时内部 goroutine 自动退出（无需显式
//     stop）；其他提交错误（如调度器满 ErrSchedulerFull）仅跳过本次，
//     下个 tick 继续；
//   - ctx 取消或调用返回的 stop() 后停止；stop 幂等，可安全多次调用。
//     stop 为异步语义：仅发出停止信号（关闭内部 channel），不等待内部
//     goroutine 退出，停止瞬间至多再发生一次在途提交。
//
// 使用示例：
//
//	stop := karta.SubmitEvery(ctx, pipe, 5*time.Minute, CleanupInput{})
//	defer stop()
//
// 需要纳入优雅停机时，用 ctx 派生自 LifecycleManager 管理的生命周期，
// 或将 stop 挂接到任意 Shutdownable 组件的停止路径（cancel ctx 即可停止）。
//
// Panics: interval 必须为正值，否则 panic
// （与 NewPipeline 对 nil scheduler 的参数校验风格一致）。
func SubmitEvery[In, Out any](ctx context.Context, pipe *Pipeline[In, Out], interval time.Duration, input In) (stop func()) {
	if interval <= 0 {
		panic("karta: interval must be positive")
	}

	stopCh := make(chan struct{})
	var once sync.Once
	stop = func() {
		once.Do(func() { close(stopCh) })
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var inflight *Future[Out] // 上次成功提交的 Future；nil 表示当前无在途任务
		for {
			select {
			case <-ticker.C:
				// skip-if-busy：上次任务尚未 Resolve（futureResolved 位未置）
				// 则跳过本次 tick。包内直接读 state 状态字，与 Future.Get
				// 快速路径同款 atomic Load，无需新增导出方法。
				if inflight != nil && inflight.state.Load()&futureResolved == 0 {
					continue
				}
				f, err := pipe.Submit(ctx, input)
				if err != nil {
					if errors.Is(err, ErrPipelineClosed) {
						return // Pipeline 已关闭：周期提交失去意义，退出
					}
					continue // 其他提交错误：跳过本次，下个 tick 重试
				}
				inflight = f
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			}
		}
	}()

	return stop
}
