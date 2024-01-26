package karta

import "sync"

// 工作元素, 用于 Group 和 Queue
// Worker Element, used by Group and Queue
type Element struct {
	data  any   // 数据
	value int64 // 值
}

// 获取数据
// get data.
func (e *Element) GetData() any {
	return e.data
}

// 获取值
// get value.
func (e *Element) GetValue() int64 {
	return e.value
}

// 设置数据
// set data.
func (e *Element) SetData(data any) {
	e.data = data
}

// 设置值
// set value.
func (e *Element) SetValue(value int64) {
	e.value = value
}

// 重置
// reset.
func (e *Element) Reset() {
	e.data = nil
	e.value = 0
}

// 对象池
// object ElementPool.
type ElementPool struct {
	pool *sync.Pool
}

// NewElementPool 创建一个新的对象池
// NewElementPool creates a new object pool.
func NewElementPool() *ElementPool {
	return &ElementPool{
		pool: &sync.Pool{
			New: func() any {
				return &Element{}
			},
		},
	}
}

// Get 从对象池中获取一个元素
// Get gets an element from the object pool.
func (p *ElementPool) Get() *Element {
	return p.pool.Get().(*Element)
}

// Put 将一个元素放回对象池中
// Put puts an element back into the object pool.
func (p *ElementPool) Put(e *Element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}

// ElementExt 是 element 的扩展，包含了一个消息处理函数
// ElementExt is an extension of element, which includes a message handling function.
type ElementExt struct {
	Element
	fn MessageHandleFunc // 消息处理函数
}

// GetHandleFunc 获取消息处理函数
// GetHandleFunc gets the message handling function.
func (e *ElementExt) GetHandleFunc() MessageHandleFunc {
	return e.fn
}

// SetHandleFunc 设置消息处理函数
// SetHandleFunc sets the message handling function.
func (e *ElementExt) SetHandleFunc(fn MessageHandleFunc) {
	e.fn = fn
}

// Reset 重置元素扩展
// Reset resets the element extension.
func (e *ElementExt) Reset() {
	e.Element.Reset()
	e.fn = nil
}

// ElemmentExtPool 是对象池的扩展，用于存储 elementExt 对象
// ElemmentExtPool is an extension of the object pool, used to store elementExt objects.
type ElemmentExtPool struct {
	pool *sync.Pool
}

// NewElementExtPool 创建一个新的对象池
// NewElementExtPool creates a new object pool.
func NewElementExtPool() *ElemmentExtPool {
	return &ElemmentExtPool{
		pool: &sync.Pool{
			New: func() any {
				return &ElementExt{}
			},
		},
	}
}

// Get 从对象池中获取一个元素扩展
// Get gets an element extension from the object pool.
func (p *ElemmentExtPool) Get() *ElementExt {
	return p.pool.Get().(*ElementExt)
}

// Put 将一个元素扩展放回对象池中
// Put puts an element extension back into the object pool.
func (p *ElemmentExtPool) Put(e *ElementExt) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
