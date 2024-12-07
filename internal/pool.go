package internal

import "sync"

type Element struct {
	data  any
	value int64
}

func (element *Element) GetData() any {
	return element.data
}

func (element *Element) GetValue() int64 {
	return element.value
}

func (element *Element) SetData(data any) {
	element.data = data
}

func (element *Element) SetValue(value int64) {
	element.value = value
}

func (element *Element) Reset() {
	element.data = nil
	element.value = 0
}

type ElementPool struct {
	syncPool *sync.Pool
}

func NewElementPool() *ElementPool {
	return &ElementPool{
		syncPool: &sync.Pool{
			New: func() any {
				return &Element{}
			},
		},
	}
}

func (elementPool *ElementPool) Get() *Element {
	return elementPool.syncPool.Get().(*Element)
}

func (elementPool *ElementPool) Put(element *Element) {
	if element != nil {
		element.Reset()
		elementPool.syncPool.Put(element)
	}
}

type MessageHandleFunc = func(msg any) (any, error)

type ElementExt struct {
	Element
	fn MessageHandleFunc
}

func (e *ElementExt) GetHandleFunc() MessageHandleFunc {
	return e.fn
}

func (e *ElementExt) SetHandleFunc(fn MessageHandleFunc) {
	e.fn = fn
}

func (e *ElementExt) Reset() {
	e.Element.Reset()
	e.fn = nil
}

type ElementExtPool struct {
	syncPool *sync.Pool
}

func NewElementExtPool() *ElementExtPool {
	return &ElementExtPool{
		syncPool: &sync.Pool{
			New: func() any {
				return &ElementExt{}
			},
		},
	}
}

func (elementExtPool *ElementExtPool) Get() *ElementExt {
	return elementExtPool.syncPool.Get().(*ElementExt)
}

func (elementExtPool *ElementExtPool) Put(element *ElementExt) {
	if element != nil {
		element.Reset()
		elementExtPool.syncPool.Put(element)
	}
}
