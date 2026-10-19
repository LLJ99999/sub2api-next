package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFailoverTimeBudgetFromConfig(t *testing.T) {
	require.Zero(t, failoverTimeBudgetFromConfig(nil))
	cfg := &config.Config{}
	require.Zero(t, failoverTimeBudgetFromConfig(cfg))
	cfg.Gateway.FailoverTimeBudgetSeconds = 45
	require.Equal(t, 45*time.Second, failoverTimeBudgetFromConfig(cfg))
}

func TestFailoverTimeBudgetExceeded(t *testing.T) {
	require.False(t, failoverTimeBudgetExceeded(time.Now().Add(-time.Hour), 0), "zero budget disables the limit")
	require.False(t, failoverTimeBudgetExceeded(time.Time{}, time.Second))
	require.False(t, failoverTimeBudgetExceeded(time.Now(), time.Minute))
	require.True(t, failoverTimeBudgetExceeded(time.Now().Add(-2*time.Minute), time.Minute))
}

func TestHandleFailoverError_StopsSwitchingWhenTimeBudgetExhausted(t *testing.T) {
	fs := NewFailoverState(10, false).WithTimeBudget(time.Minute)
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}

	// Within budget: switching continues.
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, service.PlatformAnthropic, 0, failoverErr))
	require.Equal(t, 1, fs.SwitchCount)

	// Budget exhausted: stop switching, keep the last upstream error (and its status).
	fs.startedAt = time.Now().Add(-2 * time.Minute)
	lastErr := &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(`{"error":"upstream"}`)}
	require.Equal(t, FailoverExhausted, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 2, service.PlatformAnthropic, 0, lastErr))
	require.Equal(t, 1, fs.SwitchCount, "no further switch after the budget is spent")
	require.Same(t, lastErr, fs.LastFailoverErr)
	require.Equal(t, http.StatusBadGateway, fs.LastFailoverErr.StatusCode)
}

func TestHandleFailoverError_TimeBudgetAlsoStopsSameAccountRetry(t *testing.T) {
	fs := NewFailoverState(10, false).WithTimeBudget(time.Second)
	fs.startedAt = time.Now().Add(-time.Minute)
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true}
	require.Equal(t, FailoverExhausted, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, service.PlatformAnthropic, 3, failoverErr))
	require.Zero(t, fs.SameAccountRetryCount[1])
}

func TestHandleFailoverError_NoBudgetKeepsMaxSwitchSemantics(t *testing.T) {
	fs := NewFailoverState(1, false).WithTimeBudget(0)
	fs.startedAt = time.Now().Add(-time.Hour)
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, service.PlatformAnthropic, 0, failoverErr))
	require.Equal(t, FailoverExhausted, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 2, service.PlatformAnthropic, 0, failoverErr))
}

func TestHandleSelectionExhausted_RespectsTimeBudget(t *testing.T) {
	fs := NewFailoverState(10, false).WithTimeBudget(time.Second)
	fs.LastFailoverErr = &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	fs.startedAt = time.Now().Add(-time.Minute)
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(context.Background()))
}
