package imitate

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/leokwsw/go-chatgpt-api/api/chatgpt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/linweiyuan/go-logger/logger"
)

var (
	reg   *regexp.Regexp
	token string
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

	authHeader := c.GetHeader(api.AuthorizationHeader)
	imitateToken := os.Getenv("IMITATE_API_KEY")
	if authHeader != "" {
		customAccessToken := strings.Replace(authHeader, "Bearer ", "", 1)
		// Check if customAccessToken starts with sk-
		if strings.HasPrefix(customAccessToken, "eyJhbGciOiJSUzI1NiI") {
			token = customAccessToken
			// use defiend access token if the provided api key is equal to "IMITATE_API_KEY"
		} else if imitateToken != "" && customAccessToken == imitateToken {
			token = os.Getenv("IMITATE_ACCESS_TOKEN")
			if token == "" {
				token = api.IMITATE_accessToken
			}
		}
	}

	if token == "" {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "API KEY is missing or invalid",
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    "400",
		}})
		return
	}

	uid := uuid.NewString()
	var chatRequirements *chatgpt.ChatRequirements
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		err = chatgpt.InitWebSocketConnect(token, uid)
	}()
	go func() {
		defer waitGroup.Done()
		chatRequirements, err = chatgpt.GetChatRequirementsByAccessToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
			return
		}
	}()
	waitGroup.Wait()
	if err != nil {
		c.JSON(500, gin.H{"error": "unable to create ws tunnel"})
		return
	}
	if chatRequirements == nil {
		c.JSON(500, gin.H{"error": "unable to check chat requirement"})
		return
	}

	// 将聊天请求转换为ChatGPT请求。
	translatedRequest := convertAPIRequest(originalRequest, chatRequirements.Arkose.Required)

	response, done := sendConversationRequest(c, translatedRequest, token, chatRequirements.Token)
	if done {
		c.JSON(500, gin.H{
			"error": "error sending request",
		})
		return
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(response.Body)

	if HandleRequestError(c, response) {
		return
	}

	var fullResponse string

	for i := 3; i > 0; i-- {
		var continueInfo *ContinueInfo
		var responsePart string
		responsePart, continueInfo = Handler(c, response, token, uid, originalRequest.Stream)
		fullResponse += responsePart
		if continueInfo == nil {
			break
		}
		println("Continuing conversation")
		translatedRequest.Messages = nil
		translatedRequest.Action = "continue"
		translatedRequest.ConversationID = &continueInfo.ConversationID
		translatedRequest.ParentMessageID = continueInfo.ParentID
		if chatRequirements.Arkose.Required {
			chatgpt.RenewTokenForRequest(&translatedRequest)
		}
		response, done = sendConversationRequest(c, translatedRequest, token, chatRequirements.Token)

		if done {
			c.JSON(500, gin.H{
				"error": "error sending request",
			})
			return
		}

		// 以下修复代码来自ChatGPT
		// 在循环内部创建一个局部作用域，并将资源的引用传递给匿名函数，保证资源将在每次迭代结束时被正确释放
		func() {
			defer func(Body io.ReadCloser) {
				err := Body.Close()
				if err != nil {
					return
				}
			}(response.Body)
		}()

		if HandleRequestError(c, response) {
			return
		}
	}

	if c.Writer.Status() != 200 {
		c.JSON(500, gin.H{
			"error": "error sending request",
		})
		return
	}
	if !originalRequest.Stream {
		c.JSON(200, newChatCompletion(fullResponse, translatedRequest.Model, uid))
	} else {
		c.String(200, "data: [DONE]\n\n")
	}
}

func generateId() string {
	id := uuid.NewString()
	id = strings.ReplaceAll(id, "-", "")
	id = base64.StdEncoding.EncodeToString([]byte(id))
	id = reg.ReplaceAllString(id, "")
	return "chatcmpl-" + id
}

func convertAPIRequest(apiRequest APIRequest, chatRequirementsArkoseRequired bool) chatgpt.CreateConversationRequest {
	chatgptRequest := NewChatGPTRequest()

	var apiVersion int
	if strings.HasPrefix(apiRequest.Model, "gpt-3.5") {
		apiVersion = 3
		chatgptRequest.Model = "text-davinci-002-render-sha"
	} else if strings.HasPrefix(apiRequest.Model, "gpt-4") {
		apiVersion = 4
		chatgptRequest.Model = apiRequest.Model
		// Cover some models like gpt-4-32k
		if len(apiRequest.Model) >= 7 && apiRequest.Model[6] >= 48 && apiRequest.Model[6] <= 57 {
			chatgptRequest.Model = "gpt-4"
		}
	}

	if chatRequirementsArkoseRequired {
		token, err := api.GetArkoseToken(apiVersion)
		if err == nil {
			chatgptRequest.ArkoseToken = token
		} else {
			fmt.Println("Error getting Arkose token: ", err)
		}
	}

	if apiRequest.PluginIDs != nil {
		chatgptRequest.PluginIDs = apiRequest.PluginIDs
		chatgptRequest.Model = "gpt-4-plugins"
	}

	for _, apiMessage := range apiRequest.Messages {
		if apiMessage.Role == "system" {
			apiMessage.Role = "critic"
		}
		if apiMessage.Metadata == nil {
			apiMessage.Metadata = map[string]string{}
		}
		chatgptRequest.AddMessage(apiMessage.Role, apiMessage.Content, apiMessage.Metadata)
	}

	if chatgptRequest.ConversationMode.Kind == "" {
		chatgptRequest.ConversationMode.Kind = "primary_assistant"
	}

	return chatgptRequest
}

func NewChatGPTRequest() chatgpt.CreateConversationRequest {
	enableHistory := os.Getenv("ENABLE_HISTORY") == ""
	return chatgpt.CreateConversationRequest{
		Action:                     "next",
		ParentMessageID:            uuid.NewString(),
		Model:                      "text-davinci-002-render-sha",
		HistoryAndTrainingDisabled: !enableHistory,
	}
}

func sendConversationRequest(c *gin.Context, request chatgpt.CreateConversationRequest, accessToken string, chatRequirementsToken string) (*http.Response, bool) {
	jsonBytes, _ := json.Marshal(request)
	req, _ := http.NewRequest(http.MethodPost, api.ChatGPTApiUrlPrefix+"/backend-api/conversation", bytes.NewBuffer(jsonBytes))
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, accessToken)
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
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	req.Header.Set("Oai-Language", api.Language)
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID)
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set("Accept", "*/*")
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

func Handler(c *gin.Context, resp *http.Response, token string, uuid string, stream bool) (string, *ContinueInfo) {
	maxTokens := false

	// Create a bufio.Reader from the resp body
	reader := bufio.NewReader(resp.Body)

	// Read the resp byte by byte until a newline character is encountered
	if stream {
		// Response content type is text/event-stream
		c.Header("Content-Type", "text/event-stream")
	} else {
		// Response content type is application/json
		c.Header("Content-Type", "application/json")
	}
	var finishReason string
	var previousText StringStruct
	var originalResponse ChatGPTResponse
	var isRole = true
	var waitSource = false
	var isEnd = false
	var imgSource []string
	var isWebSocket = false
	var convId string
	var respId string
	var wssUrl string
	var connInfo *api.ConnectInfo
	var wsSeq int
	var isWSInterrupt bool = false
	var interruptTimer *time.Timer
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		isWebSocket = true
		connInfo = chatgpt.FindSpecConnection(token, uuid)
		if connInfo.Connect == nil {
			c.JSON(500, gin.H{"error": "No websocket connection"})
			return "", nil
		}
		var wssResponse chatgpt.WebSocketResponse
		json.NewDecoder(resp.Body).Decode(&wssResponse)
		wssUrl = wssResponse.WssUrl
		respId = wssResponse.ResponseId
		convId = wssResponse.ConversationId
	}
	for {
		var line string
		var err error
		if isWebSocket {
			var messageType int
			var message []byte
			if isWSInterrupt {
				if interruptTimer == nil {
					interruptTimer = time.NewTimer(10 * time.Second)
				}
				select {
				case <-interruptTimer.C:
					c.JSON(500, gin.H{"error": "WS interrupt & new WS timeout"})
					return "", nil
				default:
					goto reader
				}
			}
		reader:
			messageType, message, err = connInfo.Connect.ReadMessage()
			if err != nil {
				connInfo.Ticker.Stop()
				connInfo.Connect.Close()
				connInfo.Connect = nil
				err := chatgpt.CreateWebSocketConnection(wssUrl, connInfo, 0)
				if err != nil {
					c.JSON(500, gin.H{"error": err.Error()})
					return "", nil
				}
				isWSInterrupt = true
				connInfo.Connect.WriteMessage(websocket.TextMessage, []byte("{\"type\":\"sequenceAck\",\"sequenceId\":"+strconv.Itoa(wsSeq)+"}"))
				continue
			}
			if messageType == websocket.TextMessage {
				var wssMsgResponse chatgpt.WebSocketMessageResponse
				json.Unmarshal(message, &wssMsgResponse)
				if wssMsgResponse.Data.ResponseId != respId {
					continue
				}
				wsSeq = wssMsgResponse.SequenceId
				if wsSeq%50 == 0 {
					connInfo.Connect.WriteMessage(websocket.TextMessage, []byte("{\"type\":\"sequenceAck\",\"sequenceId\":"+strconv.Itoa(wsSeq)+"}"))
				}
				base64Body := wssMsgResponse.Data.Body
				bodyByte, err := base64.StdEncoding.DecodeString(base64Body)
				if err != nil {
					continue
				}
				if isWSInterrupt {
					isWSInterrupt = false
					interruptTimer.Stop()
				}
				line = string(bodyByte)
			}
		} else {
			line, err = reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				return "", nil
			}
		}
		if len(line) < 6 {
			continue
		}
		// Remove "data: " from the beginning of the line
		line = line[6:]
		// Check if line starts with [DONE]
		if !strings.HasPrefix(line, "[DONE]") {
			// Parse the line as JSON
			err = json.Unmarshal([]byte(line), &originalResponse)
			if err != nil {
				continue
			}
			if originalResponse.Error != nil {
				c.JSON(500, gin.H{"error": originalResponse.Error})
				return "", nil
			}
			if originalResponse.ConversationID != convId {
				if convId == "" {
					convId = originalResponse.ConversationID
				} else {
					continue
				}
			}
			if !(originalResponse.Message.Author.Role == "assistant" || (originalResponse.Message.Author.Role == "tool" && originalResponse.Message.Content.ContentType != "text")) || originalResponse.Message.Content.Parts == nil {
				continue
			}
			if originalResponse.Message.Metadata.MessageType != "next" && originalResponse.Message.Metadata.MessageType != "continue" || !strings.HasSuffix(originalResponse.Message.Content.ContentType, "text") {
				continue
			}
			if originalResponse.Message.EndTurn != nil {
				if waitSource {
					waitSource = false
				}
				isEnd = true
			}
			if len(originalResponse.Message.Metadata.Citations) != 0 {
				r := []rune(originalResponse.Message.Content.Parts[0].(string))
				if waitSource {
					if string(r[len(r)-1:]) == "】" {
						waitSource = false
					} else {
						continue
					}
				}
				offset := 0
				for i, citation := range originalResponse.Message.Metadata.Citations {
					rl := len(r)
					originalResponse.Message.Content.Parts[0] = string(r[:citation.StartIx+offset]) + "[^" + strconv.Itoa(i+1) + "^](" + citation.Metadata.URL + " \"" + citation.Metadata.Title + "\")" + string(r[citation.EndIx+offset:])
					r = []rune(originalResponse.Message.Content.Parts[0].(string))
					offset += len(r) - rl
				}
			} else if waitSource {
				continue
			}
			responseString := ""
			if originalResponse.Message.Recipient != "all" {
				continue
			}
			if originalResponse.Message.Content.ContentType == "multimodal_text" {
				apiUrl := chatgpt.ApiPrefix + "/files/"
				FilesReverseProxy := os.Getenv("FILES_REVERSE_PROXY")
				if FilesReverseProxy != "" {
					apiUrl = FilesReverseProxy
				}
				imgSource = make([]string, len(originalResponse.Message.Content.Parts))
				var waitGroup sync.WaitGroup
				for index, part := range originalResponse.Message.Content.Parts {
					jsonItem, _ := json.Marshal(part)
					var dalleContent chatgpt.DallEContent
					err = json.Unmarshal(jsonItem, &dalleContent)
					if err != nil {
						continue
					}
					url := apiUrl + strings.Split(dalleContent.AssetPointer, "//")[1] + "/download"
					waitGroup.Add(1)
					go GetImageSource(&waitGroup, url, dalleContent.Metadata.Dalle.Prompt, token, index, imgSource)
				}
				waitGroup.Wait()
				translatedResponse := NewChatCompletionChunk(strings.Join(imgSource, ""))
				if isRole {
					translatedResponse.Choices[0].Delta.Role = originalResponse.Message.Author.Role
				}
				responseString = "data: " + translatedResponse.String() + "\n\n"
			}
			if responseString == "" {
				responseString = ConvertToString(&originalResponse, &previousText, isRole)
			}
			if responseString == "" {
				if isEnd {
					goto endProcess
				} else {
					continue
				}
			}
			if responseString == "【" {
				waitSource = true
				continue
			}
			isRole = false
			if stream {
				_, err = c.Writer.WriteString(responseString)
				if err != nil {
					return "", nil
				}
			}
		endProcess:
			// Flush the resp writer buffer to ensure that the client receives each line as it's written
			c.Writer.Flush()

			if originalResponse.Message.Metadata.FinishDetails != nil {
				if originalResponse.Message.Metadata.FinishDetails.Type == "max_tokens" {
					maxTokens = true
				}
				finishReason = originalResponse.Message.Metadata.FinishDetails.Type
			}
			if isEnd {
				if stream {
					final_line := StopChunk(finishReason)
					c.Writer.WriteString("data: " + final_line.String() + "\n\n")
				}
				break
			}
		}
	}
	if !maxTokens {
		return strings.Join(imgSource, "") + previousText.Text, nil
	}
	return strings.Join(imgSource, "") + previousText.Text, &ContinueInfo{
		ConversationID: originalResponse.ConversationID,
		ParentID:       originalResponse.Message.ID,
	}
}
