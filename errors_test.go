package karta

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSentinelErrors(t *testing.T) {
	require.NotNil(t, ErrPipelineClosed)
	require.NotNil(t, ErrGroupStopped)
	require.NotNil(t, ErrFutureTimeout)
	require.NotNil(t, ErrSchedulerClosed)
}

func TestSubmitError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	se := &SubmitError{Cause: cause}
	assert.ErrorIs(t, se, cause)
	assert.NotEmpty(t, se.Error())
}

func TestSubmitError_ErrorFormat(t *testing.T) {
	se := &SubmitError{Cause: errors.New("queue full")}
	assert.Equal(t, "karta: submit error: queue full", se.Error())
}
