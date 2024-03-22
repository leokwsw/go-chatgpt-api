package chatgpt

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/linweiyuan/go-logger/logger"
	"io"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/linweiyuan/go-chatgpt-api/api"

	http "github.com/bogdanfinn/fhttp"
	http2 "net/http"
)

func GetConversations(c *gin.Context) {
	offset, ok := c.GetQuery("offset")
	if !ok {
		offset = "0"
	}
	limit, ok := c.GetQuery("limit")
	if !ok {
		limit = "20"
	}
	handleGet(c, ApiPrefix+"/conversations?offset="+offset+"&limit="+limit, getConversationsErrorMessage)
}

func CreateConversation(c *gin.Context) {
	var request CreateConversationRequest
	var apiVersion int

	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	if request.ConversationID == nil || *request.ConversationID == "" {
		request.ConversationID = nil
	}

	if len(request.Messages) != 0 {
		message := request.Messages[0]
		if message.Author.Role == "" {
			message.Author.Role = defaultRole
		}

		if message.Metadata == nil {
			message.Metadata = map[string]string{}
		}

		request.Messages[0] = message
	}

	if strings.HasPrefix(request.Model, gpt4Model) {
		apiVersion = 4
	} else {
		apiVersion = 3
	}

	chatRequirements, err := GetChatRequirementsByGin(c)

	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	fmt.Println("chat_require token" + chatRequirements.Token)

	if chatRequirements.Arkose.Required == true && request.ArkoseToken == "" {
		arkoseToken, err := api.GetArkoseToken(apiVersion)
		if err != nil || arkoseToken == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, api.ReturnMessage(err.Error()))
			return
		}

		request.ArkoseToken = arkoseToken
	}

	fmt.Println("chat_require arkoseToken" + request.ArkoseToken)

	resp, done := sendConversationRequest(c, request, chatRequirements.Token)
	if done {
		return
	}
	handleConversationResponse(c, resp, request, chatRequirements.Token)
}

func sendConversationRequest(c *gin.Context, request CreateConversationRequest, chatRequirementsToken string) (*http.Response, bool) {
	jsonBytes, _ := json.Marshal(request)
	req, _ := http.NewRequest(http.MethodPost, ApiPrefix+"/conversation", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	req.Header.Set("Accept", "text/event-stream")
	if request.ArkoseToken != "" {
		req.Header.Set("Openai-Sentinel-Arkose-Token", request.ArkoseToken)
	}
	if chatRequirementsToken != "" {
		req.Header.Set("Openai-Sentinel-Chat-Requirements-Token", chatRequirementsToken)
	}
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	req.Header.Set("Oai-Language", api.Language)
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID)
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return nil, true
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			logger.Error(fmt.Sprintf(api.AccountDeactivatedErrorMessage, c.GetString(api.EmailKey)))
			responseMap := make(map[string]interface{})
			json.NewDecoder(resp.Body).Decode(&responseMap)
			c.AbortWithStatusJSON(resp.StatusCode, responseMap)
			return nil, true
		}

		req, _ := http.NewRequest(http.MethodGet, ApiPrefix+"/models", nil)
		req.Header.Set("User-Agent", api.UserAgent)
		req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
		response, err := api.Client.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
			return nil, true
		}

		defer response.Body.Close()
		modelAvailable := false
		var getModelsResponse GetModelsResponse
		json.NewDecoder(response.Body).Decode(&getModelsResponse)
		for _, model := range getModelsResponse.Models {
			if model.Slug == request.Model {
				modelAvailable = true
				break
			}
		}
		if !modelAvailable {
			c.AbortWithStatusJSON(http.StatusForbidden, api.ReturnMessage(noModelPermissionErrorMessage))
			return nil, true
		}

		fmt.Printf("OpenAI Request Method : %s ; url : %s ; Status : %d\n\n", http.MethodPost, ApiPrefix+"/conversation", resp.StatusCode)
		responseMap := make(map[string]interface{})
		json.NewDecoder(resp.Body).Decode(&responseMap)

		fmt.Println(responseMap)
		c.AbortWithStatusJSON(resp.StatusCode, responseMap)
		return nil, true
	}

	return resp, false
}

func handleConversationResponse(c *gin.Context, resp *http.Response, request CreateConversationRequest, chatRequirementsToken string) {
	c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

	isMaxTokens := false
	continueParentMessageID := ""
	continueConversationID := ""

	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		readStr, _ := reader.ReadString(' ')

		var webSocketResponse WebSocketResponse
		json.Unmarshal([]byte(readStr), &webSocketResponse)
		wssUrlStr := webSocketResponse.WssUrl

		fmt.Println("WebSocket Url : " + wssUrlStr)

		//wssUrl, _ := url.Parse(wssUrlStr)
		//
		//fmt.Println(wssUrl.RawQuery)

		webSocketSubProtocols := []string{WebSocketProtocols}

		dialer := websocket.DefaultDialer
		wssRequest, err := http.NewRequest("GET", wssUrlStr, nil)
		if err != nil {
			log.Fatal("Error creating request:", err)
		}
		wssRequest.Header.Add("Sec-WebSocket-Protocol", webSocketSubProtocols[0])

		connect, _, err := dialer.Dial(wssUrlStr, http2.Header(wssRequest.Header))
		if err != nil {
			log.Fatal("Error dialing:", err)
		}
		defer connect.Close()

		receiveMsgCount := 0

		for {
			messageType, message, err := connect.ReadMessage()

			if err != nil {
				log.Println("Error reading message:", err)
				break
			}

			switch messageType {
			case websocket.TextMessage:
				log.Printf("Received Text Message: %s", message)
				var wssConversationResponse WebSocketMessageResponse
				json.Unmarshal(message, &wssConversationResponse)

				sequenceId := wssConversationResponse.SequenceId

				sequenceMsg := WSSSequenceAckMessage{
					Type:       "sequenceAck",
					SequenceId: sequenceId,
				}
				sequenceMsgStr, err := json.Marshal(sequenceMsg)

				base64Body := wssConversationResponse.Data.Body
				bodyByte, err := base64.StdEncoding.DecodeString(base64Body)

				if err != nil {
					return
				}
				body := string(bodyByte[:])

				if len(body) > 0 {
					c.Writer.Write([]byte(body))
					c.Writer.Flush()
				}

				if strings.Contains(body[:], "[DONE]") {
					connect.WriteMessage(websocket.TextMessage, sequenceMsgStr)
					connect.Close()
					return
				}

				receiveMsgCount++

				if receiveMsgCount > 10 {
					connect.WriteMessage(websocket.TextMessage, sequenceMsgStr)
				}

			case websocket.BinaryMessage:
				log.Printf("Received Binary Message: %d bytes", len(message))
			default:
				log.Printf("Received Other Message Type: %d", messageType)
			}
		}

	} else {
		for {
			if c.Request.Context().Err() != nil {
				break
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}

			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "event") ||
				strings.HasPrefix(line, "data: 20") ||
				line == "" {
				continue
			}

			responseJson := line[6:]
			if strings.HasPrefix(responseJson, "[DONE]") && isMaxTokens {
				continue
			}

			// no need to unmarshal every time, but if response content has this "max_tokens", need to further check
			if strings.TrimSpace(responseJson) != "" && strings.Contains(responseJson, responseTypeMaxTokens) {
				var createConversationResponse CreateConversationResponse
				json.Unmarshal([]byte(responseJson), &createConversationResponse)
				message := createConversationResponse.Message
				if message.Metadata.FinishDetails.Type == responseTypeMaxTokens && createConversationResponse.Message.Status == responseStatusFinishedSuccessfully {
					isMaxTokens = true
					continueParentMessageID = message.ID
					continueConversationID = createConversationResponse.ConversationID
				}
			}

			c.Writer.Write([]byte(line + "\n\n"))
			c.Writer.Flush()
		}
	}

	if isMaxTokens {
		var continueConversationRequest = CreateConversationRequest{
			ArkoseToken:                request.ArkoseToken,
			HistoryAndTrainingDisabled: request.HistoryAndTrainingDisabled,
			Model:                      request.Model,
			TimezoneOffsetMin:          request.TimezoneOffsetMin,

			Action:          actionContinue,
			ParentMessageID: continueParentMessageID,
			ConversationID:  &continueConversationID,
		}
		resp, done := sendConversationRequest(c, continueConversationRequest, chatRequirementsToken)
		if done {
			return
		}

		handleConversationResponse(c, resp, continueConversationRequest, chatRequirementsToken)
	}
}

func GenerateTitle(c *gin.Context) {
	var request GenerateTitleRequest
	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	jsonBytes, _ := json.Marshal(request)
	handlePost(c, ApiPrefix+"/conversation/gen_title/"+c.Param("id"), string(jsonBytes), generateTitleErrorMessage)
}

func GetConversation(c *gin.Context) {
	handleGet(c, ApiPrefix+"/conversation/"+c.Param("id"), getContentErrorMessage)
}

func UpdateConversation(c *gin.Context) {
	var request PatchConversationRequest
	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	// bool default to false, then will hide (delete) the conversation
	if request.Title != nil {
		request.IsVisible = true
	}
	jsonBytes, _ := json.Marshal(request)
	handlePatch(c, ApiPrefix+"/conversation/"+c.Param("id"), string(jsonBytes), updateConversationErrorMessage)
}

func FeedbackMessage(c *gin.Context) {
	var request FeedbackMessageRequest
	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	jsonBytes, _ := json.Marshal(request)
	handlePost(c, ApiPrefix+"/conversation/message_feedback", string(jsonBytes), feedbackMessageErrorMessage)
}

func ClearConversations(c *gin.Context) {
	jsonBytes, _ := json.Marshal(PatchConversationRequest{
		IsVisible: false,
	})
	handlePatch(c, ApiPrefix+"/conversations", string(jsonBytes), clearConversationsErrorMessage)
}

func GetModels(c *gin.Context) {
	handleGet(c, ApiPrefix+"/models", getModelsErrorMessage)
}

func GetAccountCheck(c *gin.Context) {
	handleGet(c, ApiPrefix+"/accounts/check", getAccountCheckErrorMessage)
}

func handleNoAuthGet(c *gin.Context, url string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage(errorMessage))
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func handleGet(c *gin.Context, url string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatusJSON(resp.StatusCode, api.ReturnMessage(errorMessage))
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func handlePost(c *gin.Context, url string, requestBody string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(requestBody))
	handlePostOrPatch(c, req, errorMessage)
}

func handlePatch(c *gin.Context, url string, requestBody string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(requestBody))
	handlePostOrPatch(c, req, errorMessage)
}

func handlePostOrPatch(c *gin.Context, req *http.Request, errorMessage string) {
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatusJSON(resp.StatusCode, api.ReturnMessage(errorMessage))
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func GetChatRequirementsByGin(c *gin.Context) (*ChatRequirements, error) {

	chatRequirements, err := GetChatRequirementsByAccessToken(api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))

	if err != nil {
		return nil, err
	}

	return chatRequirements, nil
}

func GetChatRequirementsByAccessToken(accessToken string) (*ChatRequirements, error) {
	req, _ := http.NewRequest(http.MethodPost, ApiPrefix+"/sentinel/chat-requirements", bytes.NewBuffer([]byte(`{"conversation_mode_kind":"primary_assistant"}`)))

	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	req.Header.Set("Oai-Language", api.Language)
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID)
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, accessToken)

	res, err := api.Client.Do(req)

	if err != nil {
		return nil, err
	}

	defer res.Body.Close()
	var require ChatRequirements
	err = json.NewDecoder(res.Body).Decode(&require)
	if err != nil {
		return nil, err
	}
	return &require, nil
}

func RenewTokenForRequest(request *CreateConversationRequest) {
	var apiVersion int
	if strings.HasPrefix(request.Model, "gpt-4") {
		apiVersion = 4
	} else {
		apiVersion = 3
	}
	token, err := api.GetArkoseToken(apiVersion)
	if err == nil {
		request.ArkoseToken = token
	} else {
		fmt.Println("Error getting Arkose token: ", err)
	}
}

func Ping(c *gin.Context) {
	handleNoAuthGet(c, conversationLimit, "error")
}
