package main

import (
	"time"

	k "github.com/shengyanli1982/karta"
)

// handleFunc 是一个处理函数，它接收一个任意类型的消息，暂停一段时间（消息值的100毫秒），然后返回该消息和nil错误。
// handleFunc is a handler function that takes a message of any type, sleeps for a duration (100 milliseconds of the message value), and then returns the message and a nil error.
func handleFunc(msg any) (any, error) {
	// 将消息转换为整数，然后暂停该整数值的100毫秒。
	// Convert the message to an integer, then pause for 100 milliseconds of the integer value.
	time.Sleep(time.Duration(msg.(int)) * time.Millisecond * 100)

	// 返回接收到的消息和nil错误。
	// Return the received message and a nil error.
	return msg, nil
}

func main() {
	// 创建一个新的配置对象。
	// Create a new configuration object.
	c := k.NewConfig()
	// 设置处理函数，工作线程数量和结果处理。
	// Set the handler function, the number of worker threads, and result processing.
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	// 使用配置创建一个新的工作组。
	// Create a new work group using the configuration.
	g := k.NewGroup(c)

	// 确保在main函数结束时停止工作组。
	// Ensure the work group is stopped when the main function ends.
	defer g.Stop()

	// 将处理函数映射到一组输入值。
	// Map the handler function to a set of input values.
	r0 := g.Map([]any{3, 5, 2})

	// 打印第一个结果的整数值。
	// Print the integer value of the first result.
	println(r0[0].(int))
}
