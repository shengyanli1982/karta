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
func (e *element) Data() any {
	return e.data
}

// 获取值
// get value.
func (e *element) Value() int64 {
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
// object pool.
type pool struct {
	p *sync.Pool
}

func NewElementPool() *pool {
	return &pool{
		p: &sync.Pool{
			New: func() any {
				return &element{}
			},
		},
	}
}

func (p *pool) Get() *element {
	return p.p.Get().(*element)
}

func (p *pool) Put(e *element) {
	if e != nil {
		e.Reset()
		p.p.Put(e)
	}
}

type elementExt struct {
	element
	fn MessageHandleFunc
}

func (e *elementExt) Handler() MessageHandleFunc {
	return e.fn
}

func (e *elementExt) SetHandler(fn MessageHandleFunc) {
	e.fn = fn
}

func (e *elementExt) Reset() {
	e.element.Reset()
	e.fn = nil
}

// 对象池
type extpool struct {
	p *sync.Pool
}

func NewElementExtPool() *extpool {
	return &extpool{
		p: &sync.Pool{
			New: func() any {
				return &elementExt{}
			},
		},
	}
}

func (p *extpool) Get() *elementExt {
	return p.p.Get().(*elementExt)
}

func (p *extpool) Put(e *elementExt) {
	if e != nil {
		e.Reset()
		p.p.Put(e)
	}
}
