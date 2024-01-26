package test

import (
	"testing"

	k "github.com/shengyanli1982/karta"
	"github.com/shengyanli1982/workqueue"
	"github.com/stretchr/testify/assert"
)

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

func TestQueueSubmitWithLargeWorkers(t *testing.T) {
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
