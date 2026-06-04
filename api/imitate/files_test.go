package imitate

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"
)

func TestParseExpiresAfterSecondsAcceptsOfficialFormFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	form := url.Values{
		"expires_after[anchor]":  {"created_at"},
		"expires_after[seconds]": {"3600"},
	}
	req := httptest.NewRequest("POST", "/imitate/v1/files", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req

	got, err := parseExpiresAfterSeconds(c)
	if err != nil {
		t.Fatalf("parseExpiresAfterSeconds() error = %v", err)
	}
	if got == nil || *got != 3600 {
		t.Fatalf("seconds = %v, want 3600", got)
	}
}

func TestParseExpiresAfterSecondsRejectsOutOfRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	form := url.Values{
		"expires_after.anchor":  {"created_at"},
		"expires_after.seconds": {"3599"},
	}
	req := httptest.NewRequest("POST", "/imitate/v1/files", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req

	_, err := parseExpiresAfterSeconds(c)
	if err == nil {
		t.Fatal("parseExpiresAfterSeconds() error = nil, want range error")
	}
	if !strings.Contains(err.Error(), "between 3600 and 2592000") {
		t.Fatalf("error = %q, want range message", err.Error())
	}
}

func TestSupportedOpenAIFilePurposes(t *testing.T) {
	for _, purpose := range []string{"assistants", "batch", "fine-tune", "vision", "user_data", "evals"} {
		if !isSupportedOpenAIFilePurpose(purpose) {
			t.Fatalf("purpose %q rejected", purpose)
		}
	}
	for _, purpose := range []string{"", "assistants_output", "batch_output", "fine-tune-results"} {
		if isSupportedOpenAIFilePurpose(purpose) {
			t.Fatalf("purpose %q accepted, want create-time rejection", purpose)
		}
	}
}

func TestDecodeInlineFileDataSupportsOfficialDataURLAndBareBase64(t *testing.T) {
	payload := []byte("image-bytes")
	encoded := base64.StdEncoding.EncodeToString(payload)

	contentType, got, err := decodeInlineFileData("data:image/png;base64,"+encoded, "")
	if err != nil {
		t.Fatalf("decodeInlineFileData(data URL) error = %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
	if string(got) != string(payload) {
		t.Fatalf("decoded bytes = %q, want %q", string(got), string(payload))
	}

	contentType, got, err = decodeInlineFileData(encoded, "image/jpeg")
	if err != nil {
		t.Fatalf("decodeInlineFileData(bare base64) error = %v", err)
	}
	if contentType != "image/jpeg" {
		t.Fatalf("fallback content type = %q, want image/jpeg", contentType)
	}
	if string(got) != string(payload) {
		t.Fatalf("decoded fallback bytes = %q, want %q", string(got), string(payload))
	}
}

func TestDecodeInlineFileDataRejectsInvalidBase64(t *testing.T) {
	_, _, err := decodeInlineFileData("data:image/png;base64,not-valid-base64!", "")
	if err == nil {
		t.Fatal("decodeInlineFileData() error = nil, want invalid base64")
	}
	if !strings.Contains(err.Error(), "invalid base64") {
		t.Fatalf("error = %q, want invalid base64", err.Error())
	}
}

func TestMergeProcessUploadEventMetadataFromIndexingCompleted(t *testing.T) {
	line := `{"file_id":"file_old","event":"file.indexing.completed","message":"","progress":null,"extra":{"metadata_object_id":"libfile_old","library_file_name":"image.webp","mime_type":"image/webp"}}`
	var evt processUploadEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	var libraryID, libraryName, libraryMime string
	mergeProcessUploadEventMetadata(evt, &libraryID, &libraryName, &libraryMime)

	if libraryID != "libfile_old" {
		t.Fatalf("libraryID = %q, want libfile_old", libraryID)
	}
	if libraryName != "image.webp" {
		t.Fatalf("libraryName = %q, want image.webp", libraryName)
	}
	if libraryMime != "image/webp" {
		t.Fatalf("libraryMime = %q, want image/webp", libraryMime)
	}
}

func TestMergeProcessUploadEventMetadataFromProcessingCompleted(t *testing.T) {
	line := `{"file_id":"file_new","event":"file.processing.completed","message":"Succeeded processing file file_new","progress":100.0,"extra":{"metadata_object_id":"libfile_new","library_file_name":"lecture.jpg","mime_type":"image/jpeg"}}`
	var evt processUploadEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	var libraryID, libraryName, libraryMime string
	mergeProcessUploadEventMetadata(evt, &libraryID, &libraryName, &libraryMime)

	if libraryID != "libfile_new" {
		t.Fatalf("libraryID = %q, want libfile_new", libraryID)
	}
	if libraryName != "lecture.jpg" {
		t.Fatalf("libraryName = %q, want lecture.jpg", libraryName)
	}
	if libraryMime != "image/jpeg" {
		t.Fatalf("libraryMime = %q, want image/jpeg", libraryMime)
	}
}

func TestApplyProcessUploadEventMarksCompletedAndMetadata(t *testing.T) {
	line := `{"file_id":"file_new","event":"file.processing.completed","message":"Succeeded processing file file_new","progress":100.0,"extra":{"metadata_object_id":"libfile_new","library_file_name":"lecture.jpg","mime_type":"image/jpeg"}}`
	var evt processUploadEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	var result processUploadResult
	if err := applyProcessUploadEvent(&result, evt); err != nil {
		t.Fatalf("applyProcessUploadEvent() error = %v", err)
	}
	if !result.Completed {
		t.Fatal("Completed = false, want true")
	}
	if result.LibraryID != "libfile_new" || result.LibraryName != "lecture.jpg" || result.LibraryMime != "image/jpeg" {
		t.Fatalf("metadata = %#v, want completed image metadata", result)
	}
}

func TestApplyProcessUploadEventReturnsFailedEvent(t *testing.T) {
	evt := processUploadEvent{
		Event:   "file.processing.failed",
		Message: "processing failed",
	}
	var result processUploadResult
	err := applyProcessUploadEvent(&result, evt)
	if err == nil {
		t.Fatal("applyProcessUploadEvent() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "file.processing.failed") {
		t.Fatalf("error = %q, want event name", err.Error())
	}
}

func TestValidateProcessUploadCompletedRequiresCompletedEvent(t *testing.T) {
	err := validateProcessUploadCompleted(processUploadResult{
		FileReady:   true,
		LastEvent:   "file.processing.file_ready",
		LastMessage: "File is ready to download",
	})
	if err == nil {
		t.Fatal("validateProcessUploadCompleted() error = nil, want incomplete error")
	}
	if !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("error = %q, want did not complete", err.Error())
	}
}

func TestValidateProcessUploadResultForImageRequiresCompleted(t *testing.T) {
	err := validateProcessUploadResultForImage(processUploadResult{
		LastEvent: "file.processing.file_ready",
	}, "image/jpeg")
	if err == nil {
		t.Fatal("validateProcessUploadResultForImage() error = nil, want incomplete error")
	}
	if !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("error = %q, want did not complete", err.Error())
	}
}

func TestValidateProcessUploadResultForImageRequiresLibraryMetadata(t *testing.T) {
	err := validateProcessUploadResultForImage(processUploadResult{
		Completed: true,
		LastEvent: "file.processing.completed",
	}, "image/jpeg")
	if err == nil {
		t.Fatal("validateProcessUploadResultForImage() error = nil, want metadata error")
	}
	if !strings.Contains(err.Error(), "without library metadata") {
		t.Fatalf("error = %q, want missing library metadata", err.Error())
	}
}

func TestBackendFileDownloadMetadataEndpointsPreferConversationScopedDownload(t *testing.T) {
	endpoints := backendFileDownloadMetadataEndpoints("file_generated", "conv 1")
	if len(endpoints) != 3 {
		t.Fatalf("len(endpoints) = %d, want 3", len(endpoints))
	}
	first, err := url.Parse(endpoints[0])
	if err != nil {
		t.Fatalf("parse first endpoint: %v", err)
	}
	if !strings.HasSuffix(first.Path, "/backend-api/files/download/file_generated") {
		t.Fatalf("first path = %q, want files/download path", first.Path)
	}
	if first.Query().Get("conversation_id") != "conv 1" {
		t.Fatalf("conversation_id query = %q, want conv 1", first.Query().Get("conversation_id"))
	}
	if first.Query().Get("inline") != "false" {
		t.Fatalf("inline query = %q, want false", first.Query().Get("inline"))
	}
}

func TestGeneratedImageDownloadMetadataEndpointsUseHARPrimaryPath(t *testing.T) {
	endpoints := generatedImageDownloadMetadataEndpoints("file_generated", "conv 1")
	if len(endpoints) != 4 {
		t.Fatalf("len(endpoints) = %d, want 4", len(endpoints))
	}
	first, err := url.Parse(endpoints[0])
	if err != nil {
		t.Fatalf("parse first endpoint: %v", err)
	}
	if !strings.HasSuffix(first.Path, "/backend-api/files/download/file_generated") {
		t.Fatalf("first path = %q, want files/download path", first.Path)
	}
	if first.Query().Get("conversation_id") != "conv 1" {
		t.Fatalf("conversation_id query = %q, want conv 1", first.Query().Get("conversation_id"))
	}
	if first.Query().Get("inline") != "false" {
		t.Fatalf("inline query = %q, want false", first.Query().Get("inline"))
	}
	second, err := url.Parse(endpoints[1])
	if err != nil {
		t.Fatalf("parse second endpoint: %v", err)
	}
	if second.Query().Get("conversation_id") != "conv 1" {
		t.Fatalf("second conversation_id query = %q, want conv 1", second.Query().Get("conversation_id"))
	}
	if second.Query().Get("inline") != "true" {
		t.Fatalf("second inline query = %q, want true fallback", second.Query().Get("inline"))
	}
	third, err := url.Parse(endpoints[2])
	if err != nil {
		t.Fatalf("parse third endpoint: %v", err)
	}
	if third.RawQuery != "" {
		t.Fatalf("third raw query = %q, want empty fallback path", third.RawQuery)
	}
	for _, endpoint := range endpoints {
		if strings.Contains(endpoint, "/files/file_generated/download") {
			t.Fatalf("generated image endpoint used legacy fallback: %q", endpoint)
		}
	}
}

func TestGeneratedImageDownloadMetadataEndpointsWithoutConversationAvoidLegacyPath(t *testing.T) {
	endpoints := generatedImageDownloadMetadataEndpoints("file_generated", "")
	if len(endpoints) != 2 {
		t.Fatalf("len(endpoints) = %d, want 2", len(endpoints))
	}
	first, err := url.Parse(endpoints[0])
	if err != nil {
		t.Fatalf("parse first endpoint: %v", err)
	}
	if !strings.HasSuffix(first.Path, "/backend-api/files/download/file_generated") {
		t.Fatalf("first path = %q, want files/download path", first.Path)
	}
	if strings.Contains(first.Path, "/files/file_generated/download") {
		t.Fatalf("generated image endpoint used legacy fallback: %q", first.Path)
	}
	second, err := url.Parse(endpoints[1])
	if err != nil {
		t.Fatalf("parse second endpoint: %v", err)
	}
	if _, ok := second.Query()["post_id"]; !ok {
		t.Fatalf("post_id query missing in %q", endpoints[1])
	}
	if second.Query().Get("inline") != "false" {
		t.Fatalf("inline query = %q, want false", second.Query().Get("inline"))
	}
}

func TestNewGeneratedImageDownloadMetadataRequestMatchesHARHeaders(t *testing.T) {
	req, err := newGeneratedImageDownloadMetadataRequest(
		"token",
		"https://chatgpt.com/backend-api/files/download/file_generated?conversation_id=conv_1&inline=false",
		"conv_1",
	)
	if err != nil {
		t.Fatalf("newGeneratedImageDownloadMetadataRequest() error = %v", err)
	}
	if got, want := req.Header.Get("Authorization"), "Bearer token"; got != want {
		t.Fatalf("Authorization header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Referer"), api.ChatGPTApiUrlPrefix+"/c/conv_1"; got != want {
		t.Fatalf("Referer header = %q, want %q", got, want)
	}
	if got := req.Header.Get("Content-Type"); got != "" {
		t.Fatalf("Content-Type header = %q, want empty", got)
	}
	if got := req.Header.Get("User-Agent"); got == "" {
		t.Fatal("User-Agent header is empty")
	}
	if got, want := req.Header.Get("Accept"), "*/*"; got != want {
		t.Fatalf("Accept header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Oai-Client-Version"), oaiClientVersion; got != want {
		t.Fatalf("Oai-Client-Version header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Oai-Client-Build-Number"), oaiClientBuildNumber; got != want {
		t.Fatalf("Oai-Client-Build-Number header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("X-Openai-Target-Path"), "/backend-api/files/download/file_generated"; got != want {
		t.Fatalf("X-Openai-Target-Path header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("X-Openai-Target-Route"), "/backend-api/files/download/{file_id}"; got != want {
		t.Fatalf("X-Openai-Target-Route header = %q, want %q", got, want)
	}
}
