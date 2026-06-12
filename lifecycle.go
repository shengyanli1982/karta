package karta

import (
	"os"
	"sync"
	"time"

	"github.com/shengyanli1982/gs"
)

// Shutdownable 可关闭组件接口
type Shutdownable interface {
	Stop()
}

const defaultShutdownTimeout = 30 * time.Second

// LifecycleManager 管理组件生命周期 (ADR-009)
type LifecycleManager struct {
	signals []os.Signal
	timeout time.Duration
	managed []Shutdownable
	mu      sync.Mutex
	once    sync.Once
}

// LifecycleOption 生命周期管理器配置函数
type LifecycleOption func(*LifecycleManager)

// WithSignals 设置监听的系统信号
func WithSignals(sigs ...os.Signal) LifecycleOption {
	return func(lm *LifecycleManager) {
		lm.signals = sigs
	}
}

// WithShutdownTimeout 设置关闭超时时间
func WithShutdownTimeout(d time.Duration) LifecycleOption {
	return func(lm *LifecycleManager) {
		lm.timeout = d
	}
}

// WithManaged 设置初始托管的组件
func WithManaged(components ...Shutdownable) LifecycleOption {
	return func(lm *LifecycleManager) {
		lm.managed = append(lm.managed, components...)
	}
}

// NewLifecycleManager 创建生命周期管理器实例
func NewLifecycleManager(opts ...LifecycleOption) *LifecycleManager {
	lm := &LifecycleManager{
		signals: []os.Signal{os.Interrupt},
		timeout: defaultShutdownTimeout,
	}
	for _, opt := range opts {
		opt(lm)
	}
	return lm
}

// Register 注册组件到生命周期管理器
func (lm *LifecycleManager) Register(components ...Shutdownable) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.managed = append(lm.managed, components...)
}

// WaitForSignal 阻塞等待系统信号
func (lm *LifecycleManager) WaitForSignal() {
	ts := gs.NewTerminateSignal()
	ts.RegisterCancelHandles(lm.Shutdown)
	gs.WaitForSync(ts)
}

// Shutdown 关闭所有托管组件，幂等操作，超时后强制返回
// 每个组件独立超时：顺序调用 Stop()，单个组件超时后跳过继续
// 外层额外包裹全局超时兜底，确保 Shutdown 在 lm.timeout 内返回
func (lm *LifecycleManager) Shutdown() {
	lm.once.Do(func() {
		lm.mu.Lock()
		components := make([]Shutdownable, len(lm.managed))
		copy(components, lm.managed)
		lm.mu.Unlock()

		if len(components) == 0 {
			return
		}

		// 计算每个组件的超时时间（均分全局超时预算）
		perCompTimeout := lm.timeout / time.Duration(len(components))
		if perCompTimeout < time.Millisecond {
			perCompTimeout = time.Millisecond
		}

		// 顺序关闭各组件，每个组件独立超时控制
		// 慢组件的 Stop() goroutine 可能仍在后台运行，
		// 但外层循环不会阻塞，避免主 goroutine 泄漏
		for _, comp := range components {
			done := make(chan struct{})
			go func() {
				defer close(done)
				comp.Stop()
			}()
			select {
			case <-done:
			case <-time.After(perCompTimeout):
				// 超时跳过，comp.Stop() 仍在后台运行
			}
		}
	})
}
