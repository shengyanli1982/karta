package karta_test

import (
	"context"
	"testing"
	"time"

	"github.com/shengyanli1982/karta/v2/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	karta "github.com/shengyanli1982/karta/v2"
)

// TestSubmitAfter_NoDoubleDelay — P1 #2 回归测试：
// SubmitAfter 的延迟已由 Pipeline 侧 goroutine 等待完成，入队前清零 envelope.Delay，
// Delay-aware 调度器不得二次施加延迟（修复前实际总耗时 ≈ 2×delay，必然 ≥120ms）
func TestSubmitAfter_NoDoubleDelay(t *testing.T) {
	handler := karta.Handler[int, int](func(ctx context.Context, input int) (int, error) {
		return input * 2, nil
	})
	p := karta.NewPipeline[int, int](handler, scheduler.NewDelayScheduler(), karta.WithPipelineWorkers(2))
	defer p.Stop()

	start := time.Now()
	f, err := p.SubmitAfter(context.Background(), 21, 50*time.Millisecond)
	require.NoError(t, err)

	getCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := f.Get(getCtx)
	require.NoError(t, res.Err)
	assert.Equal(t, 42, res.Value)
	assert.Less(t, time.Since(start), 120*time.Millisecond,
		"总耗时应 ≈ 一次 delay（50ms）+ 调度开销；≥120ms 说明延迟被施加了两次")
}
