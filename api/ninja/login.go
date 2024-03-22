package ninja

import (
	"encoding/json"
	"fmt"
	http "github.com/bogdanfinn/fhttp"
	"github.com/leokwsw/go-chatgpt-api/api"
	"io"
	"os"
	"strings"
)

func Login(email string, password string) (string, *Error) {

	ninjaUrl := os.Getenv("NINJA_URL")

	if ninjaUrl != "" {
		ninjaAuthToken := ninjaUrl + "/auth/token"

		formParams := fmt.Sprintf(
			"username=%s&password=%s&option=%s",
			email,
			password,
			"web",
		)

		req, _ := http.NewRequest("POST", ninjaAuthToken, strings.NewReader(formParams))

		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		resp, err := api.Client.Do(req)

		if err != nil {
			return "", NewError(resp.StatusCode, fmt.Sprintf("Error Request: %s", err), "ninja server error")
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println(err)
		}

		var result map[string]interface{}
		err = json.Unmarshal(body, &result)
		if err != nil {
			return "", NewError(500, fmt.Sprintf("Error unmarshalling JSON: %s", err), "server error")
		}

		if _, exists := result["accessToken"]; exists {

			var authTokenResp AuthTokenResponse

			err := json.Unmarshal(body, &authTokenResp)

			if err != nil {
				return "", NewError(500, fmt.Sprintf("Error unmarshalling JSON: %s", err), "server error")
			}

			return authTokenResp.AccessToken, nil
		} else {
			var errorResp ErrorResponse

			err := json.Unmarshal(body, &errorResp)

			if err != nil {
				return "", NewError(500, fmt.Sprintf("Error unmarshalling JSON: %s", err), "server error")
			}

			return "", NewError(errorResp.Code, errorResp.Msg, "ninja error")
		}
	} else {
		return "", NewError(500, "NINJS_URL is not setting", "server error")
	}
}
