package middleware

import (
	"context"
	"fmt"
	"runtime/debug"

	karta "github.com/shengyanli1982/karta/v2"
)

// Recovery panic 恢复中间件
// 捕获 handler panic，返回包含 stack trace 的 error
func Recovery[In, Out any]() karta.Middleware[In, Out] {
	return func(next karta.Handler[In, Out]) karta.Handler[In, Out] {
		return func(ctx context.Context, input In) (out Out, err error) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := debug.Stack()
					err = fmt.Errorf("karta: recovered panic: %v\n%s", rec, stack)
				}
			}()
			return next(ctx, input)
		}
	}
}
