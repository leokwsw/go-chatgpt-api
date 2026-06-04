package imitate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/leokwsw/go-chatgpt-api/api/chatgpt"
)

type generatedImageAsset struct {
	AssetPointer  string
	Result        string
	RevisedPrompt string
	Width         int64
	Height        int64
	SizeBytes     int64
}

type generatedImageAssetFetcher func(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error)

var (
	fetchImageGenerationTasksForResult                                = fetchImageGenerationTasks
	fetchGeneratedImageAssetForResult      generatedImageAssetFetcher = fetchGeneratedImageAsset
	getBackendFileDownloadURLsForResult                               = getGeneratedImageDownloadURLs
	downloadGeneratedImageContentForResult                            = downloadGeneratedImageContent
)

const generatedImageDownloadAccept = "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"

type upstreamHTTPError struct {
	Operation  string
	StatusCode int
	Status     string
	Body       string
	RetryAfter time.Duration
}

func (err *upstreamHTTPError) Error() string {
	body := strings.TrimSpace(err.Body)
	if body == "" {
		body = strings.TrimSpace(err.Status)
	}
	if body == "" {
		body = fmt.Sprintf("status %d", err.StatusCode)
	}
	return fmt.Sprintf("%s failed: %s", err.Operation, body)
}

func newUpstreamHTTPError(operation string, resp *http.Response, body []byte) error {
	status := ""
	statusCode := 0
	retryAfter := time.Duration(0)
	if resp != nil {
		status = resp.Status
		statusCode = resp.StatusCode
		retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	}
	return &upstreamHTTPError{
		Operation:  operation,
		StatusCode: statusCode,
		Status:     status,
		Body:       string(body),
		RetryAfter: retryAfter,
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := nethttp.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
	}
	return 0
}

func isUpstreamRateLimit(err error) bool {
	var upstreamErr *upstreamHTTPError
	return errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusTooManyRequests
}

func retryAfterOrDefault(err error, fallback time.Duration) time.Duration {
	var upstreamErr *upstreamHTTPError
	if errors.As(err, &upstreamErr) && upstreamErr.RetryAfter > 0 {
		return upstreamErr.RetryAfter
	}
	return fallback
}

func apiRequestUsesImageGeneration(request APIRequest) bool {
	return valueUsesImageGeneration(request.Tools) || valueUsesImageGeneration(request.ToolChoice)
}

func waitImageGenerationOutputs(accessToken string, result *conversationResult) error {
	if result == nil {
		return fmt.Errorf("image generation result not found: missing conversation result")
	}
	if responsesOutputHasImageGeneration(result.OutputMessages) {
		return nil
	}
	if result.ConversationID == "" && result.MessageID == "" && result.TurnExchangeID == "" && len(result.GeneratedImageCandidates) == 0 {
		return fmt.Errorf("image generation result not found: missing conversation identifiers")
	}

	interval := time.Duration(envInt("IMITATE_IMAGE_GENERATION_POLL_INTERVAL_MS", 5000)) * time.Millisecond
	conversationInterval := time.Duration(envInt("IMITATE_IMAGE_GENERATION_CONVERSATION_POLL_INTERVAL_MS", 15000)) * time.Millisecond
	rateLimitBackoff := time.Duration(envInt("IMITATE_IMAGE_GENERATION_RATE_LIMIT_BACKOFF_MS", 30000)) * time.Millisecond
	timeout := time.Duration(envInt("IMITATE_IMAGE_GENERATION_TIMEOUT_MS", 180000)) * time.Millisecond
	deadline := time.Now().Add(timeout)
	var lastErr error
	var lastNonRateLimitErr error
	var lastRateLimitErr error
	rememberErr := func(err error) {
		if err == nil {
			return
		}
		lastErr = err
		if isUpstreamRateLimit(err) {
			lastRateLimitErr = err
			return
		}
		lastNonRateLimitErr = err
	}

	asyncStatusSent := false
	resolvedPointers := make(map[string]bool)
	now := time.Now()
	nextCandidateAt := now
	nextTasksAt := now
	nextConversationAt := now.Add(conversationInterval)

	for {
		if responsesOutputHasImageGeneration(result.OutputMessages) {
			return nil
		}

		now = time.Now()
		if len(result.GeneratedImageCandidates) != 0 && !now.Before(nextCandidateAt) {
			outputs, err := resolveGeneratedImageCandidates(accessToken, result.ConversationID, result.GeneratedImageCandidates, resolvedPointers)
			if err != nil {
				rememberErr(err)
			}
			if responsesOutputHasImageGeneration(outputs) {
				result.OutputMessages = mergeResponsesOutputMessages(outputs, result.OutputMessages)
				return nil
			}
			nextCandidateAt = nextImageGenerationPollTime(now, interval, rateLimitBackoff, err)
		}

		if !asyncStatusSent && result.ConversationID != "" {
			asyncStatusSent = true
			if err := notifyConversationAsyncStatus(accessToken, result.ConversationID); err != nil {
				rememberErr(err)
			}
		}

		now = time.Now()
		if !now.Before(nextTasksAt) {
			outputs, err := fetchImageGenerationOutputsFromTasks(accessToken, result)
			if err != nil {
				rememberErr(err)
			}
			if responsesOutputHasImageGeneration(outputs) {
				result.OutputMessages = mergeResponsesOutputMessages(outputs, result.OutputMessages)
				return nil
			}
			nextTasksAt = nextImageGenerationPollTime(now, interval, rateLimitBackoff, err)
		}

		now = time.Now()
		if result.ConversationID != "" && !now.Before(nextConversationAt) {
			outputs, err := fetchImageGenerationOutputsFromConversation(accessToken, result)
			if err != nil {
				rememberErr(err)
			}
			if responsesOutputHasImageGeneration(outputs) {
				result.OutputMessages = mergeResponsesOutputMessages(outputs, result.OutputMessages)
				return nil
			}
			nextConversationAt = nextImageGenerationPollTime(now, conversationInterval, rateLimitBackoff, err)
		}

		if time.Now().After(deadline) {
			if lastNonRateLimitErr != nil {
				return fmt.Errorf("image generation result not found: %w", lastNonRateLimitErr)
			}
			if lastRateLimitErr != nil {
				return fmt.Errorf("image generation result not found: upstream rate limited while waiting: %w", lastRateLimitErr)
			}
			if lastErr != nil {
				return fmt.Errorf("image generation result not found: %w", lastErr)
			}
			return fmt.Errorf("image generation result not found: no image asset in conversation/tasks")
		}
		time.Sleep(nextImageGenerationPollDelay(deadline, result, nextCandidateAt, nextTasksAt, nextConversationAt))
	}
}

func nextImageGenerationPollTime(now time.Time, interval time.Duration, rateLimitBackoff time.Duration, err error) time.Time {
	delay := interval
	if isUpstreamRateLimit(err) {
		delay = retryAfterOrDefault(err, rateLimitBackoff)
	}
	if delay <= 0 {
		delay = time.Millisecond
	}
	return now.Add(delay)
}

func nextImageGenerationPollDelay(deadline time.Time, result *conversationResult, nextCandidateAt time.Time, nextTasksAt time.Time, nextConversationAt time.Time) time.Duration {
	nextWake := deadline
	if result != nil && len(result.GeneratedImageCandidates) != 0 && nextCandidateAt.Before(nextWake) {
		nextWake = nextCandidateAt
	}
	if nextTasksAt.Before(nextWake) {
		nextWake = nextTasksAt
	}
	if result != nil && result.ConversationID != "" && nextConversationAt.Before(nextWake) {
		nextWake = nextConversationAt
	}
	delay := time.Until(nextWake)
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

func fetchImageGenerationOutputsFromConversation(accessToken string, result *conversationResult) ([]ResponsesOutputMessage, error) {
	if result == nil || result.ConversationID == "" {
		return nil, nil
	}
	text, messageID, outputMessages, err := fetchConversationOutputsForResult(accessToken, result.ConversationID, result.MessageID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) != "" {
		result.Text = text
	}
	if messageID != "" {
		result.MessageID = messageID
	}
	if len(outputMessages) != 0 {
		result.OutputMessages = mergeResponsesOutputMessages(outputMessages, result.OutputMessages)
	}
	return outputMessages, nil
}

func resolveGeneratedImageCandidates(accessToken string, conversationID string, candidates []generatedImagePointerCandidate, resolvedPointers map[string]bool) ([]ResponsesOutputMessage, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if resolvedPointers == nil {
		resolvedPointers = make(map[string]bool)
	}
	outputs := make([]ResponsesOutputMessage, 0, len(candidates))
	attemptedPointers := make(map[string]bool)
	var lastErr error
	for _, candidate := range candidates {
		pointer := strings.TrimSpace(candidate.Pointer)
		if pointer == "" {
			continue
		}
		key := generatedImageCandidateKey(candidate)
		if key == "" || resolvedPointers[key] || attemptedPointers[key] {
			continue
		}
		attemptedPointers[key] = true

		candidateConversationID := firstNonEmptyString(candidate.ConversationID, conversationID)
		if candidateConversationID == "" {
			lastErr = fmt.Errorf("generated image candidate missing conversation id")
			continue
		}

		asset, err := fetchGeneratedImageAssetForResult(accessToken, pointer, candidateConversationID)
		if err != nil {
			lastErr = err
			continue
		}
		asset.AssetPointer = pointer
		asset.Width = candidate.Width
		asset.Height = candidate.Height
		asset.SizeBytes = candidate.SizeBytes
		if asset.RevisedPrompt == "" {
			asset.RevisedPrompt = candidate.RevisedPrompt
		}
		resolvedPointers[key] = true
		outputs = append(outputs, newResponsesImageGenerationCall(asset))
	}
	return outputs, lastErr
}

func notifyConversationAsyncStatus(accessToken string, conversationID string) error {
	body := bytes.NewBufferString(`{"status":null}`)
	resp, err := chatGPTJSONRequester(accessToken, http.MethodPost, chatgpt.ApiPrefix+"/conversation/"+conversationID+"/async-status", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("conversation async-status failed: %s", string(responseBody))
	}
	return nil
}

func fetchImageGenerationOutputsFromTasks(accessToken string, result *conversationResult) ([]ResponsesOutputMessage, error) {
	tasks, err := fetchImageGenerationTasksForResult(accessToken)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, task := range tasks {
		if !taskMatchesConversation(task, result) {
			continue
		}
		outputs, err := extractImageGenerationTaskOutputs(accessToken, task)
		if err != nil {
			lastErr = err
			continue
		}
		if responsesOutputHasImageGeneration(outputs) {
			return outputs, nil
		}
	}
	return nil, lastErr
}

func fetchImageGenerationTasks(accessToken string) ([]map[string]interface{}, error) {
	resp, err := doChatGPTJSONRequest(accessToken, http.MethodGet, chatgpt.ApiPrefix+"/tasks", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, newUpstreamHTTPError("fetch image generation tasks", resp, body)
	}

	var payload map[string]interface{}
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	rawTasks, _ := payload["tasks"].([]interface{})
	tasks := make([]map[string]interface{}, 0, len(rawTasks))
	for _, rawTask := range rawTasks {
		task, ok := rawTask.(map[string]interface{})
		if !ok {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func taskMatchesConversation(task map[string]interface{}, result *conversationResult) bool {
	if result == nil {
		return false
	}
	for _, key := range []string{"conversation_id", "original_conversation_id"} {
		if result.ConversationID != "" && stringValue(task[key]) == result.ConversationID {
			return true
		}
	}
	for _, key := range []string{"response_message_id", "original_conversation_user_message_id"} {
		if result.MessageID != "" && stringValue(task[key]) == result.MessageID {
			return true
		}
	}
	if result.TurnExchangeID != "" && rawValueContainsString(task, result.TurnExchangeID) {
		return true
	}
	return false
}

func extractImageGenerationTaskOutputs(accessToken string, task map[string]interface{}) ([]ResponsesOutputMessage, error) {
	messages := collectTaskMessages(task)
	conversationID := firstNonEmptyString(
		stringValue(task["conversation_id"]),
		stringValue(task["original_conversation_id"]),
	)
	outputs := make([]ResponsesOutputMessage, 0)
	seenPointers := make(map[string]bool)
	seenTexts := make(map[string]bool)

	for _, message := range messages {
		imageOutputs, err := extractImageGenerationOutputsFromRawMessage(accessToken, conversationID, message, seenPointers)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, imageOutputs...)

		if text := strings.TrimSpace(extractRawMessageText(message)); text != "" && !seenTexts[text] {
			seenTexts[text] = true
			outputs = append(outputs, newResponsesOutputMessage(text))
		}
	}
	return outputs, nil
}

func collectTaskMessages(task map[string]interface{}) []map[string]interface{} {
	messages := make([]map[string]interface{}, 0, 4)
	for _, key := range []string{"image_gen_message", "final_message"} {
		if message, ok := task[key].(map[string]interface{}); ok && len(message) != 0 {
			messages = append(messages, message)
		}
	}
	if rawMessages, ok := task["messages"].([]interface{}); ok {
		for _, rawMessage := range rawMessages {
			if message, ok := rawMessage.(map[string]interface{}); ok && len(message) != 0 {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

func extractConversationImageGenerationMessages(accessToken string, conversationID string, mapping map[string]interface{}, selectedID string) ([]ResponsesOutputMessage, error) {
	targetTurnID := extractConversationTurnExchangeID(mapping, selectedID)
	candidates := make([]conversationImageOutputCandidate, 0)
	for nodeID := range mapping {
		node, _ := mapping[nodeID].(map[string]interface{})
		message, _ := node["message"].(map[string]interface{})
		if len(message) == 0 {
			continue
		}
		if targetTurnID != "" {
			metadata, _ := message["metadata"].(map[string]interface{})
			if stringValue(metadata["turn_exchange_id"]) != targetTurnID {
				continue
			}
		}
		author, _ := message["author"].(map[string]interface{})
		role := stringValue(author["role"])
		if role != "assistant" && role != "tool" {
			continue
		}
		if !rawMessageHasImageAsset(message) {
			continue
		}
		candidates = append(candidates, conversationImageOutputCandidate{
			NodeID:     nodeID,
			CreateTime: floatValue(message["create_time"]),
			Message:    message,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].CreateTime == candidates[j].CreateTime {
			return candidates[i].NodeID < candidates[j].NodeID
		}
		return candidates[i].CreateTime < candidates[j].CreateTime
	})

	outputs := make([]ResponsesOutputMessage, 0)
	seenPointers := make(map[string]bool)
	var lastErr error
	for _, candidate := range candidates {
		imageOutputs, err := extractImageGenerationOutputsFromRawMessage(accessToken, conversationID, candidate.Message, seenPointers)
		if err != nil {
			lastErr = err
			continue
		}
		outputs = append(outputs, imageOutputs...)
	}
	if len(outputs) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return outputs, nil
}

type conversationImageOutputCandidate struct {
	NodeID     string
	CreateTime float64
	Message    map[string]interface{}
}

func extractImageGenerationOutputsFromRawMessage(accessToken string, conversationID string, message map[string]interface{}, seenPointers map[string]bool) ([]ResponsesOutputMessage, error) {
	assets, err := extractGeneratedImageAssets(accessToken, conversationID, message, seenPointers)
	if err != nil {
		return nil, err
	}
	outputs := make([]ResponsesOutputMessage, 0, len(assets))
	for _, asset := range assets {
		outputs = append(outputs, newResponsesImageGenerationCall(asset))
	}
	return outputs, nil
}

func extractGeneratedImageAssets(accessToken string, conversationID string, raw interface{}, seenPointers map[string]bool) ([]generatedImageAsset, error) {
	candidates := make([]generatedImagePointerCandidate, 0)
	collectGeneratedImagePointerCandidates(raw, "", &candidates)
	assets := make([]generatedImageAsset, 0)
	for _, candidate := range candidates {
		pointer := strings.TrimSpace(candidate.Pointer)
		if pointer == "" {
			continue
		}
		fileID := assetPointerFileID(pointer)
		if fileID == "" {
			continue
		}
		if seenPointers[fileID] {
			continue
		}
		seenPointers[fileID] = true

		candidateConversationID := firstNonEmptyString(candidate.ConversationID, conversationID)
		asset, err := fetchGeneratedImageAssetForResult(accessToken, pointer, candidateConversationID)
		if err != nil {
			return nil, err
		}
		asset.AssetPointer = pointer
		asset.Width = candidate.Width
		asset.Height = candidate.Height
		asset.SizeBytes = candidate.SizeBytes
		if asset.RevisedPrompt == "" {
			asset.RevisedPrompt = candidate.RevisedPrompt
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

type generatedImagePointerCandidate struct {
	Pointer        string
	ConversationID string
	RevisedPrompt  string
	Width          int64
	Height         int64
	SizeBytes      int64
}

func fetchGeneratedImageAsset(accessToken string, assetPointer string, conversationID string) (generatedImageAsset, error) {
	fileID := assetPointerFileID(assetPointer)
	if fileID == "" {
		return generatedImageAsset{}, fmt.Errorf("generated image asset pointer is invalid")
	}

	body, err := fetchGeneratedImageContent(accessToken, fileID, conversationID)
	if err != nil {
		return generatedImageAsset{}, err
	}
	return generatedImageAsset{
		AssetPointer: assetPointer,
		Result:       base64.StdEncoding.EncodeToString(body),
	}, nil
}

func fetchGeneratedImageContent(accessToken string, fileID string, conversationID string) ([]byte, error) {
	interval := time.Duration(envInt("IMITATE_IMAGE_GENERATION_DOWNLOAD_POLL_INTERVAL_MS", 2000)) * time.Millisecond
	timeout := time.Duration(envInt("IMITATE_IMAGE_GENERATION_DOWNLOAD_TIMEOUT_MS", 45000)) * time.Millisecond
	deadline := time.Now().Add(timeout)
	var lastErr error
	metadataAttempts := 0
	contentAttempts := 0

	for {
		downloadURLs, err := getBackendFileDownloadURLsForResult(accessToken, fileID, conversationID)
		metadataAttempts++
		if err != nil {
			lastErr = fmt.Errorf("generated image download metadata unavailable: %w", err)
		}

		retryable := isRetryableGeneratedImageDownloadMetadataError(err)
		for _, downloadURL := range downloadURLs {
			contentAttempts++
			body, err := downloadGeneratedImageContentForResult(accessToken, downloadURL, conversationID)
			if err == nil {
				return body, nil
			}
			lastErr = err
			if isRetryableGeneratedImageDownloadError(err) {
				retryable = true
				continue
			}
		}

		if !retryable || time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = fmt.Errorf("generated image download returned no usable download urls")
			}
			if retryable && time.Now().After(deadline) {
				return nil, fmt.Errorf("generated image download unavailable after retry: metadata_attempts=%d content_attempts=%d: %w", metadataAttempts, contentAttempts, lastErr)
			}
			return nil, fmt.Errorf("generated image download unavailable: metadata_attempts=%d content_attempts=%d: %w", metadataAttempts, contentAttempts, lastErr)
		}
		time.Sleep(interval)
	}
}

func downloadGeneratedImageContent(accessToken string, downloadURL string, conversationID string) ([]byte, error) {
	imageReq, err := newGeneratedImageDownloadRequest(downloadURL, conversationID)
	if err != nil {
		return nil, err
	}
	body, err := doGeneratedImageDownloadRequest(imageReq)
	if err == nil {
		return body, nil
	}
	if !isGeneratedImageFileLinkNotFound(err) || strings.TrimSpace(accessToken) == "" {
		return nil, err
	}

	authReq, authReqErr := newAuthenticatedGeneratedImageDownloadRequest(accessToken, downloadURL, conversationID)
	if authReqErr != nil {
		return nil, err
	}
	if body, authErr := doGeneratedImageDownloadRequest(authReq); authErr == nil {
		return body, nil
	}
	return nil, err
}

func doGeneratedImageDownloadRequest(req *http.Request) ([]byte, error) {
	imageResp, err := api.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer imageResp.Body.Close()
	if imageResp.StatusCode < http.StatusOK || imageResp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(imageResp.Body)
		return nil, newUpstreamHTTPError("generated image content download", imageResp, body)
	}
	body, err := io.ReadAll(imageResp.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("generated image download returned empty body")
	}
	return body, nil
}

func isGeneratedImageFileLinkNotFound(err error) bool {
	var upstreamErr *upstreamHTTPError
	return errors.As(err, &upstreamErr) &&
		upstreamErr.StatusCode == http.StatusNotFound &&
		strings.Contains(strings.ToLower(upstreamErr.Body), "file link not found")
}

func isRetryableGeneratedImageDownloadError(err error) bool {
	if err == nil {
		return false
	}
	if isUpstreamRateLimit(err) {
		return true
	}
	if isGeneratedImageFileLinkNotFound(err) {
		return true
	}
	var upstreamErr *upstreamHTTPError
	if errors.As(err, &upstreamErr) {
		if upstreamErr.StatusCode >= http.StatusInternalServerError {
			return true
		}
		return false
	}
	return true
}

func isRetryableGeneratedImageDownloadMetadataError(err error) bool {
	if err == nil {
		return false
	}
	if isUpstreamRateLimit(err) {
		return true
	}
	var upstreamErr *upstreamHTTPError
	if errors.As(err, &upstreamErr) {
		if upstreamErr.StatusCode >= http.StatusInternalServerError {
			return true
		}
		if upstreamErr.StatusCode == http.StatusNotFound {
			return true
		}
		return false
	}
	return true
}

func newGeneratedImageDownloadRequest(downloadURL string, conversationID string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	referer := api.ChatGPTApiUrlPrefix + "/"
	if conversationID != "" {
		referer = api.ChatGPTApiUrlPrefix + "/c/" + conversationID
	}
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set("Accept", generatedImageDownloadAccept)
	req.Header.Set("Accept-Language", api.AcceptLanguage)
	req.Header.Set("Referer", referer)
	req.Header.Set("Sec-CH-UA", api.SecCHUA)
	req.Header.Set("Sec-CH-UA-Mobile", api.SecCHUAMobile)
	req.Header.Set("Sec-CH-UA-Platform", api.SecCHUAPlatform)
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Priority", "i")
	return req, nil
}

func newAuthenticatedGeneratedImageDownloadRequest(accessToken string, downloadURL string, conversationID string) (*http.Request, error) {
	req, err := newGeneratedImageDownloadRequest(downloadURL, conversationID)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", api.GetAccessToken(accessToken))
	req.Header.Set("Oai-Client-Version", oaiClientVersion)
	req.Header.Set("Oai-Client-Build-Number", oaiClientBuildNumber)
	req.Header.Set("Oai-Language", api.Language)
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	return req, nil
}

func newResponsesImageGenerationCall(asset generatedImageAsset) ResponsesOutputMessage {
	return ResponsesOutputMessage{
		ID:            newResponseObjectID("ig_"),
		Type:          "image_generation_call",
		Status:        "completed",
		Result:        asset.Result,
		RevisedPrompt: asset.RevisedPrompt,
	}
}

func responsesOutputHasImageGeneration(outputs []ResponsesOutputMessage) bool {
	for _, output := range outputs {
		if output.Type == "image_generation_call" && strings.TrimSpace(output.Result) != "" {
			return true
		}
	}
	return false
}

func mergeResponsesOutputMessages(groups ...[]ResponsesOutputMessage) []ResponsesOutputMessage {
	merged := make([]ResponsesOutputMessage, 0)
	seenImageResults := make(map[string]bool)
	seenTexts := make(map[string]bool)
	for _, group := range groups {
		for _, item := range group {
			switch item.Type {
			case "image_generation_call":
				if item.Result == "" || seenImageResults[item.Result] {
					continue
				}
				seenImageResults[item.Result] = true
			case "message":
				text := ""
				if len(item.Content) != 0 {
					text = item.Content[0].Text
				}
				if strings.TrimSpace(text) == "" || seenTexts[text] {
					continue
				}
				seenTexts[text] = true
			}
			merged = append(merged, item)
		}
	}
	return merged
}

func mergeGeneratedImageCandidates(existing []generatedImagePointerCandidate, groups ...[]generatedImagePointerCandidate) []generatedImagePointerCandidate {
	merged := make([]generatedImagePointerCandidate, 0, len(existing))
	indexByKey := make(map[string]int)
	addCandidate := func(candidate generatedImagePointerCandidate) {
		key := generatedImageCandidateKey(candidate)
		if key == "" {
			return
		}
		if index, ok := indexByKey[key]; ok {
			mergeGeneratedImageCandidateFields(&merged[index], candidate)
			return
		}
		indexByKey[key] = len(merged)
		merged = append(merged, candidate)
	}
	for _, candidate := range existing {
		addCandidate(candidate)
	}
	for _, group := range groups {
		for _, candidate := range group {
			addCandidate(candidate)
		}
	}
	return merged
}

func mergeGeneratedImageCandidateFields(existing *generatedImagePointerCandidate, incoming generatedImagePointerCandidate) {
	if existing.ConversationID == "" {
		existing.ConversationID = incoming.ConversationID
	}
	if existing.RevisedPrompt == "" {
		existing.RevisedPrompt = incoming.RevisedPrompt
	}
	if existing.Width == 0 {
		existing.Width = incoming.Width
	}
	if existing.Height == 0 {
		existing.Height = incoming.Height
	}
	if existing.SizeBytes == 0 {
		existing.SizeBytes = incoming.SizeBytes
	}
}

func bindGeneratedImageCandidateConversationID(candidates []generatedImagePointerCandidate, conversationID string) []generatedImagePointerCandidate {
	if conversationID == "" {
		return candidates
	}
	for index := range candidates {
		if candidates[index].ConversationID == "" {
			candidates[index].ConversationID = conversationID
		}
	}
	return candidates
}

func generatedImageCandidateKey(candidate generatedImagePointerCandidate) string {
	pointer := strings.TrimSpace(candidate.Pointer)
	if pointer == "" {
		return ""
	}
	if fileID := assetPointerFileID(pointer); fileID != "" {
		return fileID
	}
	return pointer
}

func rawMessageHasImageAsset(message map[string]interface{}) bool {
	candidates := make([]generatedImagePointerCandidate, 0, 1)
	collectGeneratedImagePointerCandidates(message, "", &candidates)
	return len(candidates) != 0
}

func collectGeneratedImagePointerCandidates(value interface{}, path string, candidates *[]generatedImagePointerCandidate) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if pointer := strings.TrimSpace(stringValue(typed["asset_pointer"])); pointer != "" && looksLikeGeneratedImagePointer(typed, path, pointer) {
			*candidates = append(*candidates, newGeneratedImagePointerCandidate(pointer, typed))
		}
		if fileID := strings.TrimSpace(stringValue(typed["file_id"])); fileID != "" && strings.HasPrefix(fileID, "file_") && looksLikeGeneratedImagePointer(typed, path, fileID) {
			*candidates = append(*candidates, newGeneratedImagePointerCandidate(fileID, typed))
		}
		for key, item := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			collectGeneratedImagePointerCandidates(item, childPath, candidates)
		}
	case []interface{}:
		for _, item := range typed {
			collectGeneratedImagePointerCandidates(item, path, candidates)
		}
	}
}

func newGeneratedImagePointerCandidate(pointer string, raw map[string]interface{}) generatedImagePointerCandidate {
	return generatedImagePointerCandidate{
		Pointer:       pointer,
		RevisedPrompt: extractDallEPrompt(raw),
		Width:         numericValue(raw["width"]),
		Height:        numericValue(raw["height"]),
		SizeBytes:     numericValue(raw["size_bytes"]),
	}
}

func looksLikeGeneratedImagePointer(raw map[string]interface{}, path string, pointer string) bool {
	contentType := strings.ToLower(stringValue(raw["content_type"]))
	if contentType == "image_asset_pointer" || strings.Contains(contentType, "image") {
		return true
	}
	if strings.Contains(pointer, "://") {
		return true
	}
	if !strings.HasPrefix(pointer, "file_") {
		return false
	}
	if numericValue(raw["width"]) > 0 || numericValue(raw["height"]) > 0 || numericValue(raw["size_bytes"]) > 0 {
		return true
	}
	path = strings.ToLower(path)
	return strings.Contains(path, "image") ||
		strings.Contains(path, "generation") ||
		strings.Contains(path, "asset") ||
		strings.Contains(path, "dalle")
}

func extractRawMessageText(message map[string]interface{}) string {
	content, _ := message["content"].(map[string]interface{})
	return extractConversationContentText(content)
}

func extractDallEPrompt(part map[string]interface{}) string {
	metadata, _ := part["metadata"].(map[string]interface{})
	dalle, _ := metadata["dalle"].(map[string]interface{})
	return stringValue(dalle["prompt"])
}

func assetPointerFileID(assetPointer string) string {
	assetPointer = strings.TrimSpace(assetPointer)
	if assetPointer == "" {
		return ""
	}
	if parts := strings.SplitN(assetPointer, "://", 2); len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	if parts := strings.SplitN(assetPointer, "//", 2); len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return assetPointer
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func rawValueContainsString(value interface{}, needle string) bool {
	if needle == "" {
		return false
	}
	switch typed := value.(type) {
	case string:
		return typed == needle
	case []interface{}:
		for _, item := range typed {
			if rawValueContainsString(item, needle) {
				return true
			}
		}
	case map[string]interface{}:
		for _, item := range typed {
			if rawValueContainsString(item, needle) {
				return true
			}
		}
	}
	return false
}
