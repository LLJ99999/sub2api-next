package service

import (
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/tidwall/gjson"
)

// chatRequestRequiresResponsesForTools reports whether a Chat Completions
// request can only be served through the Responses API for the given upstream
// model: gpt-6.1-sol tool calls, and gpt-6-sol/luna tool calls with reasoning.
// The raw Chat Completions path rejects these with a 400.
func chatRequestRequiresResponsesForTools(body []byte, upstreamModel string) bool {
	hasTools := len(gjson.GetBytes(body, "tools").Array()) > 0 || len(gjson.GetBytes(body, "functions").Array()) > 0
	if !hasTools {
		return false
	}
	if openai.IsGPT61SolModelSpelling(upstreamModel) {
		return true
	}
	return openai.IsGPT6SolOrLunaModelSpelling(upstreamModel) && gjson.GetBytes(body, "reasoning_effort").String() != "none"
}

// accountCanServeResponsesForChatTools reports whether an account whose routing
// picked Chat Completions for this request can still be sent the request via
// Responses. Used so that "强制 Responses" / adaptive / model-routed accounts do
// not reject tool calls that only Responses supports (#8020). Accounts pinned
// to Chat Completions (explicit api_protocol or force_chat_completions) keep
// the existing behaviour.
func accountCanServeResponsesForChatTools(account *Account, model string) bool {
	if account == nil || account.IsCopilotSDKEnabled() {
		return false
	}
	if account.Extra != nil {
		if mode, _ := account.Extra[openai_compat.ExtraKeyResponsesMode].(string); mode != "" {
			switch openai_compat.NormalizeResponsesSupportMode(mode) {
			case openai_compat.ResponsesSupportModeForceResponses:
				return true
			case openai_compat.ResponsesSupportModeForceChatCompletions:
				return false
			}
		}
	}
	switch account.GetAPIProtocol() {
	case APIProtocolChatCompletions, APIProtocolAnthropic:
		if account.IsMultiProtocolAPIKey() {
			// Explicitly pinned by the operator.
			return false
		}
	}
	if account.routesByModel() {
		return slices.Contains(account.modelProtocolSet(model, nil), APIProtocolResponses)
	}
	if account.IsAdaptiveAPIProtocol() {
		return account.providerSupportsProtocol(APIProtocolResponses)
	}
	return false
}
