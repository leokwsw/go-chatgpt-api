package imitate

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
	"github.com/leokwsw/go-chatgpt-api/api"
)

func TestResponsesRequestUsesImageGeneration(t *testing.T) {
	req := ResponsesRequest{
		Tools: []interface{}{
			map[string]interface{}{
				"type": "image_generation",
				"size": "1024x1024",
			},
		},
	}
	if !responsesRequestUsesImageGeneration(req) {
		t.Fatal("responsesRequestUsesImageGeneration() = false, want true")
	}

	req = ResponsesRequest{
		ToolChoice: map[string]interface{}{
			"type": "image_generation",
		},
	}
	if !responsesRequestUsesImageGeneration(req) {
		t.Fatal("responsesRequestUsesImageGeneration(tool_choice) = false, want true")
	}
}

func TestResponsesConversationOutputAssistantUsesOutputTextWithoutRecipient(t *testing.T) {
	message := newResponsesConversationOutputMessage(conversationOutputNode{
		NodeID:      "msg_1",
		Role:        "assistant",
		ContentType: "text",
		Text:        `{"ok":true}`,
	})
	if len(message.Content) != 1 {
		t.Fatalf("len(content) = %d, want 1", len(message.Content))
	}
	if message.Content[0].Type != "output_text" {
		t.Fatalf("content type = %q, want output_text", message.Content[0].Type)
	}
}

func TestNewGeneratedImageDownloadRequestMatchesHARImageHeaders(t *testing.T) {
	req, err := newGeneratedImageDownloadRequest("https://chatgpt.com/backend-api/estuary/content?id=file_1", "conv_1")
	if err != nil {
		t.Fatalf("newGeneratedImageDownloadRequest() error = %v", err)
	}

	wantHeaders := map[string]string{
		"Accept":             generatedImageDownloadAccept,
		"Accept-Language":    api.AcceptLanguage,
		"Referer":            api.ChatGPTApiUrlPrefix + "/c/conv_1",
		"Sec-CH-UA":          api.SecCHUA,
		"Sec-CH-UA-Mobile":   api.SecCHUAMobile,
		"Sec-CH-UA-Platform": api.SecCHUAPlatform,
		"Sec-Fetch-Dest":     "image",
		"Sec-Fetch-Mode":     "no-cors",
		"Sec-Fetch-Site":     "same-origin",
		"Priority":           "i",
	}
	for key, want := range wantHeaders {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("%s header = %q, want %q", key, got, want)
		}
	}
	if got := req.Header.Get("User-Agent"); got == "" {
		t.Fatal("User-Agent header is empty")
	}
	for _, key := range []string{"Authorization", "Cookie", "Oai-Device-Id", "Oai-Language", "Origin"} {
		if got := req.Header.Get(key); got != "" {
			t.Fatalf("%s header = %q, want empty", key, got)
		}
	}
}

func TestNewGeneratedImageDownloadRequestUsesRootRefererWithoutConversation(t *testing.T) {
	req, err := newGeneratedImageDownloadRequest("https://chatgpt.com/backend-api/estuary/content?id=file_1", "")
	if err != nil {
		t.Fatalf("newGeneratedImageDownloadRequest() error = %v", err)
	}
	if got, want := req.Header.Get("Referer"), api.ChatGPTApiUrlPrefix+"/"; got != want {
		t.Fatalf("Referer header = %q, want %q", got, want)
	}
}

func TestFetchGeneratedImageAssetRetriesFileLinkNotFound(t *testing.T) {
	originalGetURLs := getBackendFileDownloadURLsForResult
	originalDownload := downloadGeneratedImageContentForResult
	defer func() {
		getBackendFileDownloadURLsForResult = originalGetURLs
		downloadGeneratedImageContentForResult = originalDownload
	}()
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_TIMEOUT_MS", "100")

	metadataCalls := 0
	getBackendFileDownloadURLsForResult = func(accessToken string, fileID string, conversationID string) ([]string, error) {
		metadataCalls++
		if accessToken != "token" {
			t.Fatalf("access token = %q, want token", accessToken)
		}
		if fileID != "file_generated" {
			t.Fatalf("file id = %q, want file_generated", fileID)
		}
		if conversationID != "conv_1" {
			t.Fatalf("conversation id = %q, want conv_1", conversationID)
		}
		sig := "first"
		if metadataCalls > 1 {
			sig = "second"
		}
		return []string{"https://chatgpt.com/backend-api/estuary/content?id=file_generated&sig=" + sig}, nil
	}

	downloadCalls := 0
	downloadURLs := make([]string, 0, 2)
	downloadGeneratedImageContentForResult = func(accessToken string, downloadURL string, conversationID string) ([]byte, error) {
		downloadCalls++
		downloadURLs = append(downloadURLs, downloadURL)
		if accessToken != "token" {
			t.Fatalf("access token = %q, want token", accessToken)
		}
		if downloadCalls == 1 {
			return nil, &upstreamHTTPError{
				Operation:  "generated image content download",
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Body:       `{"detail":"File link not found."}`,
			}
		}
		return []byte("image-bytes"), nil
	}

	asset, err := fetchGeneratedImageAsset("token", "sediment://file_generated", "conv_1")
	if err != nil {
		t.Fatalf("fetchGeneratedImageAsset() error = %v", err)
	}
	if metadataCalls != 2 {
		t.Fatalf("metadata calls = %d, want 2", metadataCalls)
	}
	if downloadCalls != 2 {
		t.Fatalf("download calls = %d, want 2", downloadCalls)
	}
	if len(downloadURLs) != 2 || downloadURLs[0] == downloadURLs[1] {
		t.Fatalf("download URLs = %#v, want fresh metadata URL after retry", downloadURLs)
	}
	if want := base64.StdEncoding.EncodeToString([]byte("image-bytes")); asset.Result != want {
		t.Fatalf("asset result = %q, want %q", asset.Result, want)
	}
}

func TestFetchGeneratedImageAssetRetriesMetadataEOF(t *testing.T) {
	originalGetURLs := getBackendFileDownloadURLsForResult
	originalDownload := downloadGeneratedImageContentForResult
	defer func() {
		getBackendFileDownloadURLsForResult = originalGetURLs
		downloadGeneratedImageContentForResult = originalDownload
	}()
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_TIMEOUT_MS", "100")

	metadataCalls := 0
	getBackendFileDownloadURLsForResult = func(accessToken string, fileID string, conversationID string) ([]string, error) {
		metadataCalls++
		if metadataCalls == 1 {
			return nil, io.EOF
		}
		return []string{"https://chatgpt.com/backend-api/estuary/content?id=file_generated"}, nil
	}
	downloadGeneratedImageContentForResult = func(accessToken string, downloadURL string, conversationID string) ([]byte, error) {
		return []byte("image-bytes"), nil
	}

	asset, err := fetchGeneratedImageAsset("token", "sediment://file_generated", "conv_1")
	if err != nil {
		t.Fatalf("fetchGeneratedImageAsset() error = %v", err)
	}
	if metadataCalls != 2 {
		t.Fatalf("metadata calls = %d, want 2", metadataCalls)
	}
	if want := base64.StdEncoding.EncodeToString([]byte("image-bytes")); asset.Result != want {
		t.Fatalf("asset result = %q, want %q", asset.Result, want)
	}
}

func TestFetchGeneratedImageAssetReportsRetryAttempts(t *testing.T) {
	originalGetURLs := getBackendFileDownloadURLsForResult
	originalDownload := downloadGeneratedImageContentForResult
	defer func() {
		getBackendFileDownloadURLsForResult = originalGetURLs
		downloadGeneratedImageContentForResult = originalDownload
	}()
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_TIMEOUT_MS", "10")

	getBackendFileDownloadURLsForResult = func(accessToken string, fileID string, conversationID string) ([]string, error) {
		return []string{"https://chatgpt.com/backend-api/estuary/content?id=file_generated"}, nil
	}
	downloadGeneratedImageContentForResult = func(accessToken string, downloadURL string, conversationID string) ([]byte, error) {
		return nil, &upstreamHTTPError{
			Operation:  "generated image content download",
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       `{"detail":"File link not found."}`,
		}
	}

	_, err := fetchGeneratedImageAsset("token", "sediment://file_generated", "conv_1")
	if err == nil {
		t.Fatal("fetchGeneratedImageAsset() error = nil, want retry exhaustion")
	}
	for _, want := range []string{"metadata_attempts=", "content_attempts=", "File link not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
}

func TestFetchGeneratedImageAssetStopsOnMetadataForbidden(t *testing.T) {
	originalGetURLs := getBackendFileDownloadURLsForResult
	originalDownload := downloadGeneratedImageContentForResult
	defer func() {
		getBackendFileDownloadURLsForResult = originalGetURLs
		downloadGeneratedImageContentForResult = originalDownload
	}()
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_IMAGE_GENERATION_DOWNLOAD_TIMEOUT_MS", "100")

	getBackendFileDownloadURLsForResult = func(accessToken string, fileID string, conversationID string) ([]string, error) {
		return nil, &upstreamHTTPError{
			Operation:  "generated image download metadata",
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Body:       `{"detail":"Forbidden"}`,
		}
	}
	downloadGeneratedImageContentForResult = func(accessToken string, downloadURL string, conversationID string) ([]byte, error) {
		t.Fatal("content download should not run after metadata 403")
		return nil, nil
	}

	_, err := fetchGeneratedImageAsset("token", "sediment://file_generated", "conv_1")
	if err == nil {
		t.Fatal("fetchGeneratedImageAsset() error = nil, want forbidden error")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("error = %q, want Forbidden", err.Error())
	}
}

func TestExtractImageGenerationTaskOutputs(t *testing.T) {
	originalFetchAsset := fetchGeneratedImageAssetForResult
	defer func() {
		fetchGeneratedImageAssetForResult = originalFetchAsset
	}()
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		if accessToken != "token" {
			t.Fatalf("access token = %q, want token", accessToken)
		}
		if assetPointer != "sediment://file_generated" {
			t.Fatalf("asset pointer = %q, want sediment://file_generated", assetPointer)
		}
		return generatedImageAsset{Result: "aW1hZ2U="}, nil
	}

	task := map[string]interface{}{
		"conversation_id": "conv_1",
		"image_gen_message": map[string]interface{}{
			"author": map[string]interface{}{"role": "tool"},
			"content": map[string]interface{}{
				"content_type": "multimodal_text",
				"parts": []interface{}{
					map[string]interface{}{
						"content_type":  "image_asset_pointer",
						"asset_pointer": "sediment://file_generated",
						"metadata": map[string]interface{}{
							"dalle": map[string]interface{}{"prompt": "revised prompt"},
						},
					},
				},
			},
		},
		"final_message": map[string]interface{}{
			"author": map[string]interface{}{"role": "assistant"},
			"content": map[string]interface{}{
				"content_type": "text",
				"parts":        []interface{}{`{"ok":true}`},
			},
		},
	}

	outputs, err := extractImageGenerationTaskOutputs("token", task)
	if err != nil {
		t.Fatalf("extractImageGenerationTaskOutputs() error = %v", err)
	}
	if len(outputs) != 2 {
		t.Fatalf("len(outputs) = %d, want 2", len(outputs))
	}
	if outputs[0].Type != "image_generation_call" {
		t.Fatalf("first output type = %q, want image_generation_call", outputs[0].Type)
	}
	if outputs[0].Result != "aW1hZ2U=" {
		t.Fatalf("image result = %q, want base64", outputs[0].Result)
	}
	if outputs[0].RevisedPrompt != "revised prompt" {
		t.Fatalf("revised prompt = %q, want revised prompt", outputs[0].RevisedPrompt)
	}
	if outputs[1].Type != "message" || outputs[1].Content[0].Text != `{"ok":true}` {
		t.Fatalf("text output = %#v, want sidecar message", outputs[1])
	}
}

func TestWaitImageGenerationOutputsMatchesConversationTask(t *testing.T) {
	originalFetchTasks := fetchImageGenerationTasksForResult
	originalFetchAsset := fetchGeneratedImageAssetForResult
	originalFetchOutputs := fetchConversationOutputsForResult
	originalRequester := chatGPTJSONRequester
	defer func() {
		fetchImageGenerationTasksForResult = originalFetchTasks
		fetchGeneratedImageAssetForResult = originalFetchAsset
		fetchConversationOutputsForResult = originalFetchOutputs
		chatGPTJSONRequester = originalRequester
	}()
	t.Setenv("IMITATE_IMAGE_GENERATION_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_IMAGE_GENERATION_TIMEOUT_MS", "100")

	fetchConversationOutputsForResult = func(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
		return "", "", nil, nil
	}
	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"status":"OK"}`)),
		}, nil
	}
	fetchImageGenerationTasksForResult = func(accessToken string) ([]map[string]interface{}, error) {
		return []map[string]interface{}{
			{
				"conversation_id": "other",
				"image_gen_message": map[string]interface{}{
					"content": map[string]interface{}{"parts": []interface{}{}},
				},
			},
			{
				"original_conversation_id": "conv_1",
				"image_gen_message": map[string]interface{}{
					"content": map[string]interface{}{
						"content_type": "multimodal_text",
						"parts": []interface{}{
							map[string]interface{}{
								"content_type":  "image_asset_pointer",
								"asset_pointer": "sediment://file_generated",
							},
						},
					},
				},
			},
		}, nil
	}
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		return generatedImageAsset{Result: "aW1hZ2U="}, nil
	}

	result := &conversationResult{ConversationID: "conv_1"}
	if err := waitImageGenerationOutputs("token", result); err != nil {
		t.Fatalf("waitImageGenerationOutputs() error = %v", err)
	}
	if !responsesOutputHasImageGeneration(result.OutputMessages) {
		t.Fatalf("output messages = %#v, want image generation output", result.OutputMessages)
	}
}

func TestWaitImageGenerationOutputsUsesStreamCandidatesBeforePolling(t *testing.T) {
	originalFetchTasks := fetchImageGenerationTasksForResult
	originalFetchAsset := fetchGeneratedImageAssetForResult
	originalFetchOutputs := fetchConversationOutputsForResult
	originalRequester := chatGPTJSONRequester
	defer func() {
		fetchImageGenerationTasksForResult = originalFetchTasks
		fetchGeneratedImageAssetForResult = originalFetchAsset
		fetchConversationOutputsForResult = originalFetchOutputs
		chatGPTJSONRequester = originalRequester
	}()

	fetchConversationOutputsForResult = func(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
		t.Fatal("conversation fallback should not be called when stream candidate resolves")
		return "", "", nil, nil
	}
	fetchImageGenerationTasksForResult = func(accessToken string) ([]map[string]interface{}, error) {
		t.Fatal("tasks fallback should not be called when stream candidate resolves")
		return nil, nil
	}
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		if accessToken != "token" {
			t.Fatalf("access token = %q, want token", accessToken)
		}
		if assetPointer != "sediment://file_generated" {
			t.Fatalf("asset pointer = %q, want sediment://file_generated", assetPointer)
		}
		if conversationID != "conv_1" {
			t.Fatalf("conversation id = %q, want conv_1", conversationID)
		}
		return generatedImageAsset{Result: "aW1hZ2U="}, nil
	}
	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		t.Fatal("async-status should not be called when stream candidate resolves")
		return nil, nil
	}

	result := &conversationResult{
		ConversationID: "conv_1",
		GeneratedImageCandidates: []generatedImagePointerCandidate{
			{Pointer: "sediment://file_generated", RevisedPrompt: "revised prompt"},
		},
	}
	if err := waitImageGenerationOutputs("token", result); err != nil {
		t.Fatalf("waitImageGenerationOutputs() error = %v", err)
	}
	if !responsesOutputHasImageGeneration(result.OutputMessages) {
		t.Fatalf("output messages = %#v, want image generation output", result.OutputMessages)
	}
	if result.OutputMessages[0].RevisedPrompt != "revised prompt" {
		t.Fatalf("revised prompt = %q, want revised prompt", result.OutputMessages[0].RevisedPrompt)
	}
}

func TestWaitImageGenerationOutputsUsesCandidateConversationID(t *testing.T) {
	originalFetchTasks := fetchImageGenerationTasksForResult
	originalFetchAsset := fetchGeneratedImageAssetForResult
	originalFetchOutputs := fetchConversationOutputsForResult
	originalRequester := chatGPTJSONRequester
	defer func() {
		fetchImageGenerationTasksForResult = originalFetchTasks
		fetchGeneratedImageAssetForResult = originalFetchAsset
		fetchConversationOutputsForResult = originalFetchOutputs
		chatGPTJSONRequester = originalRequester
	}()

	fetchConversationOutputsForResult = func(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
		t.Fatal("conversation fallback should not be called when candidate conversation id resolves")
		return "", "", nil, nil
	}
	fetchImageGenerationTasksForResult = func(accessToken string) ([]map[string]interface{}, error) {
		t.Fatal("tasks fallback should not be called when candidate conversation id resolves")
		return nil, nil
	}
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		if conversationID != "conv_candidate" {
			t.Fatalf("conversation id = %q, want conv_candidate", conversationID)
		}
		return generatedImageAsset{Result: "aW1hZ2U="}, nil
	}
	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		t.Fatal("async-status should not be called without result conversation id")
		return nil, nil
	}

	result := &conversationResult{
		GeneratedImageCandidates: []generatedImagePointerCandidate{
			{Pointer: "sediment://file_generated", ConversationID: "conv_candidate"},
		},
	}
	if err := waitImageGenerationOutputs("token", result); err != nil {
		t.Fatalf("waitImageGenerationOutputs() error = %v", err)
	}
	if !responsesOutputHasImageGeneration(result.OutputMessages) {
		t.Fatalf("output messages = %#v, want image generation output", result.OutputMessages)
	}
}

func TestResolveGeneratedImageCandidatesSkipsMissingConversationID(t *testing.T) {
	originalFetchAsset := fetchGeneratedImageAssetForResult
	defer func() {
		fetchGeneratedImageAssetForResult = originalFetchAsset
	}()
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		t.Fatal("asset fetch should not run without conversation id")
		return generatedImageAsset{}, nil
	}

	outputs, err := resolveGeneratedImageCandidates("token", "", []generatedImagePointerCandidate{
		{Pointer: "sediment://file_generated"},
	}, nil)
	if err == nil {
		t.Fatal("resolveGeneratedImageCandidates() error = nil, want missing conversation id")
	}
	if len(outputs) != 0 {
		t.Fatalf("len(outputs) = %d, want 0", len(outputs))
	}
	if !strings.Contains(err.Error(), "missing conversation id") {
		t.Fatalf("error = %q, want missing conversation id", err.Error())
	}
}

func TestMergeGeneratedImageCandidatesBackfillsConversationID(t *testing.T) {
	merged := mergeGeneratedImageCandidates(
		[]generatedImagePointerCandidate{{Pointer: "sediment://file_generated"}},
		[]generatedImagePointerCandidate{{Pointer: "file_generated", ConversationID: "conv_1"}},
	)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1", len(merged))
	}
	if merged[0].ConversationID != "conv_1" {
		t.Fatalf("conversation id = %q, want conv_1", merged[0].ConversationID)
	}
}

func TestWaitImageGenerationOutputsContinuesAfterConversationRateLimit(t *testing.T) {
	originalFetchTasks := fetchImageGenerationTasksForResult
	originalFetchAsset := fetchGeneratedImageAssetForResult
	originalFetchOutputs := fetchConversationOutputsForResult
	originalRequester := chatGPTJSONRequester
	defer func() {
		fetchImageGenerationTasksForResult = originalFetchTasks
		fetchGeneratedImageAssetForResult = originalFetchAsset
		fetchConversationOutputsForResult = originalFetchOutputs
		chatGPTJSONRequester = originalRequester
	}()
	t.Setenv("IMITATE_IMAGE_GENERATION_POLL_INTERVAL_MS", "10")
	t.Setenv("IMITATE_IMAGE_GENERATION_CONVERSATION_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_IMAGE_GENERATION_RATE_LIMIT_BACKOFF_MS", "5")
	t.Setenv("IMITATE_IMAGE_GENERATION_TIMEOUT_MS", "200")

	conversationCalls := 0
	fetchConversationOutputsForResult = func(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
		conversationCalls++
		return "", "", nil, &upstreamHTTPError{
			Operation:  "fetch conversation",
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Body:       `{"detail":"Too many requests"}`,
		}
	}
	taskCalls := 0
	fetchImageGenerationTasksForResult = func(accessToken string) ([]map[string]interface{}, error) {
		taskCalls++
		if taskCalls < 2 {
			return nil, nil
		}
		return []map[string]interface{}{
			{
				"original_conversation_id": "conv_1",
				"image_gen_message": map[string]interface{}{
					"content": map[string]interface{}{
						"content_type": "multimodal_text",
						"parts": []interface{}{
							map[string]interface{}{
								"content_type":  "image_asset_pointer",
								"asset_pointer": "sediment://file_generated",
							},
						},
					},
				},
			},
		}, nil
	}
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		return generatedImageAsset{Result: "aW1hZ2U="}, nil
	}
	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"status":"OK"}`)),
		}, nil
	}

	result := &conversationResult{ConversationID: "conv_1"}
	if err := waitImageGenerationOutputs("token", result); err != nil {
		t.Fatalf("waitImageGenerationOutputs() error = %v", err)
	}
	if conversationCalls == 0 {
		t.Fatal("conversation fallback was not called")
	}
	if !responsesOutputHasImageGeneration(result.OutputMessages) {
		t.Fatalf("output messages = %#v, want image generation output", result.OutputMessages)
	}
}

func TestExtractConversationImageGenerationMessagesFindsRawFileID(t *testing.T) {
	originalFetchAsset := fetchGeneratedImageAssetForResult
	defer func() {
		fetchGeneratedImageAssetForResult = originalFetchAsset
	}()
	fetchGeneratedImageAssetForResult = func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
		if accessToken != "token" {
			t.Fatalf("access token = %q, want token", accessToken)
		}
		if assetPointer != "file_generated" {
			t.Fatalf("asset pointer = %q, want raw file id", assetPointer)
		}
		if conversationID != "conv_1" {
			t.Fatalf("conversation id = %q, want conv_1", conversationID)
		}
		return generatedImageAsset{Result: "aW1hZ2U="}, nil
	}

	mapping := map[string]interface{}{
		"tool_1": map[string]interface{}{
			"message": map[string]interface{}{
				"author":      map[string]interface{}{"role": "tool"},
				"create_time": float64(1),
				"content": map[string]interface{}{
					"content_type": "multimodal_text",
					"parts": []interface{}{
						map[string]interface{}{
							"content_type": "image/png",
							"file_id":      "file_generated",
							"width":        float64(1024),
							"height":       float64(1024),
							"metadata": map[string]interface{}{
								"dalle": map[string]interface{}{"prompt": "revised prompt"},
							},
						},
					},
				},
			},
		},
	}

	outputs, err := extractConversationImageGenerationMessages("token", "conv_1", mapping, "")
	if err != nil {
		t.Fatalf("extractConversationImageGenerationMessages() error = %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("len(outputs) = %d, want 1", len(outputs))
	}
	if outputs[0].Type != "image_generation_call" || outputs[0].Result != "aW1hZ2U=" {
		t.Fatalf("output = %#v, want image generation call", outputs[0])
	}
	if outputs[0].RevisedPrompt != "revised prompt" {
		t.Fatalf("revised prompt = %q, want revised prompt", outputs[0].RevisedPrompt)
	}
}

func TestFinalizeIncompleteConversationAllowsImageGenerationOutput(t *testing.T) {
	originalRequester := chatGPTJSONRequester
	originalFetchOutputs := fetchConversationOutputsForResult
	defer func() {
		chatGPTJSONRequester = originalRequester
		fetchConversationOutputsForResult = originalFetchOutputs
	}()
	t.Setenv("IMITATE_FINALIZE_POLL_INTERVAL_MS", "1")
	t.Setenv("IMITATE_FINALIZE_TIMEOUT_MS", "100")

	chatGPTJSONRequester = func(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"status":"COMPLETE"}`)),
		}, nil
	}
	fetchConversationOutputsForResult = func(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
		return "", "msg_1", []ResponsesOutputMessage{
			{
				ID:     "ig_1",
				Type:   "image_generation_call",
				Status: "completed",
				Result: "aW1hZ2U=",
			},
		}, nil
	}

	result := &conversationResult{ConversationID: "conv_1", MessageID: "msg_1"}
	if err := finalizeIncompleteConversation("token", result, true); err != nil {
		t.Fatalf("finalizeIncompleteConversation() error = %v", err)
	}
	if !responsesOutputHasImageGeneration(result.OutputMessages) {
		t.Fatalf("output messages = %#v, want image generation output", result.OutputMessages)
	}
}
