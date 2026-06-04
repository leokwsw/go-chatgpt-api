package imitate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertAPIMessageConvertsOfficialResponsesImageFileID(t *testing.T) {
	fileID := "file_official_image"
	cacheFileMetadata(cachedFileMetadata{
		FileID:        fileID,
		LibraryID:     "lib_official_image",
		FileName:      "image.png",
		MimeType:      "image/png",
		FileSizeBytes: 42,
		Width:         10,
		Height:        20,
	})
	t.Cleanup(func() {
		fileMetadataCache.Delete(fileID)
	})

	requestCtx := &requestConversionContext{}
	content, metadata := convertAPIMessage(ApiMessage{
		Role: "user",
		Content: []interface{}{
			map[string]interface{}{"type": "input_text", "text": "read the image"},
			map[string]interface{}{"type": "input_image", "file_id": fileID, "detail": "high"},
		},
	}, "token", requestCtx)

	if !requestCtx.HasAttachments || !requestCtx.HasImage {
		t.Fatalf("request context = %+v, want image attachment flags", requestCtx)
	}
	if requestCtx.ForceNoImageReply {
		t.Fatal("ForceNoImageReply = true, want false")
	}
	if content.ContentType != "multimodal_text" {
		t.Fatalf("content type = %q, want multimodal_text", content.ContentType)
	}
	if len(content.Parts) != 2 {
		t.Fatalf("len(parts) = %d, want image pointer and text", len(content.Parts))
	}
	imagePart, ok := content.Parts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("first part = %#v, want image part map", content.Parts[0])
	}
	if imagePart["content_type"] != "image_asset_pointer" {
		t.Fatalf("image content_type = %v, want image_asset_pointer", imagePart["content_type"])
	}
	if imagePart["asset_pointer"] != "sediment://"+fileID {
		t.Fatalf("asset pointer = %v, want sediment://%s", imagePart["asset_pointer"], fileID)
	}
	if imagePart["width"] != int64(10) || imagePart["height"] != int64(20) {
		t.Fatalf("image dimensions = %v x %v, want 10 x 20", imagePart["width"], imagePart["height"])
	}
	textPart, ok := content.Parts[1].(string)
	if !ok || !strings.Contains(textPart, "read the image") || !strings.Contains(textPart, missingImageInstruction) {
		t.Fatalf("text part = %#v, want prompt plus missing-image instruction", content.Parts[1])
	}

	meta, ok := metadata.(map[string]interface{})
	if !ok {
		t.Fatalf("metadata = %#v, want map", metadata)
	}
	attachments, ok := meta["attachments"].([]interface{})
	if !ok || len(attachments) != 1 {
		t.Fatalf("attachments = %#v, want one attachment", meta["attachments"])
	}
	attachment, ok := attachments[0].(map[string]interface{})
	if !ok || attachment["id"] != fileID || attachment["mime_type"] != "image/png" {
		t.Fatalf("attachment = %#v, want cached image attachment", attachments[0])
	}
}

func TestConvertAPIMessageMissingImageForcesNoImageReply(t *testing.T) {
	requestCtx := &requestConversionContext{}
	content, _ := convertAPIMessage(ApiMessage{
		Role: "user",
		Content: []interface{}{
			map[string]interface{}{"type": "input_text", "text": "read the image"},
			map[string]interface{}{"type": "input_image"},
		},
	}, "token", requestCtx)

	if !requestCtx.HasAttachments || !requestCtx.HasImage {
		t.Fatalf("request context = %+v, want attempted image flags", requestCtx)
	}
	if !requestCtx.ForceNoImageReply {
		t.Fatal("ForceNoImageReply = false, want true")
	}
	if content.ContentType != "text" {
		t.Fatalf("content type = %q, want text when no image pointer was built", content.ContentType)
	}
}

func TestOfficialImageInputExtractors(t *testing.T) {
	dataURL := "data:image/jpeg;base64,aW1hZ2U="
	remoteURL := "https://example.com/image.jpg"

	if got := extractExternalFileURL(map[string]interface{}{
		"type":      "input_image",
		"image_url": dataURL,
	}); got != dataURL {
		t.Fatalf("Responses image_url = %q, want %q", got, dataURL)
	}
	if got := extractExternalFileURL(map[string]interface{}{
		"type": "image_url",
		"image_url": map[string]interface{}{
			"url":    remoteURL,
			"detail": "low",
		},
	}); got != remoteURL {
		t.Fatalf("Chat image_url.url = %q, want %q", got, remoteURL)
	}
	if got := extractInlineFileData(map[string]interface{}{
		"type":      "input_file",
		"file_data": dataURL,
		"filename":  "image.jpg",
	}); got != dataURL {
		t.Fatalf("Responses input_file.file_data = %q, want %q", got, dataURL)
	}
}

func TestNewResponsesResponseIncludesOfficialDefaultFields(t *testing.T) {
	store := true
	response := newResponsesResponse(ResponsesRequest{
		Model:       "gpt-5-3",
		Reasoning:   map[string]interface{}{"effort": "low"},
		ServiceTier: "default",
		Store:       &store,
		Truncation:  "auto",
		Input:       "hello",
	}, &conversationResult{
		ID:    "resp_test",
		Model: "gpt-5-3",
		Text:  "ok",
	})

	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`"error":null`,
		`"incomplete_details":null`,
		`"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`,
		`"service_tier":"default"`,
		`"truncation":"auto"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("response JSON missing %s:\n%s", want, text)
		}
	}
}
