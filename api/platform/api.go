package platform

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/linweiyuan/go-logger/logger"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"

	http "github.com/bogdanfinn/fhttp"
)

func ListModels(c *gin.Context) {
	handleGet(c, apiListModels)
}

func RetrieveModel(c *gin.Context) {
	model := c.Param("model")
	handleGet(c, fmt.Sprintf(apiRetrieveModel, model))
}

func CreateCompletions(c *gin.Context) {
	CreateChatCompletions(c)
}

func CreateChatCompletions(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	var request struct {
		Stream bool `json:"stream"`
	}
	json.Unmarshal(body, &request)

	url := c.Request.URL.Path
	if strings.Contains(url, "/chat") {
		url = apiCreataeChatCompletions
	} else {
		url = apiCreateCompletions
	}

	resp, err := handlePost(c, url, body, request.Stream)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		logger.Error(fmt.Sprintf(api.AccountDeactivatedErrorMessage, c.GetString(api.EmailKey)))
		responseMap := make(map[string]interface{})
		json.NewDecoder(resp.Body).Decode(&responseMap)
		c.AbortWithStatusJSON(resp.StatusCode, responseMap)
		return
	}

	if request.Stream {
		handleCompletionsResponse(c, resp)
	} else {
		io.Copy(c.Writer, resp.Body)
	}
}

func handleCompletionsResponse(c *gin.Context, resp *http.Response) {
	c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

	reader := bufio.NewReader(resp.Body)
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

		c.Writer.Write([]byte(line + "\n\n"))
		c.Writer.Flush()
	}
}

func CreateEdit(c *gin.Context) {
	var request CreateEditRequest
	c.ShouldBindJSON(&request)
	data, _ := json.Marshal(request)
	resp, err := handlePost(c, apiCreateEdit, data, false)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func CreateImage(c *gin.Context) {
	var request CreateImageRequest
	c.ShouldBindJSON(&request)
	data, _ := json.Marshal(request)
	resp, err := handlePost(c, apiCreateImage, data, false)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func CreateEmbeddings(c *gin.Context) {
	var request CreateEmbeddingsRequest
	c.ShouldBindJSON(&request)
	data, _ := json.Marshal(request)
	resp, err := handlePost(c, apiCreateEmbeddings, data, false)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func CreateModeration(c *gin.Context) {
	var request CreateModerationRequest
	c.ShouldBindJSON(&request)
	data, _ := json.Marshal(request)
	resp, err := handlePost(c, apiCreateModeration, data, false)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func CreateTranscriptions(c *gin.Context) {
	var request CreateAudioTranscriptions
	c.Bind(&request)
	//data, _ := json.Marshal(request)
	resp, err := handlePost(c, apiCreateAudioTranscriptions, []byte(fmt.Sprintf("%v", request)), false)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func CreateSpeech(c *gin.Context) {
	var request CreateAudioSpeech
	c.ShouldBindJSON(&request)
	data, _ := json.Marshal(request)
	resp, err := handlePost(c, apiCreateAudioSpeech, data, false)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func ListFiles(c *gin.Context) {
	handleGet(c, apiListFiles)
}

func GetCreditGrants(c *gin.Context) {
	handleGet(c, apiGetCreditGrants)
}

func GetSubscription(c *gin.Context) {
	handleGet(c, apiGetSubscription)
}

func GetApiKeys(c *gin.Context) {
	handleGet(c, apiGetApiKeys)
}

func handleGet(c *gin.Context, url string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	resp, _ := api.Client.Do(req)
	defer resp.Body.Close()
	io.Copy(c.Writer, resp.Body)
}

func handlePost(c *gin.Context, url string, data []byte, stream bool) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return nil, err
	}

	return resp, nil
}
