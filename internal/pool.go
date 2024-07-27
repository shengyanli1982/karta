package internal

import "sync"

// Element 是一个结构体，表示工作元素，它被 Group 和 Queue 使用
// Element is a struct that represents a worker element, it is used by Group and Queue
type Element struct {
	data  any   // 数据 (Data)
	value int64 // 值 (Value)
}

// GetData 是 Element 的方法，它返回元素的数据
// GetData is a method of Element, it returns the data of the element
func (e *Element) GetData() any {
	return e.data
}

// GetValue 是 Element 的方法，它返回元素的值
// GetValue is a method of Element, it returns the value of the element
func (e *Element) GetValue() int64 {
	return e.value
}

// SetData 是 Element 的方法，它设置元素的数据
// SetData is a method of Element, it sets the data of the element
func (e *Element) SetData(data any) {
	e.data = data
}

// SetValue 是 Element 的方法，它设置元素的值
// SetValue is a method of Element, it sets the value of the element
func (e *Element) SetValue(value int64) {
	e.value = value
}

// Reset 是 Element 的方法，它重置元素的数据和值
// Reset is a method of Element, it resets the data and value of the element
func (e *Element) Reset() {
	e.data = nil
	e.value = 0
}

// ElementPool 是一个结构体，表示对象池，它用于存储和复用 Element
// ElementPool is a struct that represents an object pool, it is used to store and reuse Element
type ElementPool struct {
	pool *sync.Pool
}

// NewElementPool 是一个函数，它创建并返回一个新的 ElementPool
// NewElementPool is a function, it creates and returns a new ElementPool
func NewElementPool() *ElementPool {
	return &ElementPool{
		pool: &sync.Pool{
			New: func() any {
				return &Element{}
			},
		},
	}
}

// Get 是 ElementPool 的方法，它从对象池中获取一个 Element
// Get is a method of ElementPool, it gets an Element from the object pool
func (p *ElementPool) Get() *Element {
	return p.pool.Get().(*Element)
}

// Put 是 ElementPool 的方法，它将一个 Element 放回对象池中
// Put is a method of ElementPool, it puts an Element back into the object pool
func (p *ElementPool) Put(e *Element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}

// 定义消息处理函数类型
// Define the message handle function type
type MessageHandleFunc = func(msg any) (any, error)

// ElementExt 是一个结构体，它是 Element 的扩展，包含了一个消息处理函数
// ElementExt is a struct, it is an extension of Element, which includes a message handling function
type ElementExt struct {
	Element
	fn MessageHandleFunc // 消息处理函数 (Message handling function)
}

// GetHandleFunc 是 ElementExt 的方法，它获取消息处理函数
// GetHandleFunc is a method of ElementExt, it gets the message handling function
func (e *ElementExt) GetHandleFunc() MessageHandleFunc {
	return e.fn
}

// SetHandleFunc 是 ElementExt 的方法，它设置消息处理函数
// SetHandleFunc is a method of ElementExt, it sets the message handling function
func (e *ElementExt) SetHandleFunc(fn MessageHandleFunc) {
	e.fn = fn
}

// Reset 是 ElementExt 的方法，它重置元素扩展
// Reset is a method of ElementExt, it resets the element extension
func (e *ElementExt) Reset() {
	e.Element.Reset()
	e.fn = nil
}

// ElementExtPool 是一个结构体，它是对象池的扩展，用于存储 ElementExt 对象
// ElementExtPool is a struct, it is an extension of the object pool, used to store ElementExt objects
type ElementExtPool struct {
	pool *sync.Pool
}

// NewElementExtPool 是一个函数，它创建并返回一个新的 ElementExtPool
// NewElementExtPool is a function, it creates and returns a new ElementExtPool
func NewElementExtPool() *ElementExtPool {
	return &ElementExtPool{
		pool: &sync.Pool{
			New: func() any {
				return &ElementExt{}
			},
		},
	}
}

// Get 是 ElementExtPool 的方法，它从对象池中获取一个 ElementExt
// Get is a method of ElementExtPool, it gets an ElementExt from the object pool
func (p *ElementExtPool) Get() *ElementExt {
	return p.pool.Get().(*ElementExt)
}

// Put 是 ElementExtPool 的方法，它将一个 ElementExt 放回对象池中
// Put is a method of ElementExtPool, it puts an ElementExt back into the object pool
func (p *ElementExtPool) Put(e *ElementExt) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
