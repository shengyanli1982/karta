package karta

// Chain 将多个 middleware 组合为一个（外层先执行）
// 即: Chain(mw1, mw2, mw3)(handler) = mw1(mw2(mw3(handler)))
func Chain[In, Out any](mws ...Middleware[In, Out]) Middleware[In, Out] {
	return func(next Handler[In, Out]) Handler[In, Out] {
		// 从右到左包裹，使得第一个 middleware 在最外层
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
