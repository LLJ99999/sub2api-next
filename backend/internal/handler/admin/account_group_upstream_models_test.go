package admin

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCollectGroupUpstreamModelsDedupsByBaseURL(t *testing.T) {
	accounts := []service.Account{
		{ID: 1, Name: "a", Platform: service.PlatformOpenAI, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://x.example/"}},
		{ID: 2, Name: "b", Platform: service.PlatformOpenAI, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://x.example"}},
		{ID: 3, Name: "c", Platform: service.PlatformOpenAI, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://y.example"}},
		{ID: 4, Name: "d", Platform: service.PlatformOpenAI, Status: "disabled", Credentials: map[string]any{"base_url": "https://z.example"}},
		{ID: 5, Name: "e", Platform: service.PlatformOpenAI, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://broken.example"}},
	}
	var asked []int64
	models, warnings, n := collectGroupUpstreamModels(accounts, func(a *service.Account) ([]string, error) {
		asked = append(asked, a.ID)
		switch a.ID {
		case 1:
			return []string{"gpt-5.4", " gpt-5.5 "}, nil
		case 3:
			return []string{"gpt-5.5", "claude-x"}, nil
		}
		return nil, errors.New("boom")
	})
	require.Equal(t, []int64{1, 3, 5}, asked)
	require.Equal(t, 3, n)
	require.Equal(t, []string{"gpt-5.4", "gpt-5.5", "claude-x"}, models)
	require.Len(t, warnings, 1)
}
