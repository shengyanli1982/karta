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

// TestGroupMap tests group map.
func TestGroupMap(t *testing.T) {
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

// TestGroupMapWithLargeWorkers tests group map with large workers.
func TestGroupMapWithLargeWorkers(t *testing.T) {
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

// TestGroupMapWithCallback tests group map with callback.
func TestGroupMapWithCallback(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithCallback(&callback{t: t})

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	_ = g.Map([]any{3, 5, 2})
	g.Stop()
}

// TestGroupMapWithFunc tests group map with func.
func TestGroupMapEmpty(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Map([]any{})
	assert.Nil(t, r0)
	g.Stop()
}
