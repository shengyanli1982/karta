package test

import (
	"testing"

	k "github.com/shengyanli1982/karta"
	"github.com/shengyanli1982/workqueue"
	"github.com/stretchr/testify/assert"
)

// TestQueueSubmit tests queue submit.
func TestQueueSubmit(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := workqueue.NewSimpleQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

// TestQueueSubmitWithLargeWorkers tests queue submit with large workers.
func TestQueueSubmitWithLargeWorkers(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200)
	queue := workqueue.NewSimpleQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

// TestQueueSubmitWithCustomFunc tests queue submit with custom func.
func TestQueueSubmitWithCustomFunc(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := workqueue.NewSimpleQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.SubmitWithFunc(
		func(msg any) (any, error) {
			assert.Equal(t, 2, msg)
			return msg, nil
		}, 2,
	)
	assert.Nil(t, err)

	pl.Stop()
}

// TestQueueSubmitWithCustomFuncAndLargeWorkers tests queue submit with custom func and large workers.
func TestQueueSubmitWithCustomFuncAndLargeWorkers(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200)
	queue := workqueue.NewSimpleQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.SubmitWithFunc(
		func(msg any) (any, error) {
			assert.Equal(t, 2, msg)
			return msg, nil
		}, 2,
	)
	assert.Nil(t, err)

	pl.Stop()
}

// TestQueueSubmitWithFuncWhenQueueClosed tests queue submit with func when queue closed.
func TestQueueSubmitWithFuncWhenQueueClosed(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := workqueue.NewSimpleQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	pl.Stop()

	err := pl.SubmitWithFunc(
		func(msg any) (any, error) {
			assert.Equal(t, 2, msg)
			return msg, nil
		}, 2,
	)

	assert.Equal(t, k.ErrorQueueClosed, err)
}