package karta

import (
	"context"
	"sync"
)

var elementExtPool = NewElementExtPool()

type QInterface interface {
	Add(element any) error
	Get() (element any, err error)
	Done(element any)
	IsClosed() bool
}

type Queue struct {
	queue  QInterface
	lock   sync.Mutex
	config *Config
	wg     sync.WaitGroup
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

func NewQueue(queue QInterface, conf *Config) *Queue {
	if queue == nil {
		return nil
	}
	conf = isConfigValid(conf)
	q := Queue{
		queue:  queue,
		lock:   sync.Mutex{},
		config: conf,
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
	}
	q.ctx, q.cancel = context.WithCancel(context.Background())

	q.wg.Add(conf.num)
	for i := 0; i < conf.num; i++ {
		go q.executor()
	}

	return &q
}

func (q *Queue) Stop() {
	q.once.Do(func() {
		q.cancel()
		q.wg.Wait()
		q.lock.Lock()
		q.queue = nil
		q.lock.Unlock()
	})
}

func (q *Queue) executor() {
	defer q.wg.Done()

	for {
		select {
		case <-q.ctx.Done():
			return
		default:
			if q.queue.IsClosed() {
				return
			}
			o, err := q.queue.Get()
			if err != nil {
				break
			}
			q.queue.Done(o)
			d := o.(*elementExt)
			q.config.cb.OnBefore(d)
			h := d.Handler()
			var r any
			if h != nil {
				r, err = h(d.Data())
			} else {
				r, err = q.config.h(d)
			}
			q.config.cb.OnAfter(d, r, err)
			elementExtPool.Put(d)
		}
	}
}

func (q *Queue) Submit(fn MessageHandleFunc, msg any) error {
	e := elementExtPool.Get()
	e.SetData(msg)
	e.SetHandler(fn)
	if err := q.queue.Add(e); err != nil {
		elementExtPool.Put(e)
		return err
	}
	return nil
}
