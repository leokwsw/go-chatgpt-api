package middleware

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/linweiyuan/go-logger/logger"
)

const (
	emptyAccessTokenErrorMessage      = "Please provide a valid access token or api key in 'Authorization' header."
	accessTokenHasExpiredErrorMessage = "The accessToken for account %s has expired."
)

type AccessToken struct {
	HTTPSAPIOpenaiComProfile struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	} `json:"https://api.openai.com/profile"`
	HTTPSAPIOpenaiComAuth struct {
		UserID string `json:"user_id"`
	} `json:"https://api.openai.com/auth"`
	Iss   string   `json:"iss"`
	Sub   string   `json:"sub"`
	Aud   []string `json:"aud"`
	Iat   int      `json:"iat"`
	Exp   int      `json:"exp"`
	Azp   string   `json:"azp"`
	Scope string   `json:"scope"`
}

func Authorization() gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader(api.AuthorizationHeader)
		authSource := api.AuthorizationHeader
		if authorization == "" {
			authorization = c.GetHeader(api.XAuthorizationHeader)
			authSource = api.XAuthorizationHeader
			if authorization != "" {
				c.Request.Header.Set(api.AuthorizationHeader, authorization)
			}
		}
		originalAuthorization := authorization
		if authorization == "" {
			authSource = "missing"
		}

		customFreeToken := os.Getenv("CUSTOM_FREE_TOKEN")
		if customFreeToken == "" {
			customFreeToken = "python"
		}

		customFreeTokenSeen := authorization == "Bearer "+customFreeToken || authorization == customFreeToken
		if authorization == "Bearer "+customFreeToken {
			authorization = ""
		}

		if authorization == "" {
			if strings.EqualFold(c.Request.Method, "POST") && (strings.EqualFold(c.Request.URL.Path, "/chatgpt/conversation") ||
				strings.EqualFold(c.Request.URL.Path, "/chatgpt/backend-api/conversation") ||
				strings.EqualFold(c.Request.URL.Path, "/api/conversation") ||
				strings.EqualFold(c.Request.URL.Path, "/imitate/v1/chat/completions")) {
				c.Header("Content-Type", "application/json")
			} else if c.Request.URL.Path == "/" {
				c.Header("Content-Type", "text/plain")
			} else if strings.HasSuffix(c.Request.URL.Path, "/login") ||
				strings.HasPrefix(c.Request.URL.Path, "/chatgpt/public-api") ||
				(strings.HasPrefix(c.Request.URL.Path, "/imitate") && os.Getenv("IMITATE_ACCESS_TOKEN") != "") ||
				strings.HasPrefix(c.Request.URL.Path, "/chatgpt/ping") {
				c.Header("Content-Type", "application/json")
			} else if c.Request.URL.Path == "/favicon.ico" {
				c.Abort()
				return
			} else {
				logAuthorizationReject(c, originalAuthorization, authSource, customFreeToken, customFreeTokenSeen, "missing_or_blocked_authorization")
				c.AbortWithStatusJSON(http.StatusUnauthorized, api.ReturnMessage(emptyAccessTokenErrorMessage))
				return
			}

			c.Next()
		} else {
			if expired := isExpired(c); expired {
				logAuthorizationReject(c, originalAuthorization, authSource, customFreeToken, customFreeTokenSeen, "expired_access_token")
				c.AbortWithStatusJSON(http.StatusUnauthorized, api.ReturnMessage(fmt.Sprintf(accessTokenHasExpiredErrorMessage, c.GetString(api.EmailKey))))
				return
			}

			c.Set(api.AuthorizationHeader, authorization)
		}
	}
}

func logAuthorizationReject(c *gin.Context, authHeader string, authSource string, customFreeToken string, customFreeTokenSeen bool, reason string) {
	logger.Warn(fmt.Sprintf(
		"[auth] reject stage=middleware method=%s path=%s client_ip=%s status=401 reason=%s custom_free_token_seen=%t %s %s",
		c.Request.Method,
		c.Request.URL.Path,
		c.ClientIP(),
		reason,
		customFreeTokenSeen,
		api.DescribeAuthorizationForLog(authHeader, authSource, customFreeToken).LogFields(),
		api.ImitateAuthConfigLogFields(),
	))
}

func isExpired(c *gin.Context) bool {
	accessToken := c.GetHeader(api.AuthorizationHeader)
	split := strings.Split(accessToken, ".")
	if len(split) == 3 {
		rawDecodedText, _ := base64.RawStdEncoding.DecodeString(split[1])
		var accessToken AccessToken
		json.Unmarshal(rawDecodedText, &accessToken)

		exp := int64(accessToken.Exp)
		expTime := time.Unix(exp, 0)
		now := time.Now()

		return now.After(expTime)
	}

	// apiKey
	return false
}
