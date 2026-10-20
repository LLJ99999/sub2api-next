package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newRelayStoreForTest(t *testing.T) (*miniredis.Miniredis, service.UpstreamRelayBreakerStore, service.CooldownFallbackProbeLocker) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewTempUnschedCache(client)
	store, ok := cache.(service.UpstreamRelayBreakerStore)
	require.True(t, ok)
	locker, ok := cache.(service.CooldownFallbackProbeLocker)
	require.True(t, ok)
	return server, store, locker
}

func TestUpstreamRelayStore_SuccessWithoutFailuresWritesNothing(t *testing.T) {
	server, store, _ := newRelayStoreForTest(t)
	out, err := store.RecordUpstreamRelayOutcome(context.Background(), 7, "model:gpt-4o", true, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UpstreamRelayOutcome{}, out)
	require.Empty(t, server.Keys())
}

func TestUpstreamRelayStore_CountsFailuresAndSuccessesInWindow(t *testing.T) {
	server, store, _ := newRelayStoreForTest(t)
	server.SetTime(time.Unix(1_700_000_000, 0))
	ctx := context.Background()

	out, err := store.RecordUpstreamRelayOutcome(ctx, 7, "account", false, time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, out.Failures)
	require.EqualValues(t, 1, out.Total)

	out, err = store.RecordUpstreamRelayOutcome(ctx, 7, "account", true, time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, out.Failures)
	require.EqualValues(t, 2, out.Total)
	require.False(t, out.Engaged)

	// Entries older than the window are dropped.
	server.SetTime(time.Unix(1_700_000_000+120, 0))
	out, err = store.RecordUpstreamRelayOutcome(ctx, 7, "account", false, time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, out.Failures)
	require.EqualValues(t, 1, out.Total)
}

func TestUpstreamRelayStore_TripEscalatesAndCloseResets(t *testing.T) {
	server, store, _ := newRelayStoreForTest(t)
	base := time.Unix(1_700_000_000, 0)
	server.SetTime(base)
	ctx := context.Background()

	level, err := store.TripUpstreamRelayBreaker(ctx, 9, "account", 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, level)

	// A concurrent trip within the dedupe window does not escalate.
	level, err = store.TripUpstreamRelayBreaker(ctx, 9, "account", 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, level)

	server.SetTime(base.Add(10 * time.Second))
	level, err = store.TripUpstreamRelayBreaker(ctx, 9, "account", 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2, level)

	out, err := store.RecordUpstreamRelayOutcome(ctx, 9, "account", true, time.Minute)
	require.NoError(t, err)
	require.True(t, out.Engaged, "success after a trip reports the engaged breaker so it can be closed")

	require.NoError(t, store.CloseUpstreamRelayBreaker(ctx, 9, "account"))
	level, err = store.TripUpstreamRelayBreaker(ctx, 9, "account", 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, level)
}

func TestCooldownFallbackProbeLock(t *testing.T) {
	server, _, locker := newRelayStoreForTest(t)
	ctx := context.Background()
	ok, err := locker.TryAcquireCooldownFallbackProbe(ctx, "g1|openai|gpt", 10*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = locker.TryAcquireCooldownFallbackProbe(ctx, "g1|openai|gpt", 10*time.Second)
	require.NoError(t, err)
	require.False(t, ok)
	server.FastForward(11 * time.Second)
	ok, err = locker.TryAcquireCooldownFallbackProbe(ctx, "g1|openai|gpt", 10*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestUpstreamRelayHalfOpenTrialCap(t *testing.T) {
	server, store, _ := newRelayStoreForTest(t)
	halfOpen, ok := store.(service.UpstreamRelayHalfOpenStore)
	require.True(t, ok)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		admitted, err := halfOpen.TryAcquireUpstreamRelayHalfOpenTrial(ctx, 9, "account", 2, 30*time.Second)
		require.NoError(t, err)
		require.True(t, admitted)
	}
	admitted, err := halfOpen.TryAcquireUpstreamRelayHalfOpenTrial(ctx, 9, "account", 2, 30*time.Second)
	require.NoError(t, err)
	require.False(t, admitted)

	// Other scopes are independent.
	admitted, err = halfOpen.TryAcquireUpstreamRelayHalfOpenTrial(ctx, 9, "model:gpt-4o", 2, 30*time.Second)
	require.NoError(t, err)
	require.True(t, admitted)

	require.NoError(t, halfOpen.ResetUpstreamRelayHalfOpenTrials(ctx, 9, "account"))
	admitted, err = halfOpen.TryAcquireUpstreamRelayHalfOpenTrial(ctx, 9, "account", 2, 30*time.Second)
	require.NoError(t, err)
	require.True(t, admitted)

	server.FastForward(31 * time.Second)
	for i := 0; i < 2; i++ {
		admitted, err = halfOpen.TryAcquireUpstreamRelayHalfOpenTrial(ctx, 9, "account", 2, 30*time.Second)
		require.NoError(t, err)
		require.True(t, admitted)
	}
}
