package test

import (
	"sync"
	"testing"
	"time"

	k "github.com/shengyanli1982/karta"
	"github.com/stretchr/testify/assert"
)

// handleFunc is a handle function.
func handleFunc(msg any) (any, error) {
	time.Sleep(time.Duration(msg.(int)) * time.Millisecond * 100)
	return msg, nil
}

// callback is a callback.
type callback struct {
	t *testing.T
}

func (c *callback) OnBefore(msg any) {}

func (c *callback) OnAfter(msg, result any, err error) {
	assert.Equal(c.t, msg, result)
	assert.Nil(c.t, err)
}

// TestGroup_Map_Basic tests basic Map functionality
func TestGroup_Map_Basic(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{3, 5, 2})
	assert.Equal(t, 3, len(r0))
	assert.Equal(t, 3, r0[0])
	assert.Equal(t, 5, r0[1])
	assert.Equal(t, 2, r0[2])
	g.Stop()
}

// TestGroup_Map_WithLargeWorkerPool tests Map with a large worker pool
func TestGroup_Map_WithLargeWorkerPool(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{1, 2})
	assert.Equal(t, 2, len(r0))
	assert.Equal(t, 1, r0[0])
	assert.Equal(t, 2, r0[1])
	g.Stop()
}

// TestGroup_Map_WithCallback tests Map with a callback
func TestGroup_Map_WithCallback(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithCallback(&callback{t: t})

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	_ = g.Map([]any{3, 5, 2})
	g.Stop()
}

// TestGroup_Map_WithEmptyInput tests Map with empty input
func TestGroup_Map_WithEmptyInput(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{})
	assert.Nil(t, r0)
	g.Stop()
}

// TestGroup_Map_WithNilInput tests Map with nil input
func TestGroup_Map_WithNilInput(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map(nil)
	assert.Nil(t, r0)
	g.Stop()
}

// TestGroup_Map_WithZeroWorkers tests Map with zero workers
func TestGroup_Map_WithZeroWorkers(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(0).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{1, 2, 3})
	// 即使工作者数量为 0，也应该能正常处理任务
	assert.Equal(t, 3, len(r0))
	g.Stop()
}

// TestGroup_Map_WithNilHandler tests Map with nil handler
func TestGroup_Map_WithNilHandler(t *testing.T) {
	c := k.NewConfig()
	c.WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{1, 2, 3})
	// 没有处理函数时应该返回原始数据
	assert.Equal(t, 3, len(r0))
	assert.Equal(t, 1, r0[0])
	g.Stop()
}

// TestGroup_Map_WithNegativeWorkers tests Map with negative workers
func TestGroup_Map_WithNegativeWorkers(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(-1).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{1, 2, 3})
	assert.Equal(t, 3, len(r0))
	g.Stop()
}

// TestGroup_Map_WithLargeInput tests Map with large input
func TestGroup_Map_WithLargeInput(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(16).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)

	// 创建一个包含200个元素的输入切片
	input := make([]any, 200)
	for i := 0; i < 200; i++ {
		input[i] = i % 5 // 使用更小的延迟值（0-4）以加快测试速度
	}

	r0 := g.Map(input)
	assert.Equal(t, 200, len(r0))
	for i := 0; i < 200; i++ {
		assert.Equal(t, i%5, r0[i])
	}
	g.Stop()
}

// errorHandleFunc 是一个会返回错误的处理函数
func errorHandleFunc(msg any) (any, error) {
	return nil, assert.AnError
}

// TestGroup_Map_WithErrorHandler tests Map with error handler
func TestGroup_Map_WithErrorHandler(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(errorHandleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{1, 2, 3})
	// 即使处理函数返回错误，也应该返回相同长度的结果切片
	assert.Equal(t, 3, len(r0))
	for _, v := range r0 {
		assert.Nil(t, v)
	}
	g.Stop()
}

// TestGroup_Map_MultipleCalls tests multiple consecutive calls to Map
func TestGroup_Map_MultipleCalls(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(4).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)

	// 第一次调用：处理小数据集
	r0 := g.Map([]any{1, 2})
	assert.Equal(t, 2, len(r0))
	assert.Equal(t, 1, r0[0])
	assert.Equal(t, 2, r0[1])

	// 第二次调用：处理空数据集
	r1 := g.Map([]any{})
	assert.Nil(t, r1)

	// 第三次调用：处理较大数据集
	input := []any{3, 4, 5, 6, 7}
	r2 := g.Map(input)
	assert.Equal(t, 5, len(r2))
	for i := 0; i < 5; i++ {
		assert.Equal(t, i+3, r2[i])
	}

	// 第四次调用：再次处理小数据集
	r3 := g.Map([]any{8, 9})
	assert.Equal(t, 2, len(r3))
	assert.Equal(t, 8, r3[0])
	assert.Equal(t, 9, r3[1])

	g.Stop()
}

// TestGroup_Map_AfterStop tests that Map returns nil after Stop is called
func TestGroup_Map_AfterStop(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)

	// First Map call should work
	r0 := g.Map([]any{1, 2})
	assert.Equal(t, 2, len(r0))
	assert.Equal(t, 1, r0[0])
	assert.Equal(t, 2, r0[1])

	// Stop the group
	g.Stop()

	// Map calls after Stop should return nil
	r1 := g.Map([]any{3, 4})
	assert.Nil(t, r1)
}

// TestGroup_Map_ConcurrentCalls tests concurrent calls to Map
func TestGroup_Map_ConcurrentCalls(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)

	var wg sync.WaitGroup
	results := make([][]any, 3)

	// 并发执行3个Map调用
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = g.Map([]any{index * 2, index*2 + 1})
		}(i)
	}

	wg.Wait()

	// 验证结果
	for i := 0; i < 3; i++ {
		assert.Equal(t, 2, len(results[i]))
		assert.Equal(t, i*2, results[i][0])
		assert.Equal(t, i*2+1, results[i][1])
	}

	g.Stop()
}
