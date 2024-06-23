package chatgpt

import (
	http "github.com/bogdanfinn/fhttp"
	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/leokwsw/go-chatgpt-api/api/ninja"
	"github.com/xqdoo00o/OpenAIAuth/auth"
	"os"
)

func Login(c *gin.Context) {
	var loginInfo api.LoginInfo
	if err := c.ShouldBindJSON(&loginInfo); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(api.ParseUserInfoErrorMessage))
		return
	}

	accessToken, _, statusCode, errStr := LoginWithUsernameAndPassword(loginInfo.Username, loginInfo.Password)

	if len(errStr) > 0 {
		c.AbortWithStatusJSON(statusCode, api.ReturnMessage(errStr))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken": accessToken,
		//"puid":        puid,
		//"oaidid":      oaidid,
	})
}

func LoginWithUsernameAndPassword(username string, password string) (string, string, int, string) {
	if os.Getenv("NINJA_URL") != "" {
		authToken, err := ninja.Login(username, password)

		if err != nil {
			return "", "", err.StatusCode, err.Details
		}

		puid := api.GetPUID(authToken)

		return authToken, puid, 200, ""
	} else {
		authenticator := auth.NewAuthenticator(username, password, api.ProxyUrl)
		if err := authenticator.Begin(); err != nil {
			return "", "", err.StatusCode, err.Details
		}

		puid := api.GetPUID(authenticator.GetAccessToken())

		return authenticator.GetAccessToken(), puid, 200, ""
	}
}
