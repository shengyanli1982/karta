package test

import (
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

// TestGroup_Map_StopAndReuse tests stopping and reusing Group
func TestGroup_Map_StopAndReuse(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)

	// 第一次使用
	r0 := g.Map([]any{1, 2, 3})
	assert.Equal(t, 3, len(r0))
	g.Stop()

	// 停止后再次使用
	r1 := g.Map([]any{4, 5, 6})
	assert.Equal(t, 3, len(r1))
	g.Stop()
}
