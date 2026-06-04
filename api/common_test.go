package api

import (
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestApplyChatGPTBrowserHeadersUsesConfiguredBrowserHints(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, ChatGPTApiUrlPrefix+"/backend-api/f/conversation", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	ApplyChatGPTBrowserHeaders(req, ChatGPTBrowserHeaderOptions{
		Accept:      "text/event-stream",
		ContentType: "application/json",
	})

	if got := req.Header.Get("User-Agent"); got != UserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, UserAgent)
	}
	if got := req.Header.Get("Sec-CH-UA"); got != SecCHUA {
		t.Fatalf("Sec-CH-UA = %q, want %q", got, SecCHUA)
	}
	if got := req.Header.Get("Accept-Language"); got != AcceptLanguage {
		t.Fatalf("Accept-Language = %q, want %q", got, AcceptLanguage)
	}
	if got := req.Header.Get("Sec-Fetch-Site"); got != "same-origin" {
		t.Fatalf("Sec-Fetch-Site = %q, want same-origin", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestApplyChatGPTBrowserHeadersCanOverrideBrowserHints(t *testing.T) {
	oldUserAgent := UserAgent
	oldSecCHUA := SecCHUA
	oldAcceptLanguage := AcceptLanguage
	defer func() {
		UserAgent = oldUserAgent
		SecCHUA = oldSecCHUA
		AcceptLanguage = oldAcceptLanguage
	}()

	UserAgent = "test-user-agent"
	SecCHUA = `"Test Browser";v="1"`
	AcceptLanguage = "zh-CN,zh;q=0.9"

	req, err := http.NewRequest(http.MethodPost, ChatGPTApiUrlPrefix+"/backend-api/f/conversation", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	ApplyChatGPTBrowserHeaders(req, ChatGPTBrowserHeaderOptions{})

	if got := req.Header.Get("User-Agent"); got != UserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, UserAgent)
	}
	if got := req.Header.Get("Sec-CH-UA"); got != SecCHUA {
		t.Fatalf("Sec-CH-UA = %q, want %q", got, SecCHUA)
	}
	if got := req.Header.Get("Accept-Language"); got != AcceptLanguage {
		t.Fatalf("Accept-Language = %q, want %q", got, AcceptLanguage)
	}
}

func TestDescribeAuthorizationForLogClassifiesWithoutRawToken(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		source     string
		customFree string
		wantKind   string
		wantLen    int
	}{
		{
			name:     "missing",
			wantKind: "missing",
		},
		{
			name:     "platform key",
			header:   "Bearer sk-test-secret",
			source:   AuthorizationHeader,
			wantKind: "platform_api_key",
			wantLen:  len("sk-test-secret"),
		},
		{
			name:       "custom free token",
			header:     "Bearer python",
			source:     AuthorizationHeader,
			customFree: "python",
			wantKind:   "custom_free_token",
			wantLen:    len("python"),
		},
		{
			name:     "chatgpt jwt",
			header:   "Bearer eyJhbGciOiJSUzI1NiI.payload.signature",
			source:   XAuthorizationHeader,
			wantKind: "chatgpt_jwt",
			wantLen:  len("eyJhbGciOiJSUzI1NiI.payload.signature"),
		},
		{
			name:     "malformed",
			header:   "Bearer one two",
			source:   AuthorizationHeader,
			wantKind: "malformed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeAuthorizationForLog(tt.header, tt.source, tt.customFree)
			if got.Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.TokenLen != tt.wantLen {
				t.Fatalf("token len = %d, want %d", got.TokenLen, tt.wantLen)
			}
			fields := got.LogFields()
			if strings.Contains(fields, "sk-test-secret") || strings.Contains(fields, "eyJhbGciOiJSUzI1NiI.payload.signature") {
				t.Fatalf("log fields contain raw token: %s", fields)
			}
		})
	}
}
