// abpxx6d04wxr 包含队列接口的定义
package karta

import "time"

// Callback 是一个接口，定义了在消息处理前后需要调用的方法
// Callback is an interface that defines methods to be called before and after message processing
type Callback interface {
	OnBefore(msg any)                   // 在消息处理前调用 (Called before message processing)
	OnAfter(msg, result any, err error) // 在消息处理后调用 (Called after message processing)
}

// emptyCallback 是一个实现了 Callback 接口的结构体，但是它的方法都是空的
// emptyCallback is a struct that implements the Callback interface, but its methods are all empty
type emptyCallback struct{}

// OnBefore 是 emptyCallback 的方法，它在消息处理前被调用，但是什么都不做
// OnBefore is a method of emptyCallback, it is called before message processing, but does nothing
func (emptyCallback) OnBefore(msg any) {}

// OnAfter 是 emptyCallback 的方法，它在消息处理后被调用，但是什么都不做
// OnAfter is a method of emptyCallback, it is called after message processing, but does nothing
func (emptyCallback) OnAfter(msg, result any, err error) {}

// NewEmptyCallback 是一个函数，它创建并返回一个新的 emptyCallback
// NewEmptyCallback is a function that creates and returns a new emptyCallback
func NewEmptyCallback() Callback {
	return &emptyCallback{}
}

// QueueInterface 是一个接口，定义了队列的基本操作，如添加元素、获取元素、标记元素完成、停止队列和判断队列是否已经关闭
// QueueInterface is an interface that defines basic operations of a queue, such as adding elements, getting elements, marking elements as done, stopping the queue, and checking if the queue is closed
type QueueInterface interface {
	Add(element any) error         // 添加元素 (Add element)
	Get() (element any, err error) // 获取元素 (Get element)
	Done(element any)              // 标记元素完成 (Mark element as done)
	Stop()                         // 停止队列 (Stop the queue)
	IsClosed() bool                // 判断队列是否已经关闭 (Check if the queue is closed)
}

// DelayingQueueInterface 是一个接口，它继承了 QueueInterface，并添加了一个新的方法 AddAfter，用于在指定的延迟后添加元素到队列
// DelayingQueueInterface is an interface that inherits from QueueInterface and adds a new method AddAfter for adding elements to the queue after a specified delay
type DelayingQueueInterface interface {
	QueueInterface
	AddAfter(element any, delay time.Duration) error // 在指定的延迟后添加元素到队列 (Add an element to the queue after a specified delay)
}

// FakeDelayingQueue 是一个结构体，它实现了 DelayingQueueInterface 接口，但是它的 AddAfter 方法实际上并不会延迟添加元素
// FakeDelayingQueue is a struct that implements the DelayingQueueInterface interface, but its AddAfter method does not actually delay adding elements
type FakeDelayingQueue struct {
	QueueInterface
}

// NewFakeDelayingQueue 是一个函数，它创建并返回一个新的 FakeDelayingQueue
// NewFakeDelayingQueue is a function that creates and returns a new FakeDelayingQueue
func NewFakeDelayingQueue(queue QueueInterface) *FakeDelayingQueue {
	return &FakeDelayingQueue{
		QueueInterface: queue,
	}
}

// AddAfter 是 FakeDelayingQueue 的方法，它将元素添加到队列，但是并不会延迟
// AddAfter is a method of FakeDelayingQueue, it adds an element to the queue, but does not delay
func (q *FakeDelayingQueue) AddAfter(element any, delay time.Duration) error {
	return q.Add(element)
}
