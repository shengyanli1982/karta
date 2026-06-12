package karta

import (
	"errors"
	"fmt"
)

// 哨兵错误 (ADR-012: 分层错误模型)
var (
	ErrPipelineClosed  = errors.New("karta: pipeline is closed")
	ErrGroupStopped    = errors.New("karta: group is stopped")
	ErrFutureTimeout   = errors.New("karta: future get timeout")
	ErrSchedulerClosed = errors.New("karta: scheduler is closed")
)

// SubmitError 表示提交时刻的错误 (ADR-012)
// 包装来自 Scheduler 的底层错误
type SubmitError struct {
	Cause error
}

func (e *SubmitError) Error() string {
	return fmt.Sprintf("karta: submit error: %v", e.Cause)
}

func (e *SubmitError) Unwrap() error {
	return e.Cause
}
