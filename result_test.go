package karta

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResult_Ok_WhenSuccess(t *testing.T) {
	r := Result[int]{Value: 42, Err: nil}
	assert.True(t, r.Ok())
}

func TestResult_Ok_WhenError(t *testing.T) {
	r := Result[int]{Value: 0, Err: errors.New("fail")}
	assert.False(t, r.Ok())
}

func TestResult_Unwrap_Success(t *testing.T) {
	r := Result[string]{Value: "hello"}
	val, err := r.Unwrap()
	require.NoError(t, err)
	assert.Equal(t, "hello", val)
}

func TestResult_Unwrap_Error(t *testing.T) {
	expected := errors.New("boom")
	r := Result[string]{Err: expected}
	val, err := r.Unwrap()
	assert.ErrorIs(t, err, expected)
	assert.Equal(t, "", val)
}

func TestResult_Unwrap_ErrorChain(t *testing.T) {
	base := errors.New("base")
	wrapped := fmt.Errorf("wrapped: %w", base)
	r := Result[int]{Err: wrapped}
	_, err := r.Unwrap()
	assert.ErrorIs(t, err, base)
}
