package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizePelicanPrompts(t *testing.T) {
	got, err := normalizePelicanPrompts(" legacy ", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"legacy"}, got)

	got, err = normalizePelicanPrompts("ignored", []string{" a ", "", "b"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, got)

	_, err = normalizePelicanPrompts(" ", []string{"  "})
	require.Error(t, err)

	many := make([]string, pelicanGroupTestMaxPrompts+1)
	for i := range many {
		many[i] = "p"
	}
	_, err = normalizePelicanPrompts("", many)
	require.Error(t, err)
}

type pelicanRotatingRepoFake struct {
	PelicanGroupTestRepository
	next int64
}

func (r *pelicanRotatingRepoFake) NextPromptIndex(context.Context, int64) (int64, error) {
	slot := r.next
	r.next++
	return slot, nil
}

func TestPelicanWithRotatedPromptRoundRobin(t *testing.T) {
	plan := &PelicanGroupTestPlan{ID: 7, PelicanConfig: &PelicanTestConfig{Prompt: "a", Prompts: []string{"a", "b", "c"}}}
	for _, svc := range []*PelicanGroupTestService{{repo: &pelicanRotatingRepoFake{}}, {repo: &groupTestRepoFake{}}} {
		var used []string
		for i := 0; i < 4; i++ {
			run := svc.withRotatedPrompt(context.Background(), plan)
			used = append(used, run.PelicanConfig.Prompt)
			require.NotNil(t, run.PelicanConfig.PromptIndex)
		}
		require.Equal(t, []string{"a", "b", "c", "a"}, used)
		require.Equal(t, "a", plan.PelicanConfig.Prompt, "stored plan must not change")
		require.Nil(t, plan.PelicanConfig.PromptIndex)
	}

	single := &PelicanGroupTestPlan{ID: 8, PelicanConfig: &PelicanTestConfig{Prompt: "only"}}
	require.Same(t, single, (&PelicanGroupTestService{repo: &pelicanRotatingRepoFake{}}).withRotatedPrompt(context.Background(), single))
}
