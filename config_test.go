package karta

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithGroupWorkers_Valid(t *testing.T) {
	opts := defaultGroupOptions()
	WithGroupWorkers(4)(opts)
	assert.Equal(t, 4, opts.workers)
}

func TestWithGroupWorkers_InvalidTooLow(t *testing.T) {
	opts := defaultGroupOptions()
	WithGroupWorkers(0)(opts)
	assert.Equal(t, int(defaultMinWorkerNum), opts.workers)
}

func TestWithGroupWorkers_InvalidTooHigh(t *testing.T) {
	opts := defaultGroupOptions()
	WithGroupWorkers(999999999)(opts)
	assert.Equal(t, int(defaultMinWorkerNum), opts.workers)
}

func TestWithPipelineWorkers(t *testing.T) {
	opts := defaultPipelineOptions()
	WithPipelineWorkers(8)(opts)
	assert.Equal(t, 8, opts.workers)
}

func TestWithIdleTimeout(t *testing.T) {
	opts := defaultPipelineOptions()
	WithIdleTimeout(30 * time.Second)(opts)
	assert.Equal(t, 30*time.Second, opts.idleTimeout)
}

func TestWithScanInterval(t *testing.T) {
	opts := defaultPipelineOptions()
	WithScanInterval(5 * time.Second)(opts)
	assert.Equal(t, 5*time.Second, opts.scanInterval)
}

func TestDefaultGroupOptions(t *testing.T) {
	opts := defaultGroupOptions()
	assert.Equal(t, DefaultWorkers, opts.workers)
	assert.NotNil(t, opts.callback)
}

func TestDefaultPipelineOptions(t *testing.T) {
	opts := defaultPipelineOptions()
	assert.Equal(t, DefaultWorkers, opts.workers)
	assert.Equal(t, DefaultIdleTimeout, opts.idleTimeout)
	assert.Equal(t, DefaultScanInterval, opts.scanInterval)
	assert.Equal(t, DefaultSpawnRate, opts.spawnRate)
	assert.Equal(t, DefaultBurstLimit, opts.burstLimit)
	assert.NotNil(t, opts.callback)
}

func TestV2Callback_Interface(t *testing.T) {
	var cb Callback = NewEmptyCallback()
	cb.OnBefore(context.Background(), "input")
	cb.OnAfter(context.Background(), "input", "output", nil)
}
