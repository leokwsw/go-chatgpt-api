package imitate

import (
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	http "github.com/bogdanfinn/fhttp"
)

type ContinueInfo struct {
	ConversationID string `json:"conversation_id"`
	ParentID       string `json:"parent_id"`
}

type APIRequest struct {
	Messages            []ApiMessage       `json:"messages"`
	Stream              bool               `json:"stream"`
	Model               string             `json:"model"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"`
	ThinkingEffort      string             `json:"thinking_effort,omitempty"`
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	N                   *int               `json:"n,omitempty"`
	Stop                interface{}        `json:"stop,omitempty"`
	PresencePenalty     *float64           `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64           `json:"frequency_penalty,omitempty"`
	LogitBias           map[string]float64 `json:"logit_bias,omitempty"`
	User                string             `json:"user,omitempty"`
	Seed                *int64             `json:"seed,omitempty"`
	ResponseFormat      interface{}        `json:"response_format,omitempty"`
	Tools               interface{}        `json:"tools,omitempty"`
	ToolChoice          interface{}        `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool              `json:"parallel_tool_calls,omitempty"`
	StreamOptions       interface{}        `json:"stream_options,omitempty"`
	Metadata            interface{}        `json:"metadata,omitempty"`
	Modalities          []string           `json:"modalities,omitempty"`
	Audio               interface{}        `json:"audio,omitempty"`
	Store               *bool              `json:"store,omitempty"`
}

type ApiMessage struct {
	Role     string      `json:"role"`
	Content  interface{} `json:"content"`
	Metadata interface{} `json:"metadata,omitempty"`
}

func HandleRequestError(c *gin.Context, response *http.Response) bool {
	if response.StatusCode != 200 {
		// Try read response body as JSON
		var errorResponse map[string]interface{}
		err := json.NewDecoder(response.Body).Decode(&errorResponse)
		if err != nil {
			// Read response body
			body, _ := io.ReadAll(response.Body)
			c.JSON(500, gin.H{"error": gin.H{
				"message": "Unknown error",
				"type":    "internal_server_error",
				"param":   nil,
				"code":    "500",
				"details": string(body),
			}})
			return true
		}
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": errorResponse["detail"],
			"type":    response.Status,
			"param":   nil,
			"code":    "error",
		}})
		return true
	}
	return false
}
