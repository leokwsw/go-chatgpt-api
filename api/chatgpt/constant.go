package chatgpt

const (
	defaultRole                    = "user"
	PublicApiPrefix                = "https://chat.openai.com/public-api"
	conversationLimit              = PublicApiPrefix + "/conversation_limit"
	ApiPrefix                      = "https://chat.openai.com/backend-api"
	AnonPrefix                     = "https://chat.openai.com/backend-anon"
	updateMySettingErrorMessage    = "Failed to update setting"
	getMySettingErrorMessage       = "Failed to get setting"
	getSynthesizeErrorMessage      = "Failed to get synthesize."
	getConversationsErrorMessage   = "Failed to get conversations."
	generateTitleErrorMessage      = "Failed to generate title."
	getContentErrorMessage         = "Failed to get content."
	updateConversationErrorMessage = "Failed to update conversation."
	clearConversationsErrorMessage = "Failed to clear conversations."
	feedbackMessageErrorMessage    = "Failed to add feedback."
	getModelsErrorMessage          = "Failed to get models."
	meErrorMessage                 = "Failed to get me"
	promptLibraryErrorMessage      = "Failed to get Prompt Library"
	gizmosErrorMessage             = "Failed to get Gizmos"
	getAccountCheckErrorMessage    = "Check failed." // Placeholder. Never encountered.
	parseJsonErrorMessage          = "Failed to parse json request body."

	gpt4Model     = "gpt-4"
	gpt3dot5Model = "text-davinci-002-render-sha"

	actionContinue                     = "continue"
	actionVariant                      = "variant"
	responseTypeMaxTokens              = "max_tokens"
	responseStatusFinishedSuccessfully = "finished_successfully"
	noModelPermissionErrorMessage      = "you have no permission to use this model"
	WebSocketProtocols                 = "json.reliable.webpubsub.azure.v1"
)
