package imitate

import (
	"bufio"
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestParseConversationStreamV1Complete(t *testing.T) {
	var deltas []string
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"type":"resume_conversation_token","conversation_id":"conv_1","token":"token"}`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"msg_1","author":{"role":"assistant"},"content":{"content_type":"text","parts":["Hel"]},"metadata":{"message_type":"next","model_slug":"gpt-5-5-pro"},"recipient":"all","status":"in_progress"}}}`,
		`data: {"o":"append","p":"/message/content/parts/0","v":"lo"}`,
		`data: {"o":"patch","v":[{"o":"replace","p":"/message/status","v":"finished_successfully"},{"o":"replace","p":"/message/end_turn","v":true},{"o":"append","p":"/message/metadata","v":{"is_complete":true,"finish_details":{"type":"stop","stop_tokens":[200002]}}}]}`,
		`data: {"type":"message_marker","event":"last","message_id":"msg_1","conversation_id":"conv_1"}`,
		`data: {"type":"message_stream_complete","conversation_id":"conv_1"}`,
		`data: [DONE]`,
		``,
	}, "\n"))), func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if result.Text != "Hello" {
		t.Fatalf("text = %q, want Hello", result.Text)
	}
	if got := strings.Join(deltas, ""); got != "Hello" {
		t.Fatalf("deltas = %q, want Hello", got)
	}
	if result.Incomplete {
		t.Fatal("result.Incomplete = true, want false")
	}
	if !result.SawDone || !result.SawMessageStreamComplete || !result.SawLastMarker {
		t.Fatalf("completion markers not recorded: %#v", result)
	}
	if result.FinishReason != "stop" {
		t.Fatalf("finish reason = %q, want stop", result.FinishReason)
	}
}

func TestParseConversationStreamBindsGeneratedImageCandidateConversationID(t *testing.T) {
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"type":"resume_conversation_token","conversation_id":"conv_1","token":"token"}`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"msg_image","author":{"role":"tool"},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"sediment://file_generated"}]},"metadata":{"message_type":"next"},"recipient":"all","status":"finished_successfully"}}}`,
		`data: [DONE]`,
		``,
	}, "\n"))), nil)
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if len(result.GeneratedImageCandidates) != 1 {
		t.Fatalf("len(GeneratedImageCandidates) = %d, want 1", len(result.GeneratedImageCandidates))
	}
	if result.GeneratedImageCandidates[0].ConversationID != "conv_1" {
		t.Fatalf("candidate conversation id = %q, want conv_1", result.GeneratedImageCandidates[0].ConversationID)
	}
}

func TestParseConversationStreamV1HandoffIsIncomplete(t *testing.T) {
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"type":"resume_conversation_token","conversation_id":"conv_1","token":"token"}`,
		`data: {"type":"stream_handoff","conversation_id":"conv_1","turn_exchange_id":"turn_1","options":{}}`,
		`data: [DONE]`,
		``,
	}, "\n"))), nil)
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if !result.Incomplete {
		t.Fatal("result.Incomplete = false, want true")
	}
	if !result.SawStreamHandoff {
		t.Fatal("result.SawStreamHandoff = false, want true")
	}
	if result.ConversationID != "conv_1" {
		t.Fatalf("conversation id = %q, want conv_1", result.ConversationID)
	}
	if result.TurnExchangeID != "turn_1" {
		t.Fatalf("turn exchange id = %q, want turn_1", result.TurnExchangeID)
	}
}

func TestParseConversationStreamV1FinalChannelMarkerReceivesImplicitAppends(t *testing.T) {
	var deltas []string
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"type":"resume_conversation_token","conversation_id":"conv_1","token":"token"}`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"system_1","author":{"role":"system"},"content":{"content_type":"text","parts":[""]},"metadata":{"message_type":"next"},"recipient":"all","status":"finished_successfully","end_turn":true}}}`,
		`data: {"type":"message_marker","event":"first","marker":"cot_token","message_id":"reasoning_1","conversation_id":"conv_1"}`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"reasoning_1","author":{"role":"assistant"},"content":{"content_type":"reasoning_recap","parts":[]},"metadata":{"message_type":"next"},"recipient":"all","status":"finished_successfully","end_turn":false}}}`,
		`data: {"type":"message_marker","event":"first","marker":"user_visible_token","message_id":"msg_1","conversation_id":"conv_1"}`,
		`data: {"type":"message_marker","event":"first","marker":"final_channel_token","message_id":"msg_1","conversation_id":"conv_1"}`,
		`data: {"o":"append","p":"/message/content/parts/0","v":"He"}`,
		`data: {"c":0,"v":"llo"}`,
		`data: {"o":"patch","v":[{"o":"append","p":"/message/content/parts/0","v":"!"},{"o":"replace","p":"/message/status","v":"finished_successfully"},{"o":"replace","p":"/message/end_turn","v":true},{"o":"append","p":"/message/metadata","v":{"is_complete":true,"finish_details":{"type":"stop"}}}]}`,
		`data: {"type":"message_marker","event":"last","marker":"last_token","message_id":"msg_1","conversation_id":"conv_1"}`,
		`data: {"type":"message_stream_complete","conversation_id":"conv_1"}`,
		`data: [DONE]`,
		``,
	}, "\n"))), func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if result.Text != "Hello!" {
		t.Fatalf("text = %q, want Hello!", result.Text)
	}
	if got := strings.Join(deltas, ""); got != "Hello!" {
		t.Fatalf("deltas = %q, want Hello!", got)
	}
	if result.MessageID != "msg_1" {
		t.Fatalf("message id = %q, want msg_1", result.MessageID)
	}
	if result.Incomplete {
		t.Fatal("result.Incomplete = true, want false")
	}
	if result.FinishReason != "stop" {
		t.Fatalf("finish reason = %q, want stop", result.FinishReason)
	}
}

func TestParseConversationStreamV1MissingTerminalPatchIsIncomplete(t *testing.T) {
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"msg_1","author":{"role":"assistant"},"content":{"content_type":"text","parts":["partial"]},"metadata":{"message_type":"next"},"recipient":"all","status":"in_progress"}}}`,
		`data: {"type":"message_stream_complete","conversation_id":"conv_1"}`,
		`data: [DONE]`,
		``,
	}, "\n"))), nil)
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if !result.Incomplete {
		t.Fatal("result.Incomplete = false, want true")
	}
}

func TestParseConversationStreamCollectsGeneratedImageCandidate(t *testing.T) {
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"tool_1","author":{"role":"tool"},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"sediment://file_generated","width":1024,"height":1024,"metadata":{"dalle":{"prompt":"revised prompt"}}}]},"metadata":{"message_type":"next"},"recipient":"all","status":"finished_successfully"}}}`,
		`data: [DONE]`,
		``,
	}, "\n"))), nil)
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if len(result.GeneratedImageCandidates) != 1 {
		t.Fatalf("len(GeneratedImageCandidates) = %d, want 1", len(result.GeneratedImageCandidates))
	}
	candidate := result.GeneratedImageCandidates[0]
	if candidate.Pointer != "sediment://file_generated" {
		t.Fatalf("candidate pointer = %q, want sediment://file_generated", candidate.Pointer)
	}
	if candidate.RevisedPrompt != "revised prompt" {
		t.Fatalf("revised prompt = %q, want revised prompt", candidate.RevisedPrompt)
	}
	if candidate.Width != 1024 || candidate.Height != 1024 {
		t.Fatalf("dimensions = %dx%d, want 1024x1024", candidate.Width, candidate.Height)
	}
}

func TestParseConversationStreamMaxTokensReturnsContinueInfo(t *testing.T) {
	result, err := parseConversationStream(bufio.NewReader(strings.NewReader(strings.Join([]string{
		`data: "v1"`,
		`data: {"o":"add","v":{"conversation_id":"conv_1","message":{"id":"msg_1","author":{"role":"assistant"},"content":{"content_type":"text","parts":["partial"]},"metadata":{"message_type":"next"},"recipient":"all","status":"in_progress"}}}`,
		`data: {"o":"patch","v":[{"o":"replace","p":"/message/status","v":"finished_successfully"},{"o":"replace","p":"/message/end_turn","v":true},{"o":"append","p":"/message/metadata","v":{"is_complete":true,"finish_details":{"type":"max_tokens"}}}]}`,
		`data: {"type":"message_stream_complete","conversation_id":"conv_1"}`,
		`data: [DONE]`,
		``,
	}, "\n"))), nil)
	if err != nil {
		t.Fatalf("parseConversationStream() error = %v", err)
	}
	if result.ContinueInfo == nil {
		t.Fatal("ContinueInfo = nil, want value")
	}
	if result.ContinueInfo.ConversationID != "conv_1" || result.ContinueInfo.ParentID != "msg_1" {
		t.Fatalf("ContinueInfo = %#v, want conv_1/msg_1", result.ContinueInfo)
	}
}

func TestRemainingTextDeltaUsesOverlap(t *testing.T) {
	if got := remainingTextDelta("abcdef", "defghi"); got != "ghi" {
		t.Fatalf("remainingTextDelta overlap = %q, want ghi", got)
	}
	if got := remainingTextDelta("你好，世", "你好，世界"); got != "界" {
		t.Fatalf("remainingTextDelta unicode prefix = %q, want 界", got)
	}
}

func TestWaitConversationStreamCompletePollsUntilComplete(t *testing.T) {
	originalRequester := chatGPTJSONRequester
	defer func() {
		chatGPTJSONRequester = originalRequester
	}()
	t.Setenv("IMITATE_FINALIZE_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_FINALIZE_TIMEOUT_MS", "100")

	calls := 0
	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		calls++
		responseBody := ""
		if calls == 3 {
			responseBody = `{"status":"COMPLETE"}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		}, nil
	}

	if err := waitConversationStreamComplete("token", "conv_1"); err != nil {
		t.Fatalf("waitConversationStreamComplete() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestFinalizeIncompleteConversationPostsAsyncStatusAndPollsConversationOutput(t *testing.T) {
	originalRequester := chatGPTJSONRequester
	originalFetchOutputs := fetchConversationOutputsForResult
	defer func() {
		chatGPTJSONRequester = originalRequester
		fetchConversationOutputsForResult = originalFetchOutputs
	}()
	t.Setenv("IMITATE_FINALIZE_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_FINALIZE_TIMEOUT_MS", "100")

	asyncStatusCalls := 0
	streamStatusCalls := 0
	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		switch {
		case strings.Contains(endpoint, "/async-status"):
			asyncStatusCalls++
			if method != http.MethodPost {
				t.Fatalf("async-status method = %q, want POST", method)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"status":"OK"}`)),
			}, nil
		case strings.Contains(endpoint, "/stream_status"):
			streamStatusCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"status":"IS_STREAMING"}`)),
			}, nil
		default:
			t.Fatalf("unexpected endpoint %q", endpoint)
			return nil, nil
		}
	}

	fetchCalls := 0
	fetchConversationOutputsForResult = func(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
		fetchCalls++
		if conversationID != "conv_1" {
			t.Fatalf("conversation id = %q, want conv_1", conversationID)
		}
		if fetchCalls == 1 {
			return "", "", nil, nil
		}
		return "complete text", "msg_1", []ResponsesOutputMessage{newResponsesOutputMessage("complete text")}, nil
	}

	result := &conversationResult{ConversationID: "conv_1"}
	if err := finalizeIncompleteConversation("token", result, false); err != nil {
		t.Fatalf("finalizeIncompleteConversation() error = %v", err)
	}
	if asyncStatusCalls != 1 {
		t.Fatalf("async-status calls = %d, want 1", asyncStatusCalls)
	}
	if streamStatusCalls == 0 {
		t.Fatal("stream_status was not polled")
	}
	if fetchCalls < 2 {
		t.Fatalf("fetch calls = %d, want at least 2", fetchCalls)
	}
	if result.Text != "complete text" {
		t.Fatalf("result text = %q, want complete text", result.Text)
	}
	if result.MessageID != "msg_1" {
		t.Fatalf("message id = %q, want msg_1", result.MessageID)
	}
}
