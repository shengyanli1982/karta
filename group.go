package karta

import (
	"context"
	"sync"
)

var elepool = NewElementPool()

type Group struct {
	items  []*element
	lock   sync.Mutex
	config *Config
	wg     sync.WaitGroup
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
}

func NewGroup(conf *Config) *Group {
	conf = isConfigValid(conf)
	g := Group{
		items:  []*element{},
		lock:   sync.Mutex{},
		config: conf,
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
	}
	g.ctx, g.cancel = context.WithCancel(context.Background())

	return &g
}

func (g *Group) Stop() {
	g.once.Do(func() {
		g.cancel()
		g.wg.Wait()
		g.lock.Lock()
		g.items = g.items[:0]
		g.lock.Unlock()
	})
}

func (g *Group) prepare(items []any) {
	count := len(items)
	g.lock.Lock()
	g.items = make([]*element, count)
	for i := 0; i < count; i++ {
		e := elepool.Get()
		e.SetData(items[i])
		e.SetValue(int64(i))
		g.items[i] = e
	}
	g.lock.Unlock()
}

func (g *Group) fetch() []any {
	var results []any
	if g.config.result {
		results = make([]any, len(g.items))
	}

	g.wg.Add(g.config.num)
	for i := 0; i < g.config.num; i++ {
		go func() {
			defer g.wg.Done()
			for {
				select {
				case <-g.ctx.Done():
					return
				default:
					g.lock.Lock()
					if len(g.items) == 0 {
						g.lock.Unlock()
						return
					}

					item := g.items[0]
					g.items[0] = nil
					g.items = g.items[1:]
					g.lock.Unlock()

					d := item.Data()
					g.config.cb.OnBefore(d)
					r, err := g.config.h(d)
					g.config.cb.OnAfter(d, r, err)
					item.SetData(r)

					if g.config.result {
						results[item.Value()] = r
					}

					elepool.Put(item)
				}
			}
		}()
	}

	g.wg.Wait()
	return results
}

func (g *Group) Start(items []any) []any {
	if len(items) == 0 {
		return nil
	}

	g.prepare(items)
	return g.fetch()
}
