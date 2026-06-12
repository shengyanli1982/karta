package scheduler

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	karta "github.com/shengyanli1982/karta/v2"
	"github.com/shengyanli1982/workqueue/v2"
)

// --- Mock Queue Implementations ---

// errPutQueue 包装真实 Queue，Put 时返回指定错误并调用 onPut 回调。
type errPutQueue struct {
	workqueue.Queue
	putErr error
	onPut  func() // Put 执行前的回调，用于模拟并发 Shutdown
}

func (q *errPutQueue) Put(_ interface{}) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

// errPutPriorityQueue 包装真实 PriorityQueue，PutWithPriority 时返回指定错误。
type errPutPriorityQueue struct {
	workqueue.PriorityQueue
	putErr error
	onPut  func()
}

func (q *errPutPriorityQueue) PutWithPriority(_ interface{}, _ int64) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

// errPutDelayQueue 包装真实 DelayingQueue，Put/PutWithDelay 时返回指定错误。
type errPutDelayQueue struct {
	workqueue.DelayingQueue
	putErr error
	onPut  func()
}

func (q *errPutDelayQueue) Put(_ interface{}) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

func (q *errPutDelayQueue) PutWithDelay(_ interface{}, _ int64) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

// errPutLeasedQueue 包装真实 LeasedQueue，Put 时返回指定错误。
type errPutLeasedQueue struct {
	workqueue.LeasedQueue
	putErr error
	onPut  func()
}

func (q *errPutLeasedQueue) Put(_ interface{}) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

// errPutRateLimitingQueue 包装真实 RateLimitingQueue，PutWithLimited 时返回指定错误。
type errPutRateLimitingQueue struct {
	workqueue.RateLimitingQueue
	putErr error
	onPut  func()
}

func (q *errPutRateLimitingQueue) PutWithLimited(_ interface{}) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

// errPutRetryQueue 包装真实 RetryQueue，Put 时返回指定错误。
type errPutRetryQueue struct {
	workqueue.RetryQueue
	putErr error
	onPut  func()
}

func (q *errPutRetryQueue) Put(_ interface{}) error {
	if q.onPut != nil {
		q.onPut()
	}
	return q.putErr
}

// --- Double-Check 路径测试 ---
// 模拟并发 Shutdown：第一个 closed.Load() 为 false，
// 但 mock Put 在执行时将 closed 设为 true（模拟并发 Shutdown），
// 返回非 ErrQueueIsClosed 错误，验证 double-check 生效。

func TestFIFO_DoubleCheck_ClosedAfterPutError(t *testing.T) {
	customErr := errors.New("custom put error")
	s := &fifoScheduler{
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}
	mockQ := &errPutQueue{
		Queue:  workqueue.NewQueue(workqueue.NewQueueConfig()),
		putErr: customErr,
		onPut:  func() { s.closed.Store(true) },
	}
	s.queue = mockQ

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestPriority_DoubleCheck_ClosedAfterPutError(t *testing.T) {
	customErr := errors.New("custom put error")
	s := &priorityScheduler{
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}
	mockQ := &errPutPriorityQueue{
		PriorityQueue: workqueue.NewPriorityQueue(workqueue.NewPriorityQueueConfig()),
		putErr:        customErr,
		onPut:         func() { s.closed.Store(true) },
	}
	s.queue = mockQ

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1, Priority: 1})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestDelay_DoubleCheck_ClosedAfterPutError(t *testing.T) {
	customErr := errors.New("custom put error")

	// 无延迟路径
	s := &delayScheduler{
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}
	mockQ := &errPutDelayQueue{
		DelayingQueue: workqueue.NewDelayingQueue(workqueue.NewDelayingQueueConfig()),
		putErr:        customErr,
		onPut:         func() { s.closed.Store(true) },
	}
	s.queue = mockQ

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1, Delay: 0})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)

	// 有延迟路径
	s2 := &delayScheduler{
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}
	mockQ2 := &errPutDelayQueue{
		DelayingQueue: workqueue.NewDelayingQueue(workqueue.NewDelayingQueueConfig()),
		putErr:        customErr,
		onPut:         func() { s2.closed.Store(true) },
	}
	s2.queue = mockQ2

	err = s2.Enqueue(&karta.TaskEnvelope{Input: 1, Delay: 10 * time.Millisecond})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestLease_DoubleCheck_ClosedAfterPutError(t *testing.T) {
	customErr := errors.New("custom put error")
	s := &leaseScheduler{
		leaseTimeout: 5 * time.Second,
		notify:       make(chan struct{}, 1),
		doneCh:       make(chan struct{}),
	}
	mockQ := &errPutLeasedQueue{
		LeasedQueue: workqueue.NewLeasedQueue(workqueue.NewLeasedQueueConfig().WithLeaseDuration(5 * time.Second)),
		putErr:      customErr,
		onPut:       func() { s.closed.Store(true) },
	}
	s.queue = mockQ

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestRateLimiting_DoubleCheck_ClosedAfterPutError(t *testing.T) {
	customErr := errors.New("custom put error")
	s := &rateLimitingScheduler{
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}
	mockQ := &errPutRateLimitingQueue{
		RateLimitingQueue: workqueue.NewRateLimitingQueue(workqueue.NewRateLimitingQueueConfig()),
		putErr:            customErr,
		onPut:             func() { s.closed.Store(true) },
	}
	s.queue = mockQ

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

func TestRetry_DoubleCheck_ClosedAfterPutError(t *testing.T) {
	customErr := errors.New("custom put error")
	cfg := workqueue.NewRetryQueueConfig().WithKeyFunc(retryTaskKeyFunc)
	s := &retryScheduler{
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}
	mockQ := &errPutRetryQueue{
		RetryQueue: workqueue.NewRetryQueue(cfg),
		putErr:     customErr,
		onPut:      func() { s.closed.Store(true) },
	}
	s.queue = mockQ

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, karta.ErrSchedulerClosed)
}

// --- Fallback Error 路径测试 ---
// 验证 Put 返回非 ErrQueueIsClosed 错误且 closed 为 false 时返回原始错误

func TestFIFO_PutError_NotClosed_ReturnsOriginalError(t *testing.T) {
	customErr := errors.New("custom put error")
	mockQ := &errPutQueue{
		Queue:  workqueue.NewQueue(workqueue.NewQueueConfig()),
		putErr: customErr,
	}
	s := &fifoScheduler{
		queue:  mockQ,
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, customErr)
}

func TestPriority_PutError_NotClosed_ReturnsOriginalError(t *testing.T) {
	customErr := errors.New("custom put error")
	mockQ := &errPutPriorityQueue{
		PriorityQueue: workqueue.NewPriorityQueue(workqueue.NewPriorityQueueConfig()),
		putErr:        customErr,
	}
	s := &priorityScheduler{
		queue:  mockQ,
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1, Priority: 1})
	assert.ErrorIs(t, err, customErr)
}

func TestDelay_PutError_NotClosed_ReturnsOriginalError(t *testing.T) {
	customErr := errors.New("custom put error")
	mockQ := &errPutDelayQueue{
		DelayingQueue: workqueue.NewDelayingQueue(workqueue.NewDelayingQueueConfig()),
		putErr:        customErr,
	}
	s := &delayScheduler{
		queue:  mockQ,
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1, Delay: 0})
	assert.ErrorIs(t, err, customErr)

	err = s.Enqueue(&karta.TaskEnvelope{Input: 1, Delay: 10 * time.Millisecond})
	assert.ErrorIs(t, err, customErr)
}

func TestLease_PutError_NotClosed_ReturnsOriginalError(t *testing.T) {
	customErr := errors.New("custom put error")
	mockQ := &errPutLeasedQueue{
		LeasedQueue: workqueue.NewLeasedQueue(workqueue.NewLeasedQueueConfig().WithLeaseDuration(5 * time.Second)),
		putErr:      customErr,
	}
	s := &leaseScheduler{
		queue:        mockQ,
		leaseTimeout: 5 * time.Second,
		notify:       make(chan struct{}, 1),
		doneCh:       make(chan struct{}),
	}

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, customErr)
}

func TestRateLimiting_PutError_NotClosed_ReturnsOriginalError(t *testing.T) {
	customErr := errors.New("custom put error")
	mockQ := &errPutRateLimitingQueue{
		RateLimitingQueue: workqueue.NewRateLimitingQueue(workqueue.NewRateLimitingQueueConfig()),
		putErr:            customErr,
	}
	s := &rateLimitingScheduler{
		queue:  mockQ,
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, customErr)
}

func TestRetry_PutError_NotClosed_ReturnsOriginalError(t *testing.T) {
	customErr := errors.New("custom put error")
	cfg := workqueue.NewRetryQueueConfig().WithKeyFunc(retryTaskKeyFunc)
	mockQ := &errPutRetryQueue{
		RetryQueue: workqueue.NewRetryQueue(cfg),
		putErr:     customErr,
	}
	s := &retryScheduler{
		queue:  mockQ,
		notify: make(chan struct{}, 1),
		doneCh: make(chan struct{}),
	}

	err := s.Enqueue(&karta.TaskEnvelope{Input: 1})
	assert.ErrorIs(t, err, customErr)
}

// --- 并发 Race 测试 ---
// 验证 Enqueue 与 Shutdown 并发时不会 panic 且无 data race

func TestFIFO_EnqueueRaceWithShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := NewFIFOScheduler()
		env := &karta.TaskEnvelope{Input: i}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = s.Enqueue(env)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 50)
			s.Shutdown()
		}()

		wg.Wait()
	}
}

func TestPriority_EnqueueRaceWithShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := NewPriorityScheduler()
		env := &karta.TaskEnvelope{Input: i, Priority: int64(i)}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = s.Enqueue(env)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 50)
			s.Shutdown()
		}()

		wg.Wait()
	}
}

func TestDelay_EnqueueRaceWithShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := NewDelayScheduler()
		env := &karta.TaskEnvelope{Input: i}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = s.Enqueue(env)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 50)
			s.Shutdown()
		}()

		wg.Wait()
	}
}

func TestLease_EnqueueRaceWithShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := NewLeaseScheduler(5 * time.Second)
		env := &karta.TaskEnvelope{Input: i}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = s.Enqueue(env)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 50)
			s.Shutdown()
		}()

		wg.Wait()
	}
}

func TestRateLimiting_EnqueueRaceWithShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := NewRateLimitingScheduler(nil)
		env := &karta.TaskEnvelope{Input: i}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = s.Enqueue(env)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 50)
			s.Shutdown()
		}()

		wg.Wait()
	}
}

func TestRetry_EnqueueRaceWithShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := NewRetryScheduler(nil)
		env := &karta.TaskEnvelope{Input: i}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = s.Enqueue(env)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 50)
			s.Shutdown()
		}()

		wg.Wait()
	}
}
