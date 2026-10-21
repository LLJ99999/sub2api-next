package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestChatRequestRequiresResponsesForTools(t *testing.T) {
	tools := []byte(`{"model":"x","tools":[{"type":"function","function":{"name":"f"}}]}`)
	require.True(t, chatRequestRequiresResponsesForTools(tools, "gpt-6.1-sol"))
	require.False(t, chatRequestRequiresResponsesForTools([]byte(`{"model":"x"}`), "gpt-6.1-sol"))
	require.False(t, chatRequestRequiresResponsesForTools(tools, "gpt-5.4"))
}

// #8020: an account set to 强制 Responses must not reject Chat Completions tool
// calls that only Responses can serve.
func TestAccountCanServeResponsesForChatTools(t *testing.T) {
	forced := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceResponses),
	}}
	require.True(t, accountCanServeResponsesForChatTools(forced, "gpt-6.1-sol"))

	forcedCC := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}}
	require.False(t, accountCanServeResponsesForChatTools(forcedCC, "gpt-6.1-sol"))

	plain := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.False(t, accountCanServeResponsesForChatTools(plain, "gpt-6.1-sol"))
	require.False(t, accountCanServeResponsesForChatTools(nil, "gpt-6.1-sol"))
}
