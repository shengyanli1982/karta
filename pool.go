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

type elementPool struct {
	p *sync.Pool
}

func NewElementPool() *elementPool {
	return &elementPool{
		p: &sync.Pool{
			New: func() any {
				return &element{}
			},
		},
	}
}

func (p *elementPool) Get() *element {
	return p.p.Get().(*element)
}

func (p *elementPool) Put(e *element) {
	if e != nil {
		e.Reset()
		p.p.Put(e)
	}
}
