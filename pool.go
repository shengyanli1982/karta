package karta

import "sync"

// element 是一个结构体，表示工作元素，它被 Group 和 Queue 使用
// element is a struct that represents a worker element, it is used by Group and Queue
type element struct {
	data  any   // 数据 (Data)
	value int64 // 值 (Value)
}

// GetData 是 Element 的方法，它返回元素的数据
// GetData is a method of Element, it returns the data of the element
func (e *element) GetData() any {
	return e.data
}

// GetValue 是 Element 的方法，它返回元素的值
// GetValue is a method of Element, it returns the value of the element
func (e *element) GetValue() int64 {
	return e.value
}

// SetData 是 Element 的方法，它设置元素的数据
// SetData is a method of Element, it sets the data of the element
func (e *element) SetData(data any) {
	e.data = data
}

// SetValue 是 Element 的方法，它设置元素的值
// SetValue is a method of Element, it sets the value of the element
func (e *element) SetValue(value int64) {
	e.value = value
}

// Reset 是 Element 的方法，它重置元素的数据和值
// Reset is a method of Element, it resets the data and value of the element
func (e *element) Reset() {
	e.data = nil
	e.value = 0
}

// elementPool 是一个结构体，表示对象池，它用于存储和复用 Element
// elementPool is a struct that represents an object pool, it is used to store and reuse Element
type elementPool struct {
	pool *sync.Pool
}

// newElementPool 是一个函数，它创建并返回一个新的 ElementPool
// newElementPool is a function, it creates and returns a new ElementPool
func newElementPool() *elementPool {
	return &elementPool{
		pool: &sync.Pool{
			New: func() any {
				return &element{}
			},
		},
	}
}

// Get 是 ElementPool 的方法，它从对象池中获取一个 Element
// Get is a method of ElementPool, it gets an Element from the object pool
func (p *elementPool) Get() *element {
	return p.pool.Get().(*element)
}

// Put 是 ElementPool 的方法，它将一个 Element 放回对象池中
// Put is a method of ElementPool, it puts an Element back into the object pool
func (p *elementPool) Put(e *element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}

// elementExt 是一个结构体，它是 Element 的扩展，包含了一个消息处理函数
// elementExt is a struct, it is an extension of Element, which includes a message handling function
type elementExt struct {
	element
	fn MessageHandleFunc // 消息处理函数 (Message handling function)
}

// GetHandleFunc 是 ElementExt 的方法，它获取消息处理函数
// GetHandleFunc is a method of ElementExt, it gets the message handling function
func (e *elementExt) GetHandleFunc() MessageHandleFunc {
	return e.fn
}

// SetHandleFunc 是 ElementExt 的方法，它设置消息处理函数
// SetHandleFunc is a method of ElementExt, it sets the message handling function
func (e *elementExt) SetHandleFunc(fn MessageHandleFunc) {
	e.fn = fn
}

// Reset 是 ElementExt 的方法，它重置元素扩展
// Reset is a method of ElementExt, it resets the element extension
func (e *elementExt) Reset() {
	e.element.Reset()
	e.fn = nil
}

// elemmentExtPool 是一个结构体，它是对象池的扩展，用于存储 ElementExt 对象
// elemmentExtPool is a struct, it is an extension of the object pool, used to store ElementExt objects
type elemmentExtPool struct {
	pool *sync.Pool
}

// newElementExtPool 是一个函数，它创建并返回一个新的 ElemmentExtPool
// newElementExtPool is a function, it creates and returns a new ElemmentExtPool
func newElementExtPool() *elemmentExtPool {
	return &elemmentExtPool{
		pool: &sync.Pool{
			New: func() any {
				return &elementExt{}
			},
		},
	}
}

// Get 是 ElemmentExtPool 的方法，它从对象池中获取一个 ElementExt
// Get is a method of ElemmentExtPool, it gets an ElementExt from the object pool
func (p *elemmentExtPool) Get() *elementExt {
	return p.pool.Get().(*elementExt)
}

// Put 是 ElemmentExtPool 的方法，它将一个 ElementExt 放回对象池中
// Put is a method of ElemmentExtPool, it puts an ElementExt back into the object pool
func (p *elemmentExtPool) Put(e *elementExt) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
