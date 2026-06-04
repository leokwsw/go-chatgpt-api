package chatgpt

import (
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
	"github.com/leokwsw/go-chatgpt-api/api"
)

func TestNewChatRequirementsRequestSetsWebHeaders(t *testing.T) {
	oldOAIDID := api.OAIDID
	oldPUID := api.PUID
	defer func() {
		api.OAIDID = oldOAIDID
		api.PUID = oldPUID
	}()
	api.OAIDID = "test-device-id"
	api.PUID = "test-puid"

	req, err := newChatRequirementsRequest("test-token", "test-device-session-id", "test-session-id", "/sentinel/chat-requirements/prepare", []byte("{}"))
	if err != nil {
		t.Fatalf("newChatRequirementsRequest() error = %v", err)
	}

	if got := req.URL.String(); got != ApiPrefix+"/sentinel/chat-requirements/prepare" {
		t.Fatalf("url = %q", got)
	}
	if got := req.Header.Get(api.AuthorizationHeader); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("Oai-Device-Id"); got != "test-device-id" {
		t.Fatalf("Oai-Device-Id = %q", got)
	}
	if got := req.Header.Get("Oai-Session-Id"); got != "test-session-id" {
		t.Fatalf("Oai-Session-Id = %q", got)
	}
	if got := req.Header.Get("Oai-Client-Version"); got != oaiClientVersion {
		t.Fatalf("Oai-Client-Version = %q", got)
	}
	if got := req.Header.Get("Oai-Client-Build-Number"); got != oaiClientBuildNumber {
		t.Fatalf("Oai-Client-Build-Number = %q", got)
	}
	if got := req.Header.Get("X-Openai-Target-Path"); got != "/backend-api/sentinel/chat-requirements/prepare" {
		t.Fatalf("X-Openai-Target-Path = %q", got)
	}
	if got := req.Header.Get("X-Openai-Target-Route"); got != "/backend-api/sentinel/chat-requirements/prepare" {
		t.Fatalf("X-Openai-Target-Route = %q", got)
	}
	if got := req.Header.Get("Cookie"); !strings.Contains(got, "_puid=test-puid;") || !strings.Contains(got, "oai-did=test-device-id;") {
		t.Fatalf("Cookie = %q", got)
	}
}

func TestNewChatRequirementsRequestUsesAnonTargetForMissingToken(t *testing.T) {
	req, err := newChatRequirementsRequest("", "anon-device-id", "anon-session-id", "/sentinel/chat-requirements/prepare", []byte("{}"))
	if err != nil {
		t.Fatalf("newChatRequirementsRequest() error = %v", err)
	}

	if got := req.URL.String(); got != AnonPrefix+"/sentinel/chat-requirements/prepare" {
		t.Fatalf("url = %q", got)
	}
	if got := req.Header.Get(api.AuthorizationHeader); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got := req.Header.Get("Oai-Device-Id"); got != "anon-device-id" {
		t.Fatalf("Oai-Device-Id = %q", got)
	}
	if got := req.Header.Get("Oai-Session-Id"); got != "anon-session-id" {
		t.Fatalf("Oai-Session-Id = %q", got)
	}
	if got := req.Header.Get("X-Openai-Target-Path"); got != "/backend-anon/sentinel/chat-requirements/prepare" {
		t.Fatalf("X-Openai-Target-Path = %q", got)
	}
}

func TestReadChatRequirementsJSONResponseDetectsCloudflareChallenge(t *testing.T) {
	res := &http.Response{
		StatusCode: http.StatusForbidden,
		Header: http.Header{
			"Content-Type": []string{"text/html; charset=UTF-8"},
			"Cf-Ray":       []string{"test-ray"},
		},
		Body: io.NopCloser(strings.NewReader("<html><script src=\"/cdn-cgi/challenge-platform/test\"></script></html>")),
	}

	var out ChatRequirements
	err := readChatRequirementsJSONResponse(res, "/backend-api/sentinel/chat-requirements/prepare", &out)
	if err == nil {
		t.Fatal("readChatRequirementsJSONResponse() error = nil")
	}
	if !strings.Contains(err.Error(), "chat_requirements_blocked_by_cloudflare") {
		t.Fatalf("error = %q, want Cloudflare marker", err.Error())
	}
	if strings.Contains(err.Error(), "<html>") {
		t.Fatalf("error leaked html body: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "ray=test-ray") {
		t.Fatalf("error = %q, want ray id", err.Error())
	}
}

func TestNormalizeNavigatorKeyUsesConfiguredUserAgent(t *testing.T) {
	oldUserAgent := api.UserAgent
	defer func() { api.UserAgent = oldUserAgent }()
	api.UserAgent = "Mozilla/5.0 TestBrowser/1.0"

	if got := normalizeNavigatorKey("userAgent−old"); got != "userAgent−Mozilla/5.0 TestBrowser/1.0" {
		t.Fatalf("userAgent navigator key = %q", got)
	}
	if got := normalizeNavigatorKey("appVersion−old"); got != "appVersion−5.0 TestBrowser/1.0" {
		t.Fatalf("appVersion navigator key = %q", got)
	}
}
