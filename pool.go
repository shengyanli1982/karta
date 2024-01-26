package karta

import "sync"

// 工作元素, 用于 Group 和 Queue
// Worker element, used by Group and Queue
type element struct {
	data  any   // 数据
	value int64 // 值
}

// 获取数据
// get data.
func (e *element) GetData() any {
	return e.data
}

// 获取值
// get value.
func (e *element) GetValue() int64 {
	return e.value
}

// 设置数据
// set data.
func (e *element) SetData(data any) {
	e.data = data
}

// 设置值
// set value.
func (e *element) SetValue(value int64) {
	e.value = value
}

// 重置
// reset.
func (e *element) Reset() {
	e.data = nil
	e.value = 0
}

// 对象池
// object ObjectPool.
type ObjectPool struct {
	pool *sync.Pool
}

// NewElementPool 创建一个新的对象池
// NewElementPool creates a new object pool.
func NewElementPool() *ObjectPool {
	return &ObjectPool{
		pool: &sync.Pool{
			New: func() any {
				return &element{}
			},
		},
	}
}

// Get 从对象池中获取一个元素
// Get gets an element from the object pool.
func (p *ObjectPool) Get() *element {
	return p.pool.Get().(*element)
}

// Put 将一个元素放回对象池中
// Put puts an element back into the object pool.
func (p *ObjectPool) Put(e *element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}

// elementExt 是 element 的扩展，包含了一个消息处理函数
// elementExt is an extension of element, which includes a message handling function.
type elementExt struct {
	element
	fn MessageHandleFunc // 消息处理函数
}

// GetHandleFunc 获取消息处理函数
// GetHandleFunc gets the message handling function.
func (e *elementExt) GetHandleFunc() MessageHandleFunc {
	return e.fn
}

// SetHandleFunc 设置消息处理函数
// SetHandleFunc sets the message handling function.
func (e *elementExt) SetHandleFunc(fn MessageHandleFunc) {
	e.fn = fn
}

// Reset 重置元素扩展
// Reset resets the element extension.
func (e *elementExt) Reset() {
	e.element.Reset()
	e.fn = nil
}

// ExtendObjectPool 是对象池的扩展，用于存储 elementExt 对象
// ExtendObjectPool is an extension of the object pool, used to store elementExt objects.
type ExtendObjectPool struct {
	pool *sync.Pool
}

// NewElementExtPool 创建一个新的对象池
// NewElementExtPool creates a new object pool.
func NewElementExtPool() *ExtendObjectPool {
	return &ExtendObjectPool{
		pool: &sync.Pool{
			New: func() any {
				return &elementExt{}
			},
		},
	}
}

// Get 从对象池中获取一个元素扩展
// Get gets an element extension from the object pool.
func (p *ExtendObjectPool) Get() *elementExt {
	return p.pool.Get().(*elementExt)
}

// Put 将一个元素扩展放回对象池中
// Put puts an element extension back into the object pool.
func (p *ExtendObjectPool) Put(e *elementExt) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
