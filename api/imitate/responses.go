package imitate

import (
	"encoding/json"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/leokwsw/go-chatgpt-api/api"
)

type ResponsesRequest struct {
	Model              string        `json:"model"`
	Input              interface{}   `json:"input"`
	Instructions       interface{}   `json:"instructions,omitempty"`
	Stream             bool          `json:"stream,omitempty"`
	Background         *bool         `json:"background,omitempty"`
	MaxOutputTokens    *int          `json:"max_output_tokens,omitempty"`
	Metadata           interface{}   `json:"metadata,omitempty"`
	PreviousResponseID string        `json:"previous_response_id,omitempty"`
	Reasoning          interface{}   `json:"reasoning,omitempty"`
	ServiceTier        string        `json:"service_tier,omitempty"`
	Store              *bool         `json:"store,omitempty"`
	Temperature        *float64      `json:"temperature,omitempty"`
	Text               interface{}   `json:"text,omitempty"`
	ToolChoice         interface{}   `json:"tool_choice,omitempty"`
	Tools              []interface{} `json:"tools,omitempty"`
	TopP               *float64      `json:"top_p,omitempty"`
	Truncation         string        `json:"truncation,omitempty"`
	User               string        `json:"user,omitempty"`
	ParallelToolCalls  *bool         `json:"parallel_tool_calls,omitempty"`
}

type ResponsesOutputText struct {
	Type        string        `json:"type"`
	Text        string        `json:"text"`
	Annotations []interface{} `json:"annotations"`
}

type ResponsesOutputMessage struct {
	ID      string                `json:"id"`
	Type    string                `json:"type"`
	Status  string                `json:"status"`
	Role    string                `json:"role,omitempty"`
	Content []ResponsesOutputText `json:"content,omitempty"`

	Result        string `json:"result,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ResponsesResponse struct {
	ID                 string                   `json:"id"`
	Object             string                   `json:"object"`
	CreatedAt          int64                    `json:"created_at"`
	CompletedAt        *int64                   `json:"completed_at,omitempty"`
	Error              interface{}              `json:"error"`
	IncompleteDetails  interface{}              `json:"incomplete_details"`
	Instructions       interface{}              `json:"instructions,omitempty"`
	Metadata           interface{}              `json:"metadata,omitempty"`
	Model              string                   `json:"model"`
	Output             []ResponsesOutputMessage `json:"output"`
	ParallelToolCalls  bool                     `json:"parallel_tool_calls"`
	Status             string                   `json:"status,omitempty"`
	Temperature        *float64                 `json:"temperature,omitempty"`
	Text               interface{}              `json:"text,omitempty"`
	ToolChoice         interface{}              `json:"tool_choice"`
	Tools              []interface{}            `json:"tools"`
	TopP               *float64                 `json:"top_p,omitempty"`
	Background         *bool                    `json:"background,omitempty"`
	MaxOutputTokens    *int                     `json:"max_output_tokens,omitempty"`
	PreviousResponseID string                   `json:"previous_response_id,omitempty"`
	Reasoning          interface{}              `json:"reasoning,omitempty"`
	ServiceTier        string                   `json:"service_tier,omitempty"`
	Store              *bool                    `json:"store,omitempty"`
	Truncation         string                   `json:"truncation,omitempty"`
	Usage              ResponsesUsage           `json:"usage"`
	User               string                   `json:"user,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func CreateResponses(c *gin.Context) {
	var request ResponsesRequest
	if err := c.BindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
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
		c.AbortWithStatusJSON(http.StatusRequestTimeout, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "request_timeout",
			"param":   nil,
			"code":    "queue_wait_canceled",
		}})
		return
	}
	defer releaseAccount()

	cleanupResults := make([]*conversationResult, 0, 2)
	defer func() {
		for _, item := range cleanupResults {
			maybeDeleteConversation(accessToken, item)
		}
	}()

	imageGenerationRequest := responsesRequestUsesImageGeneration(request)
	apiRequest := convertResponsesRequest(request)
	result, handled := runConversationRequest(c, apiRequest, accessToken, false)
	if handled {
		return
	}
	cleanupResults = append(cleanupResults, result)
	if imageGenerationRequest {
		if !responsesOutputHasImageGeneration(result.OutputMessages) {
			if err = waitImageGenerationOutputs(accessToken, result); err != nil {
				c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage(err.Error()))
				return
			}
		}
	} else {
		refreshConversationOutputs(accessToken, result)
	}
	if strings.TrimSpace(result.Text) == "" && !responsesOutputHasImageGeneration(result.OutputMessages) && responsesRequestHasAttachment(request.Input) {
		time.Sleep(2 * time.Second)
		result, handled = runConversationRequest(c, apiRequest, accessToken, false)
		if handled {
			return
		}
		cleanupResults = append(cleanupResults, result)
		if imageGenerationRequest {
			if !responsesOutputHasImageGeneration(result.OutputMessages) {
				if err = waitImageGenerationOutputs(accessToken, result); err != nil {
					c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage(err.Error()))
					return
				}
			}
		} else {
			refreshConversationOutputs(accessToken, result)
		}
	}

	response := newResponsesResponse(request, result)
	if !request.Stream {
		c.JSON(http.StatusOK, response)
		return
	}

	writeResponsesStream(c, response)
}

func convertResponsesRequest(request ResponsesRequest) APIRequest {
	apiRequest := APIRequest{
		Messages:            make([]ApiMessage, 0),
		Model:               request.Model,
		Stream:              false,
		MaxCompletionTokens: request.MaxOutputTokens,
		Temperature:         request.Temperature,
		TopP:                request.TopP,
		ToolChoice:          request.ToolChoice,
		Tools:               request.Tools,
		User:                request.User,
		Metadata:            request.Metadata,
		Store:               request.Store,
		ParallelToolCalls:   request.ParallelToolCalls,
		ResponseFormat:      request.Text,
	}
	if effort := extractReasoningEffort(request.Reasoning); effort != "" {
		apiRequest.ReasoningEffort = effort
	}

	appendResponsesInput(&apiRequest.Messages, request.Instructions, "developer")
	if responsesRequestUsesImageGeneration(request) {
		appendResponsesInput(&apiRequest.Messages, "The request includes an image_generation tool. Use ChatGPT image generation for the user's image request. If text output is also requested, return it as ordinary text after the image is generated.", "developer")
	}
	appendResponsesInput(&apiRequest.Messages, request.Input, "user")
	return apiRequest
}

func appendResponsesInput(messages *[]ApiMessage, raw interface{}, defaultRole string) {
	switch value := raw.(type) {
	case nil:
		return
	case string:
		*messages = append(*messages, ApiMessage{
			Role:    defaultRole,
			Content: value,
		})
	case []interface{}:
		pending := make([]interface{}, 0)
		flushPending := func() {
			if len(pending) == 0 {
				return
			}
			parts := make([]interface{}, len(pending))
			copy(parts, pending)
			*messages = append(*messages, ApiMessage{
				Role:    defaultRole,
				Content: parts,
			})
			pending = pending[:0]
		}
		for _, item := range value {
			partMap, ok := item.(map[string]interface{})
			if ok && isResponsesMessageItem(partMap) {
				flushPending()
				role, _ := partMap["role"].(string)
				if role == "" {
					role = defaultRole
				}
				content := partMap["content"]
				if content == nil {
					content = ""
				}
				*messages = append(*messages, ApiMessage{
					Role:     role,
					Content:  content,
					Metadata: partMap["metadata"],
				})
				continue
			}
			pending = append(pending, item)
		}
		flushPending()
	case map[string]interface{}:
		if isResponsesMessageItem(value) {
			role, _ := value["role"].(string)
			if role == "" {
				role = defaultRole
			}
			content := value["content"]
			if content == nil {
				content = ""
			}
			*messages = append(*messages, ApiMessage{
				Role:     role,
				Content:  content,
				Metadata: value["metadata"],
			})
			return
		}
		*messages = append(*messages, ApiMessage{
			Role:    defaultRole,
			Content: []interface{}{value},
		})
	default:
		*messages = append(*messages, ApiMessage{
			Role:    defaultRole,
			Content: value,
		})
	}
}

func isResponsesMessageItem(item map[string]interface{}) bool {
	if role, ok := item["role"].(string); ok && role != "" {
		return true
	}
	if itemType, ok := item["type"].(string); ok && itemType == "message" {
		return true
	}
	_, hasContent := item["content"]
	return hasContent
}

func extractReasoningEffort(reasoning interface{}) string {
	reasoningMap, ok := reasoning.(map[string]interface{})
	if !ok {
		return ""
	}
	effort, _ := reasoningMap["effort"].(string)
	return strings.TrimSpace(effort)
}

func responsesRequestHasAttachment(input interface{}) bool {
	switch value := input.(type) {
	case []interface{}:
		for _, item := range value {
			if responsesRequestHasAttachment(item) {
				return true
			}
		}
	case map[string]interface{}:
		if itemType, ok := value["type"].(string); ok {
			switch itemType {
			case "input_image", "input_file", "image_url", "file":
				return true
			}
		}
		for key, itemValue := range value {
			switch key {
			case "file_id", "file_url", "file_data", "image_url", "image_file", "input_file", "file":
				if itemValue != nil {
					return true
				}
			}
			if responsesRequestHasAttachment(itemValue) {
				return true
			}
		}
	}
	return false
}

func responsesRequestUsesImageGeneration(request ResponsesRequest) bool {
	return toolsUseImageGeneration(request.Tools) || valueUsesImageGeneration(request.ToolChoice)
}

func toolsUseImageGeneration(tools []interface{}) bool {
	for _, tool := range tools {
		if valueUsesImageGeneration(tool) {
			return true
		}
	}
	return false
}

func valueUsesImageGeneration(value interface{}) bool {
	switch typed := value.(type) {
	case map[string]interface{}:
		if toolType, ok := typed["type"].(string); ok && toolType == "image_generation" {
			return true
		}
		for _, child := range typed {
			if valueUsesImageGeneration(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if valueUsesImageGeneration(child) {
				return true
			}
		}
	}
	return false
}

func newResponsesResponse(request ResponsesRequest, result *conversationResult) ResponsesResponse {
	createdAt := time.Now().Unix()
	completedAt := createdAt
	toolChoice := request.ToolChoice
	if toolChoice == nil {
		toolChoice = "auto"
	}
	parallelToolCalls := false
	if request.ParallelToolCalls != nil {
		parallelToolCalls = *request.ParallelToolCalls
	}
	output := result.OutputMessages
	if len(output) == 0 {
		output = []ResponsesOutputMessage{newResponsesOutputMessage(result.Text)}
	}
	return ResponsesResponse{
		ID:                 newResponseObjectID("resp_"),
		Object:             "response",
		CreatedAt:          createdAt,
		CompletedAt:        &completedAt,
		Instructions:       request.Instructions,
		Metadata:           request.Metadata,
		Model:              result.Model,
		Output:             output,
		ParallelToolCalls:  parallelToolCalls,
		Status:             "completed",
		Temperature:        request.Temperature,
		Text:               request.Text,
		ToolChoice:         toolChoice,
		Tools:              normalizeResponseTools(request.Tools),
		TopP:               request.TopP,
		Background:         request.Background,
		MaxOutputTokens:    request.MaxOutputTokens,
		PreviousResponseID: request.PreviousResponseID,
		Reasoning:          request.Reasoning,
		ServiceTier:        request.ServiceTier,
		Store:              request.Store,
		Truncation:         request.Truncation,
		Usage:              ResponsesUsage{},
		User:               request.User,
	}
}

func newResponsesOutputMessage(text string) ResponsesOutputMessage {
	return ResponsesOutputMessage{
		ID:     newResponseObjectID("msg_"),
		Type:   "message",
		Status: "completed",
		Role:   "assistant",
		Content: []ResponsesOutputText{
			{
				Type:        "output_text",
				Text:        text,
				Annotations: []interface{}{},
			},
		},
	}
}

func normalizeResponseTools(tools []interface{}) []interface{} {
	if tools == nil {
		return []interface{}{}
	}
	return tools
}

func newResponseObjectID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func writeResponsesStream(c *gin.Context, response ResponsesResponse) {
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	inProgress := response
	inProgress.Status = "in_progress"
	inProgress.CompletedAt = nil
	inProgress.Output = []ResponsesOutputMessage{}

	writeResponseEvent(c, "response.created", gin.H{
		"type":        "response.created",
		"event_id":    newResponseObjectID("event_"),
		"response":    inProgress,
		"response_id": response.ID,
	})
	for outputIndex, item := range response.Output {
		itemInProgress := item
		itemInProgress.Status = "in_progress"
		if item.Type == "message" {
			itemInProgress.Content = []ResponsesOutputText{{
				Type:        "output_text",
				Text:        "",
				Annotations: []interface{}{},
			}}
		}

		writeResponseEvent(c, "response.output_item.added", gin.H{
			"type":         "response.output_item.added",
			"event_id":     newResponseObjectID("event_"),
			"response_id":  response.ID,
			"output_index": outputIndex,
			"item":         itemInProgress,
		})
		if item.Type != "message" {
			writeResponseEvent(c, "response.output_item.done", gin.H{
				"type":         "response.output_item.done",
				"event_id":     newResponseObjectID("event_"),
				"response_id":  response.ID,
				"output_index": outputIndex,
				"item":         item,
			})
			continue
		}

		partAdded := ResponsesOutputText{
			Type:        "output_text",
			Text:        "",
			Annotations: []interface{}{},
		}
		writeResponseEvent(c, "response.content_part.added", gin.H{
			"type":          "response.content_part.added",
			"event_id":      newResponseObjectID("event_"),
			"response_id":   response.ID,
			"item_id":       item.ID,
			"output_index":  outputIndex,
			"content_index": 0,
			"part":          partAdded,
		})
		if len(item.Content) != 0 && item.Content[0].Text != "" {
			writeResponseEvent(c, "response.output_text.delta", gin.H{
				"type":          "response.output_text.delta",
				"event_id":      newResponseObjectID("event_"),
				"response_id":   response.ID,
				"item_id":       item.ID,
				"output_index":  outputIndex,
				"content_index": 0,
				"delta":         item.Content[0].Text,
			})
		}
		if len(item.Content) != 0 {
			writeResponseEvent(c, "response.output_text.done", gin.H{
				"type":          "response.output_text.done",
				"event_id":      newResponseObjectID("event_"),
				"response_id":   response.ID,
				"item_id":       item.ID,
				"output_index":  outputIndex,
				"content_index": 0,
				"text":          item.Content[0].Text,
			})
			writeResponseEvent(c, "response.content_part.done", gin.H{
				"type":          "response.content_part.done",
				"event_id":      newResponseObjectID("event_"),
				"response_id":   response.ID,
				"item_id":       item.ID,
				"output_index":  outputIndex,
				"content_index": 0,
				"part":          item.Content[0],
			})
		}
		writeResponseEvent(c, "response.output_item.done", gin.H{
			"type":         "response.output_item.done",
			"event_id":     newResponseObjectID("event_"),
			"response_id":  response.ID,
			"output_index": outputIndex,
			"item":         item,
		})
	}
	writeResponseEvent(c, "response.completed", gin.H{
		"type":     "response.completed",
		"event_id": newResponseObjectID("event_"),
		"response": response,
	})
	c.Writer.Flush()
}

func writeResponseEvent(c *gin.Context, eventType string, payload interface{}) {
	body, _ := json.Marshal(payload)
	_, _ = c.Writer.WriteString("event: " + eventType + "\n")
	_, _ = c.Writer.WriteString("data: " + string(body) + "\n\n")
	c.Writer.Flush()
}
