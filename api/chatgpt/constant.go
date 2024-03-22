package chatgpt

const (
	defaultRole                    = "user"
	PublicApiPrefix                = "https://chat.openai.com/public-api"
	conversationLimit              = PublicApiPrefix + "/conversation_limit"
	ApiPrefix                      = "https://chat.openai.com/backend-api"
	getConversationsErrorMessage   = "Failed to get conversations."
	generateTitleErrorMessage      = "Failed to generate title."
	getContentErrorMessage         = "Failed to get content."
	updateConversationErrorMessage = "Failed to update conversation."
	clearConversationsErrorMessage = "Failed to clear conversations."
	feedbackMessageErrorMessage    = "Failed to add feedback."
	getModelsErrorMessage          = "Failed to get models."
	getAccountCheckErrorMessage    = "Check failed." // Placeholder. Never encountered.
	parseJsonErrorMessage          = "Failed to parse json request body."

	gpt4Model = "gpt-4"

	actionContinue                     = "continue"
	responseTypeMaxTokens              = "max_tokens"
	responseStatusFinishedSuccessfully = "finished_successfully"
	noModelPermissionErrorMessage      = "you have no permission to use this model"
	WebSocketProtocols                 = "json.reliable.webpubsub.azure.v1"
)
