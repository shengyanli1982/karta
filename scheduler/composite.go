package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	karta "github.com/shengyanli1982/karta/v2"
)

// 编译期接口检查：确保 compositeScheduler 实现 Scheduler 接口
var _ karta.Scheduler = (*compositeScheduler)(nil)

// pumpRetryDelay 是搬运泵向已满的下一级重试入队的间隔。
const pumpRetryDelay = 10 * time.Millisecond

// compositeScheduler 将多个 karta.Scheduler 组合为单一调度器。
//
// 入队写入第一个 scheduler（入口），出队从最后一个 scheduler 取（出口）。
// 相邻两级之间由搬运泵（pump goroutine）搬运任务：泵从第 i 级 Dequeue
// 并 Enqueue 到第 i+1 级，保证任务沿链路流向出口而不会滞留在中间级。
type compositeScheduler struct {
	schedulers []karta.Scheduler
	closed     atomic.Bool
	stopCh     chan struct{} // Shutdown 时关闭，通知搬运泵退出
	wg         sync.WaitGroup
	inflight   atomic.Int64 // 泵已取出、尚未交付下一级的在途任务数
}

// NewCompositeScheduler 创建组合调度器。
// schedulers 按顺序组成处理链路：
//   - Enqueue 写入 schedulers[0]
//   - Dequeue 从 schedulers[len-1] 取出
//   - 相邻级之间的搬运泵自动将任务从第 i 级迁移到第 i+1 级
//   - Shutdown 级联关闭所有 schedulers，并等待搬运泵全部退出
//   - Len 返回各级 Len 之和加上搬运泵的在途任务数，表示链路中的任务总数。
//     注意 Len 是最终一致的快照：任务正被交付给下一级的瞬间会同时计入
//     目标级与在途计数（至多每个活跃的泵多计 1），交付完成后收敛到真实值
//
// 交付语义（lease/retry 类源级 + 下游背压 = 至少一次交付）：下一级已满时，
// 搬运泵按 pumpRetryDelay 退避重试直至交付成功；在此背压窗口内，任务尚未
// Done 确认，若源级为租约/重试类调度器（Lease/Retry），租约过期或重试判定
// 会触发底层将同一逻辑任务重新入队，导致该任务被多次交付与执行——handler
// 副作用可能重复，同一 envelope 的 Future 完成是幂等的、不受影响；需要恰好
// 一次副作用语义的业务方应自行保证 handler 幂等。
func NewCompositeScheduler(schedulers ...karta.Scheduler) karta.Scheduler {
	s := &compositeScheduler{
		schedulers: schedulers,
		stopCh:     make(chan struct{}),
	}
	for i := 0; i+1 < len(schedulers); i++ {
		s.wg.Add(1)
		go s.pump(schedulers[i], schedulers[i+1])
	}
	return s
}

// pump 将任务从 from 级搬运到 to 级，直到调度器关闭或某一端不可用。
func (s *compositeScheduler) pump(from, to karta.Scheduler) {
	defer s.wg.Done()
	for {
		task, err := from.Dequeue(context.Background())
		if err != nil {
			// from 级已关闭（Shutdown 级联）或出错：退出泵
			return
		}
		s.inflight.Add(1)
		delivered := s.deliver(to, task)
		s.inflight.Add(-1)
		if !delivered {
			// to 级已关闭或正在关闭：任务无法交付，退出泵。
			// 关闭期间丢弃在途任务符合 Shutdown 语义。
			return
		}
		// 任务已交付下一级，确认 from 级上的处理完成
		// （对租约类调度器尤其必要，否则租约过期会重复投递任务）。
		from.Done(task)
	}
}

// deliver 将任务入队到下一级：下一级已满（ErrSchedulerFull）时退避重试，
// 直到成功或调度器关闭。返回 false 表示应放弃交付。
func (s *compositeScheduler) deliver(to karta.Scheduler, task *karta.TaskEnvelope) bool {
	for {
		err := to.Enqueue(task)
		if err == nil {
			return true
		}
		if errors.Is(err, karta.ErrSchedulerClosed) || s.closed.Load() {
			return false
		}
		// ErrSchedulerFull 或其他瞬时错误：退避后重试，避免任务丢失
		select {
		case <-s.stopCh:
			return false
		case <-time.After(pumpRetryDelay):
		}
	}
}

func (s *compositeScheduler) Enqueue(task *karta.TaskEnvelope) error {
	if s.closed.Load() {
		return karta.ErrSchedulerClosed
	}
	if len(s.schedulers) == 0 {
		return karta.ErrSchedulerClosed
	}
	return s.schedulers[0].Enqueue(task)
}

func (s *compositeScheduler) Dequeue(ctx context.Context) (*karta.TaskEnvelope, error) {
	if len(s.schedulers) == 0 {
		return nil, karta.ErrSchedulerClosed
	}
	return s.schedulers[len(s.schedulers)-1].Dequeue(ctx)
}

func (s *compositeScheduler) Done(task *karta.TaskEnvelope) {
	if len(s.schedulers) == 0 {
		return
	}
	s.schedulers[len(s.schedulers)-1].Done(task)
}

// Len 返回各级 Len 之和加上在途任务数，快照语义见 NewCompositeScheduler 的说明。
func (s *compositeScheduler) Len() int {
	total := int(s.inflight.Load())
	for _, sched := range s.schedulers {
		total += sched.Len()
	}
	return total
}

// Shutdown 级联关闭所有子调度器并等待搬运泵退出，保证不泄漏 goroutine。
func (s *compositeScheduler) Shutdown() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.stopCh)
		// 先关闭子调度器，使阻塞在 Dequeue 上的泵被唤醒退出
		for _, sched := range s.schedulers {
			sched.Shutdown()
		}
		s.wg.Wait()
	}
}

func (s *compositeScheduler) IsClosed() bool {
	return s.closed.Load()
}
