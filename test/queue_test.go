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
	q0 := workqueue.NewSimpleQueue(nil)

	queue := k.NewQueue(q0, c)
	assert.NotNil(t, queue)
	err := queue.Submit(nil, 1)
	assert.Nil(t, err)
	err = queue.Submit(
		func(msg any) (any, error) {
			assert.Equal(t, 2, msg)
			return msg, nil
		}, 2,
	)
	assert.Nil(t, err)

	queue.Stop()
}
