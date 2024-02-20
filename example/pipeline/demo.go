package main

import (
	"fmt"
	"time"

	k "github.com/shengyanli1982/karta"
	"github.com/shengyanli1982/workqueue"
)

func handleFunc(msg any) (any, error) {
	fmt.Println("default:", msg)
	return msg, nil
}

func main() {
	c := k.NewConfig()
	c.WithHandleFunc(handleFunc).WithWorkerNumber(2)
	queue := k.NewFakeDelayingQueue(workqueue.NewSimpleQueue(nil))
	pl := k.NewPipeline(queue, c)

	defer func() {
		pl.Stop()
		queue.Stop()
	}()

	_ = pl.Submit("foo")
	_ = pl.SubmitWithFunc(func(msg any) (any, error) {
		fmt.Println("SpecFunc:", msg)
		return msg, nil
	}, "bar")

	time.Sleep(time.Second)
}
