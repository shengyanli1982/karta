package karta

import "fmt"

// toMiddlewareSlice 将 []any 转换为 []Middleware[In, Out]
// Phase 2 T2.7: 运行时类型断言，绕过 Go 泛型 struct field 限制
//
// 类型不匹配视为配置错误，直接 panic（fail-fast）：
// 静默丢弃会导致 middleware 未生效且难以排查
func toMiddlewareSlice[In, Out any](raw []any) []Middleware[In, Out] {
	mws := make([]Middleware[In, Out], 0, len(raw))
	for _, v := range raw {
		mw, ok := v.(Middleware[In, Out])
		if !ok {
			var expected Middleware[In, Out]
			panic(fmt.Sprintf("karta: middleware type mismatch: expected %T, got %T", expected, v))
		}
		mws = append(mws, mw)
	}
	return mws
}
