package test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	k "github.com/shengyanli1982/karta"
	wkq "github.com/shengyanli1982/workqueue/v2"
	"github.com/stretchr/testify/assert"
)

// TestPipeline_Submit_Basic tests basic task submission
func TestPipeline_Submit_Basic(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_Submit_WithCallback tests task submission with callback
func TestPipeline_Submit_WithCallback(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithCallback(&callback{t: t})
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_Submit_WithManyWorkers tests task submission with large number of workers
func TestPipeline_Submit_WithManyWorkers(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(200)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)
	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_Submit_WithCustomHandler tests task submission with custom handler
func TestPipeline_Submit_WithCustomHandler(t *testing.T) {
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

// TestPipeline_Submit_WithManyWorkersAndHandler tests task submission with many workers and custom handler
func TestPipeline_Submit_WithManyWorkersAndHandler(t *testing.T) {
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

// TestPipeline_Submit_WhenQueueClosed tests task submission when queue is closed
func TestPipeline_Submit_WhenQueueClosed(t *testing.T) {
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

// TestPipeline_SubmitAfter_Basic tests basic delayed task submission
func TestPipeline_SubmitAfter_Basic(t *testing.T) {
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

// TestPipeline_SubmitAfter_WithCallback tests delayed task submission with callback
func TestPipeline_SubmitAfter_WithCallback(t *testing.T) {
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

// TestPipeline_SubmitAfter_WithManyWorkers tests delayed task submission with many workers
func TestPipeline_SubmitAfter_WithManyWorkers(t *testing.T) {
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

// TestPipeline_SubmitAfter_WithCustomHandler tests delayed task submission with custom handler
func TestPipeline_SubmitAfter_WithCustomHandler(t *testing.T) {
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

// TestPipeline_SubmitAfter_WithManyWorkersAndHandler tests delayed submission with many workers and handler
func TestPipeline_SubmitAfter_WithManyWorkersAndHandler(t *testing.T) {
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

// TestPipeline_SubmitAfter_WhenQueueClosed tests delayed task submission when queue is closed
func TestPipeline_SubmitAfter_WhenQueueClosed(t *testing.T) {
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

// TestPipeline_Submit_WithNilInput tests submission of nil input
func TestPipeline_Submit_WithNilInput(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(func(msg any) (any, error) {
		// 专门处理 nil 输入的情况
		if msg == nil {
			return nil, nil // 直接返回 nil，表示成功处理
		}
		// 其他情况的处理逻辑
		return msg, nil
	}).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.Submit(nil)
	assert.Nil(t, err, "submitting nil should not cause error")

	// 给一些时间让消息被处理
	time.Sleep(100 * time.Millisecond)

	pl.Stop()
}

// TestPipeline_Submit_WithInvalidWorkerCount tests pipeline with invalid worker count
func TestPipeline_Submit_WithInvalidWorkerCount(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(func(msg any) (any, error) {
		return msg, nil
	}).WithWorkerNumber(-1)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.Submit(1)
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_Submit_Concurrent tests concurrent task submission
func TestPipeline_Submit_Concurrent(t *testing.T) {
	c := k.NewConfig()
	var wg sync.WaitGroup
	taskCount := 1000

	// 使用计数器跟踪已处理的任务
	processed := int32(0)
	c.WithHandleFunc(func(msg any) (any, error) {
		// 记录处理完成的任务数
		atomic.AddInt32(&processed, 1)
		return msg, nil
	}).WithWorkerNumber(4)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	// 并发提交任务
	wg.Add(taskCount)
	for i := 0; i < taskCount; i++ {
		go func(val int) {
			defer wg.Done()
			err := pl.Submit(val)
			assert.Nil(t, err)
		}(i)
	}

	// 等待所有任务提交完成
	wg.Wait()

	// 等待所有任务处理完成
	for i := 0; i < 100; i++ { // 最多等待10秒
		if atomic.LoadInt32(&processed) == int32(taskCount) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 验证所有任务都被处理
	assert.Equal(t, int32(taskCount), atomic.LoadInt32(&processed))

	pl.Stop()
}

// TestPipeline_Submit_WithLargeMessage tests submission of large messages
func TestPipeline_Submit_WithLargeMessage(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(func(msg any) (any, error) {
		data, ok := msg.([]byte)
		assert.True(t, ok, "message should be []byte")
		assert.Equal(t, 1024*1024, len(data))
		return data, nil
	}).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	largeMsg := make([]byte, 1024*1024)
	err := pl.Submit(largeMsg)
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_SubmitAfter_WithExtremeDelays tests submission with extreme delay values
func TestPipeline_SubmitAfter_WithExtremeDelays(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := wkq.NewDelayingQueue(nil)

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	// 测试0延迟
	err := pl.SubmitAfter(1, 0)
	assert.Nil(t, err)

	// 测试极短延迟
	err = pl.SubmitAfter(2, time.Nanosecond)
	assert.Nil(t, err)

	// 测试极长延迟
	err = pl.SubmitAfter(3, 24*365*time.Hour) // 一年
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_Submit_WithError tests error handling in task processing
func TestPipeline_Submit_WithError(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(func(msg any) (any, error) {
		return nil, fmt.Errorf("test error")
	}).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	err := pl.Submit(1)
	assert.Nil(t, err) // 提交应该成功，即处理函数会返回错误

	// 使用自定义处理函数
	err = pl.SubmitWithFunc(func(msg any) (any, error) {
		return nil, fmt.Errorf("custom error")
	}, 2)
	assert.Nil(t, err)

	pl.Stop()
}

// TestPipeline_Stop_WhileProcessing tests pipeline shutdown while processing tasks
func TestPipeline_Stop_WhileProcessing(t *testing.T) {
	c := k.NewConfig()
	c.WithHandleFunc(func(msg any) (any, error) {
		time.Sleep(100 * time.Millisecond) // 模拟耗时操作
		return msg, nil
	}).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	pl := k.NewPipeline(queue, c)
	assert.NotNil(t, pl)

	// 提交多个任务
	for i := 0; i < 10; i++ {
		err := pl.Submit(i)
		assert.Nil(t, err)
	}

	// 立即停止，测试是否能正常处理
	pl.Stop()
}
