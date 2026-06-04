package imitate

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	imitateModelCreatedAt = 1710000000
	imitateModelOwner     = "chatgpt"
)

type imitateModelInfo struct {
	ID               string `json:"id"`
	Object           string `json:"object"`
	Created          int64  `json:"created"`
	OwnedBy          string `json:"owned_by"`
	Root             string `json:"root,omitempty"`
	ChatGPTModel     string `json:"chatgpt_model,omitempty"`
	ThinkingEffort   string `json:"thinking_effort,omitempty"`
	RequiresPrepare  bool   `json:"requires_prepare,omitempty"`
	CompatibilityTag string `json:"compatibility_tag,omitempty"`
}

type imitateModelList struct {
	Object string             `json:"object"`
	Data   []imitateModelInfo `json:"data"`
}

var imitateModelCatalogIDs = []string{
	"gpt-5-3",
	"instant",
	"gpt-5.5",
	"gpt-5.5-none",
	"gpt-5.5-low",
	"gpt-5.5-medium",
	"gpt-5.5-high",
	"gpt-5.5-xhigh",
	"gpt-5.5-thinking",
	"gpt-5.5-thinking-low",
	"gpt-5.5-thinking-medium",
	"gpt-5.5-thinking-high",
	"gpt-5.5-thinking-xhigh",
	"thinking",
	"gpt-5.5-pro",
	"gpt-5.5-pro-high",
	"gpt-5.5-pro-xhigh",
	"pro",
	"gpt-5.4",
	"gpt-5.4-pro",
	"gpt-4o",
	"gpt-4o-mini",
	"o1",
	"o1-mini",
	"o3",
	"o4-mini",
	"o4-mini-high",
}

func ListModels(c *gin.Context) {
	debug := modelDebugEnabled(c)
	models := make([]imitateModelInfo, 0, len(imitateModelCatalogIDs))
	for _, id := range imitateModelCatalogIDs {
		model, ok := buildImitateModelInfo(id, debug)
		if ok {
			models = append(models, model)
		}
	}
	c.JSON(http.StatusOK, imitateModelList{
		Object: "list",
		Data:   models,
	})
}

func RetrieveModel(c *gin.Context) {
	rawModel := c.Param("model")
	model, ok := buildImitateModelInfo(rawModel, modelDebugEnabled(c))
	if !ok {
		writeModelNotFound(c, rawModel)
		return
	}
	c.JSON(http.StatusOK, model)
}

func modelDebugEnabled(c *gin.Context) bool {
	debug := c.Query("debug")
	return debug == "1" || strings.EqualFold(debug, "true")
}

func buildImitateModelInfo(rawModel string, debug ...bool) (imitateModelInfo, bool) {
	modelID := normalizeModelToken(rawModel)
	if modelID == "" {
		return imitateModelInfo{}, false
	}
	if !isKnownImitateModel(modelID) {
		return imitateModelInfo{}, false
	}

	resolution, err := resolveImitateModel(modelID, "", "")
	if err != nil || resolution.Model == "" {
		return imitateModelInfo{}, false
	}

	model := imitateModelInfo{
		ID:      modelID,
		Object:  "model",
		Created: imitateModelCreatedAt,
		OwnedBy: imitateModelOwner,
	}
	if len(debug) != 0 && debug[0] {
		model.Root = resolution.Model
		model.ChatGPTModel = resolution.Model
		model.ThinkingEffort = resolution.ThinkingEffort
		model.RequiresPrepare = resolution.RequiresPrepare
		model.CompatibilityTag = imitateModelCompatibilityTag(modelID)
	}
	return model, true
}

func isKnownImitateModel(modelID string) bool {
	spec := parseGPT55ModelSpec(modelID)
	if spec.Family != imitateModelFamilyUnknown {
		_, err := resolveGPT55ModelSpec(spec, "", "")
		return err == nil
	}
	return normalizeLegacyImitateModel(modelID) != ""
}

func imitateModelCompatibilityTag(modelID string) string {
	spec := parseGPT55ModelSpec(modelID)
	switch spec.Family {
	case imitateModelFamilyInstant:
		return "instant"
	case imitateModelFamilyThinking:
		return "thinking"
	case imitateModelFamilyPro:
		return "pro"
	default:
		return "legacy"
	}
}

func writeModelNotFound(c *gin.Context, model string) {
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
		"message": fmt.Sprintf("The model %q does not exist or is not supported by /imitate.", model),
		"type":    "invalid_request_error",
		"param":   "model",
		"code":    "model_not_found",
	}})
}
