package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type probeBatchAccountRepo struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r *probeBatchAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if a, ok := r.accounts[id]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

func TestRunUpstreamProbeBatchDedupsByBaseURL(t *testing.T) {
	repo := &probeBatchAccountRepo{accounts: map[int64]*Account{
		1: {ID: 1, Credentials: map[string]any{"base_url": "https://a.example/"}},
		2: {ID: 2, Credentials: map[string]any{"base_url": "https://A.example"}},
		3: {ID: 3, Credentials: map[string]any{"base_url": "https://b.example"}},
		4: {ID: 4, Credentials: map[string]any{"base_url": "https://b.example"}},
	}}
	var applied []int64
	results, err := runUpstreamProbeBatch(context.Background(), repo, []int64{1, 2, 3, 4, 9, 1}, true, func(a *Account) error {
		applied = append(applied, a.ID)
		if a.ID == 3 {
			return errors.New("bad")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3, 4}, applied, "failed account 3 lets account 4 cover b.example")
	status := map[int64]string{}
	for _, r := range results {
		status[r.AccountID] = r.Status
	}
	require.Equal(t, map[int64]string{1: "enabled", 2: "skipped_duplicate", 3: "failed", 4: "enabled", 9: "failed"}, status)

	applied = nil
	results, err = runUpstreamProbeBatch(context.Background(), repo, []int64{1, 2}, false, func(a *Account) error {
		applied = append(applied, a.ID)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, applied, "disable applies to every account")
	require.Equal(t, "disabled", results[1].Status)

	_, err = runUpstreamProbeBatch(context.Background(), repo, nil, true, func(*Account) error { return nil })
	require.Error(t, err)
}
