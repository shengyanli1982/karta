package karta

// toMiddlewareSlice 将 []any 转换为 []Middleware[In, Out]
// Phase 2 T2.7: 运行时类型断言，绕过 Go 泛型 struct field 限制
func toMiddlewareSlice[In, Out any](raw []any) []Middleware[In, Out] {
	mws := make([]Middleware[In, Out], 0, len(raw))
	for _, v := range raw {
		if mw, ok := v.(Middleware[In, Out]); ok {
			mws = append(mws, mw)
		}
		// 类型不匹配的 middleware 被静默忽略
	}
	return mws
}
