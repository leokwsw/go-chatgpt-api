package imitate

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"
)

func TestResolveImitateAccessTokenMapsConfiguredAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("IMITATE_API_KEY", "client-key")
	t.Setenv("IMITATE_ACCESS_TOKEN", "chatgpt-access-token")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	req.Header.Set(api.AuthorizationHeader, "Bearer client-key")
	c.Request = req

	got, apiErr := resolveImitateAccessToken(c)
	if apiErr != nil {
		t.Fatalf("resolveImitateAccessToken() error = %+v", apiErr)
	}
	if got != "chatgpt-access-token" {
		t.Fatalf("access token = %q, want chatgpt-access-token", got)
	}
}

func TestResolveImitateAccessTokenRejectsPlatformAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("IMITATE_API_KEY", "client-key")
	t.Setenv("IMITATE_ACCESS_TOKEN", "chatgpt-access-token")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	req.Header.Set(api.AuthorizationHeader, "Bearer sk-test")
	c.Request = req

	_, apiErr := resolveImitateAccessToken(c)
	if apiErr == nil {
		t.Fatal("resolveImitateAccessToken() error = nil, want rejection")
	}
	if apiErr.Status != 401 || apiErr.Code != "platform_api_key_not_allowed" {
		t.Fatalf("error = %+v, want 401 platform_api_key_not_allowed", apiErr)
	}
}

func TestResolveImitateAccessTokenRejectsMissingAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	c.Request = req

	_, apiErr := resolveImitateAccessToken(c)
	if apiErr == nil {
		t.Fatal("resolveImitateAccessToken() error = nil, want rejection")
	}
	if apiErr.Status != 401 || apiErr.Code != "missing_authorization" {
		t.Fatalf("error = %+v, want 401 missing_authorization", apiErr)
	}
}

func TestResolveImitateAccessTokenReportsMissingBackendToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("IMITATE_API_KEY", "client-key")
	t.Setenv("IMITATE_ACCESS_TOKEN", "")
	oldRuntimeToken := api.IMITATE_accessToken
	api.IMITATE_accessToken = ""
	t.Cleanup(func() {
		api.IMITATE_accessToken = oldRuntimeToken
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	req.Header.Set(api.AuthorizationHeader, "Bearer client-key")
	c.Request = req

	_, apiErr := resolveImitateAccessToken(c)
	if apiErr == nil {
		t.Fatal("resolveImitateAccessToken() error = nil, want backend token error")
	}
	if apiErr.Status != 500 || apiErr.Code != "imitate_access_token_missing" {
		t.Fatalf("error = %+v, want 500 imitate_access_token_missing", apiErr)
	}
}

func TestResolveImitateAccessTokenFallsBackToRuntimeAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("IMITATE_API_KEY", "client-key")
	t.Setenv("IMITATE_ACCESS_TOKEN", "")
	oldRuntimeToken := api.IMITATE_accessToken
	api.IMITATE_accessToken = "runtime-chatgpt-access-token"
	t.Cleanup(func() {
		api.IMITATE_accessToken = oldRuntimeToken
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	req.Header.Set(api.XAuthorizationHeader, "Bearer client-key")
	c.Request = req

	got, apiErr := resolveImitateAccessToken(c)
	if apiErr != nil {
		t.Fatalf("resolveImitateAccessToken() error = %+v", apiErr)
	}
	if got != "runtime-chatgpt-access-token" {
		t.Fatalf("access token = %q, want runtime-chatgpt-access-token", got)
	}
}

func TestResolveImitateAccessTokenRejectsCustomFreeToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CUSTOM_FREE_TOKEN", "free-token")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	req.Header.Set(api.AuthorizationHeader, "Bearer free-token")
	c.Request = req

	_, apiErr := resolveImitateAccessToken(c)
	if apiErr == nil {
		t.Fatal("resolveImitateAccessToken() error = nil, want rejection")
	}
	if apiErr.Status != 401 || apiErr.Code != "custom_free_token_not_allowed" {
		t.Fatalf("error = %+v, want 401 custom_free_token_not_allowed", apiErr)
	}
}

func TestResolveImitateAccessTokenMapsCustomFreeTokenForChatCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CUSTOM_FREE_TOKEN", "free-token")
	t.Setenv("IMITATE_ACCESS_TOKEN", "chatgpt-access-token")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/chat/completions", nil)
	req.Header.Set(api.AuthorizationHeader, "Bearer free-token")
	c.Request = req

	got, apiErr := resolveImitateAccessToken(c)
	if apiErr != nil {
		t.Fatalf("resolveImitateAccessToken() error = %+v", apiErr)
	}
	if got != "chatgpt-access-token" {
		t.Fatalf("access token = %q, want chatgpt-access-token", got)
	}
}

func TestResolveImitateAccessTokenAcceptsDirectChatGPTJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CUSTOM_FREE_TOKEN", "free-token")
	token := "eyJhbGciOiJSUzI1NiI.payload.signature"

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/imitate/v1/files", nil)
	req.Header.Set(api.AuthorizationHeader, "Bearer "+token)
	c.Request = req

	got, apiErr := resolveImitateAccessToken(c)
	if apiErr != nil {
		t.Fatalf("resolveImitateAccessToken() error = %+v", apiErr)
	}
	if got != token {
		t.Fatalf("access token = %q, want %q", got, token)
	}
}
