// abpxx6d04wxr 包含队列接口的定义
package karta

import "time"

// 回调函数
// callback function.
type Callback interface {
	OnBefore(msg any)                   // 在消息处理前调用
	OnAfter(msg, result any, err error) // 在消息处理后调用
}

// 空回调函数
// empty callback function.
type emptyCallback struct{}

// OnBefore 在消息处理前调用
// OnBefore is called before message handle.
func (emptyCallback) OnBefore(msg any) {}

// OnAfter 在消息处理后调用
// OnAfter is called after message handle.
func (emptyCallback) OnAfter(msg, result any, err error) {}

// QueueInterface 是队列接口
// QueueInterface is queue interface
type QueueInterface interface {
	Add(element any) error         // 添加元素 (add element)
	Get() (element any, err error) // 获取元素 (get element)
	Done(element any)              // 标记元素完成 (mark element done)
	Stop()                         // 停止队列 (stop queue)
	IsClosed() bool                // 判断管道是否已经关闭 (judge whether queue is closed)
}

// DelayingQueueInterface 包含延迟队列接口的定义
// DelayingQueueInterface is delaying queue interface
type DelayingQueueInterface interface {
	QueueInterface
	AddAfter(element any, delay time.Duration) error // 将元素添加到队列中，并在指定的延迟后立即可用 (add an element to the queue, making it available after the specified delay)
}

// FakeDelayingQueue 是一个模拟的延迟队列
// FakeDelayingQueue is a fake delaying queue
type FakeDelayingQueue struct {
	QueueInterface
}

// NewFakeDelayingQueue 创建一个新的模拟延迟队列
// NewFakeDelayingQueue creates a new fake delaying queue
func NewFakeDelayingQueue(queue QueueInterface) *FakeDelayingQueue {
	return &FakeDelayingQueue{
		QueueInterface: queue,
	}
}

// AddAfter 将元素添加到队列中，并在指定的延迟后立即可用
// AddAfter adds an element to the queue, making it available after the specified delay.
func (q *FakeDelayingQueue) AddAfter(element any, delay time.Duration) error {
	return q.Add(element)
}
