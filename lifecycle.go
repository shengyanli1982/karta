package karta

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
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
	closed  bool // mu 保护：Shutdown 后置位，Register 据此走立即停止路径
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
// 默认监听 SIGINT/SIGTERM，可通过 WithSignals 覆盖
func NewLifecycleManager(opts ...LifecycleOption) *LifecycleManager {
	lm := &LifecycleManager{
		signals: []os.Signal{os.Interrupt, syscall.SIGTERM},
		timeout: defaultShutdownTimeout,
	}
	for _, opt := range opts {
		opt(lm)
	}
	return lm
}

// Register 注册组件到生命周期管理器。
// 若管理器已 Shutdown，组件不再被跟踪，而是立即在调用方 goroutine
// 按与 Shutdown 相同的每组件超时策略执行 Stop，避免迟到注册被静默忽略、组件永不关闭。
func (lm *LifecycleManager) Register(components ...Shutdownable) {
	lm.mu.Lock()
	if lm.closed {
		lm.mu.Unlock()
		lm.stopComponents(components)
		return
	}
	lm.managed = append(lm.managed, components...)
	lm.mu.Unlock()
}

// WaitForSignal 阻塞等待系统信号，收到任一监听信号后执行 Shutdown 并返回。
// 监听集合由 WithSignals 配置；为空时回退默认集合 SIGINT/SIGTERM。
func (lm *LifecycleManager) WaitForSignal() {
	sigs := lm.signals
	if len(sigs) == 0 {
		sigs = []os.Signal{os.Interrupt, syscall.SIGTERM}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	defer signal.Stop(ch)

	<-ch
	lm.Shutdown()
}

// Shutdown 关闭所有托管组件，幂等操作，超时后强制返回
// 每个组件独立超时（均分 lm.timeout 预算）：顺序调用 Stop()，单个组件超时后跳过继续
func (lm *LifecycleManager) Shutdown() {
	lm.once.Do(func() {
		lm.mu.Lock()
		lm.closed = true
		components := make([]Shutdownable, len(lm.managed))
		copy(components, lm.managed)
		lm.mu.Unlock()

		lm.stopComponents(components)
	})
}

// stopComponents 顺序停止组件，均分 lm.timeout 作为每组件超时预算。
// 慢组件的 Stop() goroutine 可能仍在后台运行，
// 但循环不会阻塞，避免调用方 goroutine 泄漏。
func (lm *LifecycleManager) stopComponents(components []Shutdownable) {
	if len(components) == 0 {
		return
	}

	// 计算每个组件的超时时间（均分全局超时预算）
	perCompTimeout := lm.timeout / time.Duration(len(components))
	if perCompTimeout < time.Millisecond {
		perCompTimeout = time.Millisecond
	}

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
}
