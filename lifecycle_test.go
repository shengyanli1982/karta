package karta

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockComponent 测试用可关闭组件
type mockComponent struct {
	stopped atomic.Bool
	delay   time.Duration
	mu      sync.Mutex
}

func (m *mockComponent) Stop() {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	m.stopped.Store(true)
}

func (m *mockComponent) isStopped() bool {
	return m.stopped.Load()
}

func TestLifecycleManager_Register_And_Shutdown(t *testing.T) {
	c1 := &mockComponent{}
	c2 := &mockComponent{}

	lm := NewLifecycleManager()
	lm.Register(c1, c2)

	lm.Shutdown()

	assert.True(t, c1.isStopped(), "component 1 should be stopped")
	assert.True(t, c2.isStopped(), "component 2 should be stopped")
}

func TestLifecycleManager_Shutdown_Idempotent(t *testing.T) {
	c := &mockComponent{}
	lm := NewLifecycleManager(WithManaged(c))

	require.NotPanics(t, func() {
		lm.Shutdown()
		lm.Shutdown()
		lm.Shutdown()
	}, "multiple Shutdown calls should not panic")

	assert.True(t, c.isStopped())
}

func TestLifecycleManager_ShutdownTimeout(t *testing.T) {
	slow := &mockComponent{delay: 1 * time.Second}
	lm := NewLifecycleManager(
		WithManaged(slow),
		WithShutdownTimeout(100*time.Millisecond),
	)

	start := time.Now()
	lm.Shutdown()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond,
		"Shutdown should return within timeout, not wait for slow component")
	assert.GreaterOrEqual(t, elapsed, 80*time.Millisecond,
		"Shutdown should wait at least until timeout fires")
}
