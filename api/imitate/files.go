package imitate

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"
	"github.com/leokwsw/go-chatgpt-api/api/chatgpt"
	_ "golang.org/x/image/webp"
)

var fileMetadataCache sync.Map

type cachedFileMetadata struct {
	FileID        string
	LibraryID     string
	FileName      string
	MimeType      string
	FileSizeBytes int64
	Purpose       string
	CreatedAt     int64
	ExpiresAt     int64
	Width         int
	Height        int
}

type createFileRequest struct {
	FileName               string `json:"file_name"`
	FileSize               int64  `json:"file_size"`
	UseCase                string `json:"use_case"`
	TimezoneOffsetMin      int    `json:"timezone_offset_min"`
	ResetRateLimits        bool   `json:"reset_rate_limits"`
	StoreInLibrary         bool   `json:"store_in_library"`
	LibraryPersistenceMode string `json:"library_persistence_mode"`
}

type createFileResponse struct {
	Status    string `json:"status"`
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
}

type processUploadRequest struct {
	FileID                 string                 `json:"file_id"`
	UseCase                string                 `json:"use_case"`
	IndexForRetrieval      bool                   `json:"index_for_retrieval"`
	FileName               string                 `json:"file_name"`
	LibraryPersistenceMode string                 `json:"library_persistence_mode"`
	Metadata               map[string]interface{} `json:"metadata"`
	EntrySurface           string                 `json:"entry_surface,omitempty"`
}

type processUploadEvent struct {
	FileID   string                 `json:"file_id"`
	Event    string                 `json:"event"`
	Message  string                 `json:"message"`
	Progress *float64               `json:"progress"`
	Extra    map[string]interface{} `json:"extra"`
}

type processUploadResult struct {
	LibraryID   string
	LibraryName string
	LibraryMime string
	Completed   bool
	FileReady   bool
	LastEvent   string
	LastMessage string
}

type libraryItem struct {
	ID                string `json:"id"`
	FileID            string `json:"file_id"`
	FileName          string `json:"file_name"`
	MimeType          string `json:"mime_type"`
	FileSizeBytes     int64  `json:"file_size_bytes"`
	RecordCreationRaw string `json:"record_creation_time"`
}

type libraryResponse struct {
	Items  []libraryItem `json:"items"`
	Cursor *string       `json:"cursor"`
}

type openAIFile struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt *int64 `json:"expires_at,omitempty"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	Status    string `json:"status,omitempty"`
}

type openAIFileList struct {
	Object  string       `json:"object"`
	Data    []openAIFile `json:"data"`
	FirstID string       `json:"first_id,omitempty"`
	LastID  string       `json:"last_id,omitempty"`
	HasMore bool         `json:"has_more"`
}

type openAIFileDelete struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

func CreateFile(c *gin.Context) {
	accessToken, authErr := resolveImitateAccessToken(c)
	if authErr != nil {
		writeImitateAPIError(c, authErr)
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		writeInvalidRequestError(c, "Missing file form field", "file")
		return
	}

	purpose := c.PostForm("purpose")
	if purpose == "" {
		purpose = "user_data"
	}
	if !isSupportedOpenAIFilePurpose(purpose) {
		writeInvalidRequestError(c, "Unsupported file purpose "+purpose, "purpose")
		return
	}

	expiresAfterSeconds, err := parseExpiresAfterSeconds(c)
	if err != nil {
		writeInvalidRequestError(c, err.Error(), "expires_after")
		return
	}
	if expiresAfterSeconds == nil && purpose == "batch" {
		defaultBatchExpirySeconds := int64(2592000)
		expiresAfterSeconds = &defaultBatchExpirySeconds
	}

	fileBytes, err := readMultipartFile(fileHeader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	releaseAccount, err := api.AcquireAccountRequest(c.Request.Context(), accessToken)
	if err != nil {
		c.JSON(http.StatusRequestTimeout, gin.H{"error": err.Error()})
		return
	}
	defer releaseAccount()

	openAIResp, err := createOpenAIFileFromBytes(accessToken, fileHeader.Filename, purpose, contentType, fileBytes, expiresAfterSeconds)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, openAIResp)
}

func ListFiles(c *gin.Context) {
	accessToken, authErr := resolveImitateAccessToken(c)
	if authErr != nil {
		writeImitateAPIError(c, authErr)
		return
	}

	releaseAccount, err := api.AcquireAccountRequest(c.Request.Context(), accessToken)
	if err != nil {
		c.JSON(http.StatusRequestTimeout, gin.H{"error": err.Error()})
		return
	}
	defer releaseAccount()

	items, err := listLibraryFiles(accessToken)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	files := make([]openAIFile, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		cacheLibraryItem(item)
		files = append(files, mapLibraryItemToOpenAI(item))
		seen[item.FileID] = struct{}{}
	}
	for _, cached := range listCachedFiles() {
		if _, ok := seen[cached.FileID]; ok {
			continue
		}
		if file, ok := mapCachedFileToOpenAI(cached); ok {
			files = append(files, file)
		}
	}
	files, hasMore := filterAndPaginateFiles(c, files)

	resp := openAIFileList{
		Object:  "list",
		Data:    files,
		HasMore: hasMore,
	}
	if len(files) > 0 {
		resp.FirstID = files[0].ID
		resp.LastID = files[len(files)-1].ID
	}
	c.JSON(http.StatusOK, resp)
}

func RetrieveFile(c *gin.Context) {
	accessToken, authErr := resolveImitateAccessToken(c)
	if authErr != nil {
		writeImitateAPIError(c, authErr)
		return
	}

	releaseAccount, err := api.AcquireAccountRequest(c.Request.Context(), accessToken)
	if err != nil {
		c.JSON(http.StatusRequestTimeout, gin.H{"error": err.Error()})
		return
	}
	defer releaseAccount()

	fileID := c.Param("id")
	if cached, ok := mapCachedFileToOpenAI(getCachedFileMetadata(fileID)); ok {
		c.JSON(http.StatusOK, cached)
		return
	}
	item, err := findLibraryFile(accessToken, fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	cacheLibraryItem(*item)
	c.JSON(http.StatusOK, mapLibraryItemToOpenAI(*item))
}

func RetrieveFileContent(c *gin.Context) {
	accessToken, authErr := resolveImitateAccessToken(c)
	if authErr != nil {
		writeImitateAPIError(c, authErr)
		return
	}

	releaseAccount, err := api.AcquireAccountRequest(c.Request.Context(), accessToken)
	if err != nil {
		c.JSON(http.StatusRequestTimeout, gin.H{"error": err.Error()})
		return
	}
	defer releaseAccount()

	downloadURL, err := getBackendFileDownloadURL(accessToken, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{Accept: "*/*"})
	req.Header.Set("Authorization", api.GetAccessToken(accessToken))
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	resp, err := api.Client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.JSON(http.StatusBadGateway, gin.H{"error": string(body)})
		return
	}

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	if _, err = io.Copy(c.Writer, resp.Body); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
}

func DeleteFile(c *gin.Context) {
	_, authErr := resolveImitateAccessToken(c)
	if authErr != nil {
		writeImitateAPIError(c, authErr)
		return
	}
	fileID := c.Param("id")
	fileMetadataCache.Delete(fileID)
	c.JSON(http.StatusOK, openAIFileDelete{
		ID:      fileID,
		Object:  "file",
		Deleted: true,
	})
}

func isSupportedOpenAIFilePurpose(purpose string) bool {
	switch purpose {
	case "assistants", "batch", "fine-tune", "vision", "user_data", "evals":
		return true
	default:
		return false
	}
}

func parseExpiresAfterSeconds(c *gin.Context) (*int64, error) {
	anchor := strings.TrimSpace(firstPostForm(c, "expires_after[anchor]", "expires_after.anchor"))
	rawSeconds := strings.TrimSpace(firstPostForm(c, "expires_after[seconds]", "expires_after.seconds"))
	if anchor == "" && rawSeconds == "" {
		return nil, nil
	}
	if anchor != "created_at" {
		return nil, errors.New("expires_after.anchor must be created_at")
	}
	seconds, err := strconv.ParseInt(rawSeconds, 10, 64)
	if err != nil {
		return nil, errors.New("expires_after.seconds must be an integer")
	}
	if seconds < 3600 || seconds > 2592000 {
		return nil, errors.New("expires_after.seconds must be between 3600 and 2592000")
	}
	return &seconds, nil
}

func firstPostForm(c *gin.Context, names ...string) string {
	for _, name := range names {
		if value := c.PostForm(name); value != "" {
			return value
		}
	}
	return ""
}

func createBackendFile(c *gin.Context, accessToken string, filename string, fileSize int64, useCase string) (*createFileResponse, error) {
	reqBody := createFileRequest{
		FileName:               filename,
		FileSize:               fileSize,
		UseCase:                useCase,
		TimezoneOffsetMin:      -480,
		ResetRateLimits:        false,
		StoreInLibrary:         true,
		LibraryPersistenceMode: "opportunistic",
	}
	jsonBytes, _ := json.Marshal(reqBody)
	resp, err := doChatGPTJSONRequestWithTarget(accessToken, http.MethodPost, chatgpt.ApiPrefix+"/files", bytes.NewBuffer(jsonBytes), "/backend-api/files", "/backend-api/files")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.New(string(body))
	}

	var createResp createFileResponse
	if err = json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return nil, err
	}
	if createResp.Status != "success" || createResp.FileID == "" || createResp.UploadURL == "" {
		return nil, fmt.Errorf("create file failed: status=%q file_id_present=%t upload_url_present=%t", createResp.Status, createResp.FileID != "", createResp.UploadURL != "")
	}
	return &createResp, nil
}

func uploadToSignedURL(uploadURL string, fileBytes []byte, contentType string) error {
	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewBuffer(fileBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Ms-Blob-Type", "BlockBlob")
	resp, err := api.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return errors.New(string(body))
	}
	return nil
}

func processUpload(c *gin.Context, accessToken string, fileID string, useCase string, indexForRetrieval bool, fileName string) (processUploadResult, error) {
	reqBody := processUploadRequest{
		FileID:                 fileID,
		UseCase:                useCase,
		IndexForRetrieval:      indexForRetrieval,
		FileName:               fileName,
		LibraryPersistenceMode: "opportunistic",
		Metadata: map[string]interface{}{
			"store_in_library": true,
		},
		EntrySurface: "chat_composer",
	}
	jsonBytes, _ := json.Marshal(reqBody)
	resp, err := doChatGPTJSONRequestWithTarget(accessToken, http.MethodPost, chatgpt.ApiPrefix+"/files/process_upload_stream", bytes.NewBuffer(jsonBytes), "/backend-api/files/process_upload_stream", "/backend-api/files/process_upload_stream")
	if err != nil {
		return processUploadResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return processUploadResult{}, errors.New(string(body))
	}

	var result processUploadResult
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt processUploadEvent
		if err = json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if err = applyProcessUploadEvent(&result, evt); err != nil {
			return result, err
		}
	}
	if err = scanner.Err(); err != nil {
		return result, err
	}
	if err = validateProcessUploadCompleted(result); err != nil {
		return result, err
	}
	return result, nil
}

func validateProcessUploadCompleted(result processUploadResult) error {
	if !result.Completed {
		return fmt.Errorf("file upload processing did not complete: last_event=%q last_message=%q", result.LastEvent, result.LastMessage)
	}
	return nil
}

func applyProcessUploadEvent(result *processUploadResult, evt processUploadEvent) error {
	if result == nil {
		return nil
	}
	result.LastEvent = evt.Event
	result.LastMessage = evt.Message
	switch evt.Event {
	case "file.processing.file_ready":
		result.FileReady = true
	case "file.processing.completed":
		result.Completed = true
	}
	mergeProcessUploadEventMetadata(evt, &result.LibraryID, &result.LibraryName, &result.LibraryMime)

	event := strings.ToLower(evt.Event)
	if strings.Contains(event, "failed") || strings.Contains(event, "error") {
		return fmt.Errorf("file upload processing failed: event=%q message=%q", evt.Event, evt.Message)
	}
	return nil
}

func mergeProcessUploadEventMetadata(evt processUploadEvent, libraryID *string, libraryName *string, libraryMime *string) {
	if evt.Extra == nil {
		return
	}
	if libraryID != nil && *libraryID == "" {
		if v, ok := evt.Extra["metadata_object_id"].(string); ok && v != "" {
			*libraryID = v
		}
	}
	if libraryName != nil && *libraryName == "" {
		if v, ok := evt.Extra["library_file_name"].(string); ok && v != "" {
			*libraryName = v
		}
	}
	if libraryMime != nil && *libraryMime == "" {
		if v, ok := evt.Extra["mime_type"].(string); ok && v != "" {
			*libraryMime = v
		}
	}
}

func listLibraryFiles(accessToken string) ([]libraryItem, error) {
	items := make([]libraryItem, 0)
	var cursor *string
	for {
		page, nextCursor, err := listLibraryFilesPage(accessToken, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range page {
			cacheLibraryItem(item)
		}
		items = append(items, page...)
		if nextCursor == nil || *nextCursor == "" {
			return items, nil
		}
		cursor = nextCursor
	}
}

func findLibraryFile(accessToken string, fileID string) (*libraryItem, error) {
	var cursor *string
	for {
		items, nextCursor, err := listLibraryFilesPage(accessToken, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			cacheLibraryItem(item)
			if item.FileID == fileID {
				return &item, nil
			}
		}
		if nextCursor == nil || *nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return nil, errors.New("not found")
}

func mapLibraryItemToOpenAI(item libraryItem) openAIFile {
	createdAt := time.Now().Unix()
	if item.RecordCreationRaw != "" {
		if t, err := time.Parse(time.RFC3339Nano, item.RecordCreationRaw); err == nil {
			createdAt = t.Unix()
		}
	}
	cached := getCachedFileMetadata(item.FileID)
	if cached.CreatedAt != 0 {
		createdAt = cached.CreatedAt
	}
	purpose := normalizePurpose(cached.Purpose, item.MimeType)
	expiresAt := optionalUnix(cached.ExpiresAt)
	return openAIFile{
		ID:        item.FileID,
		Object:    "file",
		Bytes:     item.FileSizeBytes,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Filename:  item.FileName,
		Purpose:   purpose,
		Status:    "processed",
	}
}

func mapCachedFileToOpenAI(meta cachedFileMetadata) (openAIFile, bool) {
	if meta.FileID == "" || meta.FileName == "" || meta.FileSizeBytes == 0 {
		return openAIFile{}, false
	}
	createdAt := meta.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	expiresAt := optionalUnix(meta.ExpiresAt)
	return openAIFile{
		ID:        meta.FileID,
		Object:    "file",
		Bytes:     meta.FileSizeBytes,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Filename:  meta.FileName,
		Purpose:   normalizePurpose(meta.Purpose, meta.MimeType),
		Status:    "processed",
	}, true
}

func inferUseCase(contentType string, purpose string) string {
	if strings.HasPrefix(contentType, "image/") || strings.EqualFold(purpose, "vision") {
		return "multimodal"
	}
	return "my_files"
}

func normalizePurpose(purpose string, contentType string) string {
	if purpose != "" {
		return purpose
	}
	if strings.HasPrefix(contentType, "image/") {
		return "vision"
	}
	return "user_data"
}

func doChatGPTJSONRequest(accessToken string, method string, endpoint string, body io.Reader) (*http.Response, error) {
	return doChatGPTJSONRequestWithTarget(accessToken, method, endpoint, body, "", "")
}

func doChatGPTJSONRequestWithTarget(accessToken string, method string, endpoint string, body io.Reader, targetPath string, targetRoute string) (*http.Response, error) {
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{
		Accept:      "*/*",
		ContentType: "application/json",
	})
	if targetPath != "" {
		req.Header.Set("X-Openai-Target-Path", targetPath)
	}
	if targetRoute != "" {
		req.Header.Set("X-Openai-Target-Route", targetRoute)
	}
	req.Header.Set("Authorization", api.GetAccessToken(accessToken))
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	return api.Client.Do(req)
}

func listLibraryFilesPage(accessToken string, cursor *string) ([]libraryItem, *string, error) {
	reqBody := map[string]interface{}{
		"limit":  200,
		"cursor": cursor,
	}
	jsonBytes, _ := json.Marshal(reqBody)
	resp, err := doChatGPTJSONRequest(accessToken, http.MethodPost, chatgpt.ApiPrefix+"/files/library", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, errors.New(string(body))
	}

	var library libraryResponse
	if err = json.NewDecoder(resp.Body).Decode(&library); err != nil {
		return nil, nil, err
	}
	return library.Items, library.Cursor, nil
}

func readMultipartFile(fileHeader *multipart.FileHeader) ([]byte, error) {
	src, err := fileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return io.ReadAll(src)
}

func createOpenAIFileFromBytes(accessToken string, filename string, purpose string, contentType string, fileBytes []byte, expiresAfterSeconds *int64) (*openAIFile, error) {
	contentType = normalizeContentType(contentType, fileBytes)
	useCase := inferUseCase(contentType, purpose)
	createResp, err := createBackendFile(nil, accessToken, filename, int64(len(fileBytes)), useCase)
	if err != nil {
		return nil, err
	}
	imageWidth, imageHeight := detectImageDimensions(fileBytes)
	if err = uploadToSignedURL(createResp.UploadURL, fileBytes, contentType); err != nil {
		return nil, err
	}

	shouldIndex := !strings.HasPrefix(contentType, "image/")
	processResult, err := processUpload(nil, accessToken, createResp.FileID, useCase, shouldIndex, filename)
	if err != nil {
		return nil, err
	}
	isImage := strings.HasPrefix(contentType, "image/")
	if isImage {
		if err = validateProcessUploadResultForImage(processResult, contentType); err != nil {
			return nil, err
		}
		if err = verifyUploadedFileDownloadReady(accessToken, createResp.FileID); err != nil {
			return nil, err
		}
	}

	if processResult.LibraryName != "" {
		filename = processResult.LibraryName
	}
	if contentType == "" {
		contentType = processResult.LibraryMime
	}
	purpose = normalizePurpose(purpose, contentType)
	createdAt := time.Now().Unix()
	var expiresAt *int64
	if expiresAfterSeconds != nil {
		value := createdAt + *expiresAfterSeconds
		expiresAt = &value
	}

	cacheFileMetadata(cachedFileMetadata{
		FileID:        createResp.FileID,
		LibraryID:     processResult.LibraryID,
		FileName:      filename,
		MimeType:      contentType,
		FileSizeBytes: int64(len(fileBytes)),
		Purpose:       purpose,
		CreatedAt:     createdAt,
		Width:         imageWidth,
		Height:        imageHeight,
		ExpiresAt:     int64Value(expiresAt),
	})

	return &openAIFile{
		ID:        createResp.FileID,
		Object:    "file",
		Bytes:     int64(len(fileBytes)),
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Filename:  filename,
		Purpose:   purpose,
		Status:    "processed",
	}, nil
}

func validateProcessUploadResultForImage(result processUploadResult, contentType string) error {
	if !result.Completed {
		return fmt.Errorf("image upload processing did not complete: last_event=%q last_message=%q", result.LastEvent, result.LastMessage)
	}
	if result.LibraryID == "" {
		return fmt.Errorf("image upload completed without library metadata: last_event=%q last_message=%q", result.LastEvent, result.LastMessage)
	}
	if result.LibraryMime != "" && !strings.HasPrefix(strings.ToLower(result.LibraryMime), "image/") && !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return fmt.Errorf("image upload completed with non-image mime type: %q", result.LibraryMime)
	}
	return nil
}

func verifyUploadedFileDownloadReady(accessToken string, fileID string) error {
	retries := envInt("IMITATE_UPLOAD_DOWNLOAD_READY_RETRIES", 3)
	if retries < 1 {
		retries = 1
	}
	interval := time.Duration(envInt("IMITATE_UPLOAD_DOWNLOAD_READY_INTERVAL_MS", 500)) * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if _, err := getBackendFileDownloadURL(accessToken, fileID); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < retries {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("uploaded file download metadata unavailable: %w", lastErr)
}

func createOpenAIFileFromExternalURL(accessToken string, rawURL string, purpose string, fallbackFilename string) (*openAIFile, error) {
	if strings.HasPrefix(rawURL, "data:") {
		return createOpenAIFileFromInlineData(accessToken, fallbackFilename, purpose, rawURL, "")
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := api.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.New(string(body))
	}

	fileBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	filename := fallbackFilename
	if filename == "" {
		filename = filenameFromURL(rawURL)
	}
	if filename == "" {
		filename = guessFilenameFromContentType(contentType, "input")
	}
	return createOpenAIFileFromBytes(accessToken, filename, purpose, contentType, fileBytes, nil)
}

func createOpenAIFileFromInlineData(accessToken string, filename string, purpose string, fileData string, fallbackContentType string) (*openAIFile, error) {
	contentType, fileBytes, err := decodeInlineFileData(fileData, fallbackContentType)
	if err != nil {
		return nil, err
	}
	if filename == "" {
		filename = guessFilenameFromContentType(contentType, "input")
	}
	return createOpenAIFileFromBytes(accessToken, filename, purpose, contentType, fileBytes, nil)
}

func normalizeContentType(contentType string, fileBytes []byte) string {
	if contentType != "" {
		return contentType
	}
	return http.DetectContentType(fileBytes)
}

func getBackendFileDownloadURL(accessToken string, fileID string) (string, error) {
	return getBackendFileDownloadURLWithConversation(accessToken, fileID, "")
}

func getBackendFileDownloadURLWithConversation(accessToken string, fileID string, conversationID string) (string, error) {
	downloadURLs, err := getBackendFileDownloadURLsWithConversation(accessToken, fileID, conversationID)
	if err != nil {
		return "", err
	}
	if len(downloadURLs) == 0 {
		return "", errors.New("download url unavailable")
	}
	return downloadURLs[0], nil
}

func getBackendFileDownloadURLsWithConversation(accessToken string, fileID string, conversationID string) ([]string, error) {
	endpoints := backendFileDownloadMetadataEndpoints(fileID, conversationID)

	var lastErr error
	downloadURLs := make([]string, 0, len(endpoints))
	seenURLs := make(map[string]bool)
	for _, endpoint := range endpoints {
		targetPath := targetPathFromEndpoint(endpoint)
		targetRoute := targetPath
		if strings.Contains(targetPath, "/backend-api/files/download/") {
			targetRoute = "/backend-api/files/download/{file_id}"
		}
		resp, err := doChatGPTJSONRequestWithTarget(accessToken, http.MethodGet, endpoint, nil, targetPath, targetRoute)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = errors.New(string(body))
			continue
		}
		var fileInfo chatgpt.FileInfo
		if err = json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
			resp.Body.Close()
			lastErr = err
			continue
		}
		resp.Body.Close()
		if fileInfo.Status == "success" && fileInfo.DownloadURL != "" {
			if !seenURLs[fileInfo.DownloadURL] {
				seenURLs[fileInfo.DownloadURL] = true
				downloadURLs = append(downloadURLs, fileInfo.DownloadURL)
			}
			continue
		}
		lastErr = errors.New("download url unavailable")
	}
	if len(downloadURLs) != 0 {
		return downloadURLs, nil
	}
	if lastErr == nil {
		lastErr = errors.New("download url unavailable")
	}
	return nil, lastErr
}

func targetPathFromEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return parsed.Path
}

func getGeneratedImageDownloadURLs(accessToken string, fileID string, conversationID string) ([]string, error) {
	endpoints := generatedImageDownloadMetadataEndpoints(fileID, conversationID)

	var lastErr error
	downloadURLs := make([]string, 0, len(endpoints))
	seenURLs := make(map[string]bool)
	for _, endpoint := range endpoints {
		req, err := newGeneratedImageDownloadMetadataRequest(accessToken, endpoint, conversationID)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := api.Client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = newUpstreamHTTPError("generated image download metadata", resp, body)
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return nil, lastErr
			}
			continue
		}
		var fileInfo chatgpt.FileInfo
		if err = json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
			resp.Body.Close()
			lastErr = err
			continue
		}
		resp.Body.Close()
		if fileInfo.Status == "success" && fileInfo.DownloadURL != "" {
			if !seenURLs[fileInfo.DownloadURL] {
				seenURLs[fileInfo.DownloadURL] = true
				downloadURLs = append(downloadURLs, fileInfo.DownloadURL)
			}
			continue
		}
		lastErr = errors.New("download url unavailable")
	}
	if len(downloadURLs) != 0 {
		return downloadURLs, nil
	}
	if lastErr == nil {
		lastErr = errors.New("download url unavailable")
	}
	return nil, lastErr
}

func newGeneratedImageDownloadMetadataRequest(accessToken string, endpoint string, conversationID string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	referer := api.ChatGPTApiUrlPrefix + "/"
	if conversationID != "" {
		referer = api.ChatGPTApiUrlPrefix + "/c/" + conversationID
	}
	api.ApplyChatGPTBrowserHeaders(req, api.ChatGPTBrowserHeaderOptions{
		Accept:  "*/*",
		Referer: referer,
	})
	targetPath := req.URL.Path
	req.Header.Set("Oai-Client-Version", oaiClientVersion)
	req.Header.Set("Oai-Client-Build-Number", oaiClientBuildNumber)
	req.Header.Set("X-Openai-Target-Path", targetPath)
	req.Header.Set("X-Openai-Target-Route", "/backend-api/files/download/{file_id}")
	req.Header.Set("Authorization", api.GetAccessToken(accessToken))
	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	if api.OAIDID != "" {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
		req.Header.Set("Oai-Device-Id", api.OAIDID)
	}
	return req, nil
}

func generatedImageDownloadMetadataEndpoints(fileID string, conversationID string) []string {
	escapedFileID := url.PathEscape(fileID)
	primary := chatgpt.ApiPrefix + "/files/download/" + escapedFileID
	if conversationID == "" {
		values := url.Values{}
		values.Set("post_id", "")
		values.Set("inline", "false")
		return []string{
			primary,
			primary + "?" + values.Encode(),
		}
	}

	conversationDownload := url.Values{}
	conversationDownload.Set("conversation_id", conversationID)
	conversationDownload.Set("inline", "false")
	conversationInline := url.Values{}
	conversationInline.Set("conversation_id", conversationID)
	conversationInline.Set("inline", "true")
	postDownload := url.Values{}
	postDownload.Set("post_id", "")
	postDownload.Set("inline", "false")
	return []string{
		primary + "?" + conversationDownload.Encode(),
		primary + "?" + conversationInline.Encode(),
		primary,
		primary + "?" + postDownload.Encode(),
	}
}

func backendFileDownloadMetadataEndpoints(fileID string, conversationID string) []string {
	escapedFileID := url.PathEscape(fileID)
	endpoints := make([]string, 0, 3)
	if conversationID != "" {
		values := url.Values{}
		values.Set("conversation_id", conversationID)
		values.Set("inline", "false")
		endpoints = append(endpoints, chatgpt.ApiPrefix+"/files/download/"+escapedFileID+"?"+values.Encode())
	}
	endpoints = append(endpoints,
		chatgpt.ApiPrefix+"/files/download/"+escapedFileID,
		chatgpt.ApiPrefix+"/files/"+escapedFileID+"/download",
	)
	return endpoints
}

func optionalUnix(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func filterAndPaginateFiles(c *gin.Context, files []openAIFile) ([]openAIFile, bool) {
	filtered := make([]openAIFile, 0, len(files))
	purposeFilter := strings.TrimSpace(c.Query("purpose"))
	for _, file := range files {
		if purposeFilter != "" && file.Purpose != purposeFilter {
			continue
		}
		filtered = append(filtered, file)
	}

	order := strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "desc")))
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt == filtered[j].CreatedAt {
			if order == "asc" {
				return filtered[i].ID < filtered[j].ID
			}
			return filtered[i].ID > filtered[j].ID
		}
		if order == "asc" {
			return filtered[i].CreatedAt < filtered[j].CreatedAt
		}
		return filtered[i].CreatedAt > filtered[j].CreatedAt
	})

	afterID := strings.TrimSpace(c.Query("after"))
	if afterID != "" {
		start := 0
		for i, file := range filtered {
			if file.ID == afterID {
				start = i + 1
				break
			}
		}
		if start < len(filtered) {
			filtered = filtered[start:]
		} else {
			filtered = []openAIFile{}
		}
	}

	limit := 10000
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		if parsed, err := parsePositiveInt(rawLimit); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 10000 {
		limit = 10000
	}

	hasMore := len(filtered) > limit
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, hasMore
}

func parsePositiveInt(raw string) (int, error) {
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid integer")
		}
		value = value*10 + int(ch-'0')
	}
	return value, nil
}

func BuildAttachmentMetadataByFileID(accessToken string, fileID string) (map[string]interface{}, error) {
	cached := getCachedFileMetadata(fileID)
	if cached.LibraryID == "" || cached.FileName == "" || cached.MimeType == "" || cached.FileSizeBytes == 0 {
		item, err := findLibraryFile(accessToken, fileID)
		if err != nil {
			return nil, err
		}
		if cached.LibraryID == "" {
			cached.LibraryID = item.ID
		}
		if cached.FileName == "" {
			cached.FileName = item.FileName
		}
		if cached.MimeType == "" {
			cached.MimeType = item.MimeType
		}
		if cached.FileSizeBytes == 0 {
			cached.FileSizeBytes = item.FileSizeBytes
		}
	}
	cacheFileMetadata(cached)

	attachment := map[string]interface{}{
		"id":              fileID,
		"size":            cached.FileSizeBytes,
		"name":            filepath.Base(cached.FileName),
		"mime_type":       cached.MimeType,
		"source":          "library",
		"library_file_id": cached.LibraryID,
		"is_big_paste":    false,
	}
	if cached.Width > 0 {
		attachment["width"] = cached.Width
	}
	if cached.Height > 0 {
		attachment["height"] = cached.Height
	}
	return attachment, nil
}

func detectImageDimensions(fileBytes []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(fileBytes))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func getCachedFileMetadata(fileID string) cachedFileMetadata {
	if cached, ok := fileMetadataCache.Load(fileID); ok {
		if meta, ok := cached.(cachedFileMetadata); ok {
			return meta
		}
	}
	return cachedFileMetadata{FileID: fileID}
}

func cacheFileMetadata(meta cachedFileMetadata) {
	if meta.FileID == "" {
		return
	}
	if existing := getCachedFileMetadata(meta.FileID); existing.FileID != "" {
		if meta.LibraryID == "" {
			meta.LibraryID = existing.LibraryID
		}
		if meta.FileName == "" {
			meta.FileName = existing.FileName
		}
		if meta.MimeType == "" {
			meta.MimeType = existing.MimeType
		}
		if meta.FileSizeBytes == 0 {
			meta.FileSizeBytes = existing.FileSizeBytes
		}
		if meta.CreatedAt == 0 {
			meta.CreatedAt = existing.CreatedAt
		}
		if meta.ExpiresAt == 0 {
			meta.ExpiresAt = existing.ExpiresAt
		}
		if meta.Width == 0 {
			meta.Width = existing.Width
		}
		if meta.Height == 0 {
			meta.Height = existing.Height
		}
	}
	fileMetadataCache.Store(meta.FileID, meta)
}

func cacheLibraryItem(item libraryItem) {
	createdAt := int64(0)
	if item.RecordCreationRaw != "" {
		if t, err := time.Parse(time.RFC3339Nano, item.RecordCreationRaw); err == nil {
			createdAt = t.Unix()
		}
	}
	cacheFileMetadata(cachedFileMetadata{
		FileID:        item.FileID,
		LibraryID:     item.ID,
		FileName:      item.FileName,
		MimeType:      item.MimeType,
		FileSizeBytes: item.FileSizeBytes,
		CreatedAt:     createdAt,
	})
}

func listCachedFiles() []cachedFileMetadata {
	files := make([]cachedFileMetadata, 0)
	fileMetadataCache.Range(func(_, value interface{}) bool {
		meta, ok := value.(cachedFileMetadata)
		if ok {
			files = append(files, meta)
		}
		return true
	})
	return files
}

func decodeInlineFileData(fileData string, fallbackContentType string) (string, []byte, error) {
	if strings.HasPrefix(fileData, "data:") {
		return decodeDataURL(fileData)
	}
	data, err := decodeBase64String(fileData)
	if err != nil {
		return "", nil, err
	}
	return fallbackContentType, data, nil
}

func decodeDataURL(raw string) (string, []byte, error) {
	meta, payload, found := strings.Cut(raw, ",")
	if !found {
		return "", nil, errors.New("invalid data url")
	}
	meta = strings.TrimPrefix(meta, "data:")
	contentType := ""
	isBase64 := false
	for _, segment := range strings.Split(meta, ";") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		if segment == "base64" {
			isBase64 = true
			continue
		}
		if contentType == "" {
			contentType = segment
		}
	}

	if isBase64 {
		data, err := decodeBase64String(payload)
		if err != nil {
			return "", nil, err
		}
		return contentType, data, nil
	}

	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return "", nil, err
	}
	return contentType, []byte(decoded), nil
}

func decodeBase64String(raw string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, raw)
	data, err := base64Decode(cleaned)
	if err == nil {
		return data, nil
	}
	return nil, err
}

func base64Decode(raw string) ([]byte, error) {
	encodings := []func(string) ([]byte, error){
		func(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) },
		func(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) },
		func(s string) ([]byte, error) { return base64.URLEncoding.DecodeString(s) },
		func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) },
	}
	for _, decode := range encodings {
		decoded, err := decode(raw)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64 file data")
}

func filenameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return ""
	}
	return name
}

func guessFilenameFromContentType(contentType string, prefix string) string {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if contentType == "" {
		return prefix
	}
	exts, err := mime.ExtensionsByType(contentType)
	if err != nil || len(exts) == 0 {
		return prefix
	}
	return prefix + exts[0]
}
