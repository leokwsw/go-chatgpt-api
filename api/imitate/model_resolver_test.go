package imitate

import (
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestResolveImitateModelGPT55Family(t *testing.T) {
	tests := []struct {
		name               string
		model              string
		reasoningEffort    string
		thinkingEffort     string
		wantModel          string
		wantThinkingEffort string
		wantPrepare        bool
		wantEmptyConduit   bool
	}{
		{
			name:      "instant alias",
			model:     "instant",
			wantModel: "gpt-5-3",
		},
		{
			name:      "none suffix maps to instant",
			model:     "gpt-5-5-none",
			wantModel: "gpt-5-3",
		},
		{
			name:               "bare gpt 5.5 defaults to thinking standard",
			model:              "gpt-5.5",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "standard",
			wantPrepare:        true,
		},
		{
			name:               "thinking low",
			model:              "gpt-5-5-low",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "min",
			wantPrepare:        true,
		},
		{
			name:               "thinking medium",
			model:              "gpt-5-5-medium",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "standard",
			wantPrepare:        true,
		},
		{
			name:               "thinking high",
			model:              "gpt-5-5-high",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "extended",
			wantPrepare:        true,
		},
		{
			name:               "thinking xhigh",
			model:              "gpt-5-5-xhigh",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "max",
			wantPrepare:        true,
		},
		{
			name:               "thinking raw heavy alias",
			model:              "thinking",
			thinkingEffort:     "heavy",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "max",
			wantPrepare:        true,
		},
		{
			name:               "pro default",
			model:              "pro",
			wantModel:          "gpt-5-5-pro",
			wantThinkingEffort: "standard",
			wantPrepare:        true,
			wantEmptyConduit:   true,
		},
		{
			name:               "pro high",
			model:              "pro-high",
			wantModel:          "gpt-5-5-pro",
			wantThinkingEffort: "standard",
			wantPrepare:        true,
			wantEmptyConduit:   true,
		},
		{
			name:               "pro xhigh",
			model:              "gpt-5.5-pro-xhigh",
			wantModel:          "gpt-5-5-pro",
			wantThinkingEffort: "extended",
			wantPrepare:        true,
			wantEmptyConduit:   true,
		},
		{
			name:               "explicit reasoning effort overrides model suffix",
			model:              "gpt-5-5-xhigh",
			reasoningEffort:    "high",
			wantModel:          "gpt-5-5-thinking",
			wantThinkingEffort: "extended",
			wantPrepare:        true,
		},
		{
			name:               "pro raw standard equals high",
			model:              "gpt-5.5-pro",
			thinkingEffort:     "standard",
			wantModel:          "gpt-5-5-pro",
			wantThinkingEffort: "standard",
			wantPrepare:        true,
			wantEmptyConduit:   true,
		},
		{
			name:               "pro raw extended equals xhigh",
			model:              "gpt-5.5-pro",
			thinkingEffort:     "extended",
			wantModel:          "gpt-5-5-pro",
			wantThinkingEffort: "extended",
			wantPrepare:        true,
			wantEmptyConduit:   true,
		},
		{
			name:               "reasoning none maps gpt 5.5 to instant",
			model:              "gpt-5.5",
			reasoningEffort:    "none",
			wantModel:          "gpt-5-3",
			wantThinkingEffort: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveImitateModel(tt.model, tt.reasoningEffort, tt.thinkingEffort)
			if err != nil {
				t.Fatalf("resolveImitateModel() error = %v", err)
			}
			if got.Model != tt.wantModel {
				t.Fatalf("model = %q, want %q", got.Model, tt.wantModel)
			}
			if got.ThinkingEffort != tt.wantThinkingEffort {
				t.Fatalf("thinking effort = %q, want %q", got.ThinkingEffort, tt.wantThinkingEffort)
			}
			if got.RequiresPrepare != tt.wantPrepare {
				t.Fatalf("requires prepare = %t, want %t", got.RequiresPrepare, tt.wantPrepare)
			}
			if got.AllowEmptyConduitToken != tt.wantEmptyConduit {
				t.Fatalf("allow empty conduit = %t, want %t", got.AllowEmptyConduitToken, tt.wantEmptyConduit)
			}
		})
	}
}

func TestResolveImitateModelRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name            string
		model           string
		reasoningEffort string
		thinkingEffort  string
		wantError       string
	}{
		{
			name:      "pro low is unsupported",
			model:     "pro-low",
			wantError: "pro model only supports",
		},
		{
			name:            "pro medium is unsupported",
			model:           "gpt-5.5-pro",
			reasoningEffort: "medium",
			wantError:       "pro model only supports",
		},
		{
			name:            "conflicting explicit efforts",
			model:           "gpt-5.5",
			reasoningEffort: "high",
			thinkingEffort:  "max",
			wantError:       "conflicts",
		},
		{
			name:           "instant does not support effort",
			model:          "instant",
			thinkingEffort: "standard",
			wantError:      "instant model does not support",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveImitateModel(tt.model, tt.reasoningEffort, tt.thinkingEffort)
			if err == nil {
				t.Fatal("resolveImitateModel() error = nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestConvertAPIRequestUsesResolvedModel(t *testing.T) {
	req, ctx, err := convertAPIRequest(APIRequest{
		Model:           "gpt-5.5-pro",
		ReasoningEffort: "xhigh",
		Messages: []ApiMessage{
			{Role: "user", Content: "hello"},
		},
	}, "token")
	if err != nil {
		t.Fatalf("convertAPIRequest() error = %v", err)
	}
	if req.Model != "gpt-5-5-pro" {
		t.Fatalf("model = %q, want gpt-5-5-pro", req.Model)
	}
	if req.ThinkingEffort != "extended" {
		t.Fatalf("thinking_effort = %q, want extended", req.ThinkingEffort)
	}
	if !ctx.RequiresPrepare {
		t.Fatal("ctx.RequiresPrepare = false, want true")
	}
	if !ctx.AllowEmptyConduitToken {
		t.Fatal("ctx.AllowEmptyConduitToken = false, want true")
	}
}

func TestConvertResponsesRequestUsesReasoningEffort(t *testing.T) {
	req := convertResponsesRequest(ResponsesRequest{
		Model: "gpt-5.5",
		Reasoning: map[string]interface{}{
			"effort": "xhigh",
		},
		Input: "hello",
	})
	if req.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want xhigh", req.ReasoningEffort)
	}
	if req.ThinkingEffort != "" {
		t.Fatalf("thinking_effort = %q, want empty", req.ThinkingEffort)
	}
}

func TestLegacyModelResolutionKeepsO4MiniHigh(t *testing.T) {
	got, err := resolveImitateModel("o4-mini-high", "", "")
	if err != nil {
		t.Fatalf("resolveImitateModel() error = %v", err)
	}
	if got.Model != "o4-mini-high" {
		t.Fatalf("model = %q, want o4-mini-high", got.Model)
	}

	got, err = resolveImitateModel("o4-mini", "", "")
	if err != nil {
		t.Fatalf("resolveImitateModel() error = %v", err)
	}
	if got.Model != "o4-mini" {
		t.Fatalf("model = %q, want o4-mini", got.Model)
	}
}

func TestImitateModelCatalogResolves(t *testing.T) {
	seen := make(map[string]bool, len(imitateModelCatalogIDs))
	for _, id := range imitateModelCatalogIDs {
		model, ok := buildImitateModelInfo(id)
		if !ok {
			t.Fatalf("catalog model %q does not resolve", id)
		}
		if model.ID == "" {
			t.Fatalf("catalog model %q returned empty id", id)
		}
		if seen[model.ID] {
			t.Fatalf("catalog model %q is duplicated after normalization", model.ID)
		}
		seen[model.ID] = true
		if model.Object != "model" {
			t.Fatalf("catalog model %q object = %q, want model", id, model.Object)
		}
		if model.Root != "" || model.ChatGPTModel != "" || model.ThinkingEffort != "" || model.CompatibilityTag != "" {
			t.Fatalf("catalog model %q returned debug fields by default: %+v", id, model)
		}
	}
}

func TestBuildImitateModelInfoNormalizesAndRejectsUnknown(t *testing.T) {
	model, ok := buildImitateModelInfo("GPT-5_5_HIGH", true)
	if !ok {
		t.Fatal("buildImitateModelInfo() ok = false, want true")
	}
	if model.ID != "gpt-5-5-high" {
		t.Fatalf("id = %q, want gpt-5-5-high", model.ID)
	}
	if model.ChatGPTModel != "gpt-5-5-thinking" {
		t.Fatalf("chatgpt model = %q, want gpt-5-5-thinking", model.ChatGPTModel)
	}
	if model.ThinkingEffort != "extended" {
		t.Fatalf("thinking effort = %q, want extended", model.ThinkingEffort)
	}

	for _, id := range []string{"", "gpt-3.5-turbo", "gpt-4", "gpt-5.5-pro-low"} {
		if model, ok = buildImitateModelInfo(id); ok {
			t.Fatalf("buildImitateModelInfo(%q) ok = true, model = %+v", id, model)
		}
	}
}

func TestNewConversationHTTPReqUsesSeparateURLAndTargetPaths(t *testing.T) {
	req, err := newConversationHTTPReq(
		"token",
		"uid",
		http.MethodPost,
		conversationPrepareURLPath,
		conversationPrepareTargetPath,
		"*/*",
		strings.NewReader("{}"),
		"session-id",
		"turn-trace-id",
	)
	if err != nil {
		t.Fatalf("newConversationHTTPReq() error = %v", err)
	}
	if got, want := req.URL.String(), "https://chatgpt.com/backend-api/f/conversation/prepare"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if got := req.Header.Get("X-Openai-Target-Path"); got != conversationPrepareTargetPath {
		t.Fatalf("target path = %q, want %q", got, conversationPrepareTargetPath)
	}
	if got := req.Header.Get("Oai-Client-Version"); got != oaiClientVersion {
		t.Fatalf("client version = %q, want %q", got, oaiClientVersion)
	}
	if got := req.Header.Get("Oai-Session-Id"); got != "session-id" {
		t.Fatalf("session id = %q, want session-id", got)
	}
	if got := req.Header.Get("X-Oai-Turn-Trace-Id"); got != "turn-trace-id" {
		t.Fatalf("turn trace id = %q, want turn-trace-id", got)
	}
}
