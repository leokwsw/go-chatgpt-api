package chatgpt

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/linweiyuan/go-logger/logger"
	"golang.org/x/crypto/sha3"
	"io"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/leokwsw/go-chatgpt-api/api"

	http "github.com/bogdanfinn/fhttp"
)

var (
	answers            = map[string]string{}
	timeLocation, _    = time.LoadLocation("Asia/Shanghai")
	timeLayout         = "Mon Jan 2 2006 15:04:05"
	cachedHardware     = 0
	cachedSid          = uuid.NewString()
	cachedScripts      = []string{}
	cachedDpl          = ""
	cachedRequireProof = ""

	PowRetryTimes    = 0
	PowMaxDifficulty = "000032"
	powMaxCalcTimes  = 500000
	navigatorKeys    = []string{
		"registerProtocolHandler−function registerProtocolHandler() { [native code] }",
		"storage−[object StorageManager]",
		"locks−[object LockManager]",
		"appCodeName−Mozilla",
		"permissions−[object Permissions]",
		"appVersion−5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"share−function share() { [native code] }",
		"webdriver−false",
		"managed−[object NavigatorManagedData]",
		"canShare−function canShare() { [native code] }",
		"vendor−Google Inc.",
		"vendor−Google Inc.",
		"mediaDevices−[object MediaDevices]",
		"vibrate−function vibrate() { [native code] }",
		"storageBuckets−[object StorageBucketManager]",
		"mediaCapabilities−[object MediaCapabilities]",
		"getGamepads−function getGamepads() { [native code] }",
		"bluetooth−[object Bluetooth]",
		"share−function share() { [native code] }",
		"cookieEnabled−true",
		"virtualKeyboard−[object VirtualKeyboard]",
		"product−Gecko",
		"mediaDevices−[object MediaDevices]",
		"canShare−function canShare() { [native code] }",
		"getGamepads−function getGamepads() { [native code] }",
		"product−Gecko",
		"xr−[object XRSystem]",
		"clipboard−[object Clipboard]",
		"storageBuckets−[object StorageBucketManager]",
		"unregisterProtocolHandler−function unregisterProtocolHandler() { [native code] }",
		"productSub−20030107",
		"login−[object NavigatorLogin]",
		"vendorSub−",
		"login−[object NavigatorLogin]",
		"userAgent−Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"getInstalledRelatedApps−function getInstalledRelatedApps() { [native code] }",
		"userAgent−Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"mediaDevices−[object MediaDevices]",
		"locks−[object LockManager]",
		"webkitGetUserMedia−function webkitGetUserMedia() { [native code] }",
		"vendor−Google Inc.",
		"xr−[object XRSystem]",
		"mediaDevices−[object MediaDevices]",
		"virtualKeyboard−[object VirtualKeyboard]",
		"userAgent−Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"virtualKeyboard−[object VirtualKeyboard]",
		"appName−Netscape",
		"storageBuckets−[object StorageBucketManager]",
		"presentation−[object Presentation]",
		"onLine−true",
		"mimeTypes−[object MimeTypeArray]",
		"credentials−[object CredentialsContainer]",
		"presentation−[object Presentation]",
		"getGamepads−function getGamepads() { [native code] }",
		"vendorSub−",
		"virtualKeyboard−[object VirtualKeyboard]",
		"serviceWorker−[object ServiceWorkerContainer]",
		"xr−[object XRSystem]",
		"product−Gecko",
		"keyboard−[object Keyboard]",
		"gpu−[object GPU]",
		"getInstalledRelatedApps−function getInstalledRelatedApps() { [native code] }",
		"webkitPersistentStorage−[object DeprecatedStorageQuota]",
		"doNotTrack",
		"clearAppBadge−function clearAppBadge() { [native code] }",
		"presentation−[object Presentation]",
		"serial−[object Serial]",
		"locks−[object LockManager]",
		"requestMIDIAccess−function requestMIDIAccess() { [native code] }",
		"locks−[object LockManager]",
		"requestMediaKeySystemAccess−function requestMediaKeySystemAccess() { [native code] }",
		"vendor−Google Inc.",
		"pdfViewerEnabled−true",
		"language−zh-CN",
		"setAppBadge−function setAppBadge() { [native code] }",
		"geolocation−[object Geolocation]",
		"userAgentData−[object NavigatorUAData]",
		"mediaCapabilities−[object MediaCapabilities]",
		"requestMIDIAccess−function requestMIDIAccess() { [native code] }",
		"getUserMedia−function getUserMedia() { [native code] }",
		"mediaDevices−[object MediaDevices]",
		"webkitPersistentStorage−[object DeprecatedStorageQuota]",
		"userAgent−Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"sendBeacon−function sendBeacon() { [native code] }",
		"hardwareConcurrency−32",
		"appVersion−5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"credentials−[object CredentialsContainer]",
		"storage−[object StorageManager]",
		"cookieEnabled−true",
		"pdfViewerEnabled−true",
		"windowControlsOverlay−[object WindowControlsOverlay]",
		"scheduling−[object Scheduling]",
		"pdfViewerEnabled−true",
		"hardwareConcurrency−32",
		"xr−[object XRSystem]",
		"userAgent−Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
		"webdriver−false",
		"getInstalledRelatedApps−function getInstalledRelatedApps() { [native code] }",
		"getInstalledRelatedApps−function getInstalledRelatedApps() { [native code] }",
		"bluetooth−[object Bluetooth]"}
	documentKeys = []string{"_reactListeningo743lnnpvdg", "location"}
	windowKeys   = []string{
		"0",
		"window",
		"self",
		"document",
		"name",
		"location",
		"customElements",
		"history",
		"navigation",
		"locationbar",
		"menubar",
		"personalbar",
		"scrollbars",
		"statusbar",
		"toolbar",
		"status",
		"closed",
		"frames",
		"length",
		"top",
		"opener",
		"parent",
		"frameElement",
		"navigator",
		"origin",
		"external",
		"screen",
		"innerWidth",
		"innerHeight",
		"scrollX",
		"pageXOffset",
		"scrollY",
		"pageYOffset",
		"visualViewport",
		"screenX",
		"screenY",
		"outerWidth",
		"outerHeight",
		"devicePixelRatio",
		"clientInformation",
		"screenLeft",
		"screenTop",
		"styleMedia",
		"onsearch",
		"isSecureContext",
		"trustedTypes",
		"performance",
		"onappinstalled",
		"onbeforeinstallprompt",
		"crypto",
		"indexedDB",
		"sessionStorage",
		"localStorage",
		"onbeforexrselect",
		"onabort",
		"onbeforeinput",
		"onbeforematch",
		"onbeforetoggle",
		"onblur",
		"oncancel",
		"oncanplay",
		"oncanplaythrough",
		"onchange",
		"onclick",
		"onclose",
		"oncontentvisibilityautostatechange",
		"oncontextlost",
		"oncontextmenu",
		"oncontextrestored",
		"oncuechange",
		"ondblclick",
		"ondrag",
		"ondragend",
		"ondragenter",
		"ondragleave",
		"ondragover",
		"ondragstart",
		"ondrop",
		"ondurationchange",
		"onemptied",
		"onended",
		"onerror",
		"onfocus",
		"onformdata",
		"oninput",
		"oninvalid",
		"onkeydown",
		"onkeypress",
		"onkeyup",
		"onload",
		"onloadeddata",
		"onloadedmetadata",
		"onloadstart",
		"onmousedown",
		"onmouseenter",
		"onmouseleave",
		"onmousemove",
		"onmouseout",
		"onmouseover",
		"onmouseup",
		"onmousewheel",
		"onpause",
		"onplay",
		"onplaying",
		"onprogress",
		"onratechange",
		"onreset",
		"onresize",
		"onscroll",
		"onsecuritypolicyviolation",
		"onseeked",
		"onseeking",
		"onselect",
		"onslotchange",
		"onstalled",
		"onsubmit",
		"onsuspend",
		"ontimeupdate",
		"ontoggle",
		"onvolumechange",
		"onwaiting",
		"onwebkitanimationend",
		"onwebkitanimationiteration",
		"onwebkitanimationstart",
		"onwebkittransitionend",
		"onwheel",
		"onauxclick",
		"ongotpointercapture",
		"onlostpointercapture",
		"onpointerdown",
		"onpointermove",
		"onpointerrawupdate",
		"onpointerup",
		"onpointercancel",
		"onpointerover",
		"onpointerout",
		"onpointerenter",
		"onpointerleave",
		"onselectstart",
		"onselectionchange",
		"onanimationend",
		"onanimationiteration",
		"onanimationstart",
		"ontransitionrun",
		"ontransitionstart",
		"ontransitionend",
		"ontransitioncancel",
		"onafterprint",
		"onbeforeprint",
		"onbeforeunload",
		"onhashchange",
		"onlanguagechange",
		"onmessage",
		"onmessageerror",
		"onoffline",
		"ononline",
		"onpagehide",
		"onpageshow",
		"onpopstate",
		"onrejectionhandled",
		"onstorage",
		"onunhandledrejection",
		"onunload",
		"crossOriginIsolated",
		"scheduler",
		"alert",
		"atob",
		"blur",
		"btoa",
		"cancelAnimationFrame",
		"cancelIdleCallback",
		"captureEvents",
		"clearInterval",
		"clearTimeout",
		"close",
		"confirm",
		"createImageBitmap",
		"fetch",
		"find",
		"focus",
		"getComputedStyle",
		"getSelection",
		"matchMedia",
		"moveBy",
		"moveTo",
		"open",
		"postMessage",
		"print",
		"prompt",
		"queueMicrotask",
		"releaseEvents",
		"reportError",
		"requestAnimationFrame",
		"requestIdleCallback",
		"resizeBy",
		"resizeTo",
		"scroll",
		"scrollBy",
		"scrollTo",
		"setInterval",
		"setTimeout",
		"stop",
		"structuredClone",
		"webkitCancelAnimationFrame",
		"webkitRequestAnimationFrame",
		"chrome",
		"caches",
		"cookieStore",
		"ondevicemotion",
		"ondeviceorientation",
		"ondeviceorientationabsolute",
		"launchQueue",
		"documentPictureInPicture",
		"getScreenDetails",
		"queryLocalFonts",
		"showDirectoryPicker",
		"showOpenFilePicker",
		"showSaveFilePicker",
		"originAgentCluster",
		"onpageswap",
		"onpagereveal",
		"credentialless",
		"speechSynthesis",
		"onscrollend",
		"webkitRequestFileSystem",
		"webkitResolveLocalFileSystemURL",
		"sendMsgToSolverCS",
		"webpackChunk_N_E",
		"__next_set_public_path__",
		"next",
		"__NEXT_DATA__",
		"__SSG_MANIFEST_CB",
		"__NEXT_P",
		"_N_E",
		"regeneratorRuntime",
		"__REACT_INTL_CONTEXT__",
		"DD_RUM",
		"_",
		"filterCSS",
		"filterXSS",
		"__SEGMENT_INSPECTOR__",
		"__NEXT_PRELOADREADY",
		"Intercom",
		"__MIDDLEWARE_MATCHERS",
		"__STATSIG_SDK__",
		"__STATSIG_JS_SDK__",
		"__STATSIG_RERENDER_OVERRIDE__",
		"_oaiHandleSessionExpired",
		"__BUILD_MANIFEST",
		"__SSG_MANIFEST",
		"__intercomAssignLocation",
		"__intercomReloadLocation"}
)

func init() {
	cores := []int{8, 12, 16, 24}
	screens := []int{3000, 4000, 6000}
	rand.New(rand.NewSource(time.Now().UnixNano()))
	core := cores[rand.Intn(4)]
	rand.New(rand.NewSource(time.Now().UnixNano()))
	screen := screens[rand.Intn(3)]
	cachedHardware = core + screen
	envHardware := os.Getenv("HARDWARE")
	if envHardware != "" {
		intValue, err := strconv.Atoi(envHardware)
		if err != nil {
			logger.Error(fmt.Sprintf("Error converting %s to integer: %v", envHardware, err))
		} else {
			cachedHardware = intValue
			logger.Info(fmt.Sprintf("cachedHardware is set to : %d", cachedHardware))
		}
	}
	envPowRetryTimes := os.Getenv("POW_RETRY_TIMES")
	if envPowRetryTimes != "" {
		intValue, err := strconv.Atoi(envPowRetryTimes)
		if err != nil {
			logger.Error(fmt.Sprintf("Error converting %s to integer: %v", envPowRetryTimes, err))
		} else {
			PowRetryTimes = intValue
			logger.Info(fmt.Sprintf("PowRetryTimes is set to : %d", PowRetryTimes))
		}
	}
	envpowMaxDifficulty := os.Getenv("POW_MAX_DIFFICULTY")
	if envpowMaxDifficulty != "" {
		PowMaxDifficulty = envpowMaxDifficulty
		logger.Info(fmt.Sprintf("PowMaxDifficulty is set to : %s", PowMaxDifficulty))
	}
	envPowMaxCalcTimes := os.Getenv("POW_MAX_CALC_TIMES")
	if envPowMaxCalcTimes != "" {
		intValue, err := strconv.Atoi(envPowMaxCalcTimes)
		if err != nil {
			logger.Error(fmt.Sprintf("Error converting %s to integer: %v", envPowMaxCalcTimes, err))
		} else {
			powMaxCalcTimes = intValue
			logger.Info(fmt.Sprintf("PowMaxCalcTimes is set to : %d", powMaxCalcTimes))
		}
	}
}

func UpdateUserSetting(c *gin.Context) {
	feature, ok := c.GetQuery("feature")
	if !ok {
		return
	}
	value, ok := c.GetQuery("value")
	if !ok {
		return
	}
	handlePatch(c, ApiPrefix+"/settings/account_user_setting?feature="+feature+"&value="+value, "{}", updateMySettingErrorMessage)
}

func GetUserSetting(c *gin.Context) {
	handleGet(c, ApiPrefix+"/settings/user", getMySettingErrorMessage)
}

func GetSynthesize(c *gin.Context) {
	conversationId, ok := c.GetQuery("conversation_id")
	if !ok {
		return
	}
	messageId, ok := c.GetQuery("message_id")
	if !ok {
		return
	}
	voice, ok := c.GetQuery("voice")
	if !ok {
		voice = "cove"
	}
	format, ok := c.GetQuery("format")
	if !ok {
		format = "aac"
	}
	handleGet(c, ApiPrefix+"/synthesize?conversation_id="+conversationId+"&message_id="+messageId+"&voice="+voice+"&format="+format, getSynthesizeErrorMessage)
}

func GetConversations(c *gin.Context) {
	offset, ok := c.GetQuery("offset")
	if !ok {
		offset = "0"
	}
	limit, ok := c.GetQuery("limit")
	if !ok {
		limit = "20"
	}
	order, ok := c.GetQuery("order")
	if !ok {
		order = "updated"
	}
	isArchived, ok := c.GetQuery("is_archived")
	if !ok {
		isArchived = "false"
	}
	handleGet(c, ApiPrefix+"/conversations?offset="+offset+"&limit="+limit+"&order="+order+"&is_archived="+isArchived, getConversationsErrorMessage)
}

func CreateConversation(c *gin.Context) {
	var request CreateConversationRequest
	var apiVersion int
	uid := uuid.NewString()

	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	if len(request.Messages) != 0 {
		message := request.Messages[0]
		if message.Author.Role == "" {
			message.Author.Role = defaultRole
		}

		if message.Metadata == nil {
			message.Metadata = map[string]string{}
		}

		request.Messages[0] = message
	}

	if strings.HasPrefix(request.Model, gpt4Model) {
		apiVersion = 4
	} else {
		apiVersion = 3
	}

	chatRequirements, p, err := GetChatRequirementsByGin(c, uid)

	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	if chatRequirements == nil {
		logger.Error("unable to check chat requirements")
		return
	}

	for i := 0; i < PowRetryTimes; i++ {
		if chatRequirements.Proof.Required && chatRequirements.Proof.Difficulty <= PowMaxDifficulty {
			logger.Warn(fmt.Sprintf("Proof of work difficulty too high: %s. Retrying... %d/%d ", chatRequirements.Proof.Difficulty, i+1, PowRetryTimes))
			chatRequirements, _, _ = GetChatRequirementsByGin(c, api.OAIDID)
			if chatRequirements == nil {
				logger.Error("unable to check chat requirements")
				return
			}
		} else {
			break
		}
	}

	var arkoseToken string
	arkoseToken = c.GetHeader(api.ArkoseTokenHeader)
	if chatRequirements.Arkose.Required == true && arkoseToken == "" {
		token, err := api.GetArkoseToken(apiVersion, chatRequirements.Arkose.Dx)
		arkoseToken = token
		if err != nil || arkoseToken == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, api.ReturnMessage(err.Error()))
			return
		}
	}

	if c.GetHeader(api.AuthorizationHeader) == "" {
		request.Model = gpt3dot5Model
	}

	var proofToken string
	if chatRequirements.Proof.Required {
		proofToken = CalcProofToken(chatRequirements)
	}

	var turnstileToken string
	if chatRequirements.Turnstile.Required {
		turnstileToken = ProcessTurnstile(chatRequirements.Turnstile.DX, p)
	}
	resp, done := sendConversationRequest(c, request, chatRequirements.Token, uid, arkoseToken, proofToken, turnstileToken)
	if done {
		return
	}
	handleConversationResponse(c, resp, request, uid)
}

func sendConversationRequest(c *gin.Context, request CreateConversationRequest, chatRequirementsToken string, uid string, arkoseToken string, proofToken string, turnstileToken string) (*http.Response, bool) {
	jsonBytes, _ := json.Marshal(request)

	urlPrefix := ""

	if c.GetHeader(api.AuthorizationHeader) == "" {
		urlPrefix = AnonPrefix
	} else {
		urlPrefix = ApiPrefix
	}
	req, _ := http.NewRequest(http.MethodPost, urlPrefix+"/conversation", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", api.UserAgent)
	if urlPrefix == ApiPrefix {
		req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	}
	req.Header.Set("Accept", "text/event-stream")
	if arkoseToken != "" {
		req.Header.Set("Openai-Sentinel-Arkose-Token", arkoseToken)
	}
	if chatRequirementsToken != "" {
		req.Header.Set("Openai-Sentinel-Chat-Requirements-Token", chatRequirementsToken)
	}
	if proofToken != "" {
		req.Header.Set("Openai-Sentinel-Proof-Token", proofToken)
	}
	if turnstileToken != "" {
		req.Header.Set("Openai-Sentinel-Turnstile-Token", turnstileToken)
	}
	req.Header.Set("Origin", api.ChatGPTApiUrlPrefix)
	if request.ConversationID != "" {
		req.Header.Set("Referer", api.ChatGPTApiUrlPrefix+"/c/"+request.ConversationID)
	} else {
		req.Header.Set("Referer", api.ChatGPTApiUrlPrefix+"/")
	}

	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	req.Header.Set("Oai-Language", api.Language)
	if urlPrefix == ApiPrefix {
		if api.OAIDID != "" {
			req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
			req.Header.Set("Oai-Device-Id", api.OAIDID)
		}
	} else {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+uid+";")
		req.Header.Set("Oai-Device-Id", uid)
	}
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return nil, true
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			logger.Error(fmt.Sprintf(api.AccountDeactivatedErrorMessage, c.GetString(api.EmailKey)))
			responseMap := make(map[string]interface{})
			json.NewDecoder(resp.Body).Decode(&responseMap)
			c.AbortWithStatusJSON(resp.StatusCode, responseMap)
			return nil, true
		}

		req, _ := http.NewRequest(http.MethodGet, urlPrefix+"/models", nil)
		req.Header.Set("User-Agent", api.UserAgent)
		if urlPrefix == ApiPrefix {
			req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
			if api.OAIDID != "" {
				req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
				req.Header.Set("Oai-Device-Id", api.OAIDID)
			}
		} else {
			req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+uid+";")
			req.Header.Set("Oai-Device-Id", uid)
		}
		response, err := api.Client.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
			return nil, true
		}

		defer response.Body.Close()
		modelAvailable := false
		var getModelsResponse GetModelsResponse
		json.NewDecoder(response.Body).Decode(&getModelsResponse)
		for _, model := range getModelsResponse.Models {
			if model.Slug == request.Model {
				modelAvailable = true
				break
			}
		}
		if !modelAvailable {
			c.AbortWithStatusJSON(http.StatusForbidden, api.ReturnMessage(noModelPermissionErrorMessage))
			return nil, true
		}

		fmt.Printf("OpenAI Request Method : %s ; url : %s ; Status : %d\n\n", http.MethodPost, urlPrefix+"/conversation", resp.StatusCode)
		responseMap := make(map[string]interface{})
		json.NewDecoder(resp.Body).Decode(&responseMap)

		fmt.Println(responseMap)
		c.AbortWithStatusJSON(resp.StatusCode, responseMap)
		return nil, true
	}

	return resp, false
}

func handleConversationResponse(c *gin.Context, resp *http.Response, request CreateConversationRequest, uid string) {
	c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

	isMaxTokens := false
	continueParentMessageID := ""
	continueConversationID := ""

	var arkoseToken string
	var proofToken string
	var turnstileToken string

	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	for {
		if c.Request.Context().Err() != nil {
			break
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event") ||
			strings.HasPrefix(line, "data: 20") ||
			line == "" {
			continue
		}

		responseJson := line[6:]
		if strings.HasPrefix(responseJson, "[DONE]") && isMaxTokens {
			continue
		}

		// no need to unmarshal every time, but if response content has this "max_tokens", need to further check
		if strings.TrimSpace(responseJson) != "" && strings.Contains(responseJson, responseTypeMaxTokens) {
			var createConversationResponse CreateConversationResponse
			json.Unmarshal([]byte(responseJson), &createConversationResponse)
			message := createConversationResponse.Message
			if message.Metadata.FinishDetails.Type == responseTypeMaxTokens && createConversationResponse.Message.Status == responseStatusFinishedSuccessfully {
				isMaxTokens = true
				continueParentMessageID = message.ID
				continueConversationID = createConversationResponse.ConversationID
			}
		}

		c.Writer.Write([]byte(line + "\n\n"))
		c.Writer.Flush()
	}

	if isMaxTokens {
		var continueConversationRequest = CreateConversationRequest{
			ConversationMode:           request.ConversationMode,
			ForceNulligen:              request.ForceNulligen,
			ForceParagen:               request.ForceParagen,
			ForceParagenModelSlug:      request.ForceParagenModelSlug,
			ForceRateLimit:             request.ForceRateLimit,
			ForceUseSse:                request.ForceUseSse,
			HistoryAndTrainingDisabled: request.HistoryAndTrainingDisabled,
			Model:                      request.Model,
			ResetRateLimits:            request.ResetRateLimits,
			TimezoneOffsetMin:          request.TimezoneOffsetMin,

			Action:             actionContinue,
			ParentMessageID:    continueParentMessageID,
			ConversationID:     continueConversationID,
			WebSocketRequestId: uuid.NewString(),
		}
		//RenewTokenForRequest(&request, chatRequirementsArkoseDx)
		chatRequirements, p, _ := GetChatRequirementsByAccessToken(
			api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)),
			uid,
		)

		if chatRequirements == nil {
			logger.Error("unable to check chat requirements")
			return
		}

		for i := 0; i < PowRetryTimes; i++ {
			if chatRequirements.Proof.Required && chatRequirements.Proof.Difficulty <= PowMaxDifficulty {
				logger.Warn(fmt.Sprintf("Proof of work difficulty too high: %s. Retrying... %d/%d ", chatRequirements.Proof.Difficulty, i+1, PowRetryTimes))
				chatRequirements, _, _ = GetChatRequirementsByGin(c, api.OAIDID)
				if chatRequirements == nil {
					logger.Error("unable to check chat requirements")
					return
				}
			} else {
				break
			}
		}
		if chatRequirements.Proof.Required {
			proofToken = CalcProofToken(chatRequirements)
		}
		if chatRequirements.Turnstile.Required {
			turnstileToken = ProcessTurnstile(chatRequirements.Turnstile.DX, p)
		}

		if chatRequirements.Arkose.Required {
			token, err := GetArkoseTokenForModel(continueConversationRequest.Model, chatRequirements.Arkose.Dx)
			arkoseToken = token
			if err != nil {
				c.AbortWithStatusJSON(http.StatusForbidden, api.ReturnMessage(err.Error()))
				return
			}
		}
		resp, done := sendConversationRequest(c, continueConversationRequest, chatRequirements.Token, uid, arkoseToken, proofToken, turnstileToken)
		if done {
			return
		}

		handleConversationResponse(c, resp, continueConversationRequest, uid)
	}
}

func GenerateTitle(c *gin.Context) {
	var request GenerateTitleRequest
	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	jsonBytes, _ := json.Marshal(request)
	handlePost(c, ApiPrefix+"/conversation/gen_title/"+c.Param("id"), string(jsonBytes), generateTitleErrorMessage)
}

func GetConversation(c *gin.Context) {
	handleGet(c, ApiPrefix+"/conversation/"+c.Param("id"), getContentErrorMessage)
}

func UpdateConversation(c *gin.Context) {
	var request PatchConversationRequest
	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	// bool default to false, then will hide (delete) the conversation
	if request.Title != nil {
		request.IsVisible = true
	}
	jsonBytes, _ := json.Marshal(request)
	handlePatch(c, ApiPrefix+"/conversation/"+c.Param("id"), string(jsonBytes), updateConversationErrorMessage)
}

func FeedbackMessage(c *gin.Context) {
	var request FeedbackMessageRequest
	if err := c.BindJSON(&request); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, api.ReturnMessage(parseJsonErrorMessage))
		return
	}

	jsonBytes, _ := json.Marshal(request)
	handlePost(c, ApiPrefix+"/conversation/message_feedback", string(jsonBytes), feedbackMessageErrorMessage)
}

func ClearConversations(c *gin.Context) {
	jsonBytes, _ := json.Marshal(PatchConversationRequest{
		IsVisible: false,
	})
	handlePatch(c, ApiPrefix+"/conversations", string(jsonBytes), clearConversationsErrorMessage)
}

func GetModels(c *gin.Context) {
	handleGet(c, ApiPrefix+"/models", getModelsErrorMessage)
}

func GetAccountCheck(c *gin.Context) {
	handleGet(c, ApiPrefix+"/accounts/check/v4-2023-04-27", getAccountCheckErrorMessage)
}

func GetMe(c *gin.Context) {
	handleGet(c, ApiPrefix+"/me", meErrorMessage)
}

func GetPromptLibrary(c *gin.Context) {

	limit, ok := c.GetQuery("limit")
	if !ok {
		limit = "4"
	}

	offset, ok := c.GetQuery("offset")
	if !ok {
		offset = "0"
	}

	handleGet(c, ApiPrefix+"/prompt_library/?limit="+limit+"&offset="+offset, promptLibraryErrorMessage)
}

func GetGizmos(c *gin.Context) {
	limit, ok := c.GetQuery("limit")
	if !ok {
		limit = "4"
	}

	handleGet(c, ApiPrefix+"/gizmos/bootstrap?limit="+limit, gizmosErrorMessage)

}

func handleNoAuthGet(c *gin.Context, url string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		c.AbortWithStatusJSON(http.StatusBadGateway, api.ReturnMessage(errorMessage))
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func handleGet(c *gin.Context, url string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatusJSON(resp.StatusCode, api.ReturnMessage(errorMessage))
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func handlePost(c *gin.Context, url string, requestBody string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(requestBody))
	handlePostOrPatch(c, req, errorMessage)
}

func handlePatch(c *gin.Context, url string, requestBody string, errorMessage string) {
	req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(requestBody))
	handlePostOrPatch(c, req, errorMessage)
}

func handlePostOrPatch(c *gin.Context, req *http.Request, errorMessage string) {
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)))
	resp, err := api.Client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, api.ReturnMessage(err.Error()))
		return
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.AbortWithStatusJSON(resp.StatusCode, api.ReturnMessage(errorMessage))
		return
	}

	io.Copy(c.Writer, resp.Body)
}

func GetChatRequirementsByGin(c *gin.Context, uid string) (*ChatRequirements, string, error) {

	chatRequirements, p, err := GetChatRequirementsByAccessToken(api.GetAccessToken(c.GetHeader(api.AuthorizationHeader)), uid)

	if err != nil {
		return nil, "", err
	}

	return chatRequirements, p, nil
}

func GetChatRequirementsByAccessToken(accessToken string, uid string) (*ChatRequirements, string, error) {

	urlPrefix := ""

	if accessToken == "Bearer " || accessToken == "" {
		urlPrefix = AnonPrefix
	} else {
		urlPrefix = ApiPrefix
	}

	if cachedRequireProof == "" {
		cachedRequireProof = "gAAAAAC" + generateAnswer(strconv.FormatFloat(rand.Float64(), 'f', -1, 64), "0")
	}

	req, _ := http.NewRequest(
		http.MethodPost,
		urlPrefix+"/sentinel/chat-requirements",
		bytes.NewBuffer([]byte(`{"conversation_mode_kind":"primary_assistant","p":"`+"gAAAAAC"+cachedRequireProof+`"}`)),
	)

	if api.PUID != "" {
		req.Header.Set("Cookie", "_puid="+api.PUID+";")
	}
	req.Header.Set("Oai-Language", api.Language)
	if urlPrefix == ApiPrefix {
		if api.OAIDID != "" {
			req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+api.OAIDID+";")
			req.Header.Set("Oai-Device-Id", api.OAIDID)
		}
	} else {
		req.Header.Set("Cookie", req.Header.Get("Cookie")+"oai-did="+uid+";")
		req.Header.Set("Oai-Device-Id", uid)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", api.UserAgent)
	if urlPrefix == ApiPrefix {
		req.Header.Set(api.AuthorizationHeader, accessToken)
	}

	res, err := api.Client.Do(req)

	if err != nil {
		return nil, "", err
	}

	defer res.Body.Close()
	var require ChatRequirements
	err = json.NewDecoder(res.Body).Decode(&require)
	if err != nil {
		return nil, "", err
	}
	return &require, cachedRequireProof, nil
}

func Ping(c *gin.Context) {
	handleNoAuthGet(c, conversationLimit, "error")
}

// region Proof Token

type ProofWork struct {
	Difficulty string `json:"difficulty,omitempty"`
	Required   bool   `json:"required"`
	Seed       string `json:"seed,omitempty"`
}

func getParseTime() string {
	now := time.Now()
	now = now.In(timeLocation)
	return now.Format(timeLayout) + " GMT+0800 (中国标准时间)"
}

func getConfig() []interface{} {
	rand.New(rand.NewSource(time.Now().UnixNano()))
	script := cachedScripts[rand.Intn(len(cachedScripts))]
	timeNum := (float64(time.Since(api.StartTime).Nanoseconds()) + rand.Float64()) / 1e6
	rand.New(rand.NewSource(time.Now().UnixNano()))
	navigatorKey := navigatorKeys[rand.Intn(len(navigatorKeys))]
	rand.New(rand.NewSource(time.Now().UnixNano()))
	documentKey := documentKeys[rand.Intn(len(documentKeys))]
	rand.New(rand.NewSource(time.Now().UnixNano()))
	windowKey := windowKeys[rand.Intn(len(windowKeys))]
	return []interface{}{cachedHardware, getParseTime(), int64(4294705152), 0, api.UserAgent, script, cachedDpl, api.Language, api.Language + "," + api.Language[:2], 0, navigatorKey, documentKey, windowKey, timeNum, cachedSid}
}

func CalcProofToken(require *ChatRequirements) string {
	start := time.Now()
	proof := generateAnswer(require.Proof.Seed, require.Proof.Difficulty)
	elapsed := time.Since(start)
	// POW logging
	logger.Info(fmt.Sprintf("POW Difficulty: %s , took %v ms", require.Proof.Difficulty, elapsed.Milliseconds()))
	return "gAAAAAB" + proof
}

func generateAnswer(seed string, diff string) string {
	GetDpl()
	config := getConfig()
	diffLen := len(diff)
	hasher := sha3.New512()
	for i := 0; i < powMaxCalcTimes; i++ {
		config[3] = i
		config[9] = (i + 2) / 2
		json, _ := json.Marshal(config)
		base := base64.StdEncoding.EncodeToString(json)
		hasher.Write([]byte(seed + base))
		hash := hasher.Sum(nil)
		hasher.Reset()
		if hex.EncodeToString(hash[:diffLen])[:diffLen] <= diff {
			return base
		}
	}
	return "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64.StdEncoding.EncodeToString([]byte(`"`+seed+`"`))
}

func GetDpl() {
	if len(cachedScripts) > 0 {
		return
	}
	cachedScripts = append(cachedScripts, "https://cdn.oaistatic.com/_next/static/cXh69klOLzS0Gy2joLDRS/_ssgManifest.js?dpl=453ebaec0d44c2decab71692e1bfe39be35a24b3")
	cachedDpl = "dpl=453ebaec0d44c2decab71692e1bfe39be35a24b3"
	request, err := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
	request.Header.Set("User-Agent", api.UserAgent)
	request.Header.Set("Accept", "*/*")
	if err != nil {
		return
	}
	response, err := api.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	doc, _ := goquery.NewDocumentFromReader(response.Body)
	scripts := []string{}
	doc.Find("script[src]").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if exists {
			scripts = append(scripts, src)
			if cachedDpl == "" {
				idx := strings.Index(src, "dpl")
				if idx >= 0 {
					cachedDpl = src[idx:]
				}
			}
		}
	})
	if len(scripts) != 0 {
		cachedScripts = scripts
	}
}

// endregion

func GetArkoseTokenForModel(model string, dx string) (string, error) {
	var api_version int
	if strings.HasPrefix(model, "gpt-4") {
		api_version = 4
	} else {
		api_version = 3
	}
	return api.GetArkoseToken(api_version, dx)
}
