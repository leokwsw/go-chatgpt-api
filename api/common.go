package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/xqdoo00o/OpenAIAuth/auth"
	"github.com/xqdoo00o/funcaptcha"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/gin-gonic/gin"
	_ "github.com/linweiyuan/go-chatgpt-api/env"
	"github.com/linweiyuan/go-logger/logger"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

const (
	ChatGPTApiPrefix    = "/chatgpt"
	ChatGPTApiUrlPrefix = "https://chat.openai.com"

	PlatformApiPrefix    = "/platform"
	PlatformApiUrlPrefix = "https://api.openai.com"

	defaultErrorMessageKey             = "errorMessage"
	AuthorizationHeader                = "Authorization"
	XAuthorizationHeader               = "X-Authorization"
	ContentType                        = "application/x-www-form-urlencoded"
	UserAgent                          = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
	Auth0Url                           = "https://auth0.openai.com"
	LoginUsernameUrl                   = Auth0Url + "/u/login/identifier?state="
	LoginPasswordUrl                   = Auth0Url + "/u/login/password?state="
	ParseUserInfoErrorMessage          = "Failed to parse user login info."
	GetAuthorizedUrlErrorMessage       = "Failed to get authorized url."
	EmailInvalidErrorMessage           = "Email is not valid."
	EmailOrPasswordInvalidErrorMessage = "Email or password is not correct."
	GetAccessTokenErrorMessage         = "Failed to get access token."
	defaultTimeoutSeconds              = 600 // 10 minutes

	ReadyHint  = "Service go-chatgpt-api is ready."
	RobotsHint = "User-agent: *\nDisallow: /"

	AccountDeactivatedErrorMessage = "Account %s is deactivated."
	EmailKey                       = "email"

	refreshPuidErrorMessage   = "failed to refresh PUID"
	refreshOaididErrorMessage = "failed to refresh oai-did"

	Language = "en-US"
)

type ConnectInfo struct {
	Connect *websocket.Conn
	Uuid    string
	Expire  time.Time
	Ticker  *time.Ticker
	Lock    bool
}

var (
	Client              tls_client.HttpClient
	ArkoseClient        tls_client.HttpClient
	PUID                string
	OAIDID              string
	ProxyUrl            string
	IMITATE_accessToken string
	ConnectPool         = map[string][]*ConnectInfo{}
)

type LoginInfo struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthLogin interface {
	GetAuthorizedUrl(csrfToken string) (string, int, error)
	GetState(authorizedUrl string) (string, int, error)
	CheckUsername(state string, username string) (int, error)
	CheckPassword(state string, username string, password string) (string, int, error)
	GetAccessToken(code string) (string, int, error)
}

func init() {
	Client, _ = tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
		tls_client.WithTimeoutSeconds(defaultTimeoutSeconds),
		tls_client.WithClientProfile(profiles.Okhttp4Android13),
	}...)
	ArkoseClient = getHttpClient()

	setupID()
}

func NewHttpClient() tls_client.HttpClient {
	client := getHttpClient()

	ProxyUrl = os.Getenv("PROXY")
	if ProxyUrl != "" {
		client.SetProxy(ProxyUrl)
	}

	return client
}

func getHttpClient() tls_client.HttpClient {
	client, _ := tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
		tls_client.WithClientProfile(profiles.Okhttp4Android13),
	}...)
	return client
}

func Proxy(c *gin.Context) {
	url := c.Request.URL.Path
	if strings.Contains(url, ChatGPTApiPrefix) {
		url = strings.ReplaceAll(url, ChatGPTApiPrefix, ChatGPTApiUrlPrefix)
	} else {
		url = strings.ReplaceAll(url, PlatformApiPrefix, PlatformApiUrlPrefix)
	}

	method := c.Request.Method
	queryParams := c.Request.URL.Query().Encode()
	if queryParams != "" {
		url += "?" + queryParams
	}

	// if not set, will return 404
	c.Status(http.StatusOK)

	var req *http.Request
	if method == http.MethodGet {
		req, _ = http.NewRequest(http.MethodGet, url, nil)
	} else {
		body, _ := io.ReadAll(c.Request.Body)
		req, _ = http.NewRequest(method, url, bytes.NewReader(body))
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set(AuthorizationHeader, GetAccessToken(c.GetHeader(AuthorizationHeader)))
	resp, err := Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseMap := make(map[string]interface{})
		json.NewDecoder(resp.Body).Decode(&responseMap)
		c.AbortWithStatusJSON(resp.StatusCode, responseMap)
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func ReturnMessage(msg string) gin.H {
	return gin.H{
		defaultErrorMessageKey: msg,
	}
}

func GetAccessToken(accessToken string) string {
	if !strings.HasPrefix(accessToken, "Bearer") {
		return "Bearer " + accessToken
	}
	return accessToken
}

func GetArkoseToken(api_version int) (string, error) {
	return funcaptcha.GetOpenAIToken(api_version, PUID, ProxyUrl)
}

func setupID() {
	username := os.Getenv("OPENAI_EMAIL")
	password := os.Getenv("OPENAI_PASSWORD")
	refreshtoken := os.Getenv("OPENAI_REFRESH_TOKEN")
	OAIDID = os.Getenv("OPENAI_DEVICE_ID")
	if username != "" && password != "" {
		go func() {
			for {

				// import cycle not allowed
				//accessToken, puid, oaidid, _, errStr := chatgpt.LoginWithUsernameAndPassword(username, password)
				//
				//if len(errStr) > 0 {
				//	logger.Warn(fmt.Sprintf("%s: %s", refreshPuidErrorMessage, errStr))
				//	return
				//}
				//
				//PUID = puid
				//OAIDID = oaidid
				//IMITATE_accessToken = accessToken

				authenticator := auth.NewAuthenticator(username, password, ProxyUrl)
				if err := authenticator.Begin(); err != nil {
					//if err.Details == "missing access token" {
					//	accessToken, err := ninja.Login(username, password)
					//
					//	if err != nil {
					//		logger.Warn(fmt.Sprintf("%s: %s", refreshPuidErrorMessage, err.Details))
					//		return
					//	}
					//
					//	puid, oaidid := GetIDs(accessToken)
					//
					//	if puid == "" {
					//		logger.Error(refreshPuidErrorMessage)
					//		return
					//	} else {
					//		PUID = puid
					//		logger.Info(fmt.Sprintf("PUID is updated"))
					//	}
					//
					//	if oaidid == "" {
					//		logger.Warn(refreshOaididErrorMessage)
					//		//return
					//	} else {
					//		OAIDID = oaidid
					//		logger.Info(fmt.Sprintf("OAIDID is updated"))
					//	}
					//
					//	// store IMITATE_accessToken
					//	IMITATE_accessToken = accessToken
					//
					//	time.Sleep(time.Hour * 24 * 7)
					//
					//} else {
					logger.Warn(fmt.Sprintf("%s: %s", refreshPuidErrorMessage, err.Details))
					return
					//}

				}

				accessToken := authenticator.GetAccessToken()
				if accessToken == "" {
					logger.Error(refreshPuidErrorMessage)
					return
				}

				puid, err := authenticator.GetPUID()
				if err != nil {
					logger.Error(refreshPuidErrorMessage)
					return
				}

				PUID = puid

				// store IMITATE_accessToken
				IMITATE_accessToken = accessToken

				time.Sleep(time.Hour * 24 * 7)
			}
		}()
	} else if refreshtoken != "" {
		go func() {
			for {
				accessToken := RefreshAccessToken(refreshtoken)
				if accessToken == "" {
					logger.Error(refreshPuidErrorMessage)
					return
				} else {
					logger.Info(fmt.Sprintf("accessToken is updated"))
				}

				puid, oaidid := GetIDs(accessToken)
				if puid == "" {
					logger.Error(refreshPuidErrorMessage)
					return
				} else {
					PUID = puid
					logger.Info(fmt.Sprintf("PUID is updated"))
				}

				if oaidid == "" {
					logger.Warn(refreshOaididErrorMessage)
					//return
				} else {
					OAIDID = oaidid
					logger.Info(fmt.Sprintf("OAIDID is updated"))
				}

				// store IMITATE_accessToken
				IMITATE_accessToken = accessToken

				time.Sleep(time.Hour * 24 * 7)
			}
		}()
	} else {
		PUID = os.Getenv("PUID")
		IMITATE_accessToken = os.Getenv("IMITATE_ACCESS_TOKEN")
	}
}

func RefreshAccessToken(refreshToken string) string {
	data := map[string]interface{}{
		"redirect_uri":  "com.openai.chat://auth0.openai.com/ios/com.openai.chat/callback",
		"grant_type":    "refresh_token",
		"client_id":     "pdlLIX2Y72MIl2rhLhTE9VV9bN905kBh",
		"refresh_token": refreshToken,
	}
	jsonData, err := json.Marshal(data)

	if err != nil {
		logger.Error(fmt.Sprintf("Failed to marshal data: %v", err))
	}

	req, err := http.NewRequest(http.MethodPost, "https://auth0.openai.com/oauth/token", bytes.NewBuffer(jsonData))
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "application/json")
	resp, err := NewHttpClient().Do(req)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to refresh token: %v", err))
		return ""
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Error(fmt.Sprintf("Server responded with status code: %d", resp.StatusCode))
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error(fmt.Sprintf("Failed to decode json: %v", err))
		return ""
	}
	// Check if access token in data
	if _, ok := result["access_token"]; !ok {
		logger.Error(fmt.Sprintf("missing access token: %v", result))
		return ""
	}
	return result["access_token"].(string)
}

func GetIDs(accessToken string) (string, string) {
	var puid string
	var oaidid string
	// Check if user has access token
	if accessToken == "" {
		logger.Error("GetIDs: Missing access token")
		return "", ""
	}
	// Make request to https://chat.openai.com/backend-api/models
	req, _ := http.NewRequest("GET", "https://chat.openai.com/backend-api/models?history_and_training_disabled=false", nil)
	// Add headers
	req.Header.Add(AuthorizationHeader, GetAccessToken(accessToken))
	req.Header.Add("User-Agent", UserAgent)

	resp, err := NewHttpClient().Do(req)
	if err != nil {
		logger.Error("GetIDs: Missing access token")
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		logger.Error(fmt.Sprintf("GetIDs: Server responded with status code: %d", resp.StatusCode))
		return "", ""
	}
	// Find `_puid` cookie in response
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "_puid" {
			puid = cookie.Value
			break
		}
	}
	// Find `oai-did` cookie in response
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "oai-did" {
			oaidid = cookie.Value
			break
		}
	}
	if puid == "" {
		logger.Error("GetIDs: PUID cookie not found")
	}
	if oaidid == "" {
		logger.Warn("GetIDs: OAI-DId cookie not found")
	}
	return puid, oaidid
}
