package karta

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func handleFunc(msg any) (any, error) {
	time.Sleep(time.Duration(msg.(int)) * time.Millisecond * 100)
	return msg, nil
}

type callback struct {
	t *testing.T
}

func (c *callback) OnBefore(msg any) {}

func (c *callback) OnAfter(msg, result any, err error) {
	assert.Equal(c.t, msg, result)
	assert.Nil(c.t, err)
}

func TestGroupStart(t *testing.T) {
	c := NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Start([]any{3, 5, 2})
	assert.Equal(t, 3, len(r0))
	assert.Equal(t, 3, r0[0])
	assert.Equal(t, 5, r0[1])
	assert.Equal(t, 2, r0[2])
	g.Stop()
}

func TestGroupStartWithLargeWorkers(t *testing.T) {
	c := NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200).WithResult()

	g := NewGroup(c)
	assert.NotNil(t, g)
	r0 := g.Start([]any{1, 2})
	assert.Equal(t, 2, len(r0))
	assert.Equal(t, 1, r0[0])
	assert.Equal(t, 2, r0[1])
	g.Stop()
}

func TestGroupStartWithCallback(t *testing.T) {
	c := NewConfig()
	c.WithHandleFunc(handleFunc).WithCallback(&callback{t: t})

	g := NewGroup(c)
	assert.NotNil(t, g)
	_ = g.Start([]any{3, 5, 2})
	g.Stop()
}
