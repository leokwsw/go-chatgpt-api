package imitate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/leokwsw/go-chatgpt-api/api/chatgpt"
)

func ConvertToString(chatgptResponse *ChatGPTResponse, previousText *StringStruct, role bool) string {
	currentText := extractTextContent(chatgptResponse.Message.Content)
	translatedResponse := NewChatCompletionChunk(diffResponseText(previousText.Text, currentText))
	if role {
		translatedResponse.Choices[0].Delta.Role = chatgptResponse.Message.Author.Role
	} else if translatedResponse.Choices[0].Delta.Content == "" || (strings.HasPrefix(chatgptResponse.Message.Metadata.ModelSlug, "gpt-4") && translatedResponse.Choices[0].Delta.Content == "【") {
		return translatedResponse.Choices[0].Delta.Content
	}
	previousText.Text = currentText
	previousText.Parts = extractTextParts(chatgptResponse.Message.Content)
	return "data: " + translatedResponse.String() + "\n\n"
}

func extractTextContent(content chatgpt.Content) string {
	parts := extractTextParts(content)
	if len(parts) == 0 {
		return ""
	}
	keys := make([]int, 0, len(parts))
	for index := range parts {
		keys = append(keys, index)
	}
	sort.Ints(keys)

	var builder strings.Builder
	for _, index := range keys {
		builder.WriteString(parts[index])
	}
	return builder.String()
}

func extractTextParts(content chatgpt.Content) map[int]string {
	parts := make(map[int]string)
	for index, part := range content.Parts {
		switch value := part.(type) {
		case string:
			parts[index] = value
		case fmt.Stringer:
			parts[index] = value.String()
		}
	}
	return parts
}

func diffResponseText(previousText string, currentText string) string {
	if strings.HasPrefix(currentText, previousText) {
		return strings.TrimPrefix(currentText, previousText)
	}
	return currentText
}
