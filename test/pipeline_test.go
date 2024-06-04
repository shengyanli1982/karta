package test

import (
	"testing"
	"time"

	k "github.com/shengyanli1982/karta"
	wkq "github.com/shengyanli1982/workqueue/v2"
	"github.com/stretchr/testify/assert"
)

// TestQueueSubmit tests queue submit.
func TestQueueSubmit(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

func TestQueueSubmitWithCallback(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithCallback(&callback{t: t})
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

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
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

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
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

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
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

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
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

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

func TestQueueSubmit_DelayingQueue(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := wkq.NewDelayingQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.Submit(1)
	assert.Nil(t, err)

	err = pl.SubmitAfter(2, time.Second)
	assert.Nil(t, err)

	time.Sleep(2 * time.Second)

	pl.Stop()
}

func TestQueueSubmitWithCallback_DelayingQueue(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithCallback(&callback{t: t})
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.Submit(1)
	assert.Nil(t, err)

	err = pl.SubmitAfter(2, time.Second)
	assert.Nil(t, err)

	time.Sleep(2 * time.Second)

	pl.Stop()
}

// TestQueueSubmitWithLargeWorkers tests queue submit with large workers.
func TestQueueSubmitWithLargeWorkers_DelayingQueue(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200)
	queue := wkq.NewDelayingQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.Submit(1)
	assert.Nil(t, err)

	err = pl.SubmitAfter(2, time.Second)
	assert.Nil(t, err)

	time.Sleep(2 * time.Second)

	pl.Stop()
}

// TestQueueSubmitWithCustomFunc tests queue submit with custom func.
func TestQueueSubmitWithCustomFunc_DelayingQueue(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := wkq.NewDelayingQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.SubmitWithFunc(
		func(msg any) (any, error) {
			assert.Equal(t, 2, msg)
			return msg, nil
		}, 2,
	)
	assert.Nil(t, err)

	err = pl.SubmitAfterWithFunc(func(msg any) (any, error) {
		assert.Equal(t, 3, msg)
		return msg, nil
	}, 3, time.Second)
	assert.Nil(t, err)

	time.Sleep(2 * time.Second)

	pl.Stop()
}

// TestQueueSubmitWithCustomFuncAndLargeWorkers tests queue submit with custom func and large workers.
func TestQueueSubmitWithCustomFuncAndLargeWorkers_DelayingQueue(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200)
	queue := wkq.NewDelayingQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.SubmitWithFunc(
		func(msg any) (any, error) {
			assert.Equal(t, 2, msg)
			return msg, nil
		}, 2,
	)
	assert.Nil(t, err)

	err = pl.SubmitAfterWithFunc(func(msg any) (any, error) {
		assert.Equal(t, 3, msg)
		return msg, nil
	}, 3, time.Second)
	assert.Nil(t, err)

	time.Sleep(2 * time.Second)

	pl.Stop()
}

// TestQueueSubmitWithFuncWhenQueueClosed tests queue submit with func when queue closed.
func TestQueueSubmitWithFuncWhenQueueClosed_DelayingQueue(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := wkq.NewDelayingQueue(nil)

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
