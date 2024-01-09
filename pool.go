package karta

import "sync"

// 工作元素, 用于 Group 和 Queue
// Worker element, used by Group and Queue
type element struct {
	data  any
	value int64
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

func NewElementPool() *ObjectPool {
	return &ObjectPool{
		pool: &sync.Pool{
			New: func() any {
				return &element{}
			},
		},
	}
}

func (p *ObjectPool) Get() *element {
	return p.pool.Get().(*element)
}

func (p *ObjectPool) Put(e *element) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}

type elementExt struct {
	element
	fn MessageHandleFunc
}

func (e *elementExt) GetHandleFunc() MessageHandleFunc {
	return e.fn
}

func (e *elementExt) SetHandleFunc(fn MessageHandleFunc) {
	e.fn = fn
}

func (e *elementExt) Reset() {
	e.element.Reset()
	e.fn = nil
}

// 对象池
type ExtendObjectPool struct {
	pool *sync.Pool
}

func NewElementExtPool() *ExtendObjectPool {
	return &ExtendObjectPool{
		pool: &sync.Pool{
			New: func() any {
				return &elementExt{}
			},
		},
	}
}

func (p *ExtendObjectPool) Get() *elementExt {
	return p.pool.Get().(*elementExt)
}

func (p *ExtendObjectPool) Put(e *elementExt) {
	if e != nil {
		e.Reset()
		p.pool.Put(e)
	}
}
