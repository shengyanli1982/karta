// abpxx6d04wxr 包含队列接口的定义
package karta

// Callback 是一个接口，定义了在消息处理前后需要调用的方法
// Callback is an interface that defines methods to be called before and after message processing
type Callback = interface {
	// OnBefore 是一个方法，它在消息处理之前被调用，接收一个任意类型的参数 msg
	// OnBefore is a method that is called before message processing, it receives an argument of any type, msg
	OnBefore(msg any)

	// OnAfter 是一个方法，它在消息处理之后被调用，接收三个任意类型的参数：msg，result 和 err
	// OnAfter is a method that is called after message processing, it receives three arguments of any type: msg, result, and err
	OnAfter(msg, result any, err error)
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
func NewEmptyCallback() Callback { return &emptyCallback{} }

// Queue 接口定义了一个队列应该具备的基本操作。
// The Queue interface defines the basic operations that a queue should have.
type Queue = interface {
	// Put 方法用于将元素放入队列。
	// The Put method is used to put an element into the queue.
	Put(value interface{}) error

	// Get 方法用于从队列中获取元素。
	// The Get method is used to get an element from the queue.
	Get() (value interface{}, err error)

	// Done 方法用于标记元素处理完成。
	// The Done method is used to mark the element as done.
	Done(value interface{})

	// Shutdown 方法用于关闭队列。
	// The Shutdown method is used to shut down the queue.
	Shutdown()

	// IsClosed 方法用于检查队列是否已关闭。
	// The IsClosed method is used to check if the queue is closed.
	IsClosed() bool
}

// DelayingQueue 接口继承了 Queue 接口，并添加了一个 PutWithDelay 方法，用于将元素延迟放入队列。
// The DelayingQueue interface inherits from the Queue interface and adds a PutWithDelay method to put an element into the queue with delay.
type DelayingQueue = interface {
	Queue

	// PutWithDelay 方法用于将元素延迟放入队列。
	// The PutWithDelay method is used to put an element into the queue with delay.
	PutWithDelay(value interface{}, delay int64) error
}

// FakeDelayingQueue 是一个结构体，它实现了 DelayingQueueInterface 接口，但是它的 AddAfter 方法实际上并不会延迟添加元素
// FakeDelayingQueue is a struct that implements the DelayingQueueInterface interface, but its AddAfter method does not actually delay adding values
type FakeDelayingQueue struct{ Queue }

// NewFakeDelayingQueue 是一个函数，它创建并返回一个新的 FakeDelayingQueue
// NewFakeDelayingQueue is a function that creates and returns a new FakeDelayingQueue
func NewFakeDelayingQueue(queue Queue) *FakeDelayingQueue {
	return &FakeDelayingQueue{
		Queue: queue,
	}
}

// AddAfter 是 FakeDelayingQueue 的方法，它将元素添加到队列，但是并不会延迟
// AddAfter is a method of FakeDelayingQueue, it adds an value to the queue, but does not delay
func (q *FakeDelayingQueue) PutWithDelay(value any, delay int64) error {
	return q.Put(value)
}
