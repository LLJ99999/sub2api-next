package service

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type slowBindOpenAIWSStateStore struct {
	OpenAIWSStateStore
	delay time.Duration
	binds atomic.Int32
}

func (s *slowBindOpenAIWSStateStore) BindResponseAccount(ctx context.Context, groupID int64, responseID string, accountID int64, ttl time.Duration) error {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}
	s.binds.Add(1)
	return s.OpenAIWSStateStore.BindResponseAccount(ctx, groupID, responseID, accountID, ttl)
}

// #6851: a degraded Redis must not hold a finished response for the full
// Redis budget.
func TestBindHTTPResponseAccountDoesNotBlockOnSlowStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &slowBindOpenAIWSStateStore{OpenAIWSStateStore: NewOpenAIWSStateStore(nil), delay: 1500 * time.Millisecond}
	svc := &OpenAIGatewayService{}
	svc.openaiWSStateStore = store
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)

	start := time.Now()
	svc.bindHTTPResponseAccount(context.Background(), c, &Account{ID: 7}, "resp_slow")
	require.Less(t, time.Since(start), time.Second)

	require.Eventually(t, func() bool { return store.binds.Load() == 1 }, 3*time.Second, 20*time.Millisecond)
	accountID, err := store.GetResponseAccount(context.Background(), 0, "resp_slow")
	require.NoError(t, err)
	require.Equal(t, int64(7), accountID)
}

func TestBindHTTPResponseAccountFastStoreIsSynchronous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	store := NewOpenAIWSStateStore(nil)
	svc.openaiWSStateStore = store
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)

	svc.bindHTTPResponseAccount(context.Background(), c, &Account{ID: 9}, "resp_fast")
	accountID, err := store.GetResponseAccount(context.Background(), 0, "resp_fast")
	require.NoError(t, err)
	require.Equal(t, int64(9), accountID)
}
