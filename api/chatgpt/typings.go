package chatgpt

import (
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/google/uuid"
)

type UserLogin struct {
	client tls_client.HttpClient
}

type CreateConversationRequest struct {
	Action                     string           `json:"action"`
	Messages                   []Message        `json:"messages"`
	Model                      string           `json:"model"`
	ParentMessageID            string           `json:"parent_message_id"`
	ConversationID             *string          `json:"conversation_id"`
	PluginIDs                  []string         `json:"plugin_ids"`
	TimezoneOffsetMin          int              `json:"timezone_offset_min"`
	ArkoseToken                string           `json:"arkose_token"`
	VariantPurpose             string           `json:"variant_purpose"`
	HistoryAndTrainingDisabled bool             `json:"history_and_training_disabled"`
	ConversationMode           ConversationMode `json:"conversation_mode"`
	ForceParagen               bool             `json:"force_paragen"`
	ForceParagenModelSlug      string           `json:"force_paragen_model_slug"`
	ForceNulligen              bool             `json:"force_nulligen"`
	ForceRateLimit             bool             `json:"force_rate_limit"`
	Suggestions                []string         `json:"suggestions"`
	WebSocketRequestId         string           `json:"websocket_request_id"`
}

type ConversationMode struct {
	Kind      string   `json:"kind"`
	PluginIds []string `json:"plugin_ids"`
}

type Message struct {
	Author Author `json:"author"`
	//Role     string      `json:"role"`
	Content  Content     `json:"content"`
	ID       string      `json:"id"`
	Metadata interface{} `json:"metadata"`
}

type MessageMetadata struct {
	ExcludeAfterNextUserMessage bool   `json:"exclude_after_next_user_message"`
	TargetReply                 string `json:"target_reply"`
}

type Author struct {
	Role string `json:"role"`
}

type Content struct {
	ContentType string        `json:"content_type"`
	Parts       []interface{} `json:"parts"`
}

type CreateConversationWSSResponse struct {
	WssUrl         string `json:"wss_url"`
	ConversationId string `json:"conversation_id"`
	ResponseId     string `json:"response_id"`
}

type WSSConversationResponse struct {
	SequenceId int                         `json:"sequenceId"`
	Type       string                      `json:"type"`
	From       string                      `json:"from"`
	DataType   string                      `json:"dataType"`
	Data       WSSConversationResponseData `json:"data"`
}

type WSSSequenceAckMessage struct {
	Type       string `json:"type"`
	SequenceId int    `json:"sequenceId"`
}

type WSSConversationResponseData struct {
	Type           string `json:"type"`
	Body           string `json:"body"`
	MoreBody       bool   `json:"more_body"`
	ResponseId     string `json:"response_id"`
	ConversationId string `json:"conversation_id"`
}

type CreateConversationResponse struct {
	Message struct {
		ID     string `json:"id"`
		Author struct {
			Role     string      `json:"role"`
			Name     interface{} `json:"name"`
			Metadata struct {
			} `json:"metadata"`
		} `json:"author"`
		CreateTime float64     `json:"create_time"`
		UpdateTime interface{} `json:"update_time"`
		Content    struct {
			ContentType string   `json:"content_type"`
			Parts       []string `json:"parts"`
		} `json:"content"`
		Status   string  `json:"status"`
		EndTurn  bool    `json:"end_turn"`
		Weight   float64 `json:"weight"`
		Metadata struct {
			MessageType   string `json:"message_type"`
			ModelSlug     string `json:"model_slug"`
			FinishDetails struct {
				Type string `json:"type"`
			} `json:"finish_details"`
		} `json:"metadata"`
		Recipient string `json:"recipient"`
	} `json:"message"`
	ConversationID string      `json:"conversation_id"`
	Error          interface{} `json:"error"`
}

type FeedbackMessageRequest struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	Rating         string `json:"rating"`
}

type GenerateTitleRequest struct {
	MessageID string `json:"message_id"`
}

type PatchConversationRequest struct {
	Title     *string `json:"title"`
	IsVisible bool    `json:"is_visible"`
}

type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Expiry int64  `json:"expiry"`
}

func (c *CreateConversationRequest) AddMessage(role string, content string, metadata interface{}) {
	c.Messages = append(c.Messages, Message{
		ID:       uuid.NewString(),
		Author:   Author{Role: role},
		Content:  Content{ContentType: "text", Parts: []interface{}{content}},
		Metadata: metadata,
	})
}

type ChatRequirements struct {
	Token  string `json:"token"`
	Arkose struct {
		Required bool   `json:"required"`
		Dx       string `json:"dx,omitempty"`
	} `json:"arkose"`
}

type GetModelsResponse struct {
	Models []struct {
		Slug         string   `json:"slug"`
		MaxTokens    int      `json:"max_tokens"`
		Title        string   `json:"title"`
		Description  string   `json:"description"`
		Tags         []string `json:"tags"`
		Capabilities struct {
		} `json:"capabilities"`
		EnabledTools []string `json:"enabled_tools,omitempty"`
	} `json:"models"`
	Categories []struct {
		Category             string `json:"category"`
		HumanCategoryName    string `json:"human_category_name"`
		SubscriptionLevel    string `json:"subscription_level"`
		DefaultModel         string `json:"default_model"`
		CodeInterpreterModel string `json:"code_interpreter_model"`
		PluginsModel         string `json:"plugins_model"`
	} `json:"categories"`
}

type WebSocketResponse struct {
	WssUrl         string `json:"wss_url"`
	ConversationId string `json:"conversation_id,omitempty"`
	ResponseId     string `json:"response_id,omitempty"`
}

type WebSocketMessageResponse struct {
	SequenceId int                          `json:"sequenceId"`
	Type       string                       `json:"type"`
	From       string                       `json:"from"`
	DataType   string                       `json:"dataType"`
	Data       WebSocketMessageResponseData `json:"data"`
}

type WebSocketMessageResponseData struct {
	Type           string `json:"type"`
	Body           string `json:"body"`
	MoreBody       bool   `json:"more_body"`
	ResponseId     string `json:"response_id"`
	ConversationId string `json:"conversation_id"`
}

type DallEContent struct {
	AssetPointer string `json:"asset_pointer"`
	Metadata     struct {
		Dalle struct {
			Prompt string `json:"prompt"`
		} `json:"dalle"`
	} `json:"metadata"`
}

type FileInfo struct {
	DownloadURL string `json:"download_url"`
	Status      string `json:"status"`
}

type UrlAttr struct {
	Url         string `json:"url"`
	Attribution string `json:"attribution"`
}
