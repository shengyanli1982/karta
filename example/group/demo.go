package main

import (
	"time"

	k "github.com/shengyanli1982/karta"
)

func handleFunc(msg any) (any, error) {
	time.Sleep(time.Duration(msg.(int)) * time.Millisecond * 100)
	return msg, nil
}

func main() {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2).WithResult()

	g := k.NewGroup(c)
	defer g.Stop()

	r0 := g.Map([]any{3, 5, 2})
	println(r0[0].(int))
}
