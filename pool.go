package karta

import "sync"

type element struct {
	data  any
	value int64
}

func (e *element) Data() any {
	return e.data
}

func (e *element) Value() int64 {
	return e.value
}

func (e *element) SetData(data any) {
	e.data = data
}

func (e *element) SetValue(value int64) {
	e.value = value
}

func (e *element) Reset() {
	e.data = nil
	e.value = 0
}

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
