package imitate

import (
	"strings"
)

func ConvertToString(chatgptResponse *ChatGPTResponse, previousText *StringStruct, role bool) string {
	translatedResponse := NewChatCompletionChunk(strings.Replace(chatgptResponse.Message.Content.Parts[0].(string), previousText.Text, "", 1))
	if role {
		translatedResponse.Choices[0].Delta.Role = chatgptResponse.Message.Author.Role
	} else if translatedResponse.Choices[0].Delta.Content == "" || (strings.HasPrefix(chatgptResponse.Message.Metadata.ModelSlug, "gpt-4") && translatedResponse.Choices[0].Delta.Content == "【") {
		return translatedResponse.Choices[0].Delta.Content
	}
	previousText.Text = chatgptResponse.Message.Content.Parts[0].(string)
	return "data: " + translatedResponse.String() + "\n\n"
}
