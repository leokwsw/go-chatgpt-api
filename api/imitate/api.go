package imitate

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/leokwsw/go-chatgpt-api/api/chatgpt"
	"github.com/linweiyuan/go-logger/logger"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	reg        *regexp.Regexp
	gptsRegexp = regexp.MustCompile(`-gizmo-g-(\w+)`)
)

var AutoDeleteConversations = true

const (
	missingImageReply       = "无图片"
	missingImageInstruction = "如果你在当前上下文中看不到用户上传的图片，必须只回复：无图片。"
	oaiClientVersion        = "prod-4987068829830ddc3ae6683bd4e633f61b79dec9"
	oaiClientBuildNumber    = "6325146"

	conversationURLPath           = "/f/conversation"
	conversationPrepareURLPath    = "/f/conversation/prepare"
	conversationTargetPath        = "/backend-api/f/conversation"
	conversationPrepareTargetPath = "/backend-api/f/conversation/prepare"
)

type requestConversionContext struct {
	HasAttachments         bool
	HasImage               bool
	ForceNoImageReply      bool
	RequiresPrepare        bool
	AllowEmptyConduitToken bool
	AttachmentMimeTypes    []string
	PartialQuery           string
}

type requestRuntime struct {
	ArkoseToken           string
	ChatRequirementsToken string
	ConduitToken          string
	ProofToken            string
	SessionID             string
	TurnstileToken        string
	TurnTraceID           string
}

type conversationPrepareRequest struct {
	Action               string                           `json:"action"`
	ForkFromSharedPost   bool                             `json:"fork_from_shared_post"`
	ParentMessageID      string                           `json:"parent_message_id,omitempty"`
	ConversationID       string                           `json:"conversation_id,omitempty"`
	Model                string                           `json:"model"`
	ThinkingEffort       string                           `json:"thinking_effort,omitempty"`
	ClientPrepareState   string                           `json:"client_prepare_state,omitempty"`
	TimezoneOffsetMin    int                              `json:"timezone_offset_min"`
	Timezone             string                           `json:"timezone"`
	ConversationMode     chatgpt.ConvMode                 `json:"conversation_mode"`
	SystemHints          []string                         `json:"system_hints"`
	AttachmentMimeTypes  []string                         `json:"attachment_mime_types,omitempty"`
	PartialQuery         *conversationPreparePartialQuery `json:"partial_query,omitempty"`
	SupportBuffering     bool                             `json:"supports_buffering"`
	SupportedEncodings   []string                         `json:"supported_encodings"`
	ClientContextualInfo chatgpt.ClientContextualInfo     `json:"client_contextual_info"`
}

type conversationPreparePartialQuery struct {
	ID      string          `json:"id"`
	Author  chatgpt.Author  `json:"author"`
	Content chatgpt.Content `json:"content"`
}

type conversationPrepareResponse struct {
	Status       string `json:"status"`
	ConduitToken string `json:"conduit_token"`
}

type conversationResult struct {
	ID                       string
	Model                    string
	Text                     string
	FixedText                bool
	ConversationID           string
	MessageID                string
	TurnExchangeID           string
	OutputMessages           []ResponsesOutputMessage
	GeneratedImageCandidates []generatedImagePointerCandidate
}

type conversationSummary struct {
	ConversationID string
	MessageID      string
	TurnExchangeID string
}

type chatCompletionStreamState struct {
	RoleSent bool
}

type imitateAPIError struct {
	Status  int
	Message string
	Type    string
	Param   interface{}
	Code    string
}

var (
	chatGPTJSONRequester              = doChatGPTJSONRequest
	fetchConversationOutputsForResult = fetchConversationOutputs
)

func init() {
	reg, _ = regexp.Compile("[^a-zA-Z0-9]+")
}

func CreateChatCompletions(c *gin.Context) {
	var originalRequest APIRequest
	err := c.BindJSON(&originalRequest)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Request must be proper JSON",
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    err.Error(),
		}})
		return
	}

	accessToken, authErr := resolveImitateAccessToken(c)
	if authErr != nil {
		writeImitateAPIError(c, authErr)
		return
	}

	releaseAccount, err := api.AcquireAccountRequest(c.Request.Context(), accessToken)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusRequestTimeout, api.ReturnMessage(err.Error()))
		return
	}
	defer releaseAccount()

	result, handled := runConversationRequest(c, originalRequest, accessToken, originalRequest.Stream)
	if handled {
		return
	}
	defer maybeDeleteConversation(accessToken, result)
	finalizeChatCompletionResult(accessToken, originalRequest, result)

	if result.FixedText {
		writeFixedChatCompletion(c, result.Model, result.ID, result.Text, originalRequest.Stream)
		return
	}

	if !originalRequest.Stream {
		c.JSON(200, newChatCompletion(result.Text, result.Model, result.ID))
	} else {
		c.String(200, "data: [DONE]\n\n")
	}
}

func writeInvalidRequestError(c *gin.Context, message string, param string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
		"message": message,
		"type":    "invalid_request_error",
		"param":   param,
		"code":    "invalid_request",
	}})
}

func runConversationRequest(c *gin.Context, originalRequest APIRequest, accessToken string, stream bool) (*conversationResult, bool) {
	uid := uuid.NewString()
	translatedRequest, requestCtx, err := convertAPIRequest(originalRequest, accessToken)
	if err != nil {
		writeInvalidRequestError(c, err.Error(), "model")
		return nil, true
	}
	if requestCtx.ForceNoImageReply {
		return &conversationResult{
			ID:        uid,
			Model:     translatedRequest.Model,
			Text:      missingImageReply,
			FixedText: true,
		}, false
	}

	runtime, err := buildRequestRuntime(accessToken, uid, translatedRequest.Model, requestCtx, "")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return nil, true
	}

	if requestCtx.HasAttachments || requestCtx.RequiresPrepare {
		runtime.ConduitToken, err = prepareConversation(accessToken, uid, translatedRequest, requestCtx, runtime.SessionID, runtime.TurnTraceID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage(fmt.Sprintf("conversation prepare failed: %v", err)))
			return nil, true
		}
	}

	response, done := sendConversationRequest(c, translatedRequest, accessToken, uid, runtime)
	if done {
		return nil, true
	}
	defer func() {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
	}()

	if HandleRequestError(c, response) {
		return nil, true
	}

	var fullResponse string
	var lastConversationID string
	var lastMessageID string
	var lastTurnExchangeID string
	generatedImageCandidates := make([]generatedImagePointerCandidate, 0)
	streamState := &chatCompletionStreamState{}
	finishReason := "stop"
	lastStreamResult := &conversationStreamResult{}
	maxContinues := imitateMaxContinues()
	allowImageGeneration := apiRequestUsesImageGeneration(originalRequest)
	for i := 0; i <= maxContinues; i++ {
		summary := &conversationSummary{}
		streamResult, handled := Handler(c, response, accessToken, stream, summary, streamState)
		if handled {
			return nil, true
		}
		lastStreamResult = streamResult
		fullResponse += streamResult.Text
		generatedImageCandidates = mergeGeneratedImageCandidates(generatedImageCandidates, streamResult.GeneratedImageCandidates)
		if streamResult.FinishReason != "" {
			finishReason = streamResult.FinishReason
		}
		if summary.ConversationID != "" {
			lastConversationID = summary.ConversationID
		}
		if summary.MessageID != "" {
			lastMessageID = summary.MessageID
		}
		if summary.TurnExchangeID != "" {
			lastTurnExchangeID = summary.TurnExchangeID
		}
		if streamResult.ContinueInfo == nil {
			break
		}
		if i == maxContinues {
			finishReason = "length"
			break
		}

		translatedRequest.Messages = nil
		translatedRequest.Action = "continue"
		translatedRequest.ConversationID = streamResult.ContinueInfo.ConversationID
		translatedRequest.ParentMessageID = streamResult.ContinueInfo.ParentID
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		runtime, err = buildRequestRuntime(accessToken, uid, translatedRequest.Model, nil, runtime.SessionID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
			return nil, true
		}
		response, done = sendConversationRequest(c, translatedRequest, accessToken, uid, runtime)
		if done {
			return nil, true
		}
		if HandleRequestError(c, response) {
			return nil, true
		}
	}

	result := &conversationResult{
		ID:                       uid,
		Model:                    translatedRequest.Model,
		Text:                     fullResponse,
		ConversationID:           lastConversationID,
		MessageID:                lastMessageID,
		TurnExchangeID:           lastTurnExchangeID,
		GeneratedImageCandidates: generatedImageCandidates,
	}

	if lastStreamResult != nil && lastStreamResult.Incomplete {
		if result.ConversationID == "" {
			if stream {
				writeStreamError(c, "upstream stream incomplete", "stream_incomplete")
			} else {
				c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage("upstream stream incomplete"))
			}
			return nil, true
		}
		if err = finalizeIncompleteConversation(accessToken, result, allowImageGeneration); err != nil {
			if stream {
				writeStreamError(c, err.Error(), "stream_incomplete")
			} else {
				c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage(err.Error()))
			}
			return nil, true
		}
		if stream {
			delta := remainingTextDelta(fullResponse, result.Text)
			if delta != "" {
				if err = writeStreamDelta(c, delta, streamState); err != nil {
					return nil, true
				}
			}
		}
		fullResponse = result.Text
	}

	if c.Writer.Status() != 0 && c.Writer.Status() != http.StatusOK {
		return nil, true
	}

	if stream {
		writeStreamStop(c, translatedRequest.Model, uid, finishReason)
	}

	result.Text = fullResponse
	return result, false
}

func buildRequestRuntime(accessToken string, uid string, model string, requestCtx *requestConversionContext, sessionID string) (*requestRuntime, error) {
	preferFinalize := requestCtx != nil && (requestCtx.HasAttachments || requestCtx.RequiresPrepare)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	chatRequirements, proofProbe, err := getChatRequirementsForConversation(accessToken, uid, sessionID, preferFinalize)
	if err != nil {
		return nil, err
	}
	if chatRequirements == nil {
		return nil, fmt.Errorf("unable to check chat requirement")
	}

	runtime := &requestRuntime{
		ChatRequirementsToken: chatRequirements.Token,
		SessionID:             sessionID,
		TurnTraceID:           uuid.NewString(),
	}

	if chatRequirements.Proof.Required {
		runtime.ProofToken = chatgpt.CalcProofToken(chatRequirements)
	}
	if chatRequirements.Arkose.Required {
		runtime.ArkoseToken, err = chatgpt.GetArkoseTokenForModel(model, chatRequirements.Arkose.Dx)
		if err != nil {
			return nil, err
		}
		if runtime.ArkoseToken == "" {
			return nil, fmt.Errorf("missing arkose token")
		}
	}
	if chatRequirements.Turnstile.Required {
		runtime.TurnstileToken = chatgpt.ProcessTurnstile(chatRequirements.Turnstile.DX, proofProbe)
	}
	return runtime, nil
}

func getChatRequirementsForConversation(accessToken string, uid string, sessionID string, preferFinalize bool) (*chatgpt.ChatRequirements, string, error) {
	chatRequirements, proofProbe, err := fetchChatRequirements(accessToken, uid, sessionID, preferFinalize)
	if err != nil {
		return nil, "", err
	}
	if chatRequirements == nil {
		return nil, "", fmt.Errorf("unable to check chat requirement")
	}

	for i := 0; i < chatgpt.PowRetryTimes; i++ {
		if chatRequirements.Proof.Required && chatRequirements.Proof.Difficulty <= chatgpt.PowMaxDifficulty {
			logger.Warn(fmt.Sprintf("Proof of work difficulty too high: %s. Retrying... %d/%d ", chatRequirements.Proof.Difficulty, i+1, chatgpt.PowRetryTimes))
			chatRequirements, proofProbe, err = fetchChatRequirements(accessToken, api.OAIDID, sessionID, preferFinalize)
			if err != nil {
				return nil, "", err
			}
			if chatRequirements == nil {
				return nil, "", fmt.Errorf("unable to check chat requirement")
			}
			continue
		}
		break
	}
	return chatRequirements, proofProbe, nil
}

func fetchChatRequirements(accessToken string, uid string, sessionID string, preferFinalize bool) (*chatgpt.ChatRequirements, string, error) {
	legacyReq, legacyProbe, legacyErr := chatgpt.GetChatRequirementsByAccessTokenWithSession(accessToken, uid, sessionID)
	if !preferFinalize {
		if legacyErr != nil {
			logger.Warn(fmt.Sprintf("chat requirements legacy failed: %s err=%v", chatRequirementLogFields(accessToken, uid, sessionID), legacyErr))
		}
		return legacyReq, legacyProbe, legacyErr
	}
	if legacyErr == nil && legacyReq != nil && legacyReq.Arkose.Required {
		return legacyReq, legacyProbe, nil
	}
	if legacyErr != nil {
		logger.Warn(fmt.Sprintf("chat requirements legacy failed: %s err=%v", chatRequirementLogFields(accessToken, uid, sessionID), legacyErr))
	}

	finalReq, finalProbe, finalErr := chatgpt.FinalizeChatRequirementsByAccessTokenWithSession(accessToken, uid, sessionID)
	if finalErr == nil && finalReq != nil {
		if legacyReq != nil {
			finalReq.Arkose = legacyReq.Arkose
		}
		return finalReq, finalProbe, nil
	}
	if legacyErr == nil && legacyReq != nil {
		logger.Warn(fmt.Sprintf("chat requirements finalize failed, falling back to legacy endpoint: %v", finalErr))
		return legacyReq, legacyProbe, nil
	}
	if finalErr == nil && finalReq != nil {
		return finalReq, finalProbe, nil
	}
	if legacyErr != nil && finalErr != nil {
		logger.Error(fmt.Sprintf("chat requirements failed: %s finalize=%v legacy=%v", chatRequirementLogFields(accessToken, uid, sessionID), finalErr, legacyErr))
		return nil, "", fmt.Errorf("chat requirements failed: finalize=%v legacy=%v", finalErr, legacyErr)
	}
	return legacyReq, legacyProbe, legacyErr
}

func chatRequirementLogFields(accessToken string, uid string, sessionID string) string {
	authInfo := api.DescribeAuthorizationForLog(accessToken, "chatgpt_access_token", "")
	return fmt.Sprintf("uid=%s session_id=%s %s", uid, sessionID, authInfo.LogFields())
}

func prepareConversation(accessToken string, uid string, request chatgpt.CreateConversationRequest, requestCtx *requestConversionContext, sessionID string, turnTraceID string) (string, error) {
	clientPrepareState := request.ClientPrepareState
	if requestCtx.PartialQuery == "" {
		clientPrepareState = "none"
	}

	prepareReq := conversationPrepareRequest{
		Action:               request.Action,
		ForkFromSharedPost:   false,
		ParentMessageID:      request.ParentMessageID,
		ConversationID:       request.ConversationID,
		Model:                request.Model,
		ThinkingEffort:       request.ThinkingEffort,
		ClientPrepareState:   clientPrepareState,
		TimezoneOffsetMin:    request.TimezoneOffsetMin,
		Timezone:             request.Timezone,
		ConversationMode:     request.ConversationMode,
		SystemHints:          request.SystemHints,
		AttachmentMimeTypes:  requestCtx.AttachmentMimeTypes,
		SupportBuffering:     request.SupportBuffering,
		SupportedEncodings:   request.SupportedEncodings,
		ClientContextualInfo: request.ClientContextualInfo,
	}
	if requestCtx.PartialQuery != "" {
		prepareReq.PartialQuery = &conversationPreparePartialQuery{
			ID:     uuid.NewString(),
			Author: chatgpt.Author{Role: "user"},
			Content: chatgpt.Content{
				ContentType: "text",
				Parts:       []interface{}{requestCtx.PartialQuery},
			},
		}
	}

	payload, _ := json.Marshal(prepareReq)
	req, err := newConversationHTTPReq(accessToken, uid, http.MethodPost, conversationPrepareURLPath, conversationPrepareTargetPath, "*/*", bytes.NewBuffer(payload), sessionID, turnTraceID)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Conduit-Token", "no-token")

	resp, err := api.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("conversation prepare failed: %s", string(body))
	}

	var prepareResp conversationPrepareResponse
	if err = json.NewDecoder(resp.Body).Decode(&prepareResp); err != nil {
		return "", err
	}
	if prepareResp.ConduitToken == "" && (!requestCtx.AllowEmptyConduitToken || requestCtx.HasAttachments) {
		return "", fmt.Errorf("conversation prepare returned empty conduit token")
	}
	return prepareResp.ConduitToken, nil
}

func newConversationHTTPReq(accessToken string, uid string, method string, urlPath string, targetPath string, accept string, body io.Reader, sessionID string, turnTraceID string) (*http.Request, error) {
	urlPrefix := chatgpt.ApiPrefix
	if accessToken == "" {
		urlPrefix = chatgpt.AnonPrefix
	}

	req, err := http.NewRequest(method, urlPrefix+urlPath, body)
	if err != nil {
		return nil, err
	}
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{
		Accept:      accept,
		ContentType: "application/json",
		Referer:     api.ChatGPTApiUrlPrefix + "/",
	})
	req.Header.Set("Oai-Client-Version", oaiClientVersion)
	req.Header.Set("Oai-Client-Build-Number", oaiClientBuildNumber)
	if sessionID != "" {
		req.Header.Set("Oai-Session-Id", sessionID)
	}
	if turnTraceID != "" {
		req.Header.Set("X-Oai-Turn-Trace-Id", turnTraceID)
	}
	req.Header.Set("X-Openai-Target-Path", targetPath)
	req.Header.Set("X-Openai-Target-Route", targetPath)

	deviceID := api.OAIDID
	if deviceID == "" {
		deviceID = uid
	}
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if deviceID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+deviceID+";")
		req.Header.Set("Oai-Device-Id", deviceID)
	}
	if accessToken != "" {
		req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(accessToken))
	}
	return req, nil
}

func writeFixedChatCompletion(c *gin.Context, model string, id string, text string, stream bool) {
	if !stream {
		c.JSON(http.StatusOK, newChatCompletion(text, model, id))
		return
	}

	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/event-stream")

	chunk := NewChatCompletionChunk(text)
	chunk.ID = id
	chunk.Model = model
	chunk.Choices[0].Delta.Role = "assistant"
	_, _ = c.Writer.WriteString("data: " + chunk.String() + "\n\n")

	stop := StopChunk("stop")
	stop.ID = id
	stop.Model = model
	_, _ = c.Writer.WriteString("data: " + stop.String() + "\n\n")
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}

func generateId() string {
	id := uuid.NewString()
	id = strings.ReplaceAll(id, "-", "")
	id = base64.StdEncoding.EncodeToString([]byte(id))
	id = reg.ReplaceAllString(id, "")
	return "chatcmpl-" + id
}

func resolveImitateAccessToken(c *gin.Context) (string, *imitateAPIError) {
	authHeader, authSource := imitateAuthorizationHeader(c)
	if authHeader == "" {
		return "", newImitateAuthError("Missing Authorization header", "missing_authorization")
	}

	customAccessToken := bearerTokenValue(authHeader)
	if customAccessToken == "" {
		return "", newImitateAuthError("Authorization header must contain a bearer token", "invalid_authorization")
	}

	customFreeToken := os.Getenv("CUSTOM_FREE_TOKEN")
	if customFreeToken == "" {
		customFreeToken = "python"
	}
	if customAccessToken == customFreeToken {
		if strings.HasPrefix(c.Request.URL.Path, "/imitate/v1/files") {
			return "", newImitateAuthError("CUSTOM_FREE_TOKEN is not accepted for /imitate file APIs; use IMITATE_API_KEY or a ChatGPT access token", "custom_free_token_not_allowed")
		}
		token, apiErr := configuredImitateAccessToken()
		if apiErr == nil {
			logImitateAuthSuccess(c, authHeader, authSource, customFreeToken, "custom_free_chat_compat")
		}
		return token, apiErr
	}
	if strings.HasPrefix(customAccessToken, "sk-") {
		return "", newImitateAuthError("OpenAI Platform API keys are not accepted by /imitate; use IMITATE_API_KEY or a ChatGPT access token", "platform_api_key_not_allowed")
	}
	if strings.HasPrefix(customAccessToken, "eyJhbGciOiJSUzI1NiI") {
		logImitateAuthSuccess(c, authHeader, authSource, customFreeToken, "direct_chatgpt_jwt")
		return customAccessToken, nil
	}

	imitateToken := os.Getenv("IMITATE_API_KEY")
	if imitateToken != "" && customAccessToken == imitateToken {
		token, apiErr := configuredImitateAccessToken()
		if apiErr == nil {
			logImitateAuthSuccess(c, authHeader, authSource, customFreeToken, "imitate_api_key")
		}
		return token, apiErr
	}

	return "", newImitateAuthError("API key is missing or invalid", "invalid_api_key")
}

func imitateAuthorizationHeader(c *gin.Context) (string, string) {
	authHeader := strings.TrimSpace(c.GetHeader(api.AuthorizationHeader))
	if authHeader != "" {
		return authHeader, api.AuthorizationHeader
	}
	authHeader = strings.TrimSpace(c.GetHeader(api.XAuthorizationHeader))
	if authHeader != "" {
		return authHeader, api.XAuthorizationHeader
	}
	return "", "missing"
}

func configuredImitateAccessToken() (string, *imitateAPIError) {
	token := os.Getenv("IMITATE_ACCESS_TOKEN")
	if token == "" {
		token = api.IMITATE_accessToken
	}
	if token == "" {
		return "", &imitateAPIError{
			Status:  http.StatusInternalServerError,
			Message: "IMITATE_ACCESS_TOKEN is not configured",
			Type:    "server_error",
			Param:   nil,
			Code:    "imitate_access_token_missing",
		}
	}
	return token, nil
}

func bearerTokenValue(authHeader string) string {
	fields := strings.Fields(authHeader)
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return strings.TrimSpace(fields[1])
	}
	if len(fields) == 1 {
		return strings.TrimSpace(fields[0])
	}
	return ""
}

func newImitateAuthError(message string, code string) *imitateAPIError {
	return &imitateAPIError{
		Status:  http.StatusUnauthorized,
		Message: message,
		Type:    "invalid_request_error",
		Param:   nil,
		Code:    code,
	}
}

func writeImitateAPIError(c *gin.Context, apiErr *imitateAPIError) {
	if apiErr == nil {
		return
	}
	status := apiErr.Status
	if status == 0 {
		status = http.StatusBadRequest
	}
	logImitateAuthReject(c, status, apiErr)
	c.JSON(status, gin.H{"error": gin.H{
		"message": apiErr.Message,
		"type":    apiErr.Type,
		"param":   apiErr.Param,
		"code":    apiErr.Code,
	}})
}

func logImitateAuthReject(c *gin.Context, status int, apiErr *imitateAPIError) {
	authHeader, authSource := imitateAuthorizationHeader(c)
	customFreeToken := os.Getenv("CUSTOM_FREE_TOKEN")
	if customFreeToken == "" {
		customFreeToken = "python"
	}
	logger.Warn(fmt.Sprintf(
		"[imitate-auth] reject stage=imitate_auth method=%s path=%s client_ip=%s status=%d code=%s message=%q %s %s",
		c.Request.Method,
		c.Request.URL.Path,
		c.ClientIP(),
		status,
		apiErr.Code,
		apiErr.Message,
		api.DescribeAuthorizationForLog(authHeader, authSource, customFreeToken).LogFields(),
		api.ImitateAuthConfigLogFields(),
	))
}

func logImitateAuthSuccess(c *gin.Context, authHeader string, authSource string, customFreeToken string, mode string) {
	if !imitateAuthDebugEnabled() {
		return
	}
	logger.Info(fmt.Sprintf(
		"[imitate-auth] success stage=imitate_auth method=%s path=%s client_ip=%s mode=%s %s %s",
		c.Request.Method,
		c.Request.URL.Path,
		c.ClientIP(),
		mode,
		api.DescribeAuthorizationForLog(authHeader, authSource, customFreeToken).LogFields(),
		api.ImitateAuthConfigLogFields(),
	))
}

func imitateAuthDebugEnabled() bool {
	value := os.Getenv("IMITATE_AUTH_DEBUG")
	return value == "1" || strings.EqualFold(value, "true")
}

func convertAPIRequest(apiRequest APIRequest, token string) (chatgpt.CreateConversationRequest, *requestConversionContext, error) {
	chatgptRequest := NewChatGPTRequest()
	requestCtx := &requestConversionContext{}

	modelResolution, err := resolveImitateModel(apiRequest.Model, apiRequest.ReasoningEffort, apiRequest.ThinkingEffort)
	if err != nil {
		return chatgptRequest, requestCtx, err
	}
	if modelResolution.Model != "" {
		chatgptRequest.Model = modelResolution.Model
	}
	chatgptRequest.ThinkingEffort = modelResolution.ThinkingEffort
	requestCtx.RequiresPrepare = modelResolution.RequiresPrepare
	requestCtx.AllowEmptyConduitToken = modelResolution.AllowEmptyConduitToken

	matches := gptsRegexp.FindStringSubmatch(apiRequest.Model)
	if len(matches) == 2 {
		chatgptRequest.ConversationMode.Kind = "gizmo_interaction"
		chatgptRequest.ConversationMode.GizmoId = "g-" + matches[1]
	}

	for _, apiMessage := range apiRequest.Messages {
		if apiMessage.Role == "system" || apiMessage.Role == "developer" {
			apiMessage.Role = "critic"
		}
		if apiMessage.Role == "user" {
			if partialQuery := extractPlainTextContent(apiMessage.Content); partialQuery != "" {
				requestCtx.PartialQuery = partialQuery
			}
		}
		content, metadata := convertAPIMessage(apiMessage, token, requestCtx)
		chatgptRequest.Messages = append(chatgptRequest.Messages, chatgpt.Message{
			ID:       uuid.NewString(),
			Author:   chatgpt.Author{Role: apiMessage.Role},
			Content:  content,
			Metadata: metadata,
		})
	}

	if chatgptRequest.ConversationMode.Kind == "" {
		chatgptRequest.ConversationMode.Kind = "primary_assistant"
	}
	if requestCtx.HasAttachments && requestCtx.PartialQuery == "" {
		chatgptRequest.ClientPrepareState = "none"
	}

	return chatgptRequest, requestCtx, nil
}

func convertAPIMessage(apiMessage ApiMessage, accessToken string, requestCtx *requestConversionContext) (chatgpt.Content, interface{}) {
	metadata := ensureMetadataMap(apiMessage.Metadata)
	content := chatgpt.Content{ContentType: "text", Parts: []interface{}{""}}
	attachments := getAttachmentList(metadata)

	switch raw := apiMessage.Content.(type) {
	case string:
		content.Parts = []interface{}{raw}
		return content, metadata
	case []interface{}:
		imageParts := make([]interface{}, 0)
		textParts := make([]string, 0)
		for _, item := range raw {
			partMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			itemType, _ := partMap["type"].(string)
			switch itemType {
			case "text", "input_text":
				if text, ok := partMap["text"].(string); ok {
					textParts = append(textParts, text)
				}
			case "input_image", "image_url":
				requestCtx.HasAttachments = true
				requestCtx.HasImage = true
				fileID, err := resolveFileIDFromPart(accessToken, partMap, "vision")
				if err != nil || fileID == "" {
					markMissingImageReply(requestCtx, "resolve_file_id_failed", fileID, err)
					continue
				}
				att, err := BuildAttachmentMetadataByFileID(accessToken, fileID)
				if err != nil {
					markMissingImageReply(requestCtx, "attachment_metadata_failed", fileID, err)
					continue
				}
				attachments = append(attachments, att)
				if mimeType, ok := att["mime_type"].(string); ok && mimeType != "" {
					requestCtx.AttachmentMimeTypes = appendUniqueString(requestCtx.AttachmentMimeTypes, mimeType)
				}
				imageParts = append(imageParts, buildImageAssetPointerPart(fileID, att))
			case "input_file", "file":
				requestCtx.HasAttachments = true
				fileID, err := resolveFileIDFromPart(accessToken, partMap, "user_data")
				if err != nil {
					continue
				}
				if fileID == "" {
					continue
				}
				if att, err := BuildAttachmentMetadataByFileID(accessToken, fileID); err == nil {
					attachments = append(attachments, att)
					if mimeType, ok := att["mime_type"].(string); ok && mimeType != "" {
						requestCtx.AttachmentMimeTypes = appendUniqueString(requestCtx.AttachmentMimeTypes, mimeType)
					}
				}
			}
		}
		if len(imageParts) != 0 {
			textBlock := strings.TrimSpace(strings.Join(textParts, "\n"))
			if textBlock != "" {
				textBlock += "\n\n"
			}
			textBlock += missingImageInstruction
			content.ContentType = "multimodal_text"
			content.Parts = append(content.Parts[:0], imageParts...)
			content.Parts = append(content.Parts, textBlock)
		} else if len(textParts) != 0 {
			content.Parts = []interface{}{strings.Join(textParts, "\n")}
		}
	default:
		content.Parts = []interface{}{fmt.Sprintf("%v", apiMessage.Content)}
	}

	metadata["attachments"] = attachments
	return content, metadata
}

func markMissingImageReply(requestCtx *requestConversionContext, reason string, fileID string, err error) {
	requestCtx.ForceNoImageReply = true
	logger.Warn(fmt.Sprintf(
		"[imitate-image] fallback=missing_image reason=%s file_id_present=%t err=%q",
		reason,
		fileID != "",
		compactLogError(err),
	))
}

func compactLogError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}

func extractPlainTextContent(content interface{}) string {
	switch raw := content.(type) {
	case string:
		return strings.TrimSpace(raw)
	case []interface{}:
		textParts := make([]string, 0)
		for _, item := range raw {
			partMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			itemType, _ := partMap["type"].(string)
			if itemType != "text" && itemType != "input_text" {
				continue
			}
			text, _ := partMap["text"].(string)
			if text != "" {
				textParts = append(textParts, text)
			}
		}
		return strings.TrimSpace(strings.Join(textParts, "\n"))
	default:
		return ""
	}
}

func buildImageAssetPointerPart(fileID string, attachment map[string]interface{}) map[string]interface{} {
	part := map[string]interface{}{
		"content_type":  "image_asset_pointer",
		"asset_pointer": "sediment://" + fileID,
	}
	if size := numericValue(attachment["size"]); size > 0 {
		part["size_bytes"] = size
	}
	if width := numericValue(attachment["width"]); width > 0 {
		part["width"] = width
	}
	if height := numericValue(attachment["height"]); height > 0 {
		part["height"] = height
	}
	return part
}

func numericValue(v interface{}) int64 {
	switch value := v.(type) {
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func appendUniqueString(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func ensureMetadataMap(metadata interface{}) map[string]interface{} {
	if metadata == nil {
		return map[string]interface{}{}
	}
	if m, ok := metadata.(map[string]interface{}); ok {
		return m
	}

	payload, err := json.Marshal(metadata)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err = json.Unmarshal(payload, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

func getAttachmentList(metadata map[string]interface{}) []interface{} {
	v, ok := metadata["attachments"]
	if !ok || v == nil {
		return make([]interface{}, 0)
	}
	list, ok := v.([]interface{})
	if !ok {
		return make([]interface{}, 0)
	}
	return list
}

func extractFileID(partMap map[string]interface{}) string {
	if fileID, ok := partMap["file_id"].(string); ok {
		return fileID
	}
	if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
		if fileID, ok := imageURL["file_id"].(string); ok {
			return fileID
		}
	}
	if imageFile, ok := partMap["image_file"].(map[string]interface{}); ok {
		if fileID, ok := imageFile["file_id"].(string); ok {
			return fileID
		}
	}
	if inputFile, ok := partMap["input_file"].(map[string]interface{}); ok {
		if fileID, ok := inputFile["file_id"].(string); ok {
			return fileID
		}
	}
	if fileData, ok := partMap["file"].(map[string]interface{}); ok {
		if fileID, ok := fileData["file_id"].(string); ok {
			return fileID
		}
	}
	return ""
}

func resolveFileIDFromPart(accessToken string, partMap map[string]interface{}, purpose string) (string, error) {
	if fileID := extractFileID(partMap); fileID != "" {
		return fileID, nil
	}

	filename := extractFilename(partMap)
	if rawURL := extractExternalFileURL(partMap); rawURL != "" {
		openAIFile, err := createOpenAIFileFromExternalURL(accessToken, rawURL, purpose, filename)
		if err != nil {
			return "", err
		}
		return openAIFile.ID, nil
	}

	if fileData := extractInlineFileData(partMap); fileData != "" {
		openAIFile, err := createOpenAIFileFromInlineData(accessToken, filename, purpose, fileData, "")
		if err != nil {
			return "", err
		}
		return openAIFile.ID, nil
	}

	return "", nil
}

func extractExternalFileURL(partMap map[string]interface{}) string {
	if imageURL, ok := partMap["image_url"].(string); ok {
		return strings.TrimSpace(imageURL)
	}
	if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
		if urlValue, ok := imageURL["url"].(string); ok {
			return strings.TrimSpace(urlValue)
		}
	}
	if fileURL, ok := partMap["file_url"].(string); ok {
		return strings.TrimSpace(fileURL)
	}
	if imageFile, ok := partMap["image_file"].(map[string]interface{}); ok {
		if fileURL, ok := imageFile["file_url"].(string); ok {
			return strings.TrimSpace(fileURL)
		}
	}
	if inputFile, ok := partMap["input_file"].(map[string]interface{}); ok {
		if fileURL, ok := inputFile["file_url"].(string); ok {
			return strings.TrimSpace(fileURL)
		}
	}
	if filePart, ok := partMap["file"].(map[string]interface{}); ok {
		if fileURL, ok := filePart["file_url"].(string); ok {
			return strings.TrimSpace(fileURL)
		}
	}
	return ""
}

func extractInlineFileData(partMap map[string]interface{}) string {
	if fileData, ok := partMap["file_data"].(string); ok {
		return strings.TrimSpace(fileData)
	}
	if inputFile, ok := partMap["input_file"].(map[string]interface{}); ok {
		if fileData, ok := inputFile["file_data"].(string); ok {
			return strings.TrimSpace(fileData)
		}
	}
	if filePart, ok := partMap["file"].(map[string]interface{}); ok {
		if fileData, ok := filePart["file_data"].(string); ok {
			return strings.TrimSpace(fileData)
		}
	}
	return ""
}

func extractFilename(partMap map[string]interface{}) string {
	if filename, ok := partMap["filename"].(string); ok {
		return strings.TrimSpace(filename)
	}
	if inputFile, ok := partMap["input_file"].(map[string]interface{}); ok {
		if filename, ok := inputFile["filename"].(string); ok {
			return strings.TrimSpace(filename)
		}
	}
	if filePart, ok := partMap["file"].(map[string]interface{}); ok {
		if filename, ok := filePart["filename"].(string); ok {
			return strings.TrimSpace(filename)
		}
	}
	return ""
}

func NewChatGPTRequest() chatgpt.CreateConversationRequest {
	enableHistory := os.Getenv("ENABLE_HISTORY") == ""
	return chatgpt.CreateConversationRequest{
		Action:                           "next",
		ParentMessageID:                  "client-created-root",
		Model:                            "text-davinci-002-render-sha",
		ClientPrepareState:               "success",
		ConversationMode:                 chatgpt.ConvMode{Kind: "primary_assistant"},
		EnableMessageFollowups:           true,
		SystemHints:                      []string{},
		SupportBuffering:                 true,
		SupportedEncodings:               []string{"v1"},
		ParagenCotSummaryDisplayOverride: "allow",
		ForceParallelSwitch:              "auto",
		HistoryAndTrainingDisabled:       !enableHistory,
		Timezone:                         "Asia/Shanghai",
		TimezoneOffsetMin:                -480,
		ClientContextualInfo: chatgpt.ClientContextualInfo{
			AppName: "chatgpt.com",
		},
		VariantPurpose:     "none",
		WebSocketRequestId: uuid.NewString(),
	}
}

func sendConversationRequest(c *gin.Context, request chatgpt.CreateConversationRequest, accessToken string, uid string, runtime *requestRuntime) (*http.Response, bool) {
	jsonBytes, _ := json.Marshal(request)

	urlPrefix := chatgpt.ApiPrefix
	if accessToken == "" {
		urlPrefix = chatgpt.AnonPrefix
	}
	req, _ := http.NewRequest(http.MethodPost, urlPrefix+conversationURLPath, bytes.NewBuffer(jsonBytes))
	referer := api.ChatGPTApiUrlPrefix + "/"
	if request.ConversationID != "" {
		referer = api.ChatGPTApiUrlPrefix + "/c/" + request.ConversationID
	}
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{
		Accept:      "text/event-stream",
		ContentType: "application/json",
		Referer:     referer,
	})
	req.Header.Set("Oai-Client-Version", oaiClientVersion)
	req.Header.Set("Oai-Client-Build-Number", oaiClientBuildNumber)
	if runtime.SessionID != "" {
		req.Header.Set("Oai-Session-Id", runtime.SessionID)
	}
	if runtime.TurnTraceID != "" {
		req.Header.Set("X-Oai-Turn-Trace-Id", runtime.TurnTraceID)
	}
	req.Header.Set("X-Openai-Target-Path", conversationTargetPath)
	req.Header.Set("X-Openai-Target-Route", conversationTargetPath)
	if runtime.ArkoseToken != "" {
		req.Header.Set("Openai-Sentinel-Arkose-Token", runtime.ArkoseToken)
	}
	if runtime.ChatRequirementsToken != "" {
		req.Header.Set("Openai-Sentinel-Chat-Requirements-Token", runtime.ChatRequirementsToken)
	}
	if runtime.ProofToken != "" {
		req.Header.Set("Openai-Sentinel-Proof-Token", runtime.ProofToken)
	}
	if runtime.TurnstileToken != "" {
		req.Header.Set("Openai-Sentinel-Turnstile-Token", runtime.TurnstileToken)
	}
	if accessToken != "" {
		req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(accessToken))
	}
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if accessToken != "" {
		if api.OAIDID != "" {
			req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
			req.Header.Set("Oai-Device-Id", api.OAIDID)
		}
	} else if uid != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+uid+";")
		req.Header.Set("Oai-Device-Id", uid)
	}
	if runtime.ConduitToken != "" {
		req.Header.Set("X-Conduit-Token", runtime.ConduitToken)
	}

	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return nil, true
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			logger.Error(fmt.Sprintf(api.AccountDeactivatedErrorMessage, c.GetString(api.EmailKey)))
		}

		responseMap := make(map[string]interface{})
		json.NewDecoder(resp.Body).Decode(&responseMap)
		c.AbortWithStatusJSON(resp.StatusCode, responseMap)
		return nil, true
	}

	return resp, false
}

func GetImageSource(wg *sync.WaitGroup, url string, prompt string, token string, idx int, imgSource []string) {
	defer wg.Done()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{Accept: "*/*"})
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	if token != "" {
		req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(token))
	}
	resp, err := api.Client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var fileInfo chatgpt.FileInfo
	err = json.NewDecoder(resp.Body).Decode(&fileInfo)
	if err != nil || fileInfo.Status != "success" {
		return
	}
	imgSource[idx] = "[![image](" + fileInfo.DownloadURL + " \"" + prompt + "\")](" + fileInfo.DownloadURL + ")"
}

func Handler(c *gin.Context, resp *http.Response, token string, stream bool, summary *conversationSummary, streamState *chatCompletionStreamState) (*conversationStreamResult, bool) {
	if stream {
		c.Header("Content-Type", "text/event-stream")
	} else {
		c.Header("Content-Type", "application/json")
	}

	reader := bufio.NewReader(resp.Body)
	streamResult, err := parseConversationStreamWithImageResolver(reader, func(delta string) error {
		if !stream {
			return nil
		}
		return writeStreamDelta(c, delta, streamState)
	}, func(message Message) string {
		return imageMarkdownFromMessage(message, token)
	})
	if err != nil {
		if stream {
			writeStreamError(c, err.Error(), "upstream_error")
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return nil, true
	}

	if summary != nil {
		summary.ConversationID = streamResult.ConversationID
		summary.MessageID = streamResult.MessageID
		summary.TurnExchangeID = streamResult.TurnExchangeID
	}
	return streamResult, false
}

func imageMarkdownFromMessage(message Message, token string) string {
	if message.Content.ContentType != "multimodal_text" {
		return ""
	}
	apiUrl := chatgpt.ApiPrefix + "/files/"
	if filesReverseProxy := os.Getenv("FILES_REVERSE_PROXY"); filesReverseProxy != "" {
		apiUrl = filesReverseProxy
	}
	imgSource := make([]string, len(message.Content.Parts))
	var waitGroup sync.WaitGroup
	for index, part := range message.Content.Parts {
		jsonItem, _ := json.Marshal(part)
		var dalleContent chatgpt.DallEContent
		if err := json.Unmarshal(jsonItem, &dalleContent); err != nil {
			continue
		}
		assetParts := strings.SplitN(dalleContent.AssetPointer, "//", 2)
		if len(assetParts) != 2 {
			continue
		}
		newURL := apiUrl + assetParts[1] + "/download"
		waitGroup.Add(1)
		go GetImageSource(&waitGroup, newURL, dalleContent.Metadata.Dalle.Prompt, token, index, imgSource)
	}
	waitGroup.Wait()
	return strings.Join(imgSource, "")
}

func writeStreamDelta(c *gin.Context, delta string, streamState *chatCompletionStreamState) error {
	if delta == "" {
		return nil
	}
	if streamState == nil {
		streamState = &chatCompletionStreamState{}
	}
	chunk := NewChatCompletionChunk(delta)
	if !streamState.RoleSent {
		chunk.Choices[0].Delta.Role = "assistant"
		streamState.RoleSent = true
	}
	if _, err := c.Writer.WriteString("data: " + chunk.String() + "\n\n"); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

func writeStreamStop(c *gin.Context, model string, id string, reason string) {
	stop := StopChunk(openAIFinishReason(reason))
	stop.ID = id
	stop.Model = model
	_, _ = c.Writer.WriteString("data: " + stop.String() + "\n\n")
	c.Writer.Flush()
}

func writeStreamError(c *gin.Context, message string, code string) {
	payload, _ := json.Marshal(gin.H{"error": gin.H{
		"message": message,
		"type":    "upstream_incomplete",
		"code":    code,
	}})
	_, _ = c.Writer.WriteString("data: " + string(payload) + "\n\n")
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}

func openAIFinishReason(reason string) string {
	switch reason {
	case "", "stop":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return reason
	}
}

func refreshConversationOutputs(accessToken string, result *conversationResult) {
	if result == nil || result.ConversationID == "" {
		return
	}
	text, messageID, outputMessages, err := fetchConversationOutputsForResult(accessToken, result.ConversationID, result.MessageID)
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	applyConversationOutputs(result, text, messageID, outputMessages)
}

func finalizeChatCompletionResult(accessToken string, originalRequest APIRequest, result *conversationResult) {
	if result == nil || result.FixedText || originalRequest.Stream || result.ConversationID == "" {
		return
	}

	refreshConversationOutputs(accessToken, result)
	if strings.TrimSpace(result.Text) != "" {
		return
	}

	time.Sleep(2 * time.Second)
	refreshConversationOutputs(accessToken, result)
}

func finalizeIncompleteConversation(accessToken string, result *conversationResult, allowImageGeneration bool) error {
	if result == nil || result.ConversationID == "" {
		return fmt.Errorf("upstream stream incomplete: missing conversation id")
	}
	var asyncErr error
	var imageWaitErr error
	if err := notifyConversationAsyncStatus(accessToken, result.ConversationID); err != nil {
		asyncErr = err
	}
	if allowImageGeneration {
		if !responsesOutputHasImageGeneration(result.OutputMessages) && len(result.GeneratedImageCandidates) == 0 {
			text, messageID, outputMessages, err := fetchConversationOutputsForResult(accessToken, result.ConversationID, result.MessageID)
			if err == nil {
				applyConversationOutputs(result, text, messageID, outputMessages)
			}
		}
		if responsesOutputHasImageGeneration(result.OutputMessages) {
			return nil
		}
		err := waitImageGenerationOutputs(accessToken, result)
		if err == nil {
			return nil
		}
		if strings.TrimSpace(result.Text) == "" {
			return err
		}
		imageWaitErr = err
	}
	if err := waitConversationOutputsComplete(accessToken, result); err != nil {
		if imageWaitErr != nil {
			return fmt.Errorf("%v; image generation wait failed: %w", err, imageWaitErr)
		}
		if asyncErr != nil {
			return fmt.Errorf("%v; async-status failed: %w", err, asyncErr)
		}
		return err
	}
	if allowImageGeneration {
		if responsesOutputHasImageGeneration(result.OutputMessages) {
			return nil
		}
		imageErr := waitImageGenerationOutputs(accessToken, result)
		if imageErr == nil {
			return nil
		}
		if strings.TrimSpace(result.Text) == "" {
			return imageErr
		}
		if imageWaitErr != nil {
			return imageWaitErr
		}
	}
	if strings.TrimSpace(result.Text) == "" {
		return fmt.Errorf("upstream stream incomplete: final conversation text is empty")
	}
	return nil
}

func applyConversationOutputs(result *conversationResult, text string, messageID string, outputMessages []ResponsesOutputMessage) {
	if result == nil {
		return
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
}

type conversationStreamStatusResponse struct {
	Status string `json:"status"`
}

func waitConversationOutputsComplete(accessToken string, result *conversationResult) error {
	if result == nil || result.ConversationID == "" {
		return fmt.Errorf("upstream stream incomplete: missing conversation id")
	}
	interval := time.Duration(envInt("IMITATE_FINALIZE_POLL_INTERVAL_MS", 500)) * time.Millisecond
	timeout := time.Duration(envInt("IMITATE_FINALIZE_TIMEOUT_MS", 120000)) * time.Millisecond
	deadline := time.Now().Add(timeout)
	endpoint := chatgpt.ApiPrefix + "/conversation/" + result.ConversationID + "/stream_status"
	var lastErr error
	streamComplete := false

	for {
		text, messageID, outputMessages, err := fetchConversationOutputsForResult(accessToken, result.ConversationID, result.MessageID)
		if err != nil {
			lastErr = err
		} else {
			applyConversationOutputs(result, text, messageID, outputMessages)
			if strings.TrimSpace(result.Text) != "" || responsesOutputHasImageGeneration(result.OutputMessages) {
				return nil
			}
		}

		resp, err := chatGPTJSONRequester(accessToken, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
		} else if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && len(strings.TrimSpace(string(body))) != 0 {
				var status conversationStreamStatusResponse
				if err = json.Unmarshal(body, &status); err == nil {
					if strings.EqualFold(status.Status, "COMPLETE") {
						streamComplete = true
					}
				} else {
					lastErr = err
				}
			} else if resp.StatusCode >= http.StatusBadRequest {
				lastErr = fmt.Errorf("stream_status returned %s: %s", resp.Status, string(body))
			}
		}

		if streamComplete {
			text, messageID, outputMessages, err = fetchConversationOutputsForResult(accessToken, result.ConversationID, result.MessageID)
			if err != nil {
				lastErr = err
			} else {
				applyConversationOutputs(result, text, messageID, outputMessages)
				if strings.TrimSpace(result.Text) != "" || responsesOutputHasImageGeneration(result.OutputMessages) {
					return nil
				}
			}
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for conversation output: %w", lastErr)
			}
			return fmt.Errorf("timed out waiting for conversation output")
		}
		time.Sleep(interval)
	}
}

func waitConversationStreamComplete(accessToken string, conversationID string) error {
	interval := time.Duration(envInt("IMITATE_FINALIZE_POLL_INTERVAL_MS", 500)) * time.Millisecond
	timeout := time.Duration(envInt("IMITATE_FINALIZE_TIMEOUT_MS", 120000)) * time.Millisecond
	deadline := time.Now().Add(timeout)
	endpoint := chatgpt.ApiPrefix + "/conversation/" + conversationID + "/stream_status"
	var lastErr error

	for {
		resp, err := chatGPTJSONRequester(accessToken, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
		} else if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && len(strings.TrimSpace(string(body))) != 0 {
				var status conversationStreamStatusResponse
				if err = json.Unmarshal(body, &status); err == nil && strings.EqualFold(status.Status, "COMPLETE") {
					return nil
				}
				if err != nil {
					lastErr = err
				}
			} else if resp.StatusCode >= http.StatusBadRequest {
				lastErr = fmt.Errorf("stream_status returned %s: %s", resp.Status, string(body))
			}
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for conversation stream completion: %w", lastErr)
			}
			return fmt.Errorf("timed out waiting for conversation stream completion")
		}
		time.Sleep(interval)
	}
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func imitateMaxContinues() int {
	return envInt("IMITATE_MAX_CONTINUES", 8)
}

func remainingTextDelta(sentText string, finalText string) string {
	if sentText == "" {
		return finalText
	}
	if strings.HasPrefix(finalText, sentText) {
		return strings.TrimPrefix(finalText, sentText)
	}

	sentRunes := []rune(sentText)
	finalRunes := []rune(finalText)
	maxOverlap := len(sentRunes)
	if len(finalRunes) < maxOverlap {
		maxOverlap = len(finalRunes)
	}
	for overlap := maxOverlap; overlap > 0; overlap-- {
		if string(sentRunes[len(sentRunes)-overlap:]) == string(finalRunes[:overlap]) {
			return string(finalRunes[overlap:])
		}
	}
	return finalText
}

func maybeDeleteConversation(accessToken string, result *conversationResult) {
	if !AutoDeleteConversations || result == nil || result.ConversationID == "" {
		return
	}
	if err := hideConversation(accessToken, result.ConversationID); err != nil {
		logger.Warn(fmt.Sprintf("hide imitate conversation failed, conversation_id=%s err=%v", result.ConversationID, err))
		return
	}
	logger.Info("hide imitate conversation success, conversation_id=" + result.ConversationID)
}

func hideConversation(accessToken string, conversationID string) error {
	reqBody := map[string]interface{}{
		"is_visible": false,
	}
	jsonBytes, _ := json.Marshal(reqBody)
	resp, err := doChatGPTJSONRequest(accessToken, http.MethodPatch, chatgpt.ApiPrefix+"/conversation/"+conversationID, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hide conversation failed: %s", string(body))
	}
	return nil
}

func fetchConversationOutputs(accessToken string, conversationID string, preferredMessageID string) (string, string, []ResponsesOutputMessage, error) {
	resp, err := doChatGPTJSONRequest(accessToken, http.MethodGet, chatgpt.ApiPrefix+"/conversation/"+conversationID, nil)
	if err != nil {
		return "", "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", nil, newUpstreamHTTPError("fetch conversation", resp, body)
	}

	var payload map[string]interface{}
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", nil, err
	}
	mapping, _ := payload["mapping"].(map[string]interface{})
	if len(mapping) == 0 {
		return "", "", nil, nil
	}

	selectedID := ""
	selectedText := ""
	for _, candidateID := range []string{preferredMessageID, stringValue(payload["current_node"])} {
		if candidateID == "" {
			continue
		}
		if text, ok := extractVisibleConversationNodeText(mapping, candidateID); ok {
			selectedID = candidateID
			selectedText = text
			break
		}
	}
	if selectedID == "" {
		bestID := ""
		bestText := ""
		bestCreateTime := float64(-1)
		for nodeID := range mapping {
			text, createTime, ok := extractVisibleConversationNodeTextWithTime(mapping, nodeID)
			if !ok {
				continue
			}
			if createTime >= bestCreateTime {
				bestCreateTime = createTime
				bestID = nodeID
				bestText = text
			}
		}
		selectedID = bestID
		selectedText = bestText
	}

	outputMessages := extractConversationOutputMessages(mapping, selectedID)
	imageMessages, imageErr := extractConversationImageGenerationMessages(accessToken, conversationID, mapping, selectedID)
	if imageErr != nil && strings.TrimSpace(selectedText) == "" {
		return "", "", nil, imageErr
	}
	outputMessages = mergeResponsesOutputMessages(imageMessages, outputMessages)
	if len(outputMessages) == 0 && strings.TrimSpace(selectedText) != "" {
		outputMessages = []ResponsesOutputMessage{newResponsesOutputMessage(selectedText)}
	}
	return selectedText, selectedID, outputMessages, nil
}

func extractVisibleConversationNodeText(mapping map[string]interface{}, nodeID string) (string, bool) {
	text, _, ok := extractVisibleConversationNodeTextWithTime(mapping, nodeID)
	return text, ok
}

func extractVisibleConversationNodeTextWithTime(mapping map[string]interface{}, nodeID string) (string, float64, bool) {
	node, _ := mapping[nodeID].(map[string]interface{})
	message, _ := node["message"].(map[string]interface{})
	if len(message) == 0 {
		return "", 0, false
	}
	author, _ := message["author"].(map[string]interface{})
	if stringValue(author["role"]) != "assistant" {
		return "", 0, false
	}
	if stringValue(message["recipient"]) != "all" {
		return "", 0, false
	}
	content, _ := message["content"].(map[string]interface{})
	if !strings.HasSuffix(stringValue(content["content_type"]), "text") {
		return "", 0, false
	}
	metadata, _ := message["metadata"].(map[string]interface{})
	if boolValue(metadata["is_visually_hidden_from_conversation"]) {
		return "", 0, false
	}
	text := extractConversationContentText(content)
	if strings.TrimSpace(text) == "" {
		return "", 0, false
	}
	return text, floatValue(message["create_time"]), true
}

func extractConversationContentText(content map[string]interface{}) string {
	if text := stringValue(content["text"]); text != "" {
		return text
	}
	parts, _ := content["parts"].([]interface{})
	if len(parts) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if text, ok := part.(string); ok {
			builder.WriteString(text)
		}
	}
	return builder.String()
}

type conversationOutputNode struct {
	NodeID      string
	Role        string
	Recipient   string
	ContentType string
	Text        string
	CreateTime  float64
}

func extractConversationOutputMessages(mapping map[string]interface{}, selectedID string) []ResponsesOutputMessage {
	targetTurnID := extractConversationTurnExchangeID(mapping, selectedID)
	nodes := make([]conversationOutputNode, 0)
	for nodeID := range mapping {
		node, ok := extractConversationOutputNode(mapping, nodeID, targetTurnID)
		if !ok {
			continue
		}
		nodes = append(nodes, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].CreateTime == nodes[j].CreateTime {
			return nodes[i].NodeID < nodes[j].NodeID
		}
		return nodes[i].CreateTime < nodes[j].CreateTime
	})

	output := make([]ResponsesOutputMessage, 0, len(nodes))
	for _, node := range nodes {
		output = append(output, newResponsesConversationOutputMessage(node))
	}
	return output
}

func extractConversationTurnExchangeID(mapping map[string]interface{}, nodeID string) string {
	node, _ := mapping[nodeID].(map[string]interface{})
	message, _ := node["message"].(map[string]interface{})
	metadata, _ := message["metadata"].(map[string]interface{})
	return stringValue(metadata["turn_exchange_id"])
}

func extractConversationOutputNode(mapping map[string]interface{}, nodeID string, targetTurnID string) (conversationOutputNode, bool) {
	node, _ := mapping[nodeID].(map[string]interface{})
	message, _ := node["message"].(map[string]interface{})
	if len(message) == 0 {
		return conversationOutputNode{}, false
	}
	author, _ := message["author"].(map[string]interface{})
	role := stringValue(author["role"])
	if role != "assistant" && role != "tool" {
		return conversationOutputNode{}, false
	}
	metadata, _ := message["metadata"].(map[string]interface{})
	if targetTurnID != "" && stringValue(metadata["turn_exchange_id"]) != targetTurnID {
		return conversationOutputNode{}, false
	}
	content, _ := message["content"].(map[string]interface{})
	contentType := stringValue(content["content_type"])
	text := extractConversationContentText(content)
	if text == "" && role == "tool" {
		aggregateResult, _ := metadata["aggregate_result"].(map[string]interface{})
		text = stringValue(aggregateResult["final_expression_output"])
	}
	if strings.TrimSpace(text) == "" {
		return conversationOutputNode{}, false
	}
	return conversationOutputNode{
		NodeID:      nodeID,
		Role:        role,
		Recipient:   stringValue(message["recipient"]),
		ContentType: contentType,
		Text:        text,
		CreateTime:  floatValue(message["create_time"]),
	}, true
}

func newResponsesConversationOutputMessage(node conversationOutputNode) ResponsesOutputMessage {
	contentType := "text"
	if node.Role == "assistant" {
		contentType = "output_text"
	}
	return ResponsesOutputMessage{
		ID:     node.NodeID,
		Type:   "message",
		Status: "completed",
		Role:   node.Role,
		Content: []ResponsesOutputText{
			{
				Type:        contentType,
				Text:        node.Text,
				Annotations: []interface{}{},
			},
		},
	}
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func boolValue(value interface{}) bool {
	flag, _ := value.(bool)
	return flag
}

func floatValue(value interface{}) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case int:
		return float64(number)
	case int64:
		return float64(number)
	default:
		return 0
	}
}

func isVisibleTextMessage(message Message) bool {
	if message.Recipient != "all" {
		return false
	}
	if message.Metadata.MessageType == "" {
		return false
	}
	if message.Metadata.MessageType != "next" && message.Metadata.MessageType != "continue" {
		return false
	}
	if !strings.HasSuffix(message.Content.ContentType, "text") {
		return false
	}
	return message.Author.Role == "assistant" || (message.Author.Role == "tool" && message.Content.ContentType != "text")
}

type patchOperation struct {
	Path  string      `json:"p"`
	Op    string      `json:"o"`
	Value interface{} `json:"v"`
}

type patchEnvelope struct {
	Op    string           `json:"o"`
	Value []patchOperation `json:"v"`
}

func extractPatchedText(line string, previousText *StringStruct) (string, bool) {
	if previousText.Parts == nil {
		previousText.Parts = make(map[int]string)
	}

	var envelope patchEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err == nil && len(envelope.Value) != 0 {
		return applyPatchOperations(previousText, envelope.Value)
	}

	var operation patchOperation
	if err := json.Unmarshal([]byte(line), &operation); err != nil {
		return "", false
	}
	if operation.Path != "" || operation.Op != "" {
		return applyPatchOperations(previousText, []patchOperation{operation})
	}

	if value, ok := operation.Value.(string); ok && value != "" {
		return applyPatchOperations(previousText, []patchOperation{{
			Path:  inferActiveContentPartPath(previousText),
			Op:    "append",
			Value: value,
		}})
	}
	return "", false
}

func applyPatchOperations(previousText *StringStruct, operations []patchOperation) (string, bool) {
	previousFullText := previousText.Text
	updated := false
	for _, operation := range operations {
		partIndex, ok := extractContentPartIndex(operation.Path)
		if !ok {
			continue
		}
		value, ok := operation.Value.(string)
		if !ok && operation.Op != "remove" {
			continue
		}

		switch operation.Op {
		case "append":
			previousText.Parts[partIndex] += value
			updated = true
		case "replace":
			previousText.Parts[partIndex] = value
			updated = true
		case "remove":
			delete(previousText.Parts, partIndex)
			updated = true
		}
	}

	if !updated {
		return "", false
	}
	previousText.Text = joinTextParts(previousText.Parts)
	deltaText := diffResponseText(previousFullText, previousText.Text)
	if deltaText == "" {
		return "", false
	}
	return deltaText, true
}

func extractContentPartIndex(path string) (int, bool) {
	const prefix = "/message/content/parts/"
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	rawIndex := strings.TrimPrefix(path, prefix)
	if rawIndex == "" || strings.Contains(rawIndex, "/") {
		return 0, false
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		return 0, false
	}
	return index, true
}

func joinTextParts(parts map[int]string) string {
	if len(parts) == 0 {
		return ""
	}
	keys := make([]int, 0, len(parts))
	for index := range parts {
		keys = append(keys, index)
	}
	sort.Ints(keys)

	var builder strings.Builder
	for _, index := range keys {
		builder.WriteString(parts[index])
	}
	return builder.String()
}

func inferActiveContentPartPath(previousText *StringStruct) string {
	if len(previousText.Parts) == 0 {
		return "/message/content/parts/0"
	}
	maxIndex := 0
	for index := range previousText.Parts {
		if index > maxIndex {
			maxIndex = index
		}
	}
	return "/message/content/parts/" + strconv.Itoa(maxIndex)
}

func cloneStringStruct(value StringStruct) StringStruct {
	cloned := StringStruct{
		Text: value.Text,
	}
	if len(value.Parts) == 0 {
		return cloned
	}
	cloned.Parts = make(map[int]string, len(value.Parts))
	for index, part := range value.Parts {
		cloned.Parts[index] = part
	}
	return cloned
}

var urlAttrMap = make(map[string]string)

func getURLAttribution(token string, puid string, url string) string {
	req, err := http.NewRequest(http.MethodPost, chatgpt.ApiPrefix+"/attributions", bytes.NewBuffer([]byte(`{"urls":["`+url+`"]}`)))
	if err != nil {
		return ""
	}
	if puid != "" {
		req.Header.Set("Cookie", "_puid="+puid+";")
	}
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{
		Accept:      "*/*",
		ContentType: "application/json",
	})
	if token != "" {
		req.Header.Set("Authorization", api.GetAccessToken(token))
	}
	if err != nil {
		return ""
	}
	resp, err := api.Client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var urlAttr chatgpt.UrlAttr
	err = json.NewDecoder(resp.Body).Decode(&urlAttr)
	if err != nil {
		return ""
	}
	return urlAttr.Attribution
}
