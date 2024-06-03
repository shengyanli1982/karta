package main

import (
	"fmt"
	"time"

	k "github.com/shengyanli1982/karta"
	wkq "github.com/shengyanli1982/workqueue/v2"
)

// handleFunc 是一个处理函数，它接收一个任意类型的消息，打印该消息，然后返回该消息和nil错误。
// handleFunc is a handler function that takes a message of any type, prints the message, and then returns the message and a nil error.
func handleFunc(msg any) (any, error) {
	// 打印接收到的消息。
	// Print the received message.
	fmt.Println("default:", msg)

	// 返回接收到的消息和nil错误。
	// Return the received message and a nil error.
	return msg, nil
}

func main() {
	// 创建一个新的配置对象。
	// Create a new configuration object.
	c := k.NewConfig()

	// 设置处理函数和工作线程数量。
	// Set the handler function and the number of worker threads.
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)

	// 创建一个新的假延迟队列。
	// Create a new fake delaying queue.
	queue := k.NewFakeDelayingQueue(wkq.NewQueue(nil))

	// 使用队列和配置创建一个新的管道。
	// Create a new pipeline using the queue and configuration.
	pl := k.NewPipeline(queue, c)

	// 确保在main函数结束时停止管道。
	// Ensure the pipeline is stopped when the main function ends.
	defer pl.Stop()

	// 提交一个消息到管道。
	// Submit a message to the pipeline.
	_ = pl.Submit("foo")

	// 使用特定的处理函数提交一个消息到管道。
	// Submit a message to the pipeline using a specific handler function.
	_ = pl.SubmitWithFunc(func(msg any) (any, error) {
		// 打印接收到的消息。
		// Print the received message.
		fmt.Println("SpecFunc:", msg)

		// 返回接收到的消息和nil错误。
		// Return the received message and a nil error.
		return msg, nil
	}, "bar")

	// 暂停一秒钟。
	// Pause for one second.
	time.Sleep(time.Second)
}
