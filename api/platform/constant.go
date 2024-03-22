package platform

import "github.com/linweiyuan/go-chatgpt-api/api"

const (
	apiListModels             = api.PlatformApiUrlPrefix + "/v1/models"
	apiRetrieveModel          = api.PlatformApiUrlPrefix + "/v1/models/%s"
	apiCreateCompletions      = api.PlatformApiUrlPrefix + "/v1/completions"
	apiCreataeChatCompletions = api.PlatformApiUrlPrefix + "/v1/chat/completions"
	apiCreateEdit             = api.PlatformApiUrlPrefix + "/v1/edits"
	apiCreateImage            = api.PlatformApiUrlPrefix + "/v1/images/generations"
	apiCreateEmbeddings       = api.PlatformApiUrlPrefix + "/v1/embeddings"
	apiListFiles              = api.PlatformApiUrlPrefix + "/v1/files"
	apiCreateModeration       = api.PlatformApiUrlPrefix + "/v1/moderations"

	apiGetCreditGrants = api.PlatformApiUrlPrefix + "/dashboard/billing/credit_grants"
	apiGetSubscription = api.PlatformApiUrlPrefix + "/dashboard/billing/subscription"
	apiGetApiKeys      = api.PlatformApiUrlPrefix + "/dashboard/user/api_keys"

	platformAuthClientID      = "DRivsnm2Mu42T3KOpqdtwB3NYviHYzwD"
	platformAuthAudience      = "https://api.openai.com/v1"
	platformAuthRedirectURL   = "https://platform.openai.com/auth/callback"
	platformAuthScope         = "openid profile email offline_access"
	platformAuthResponseType  = "code"
	platformAuthGrantType     = "authorization_code"
	platformAuth0Url          = api.Auth0Url + "/authorize?"
	getTokenUrl               = api.Auth0Url + "/oauth/token"
	auth0Client               = "eyJuYW1lIjoiYXV0aDAtc3BhLWpzIiwidmVyc2lvbiI6IjEuMjEuMCJ9" // '{"name":"auth0-spa-js","version":"1.21.0"}'
	auth0LogoutUrl            = api.Auth0Url + "/v2/logout?returnTo=https%3A%2F%2Fplatform.openai.com%2Floggedout&client_id=" + platformAuthClientID + "&auth0Client=" + auth0Client
	dashboardLoginUrl         = "https://api.openai.com/dashboard/onboarding/login"
	getSessionKeyErrorMessage = "Failed to get session key."
)
