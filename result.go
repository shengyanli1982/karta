package karta

// Result 是泛型同步结果容器 (ADR-004)
// 用于 Group.Map 的返回值，封装成功值或错误
type Result[T any] struct {
	Value T     // 成功时的值
	Err   error // 失败时的错误（nil 表示成功）
}

// Ok 返回 true 当结果成功（Err == nil）
func (r Result[T]) Ok() bool {
	return r.Err == nil
}

// Unwrap 解包结果：成功返回 (Value, nil)，失败返回 (zero, Err)
func (r Result[T]) Unwrap() (T, error) {
	return r.Value, r.Err
}
