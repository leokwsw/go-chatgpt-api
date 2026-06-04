package imitate

import (
	"fmt"
	"strings"
)

type imitateModelFamily string

const (
	imitateModelFamilyUnknown  imitateModelFamily = ""
	imitateModelFamilyInstant  imitateModelFamily = "instant"
	imitateModelFamilyThinking imitateModelFamily = "thinking"
	imitateModelFamilyPro      imitateModelFamily = "pro"
)

type imitateModelSpec struct {
	Family       imitateModelFamily
	EffortSuffix string
}

type imitateModelResolution struct {
	Model                  string
	ThinkingEffort         string
	RequiresPrepare        bool
	AllowEmptyConduitToken bool
}

func resolveImitateModel(rawModel string, reasoningEffort string, thinkingEffort string) (imitateModelResolution, error) {
	spec := parseGPT55ModelSpec(rawModel)
	if spec.Family != imitateModelFamilyUnknown {
		return resolveGPT55ModelSpec(spec, reasoningEffort, thinkingEffort)
	}

	resolution := imitateModelResolution{
		Model: normalizeLegacyImitateModel(rawModel),
	}
	effort, hasEffort, err := resolveExplicitStandardEffort(imitateModelFamilyUnknown, reasoningEffort, thinkingEffort)
	if err != nil {
		return resolution, err
	}
	if hasEffort {
		resolution.ThinkingEffort = standardEffortToGenericWireEffort(effort)
		resolution.RequiresPrepare = resolution.ThinkingEffort != ""
	}
	return resolution, nil
}

func resolveGPT55ModelSpec(spec imitateModelSpec, reasoningEffort string, thinkingEffort string) (imitateModelResolution, error) {
	explicitEffort, hasExplicitEffort, err := resolveExplicitStandardEffort(spec.Family, reasoningEffort, thinkingEffort)
	if err != nil {
		return imitateModelResolution{}, err
	}

	modelEffort := ""
	if spec.EffortSuffix != "" {
		modelEffort, err = normalizeExternalEffort(spec.EffortSuffix, spec.Family)
		if err != nil {
			return imitateModelResolution{}, err
		}
	}

	effort := modelEffort
	if hasExplicitEffort {
		effort = explicitEffort
	}

	switch spec.Family {
	case imitateModelFamilyInstant:
		if effort != "" && effort != "none" {
			return imitateModelResolution{}, fmt.Errorf("instant model does not support reasoning_effort %q", effort)
		}
		return imitateModelResolution{Model: "gpt-5-3"}, nil
	case imitateModelFamilyThinking:
		if effort == "" {
			effort = "medium"
		}
		if effort == "none" {
			return imitateModelResolution{Model: "gpt-5-3"}, nil
		}
		wireEffort, err := standardEffortToThinkingWireEffort(effort)
		if err != nil {
			return imitateModelResolution{}, err
		}
		return imitateModelResolution{
			Model:           "gpt-5-5-thinking",
			ThinkingEffort:  wireEffort,
			RequiresPrepare: true,
		}, nil
	case imitateModelFamilyPro:
		if effort == "" {
			effort = "high"
		}
		wireEffort, err := standardEffortToProWireEffort(effort)
		if err != nil {
			return imitateModelResolution{}, err
		}
		return imitateModelResolution{
			Model:                  "gpt-5-5-pro",
			ThinkingEffort:         wireEffort,
			RequiresPrepare:        true,
			AllowEmptyConduitToken: true,
		}, nil
	default:
		return imitateModelResolution{}, fmt.Errorf("unsupported model family %q", spec.Family)
	}
}

func resolveExplicitStandardEffort(family imitateModelFamily, reasoningEffort string, thinkingEffort string) (string, bool, error) {
	reasoning, hasReasoning, err := normalizeOptionalExternalEffort(reasoningEffort, family)
	if err != nil {
		return "", false, fmt.Errorf("invalid reasoning_effort: %w", err)
	}
	thinking, hasThinking, err := normalizeOptionalExternalEffort(thinkingEffort, family)
	if err != nil {
		return "", false, fmt.Errorf("invalid thinking_effort: %w", err)
	}
	if hasReasoning && hasThinking && reasoning != thinking {
		return "", false, fmt.Errorf("reasoning_effort %q conflicts with thinking_effort %q", reasoningEffort, thinkingEffort)
	}
	if hasReasoning {
		return reasoning, true, nil
	}
	if hasThinking {
		return thinking, true, nil
	}
	return "", false, nil
}

func normalizeOptionalExternalEffort(raw string, family imitateModelFamily) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return "", false, nil
	}
	effort, err := normalizeExternalEffort(raw, family)
	return effort, true, err
}

func normalizeExternalEffort(raw string, family imitateModelFamily) (string, error) {
	effort := normalizeModelToken(raw)
	switch effort {
	case "none", "minimal":
		return "none", nil
	case "min", "low":
		return "low", nil
	case "medium", "med":
		return "medium", nil
	case "high":
		return "high", nil
	case "xhigh", "x-high", "extra-high", "extra high", "max", "heavy":
		return "xhigh", nil
	case "standard":
		if family == imitateModelFamilyPro {
			return "high", nil
		}
		return "medium", nil
	case "extended":
		if family == imitateModelFamilyPro {
			return "xhigh", nil
		}
		return "high", nil
	default:
		return "", fmt.Errorf("unsupported effort %q", raw)
	}
}

func standardEffortToThinkingWireEffort(effort string) (string, error) {
	switch effort {
	case "low":
		return "min", nil
	case "medium":
		return "standard", nil
	case "high":
		return "extended", nil
	case "xhigh":
		return "max", nil
	default:
		return "", fmt.Errorf("thinking model does not support reasoning_effort %q", effort)
	}
}

func standardEffortToProWireEffort(effort string) (string, error) {
	switch effort {
	case "high":
		return "standard", nil
	case "xhigh":
		return "extended", nil
	default:
		return "", fmt.Errorf("pro model only supports reasoning_effort high or xhigh")
	}
}

func standardEffortToGenericWireEffort(effort string) string {
	switch effort {
	case "none":
		return ""
	case "low":
		return "min"
	case "medium":
		return "standard"
	case "high":
		return "extended"
	case "xhigh":
		return "max"
	default:
		return effort
	}
}

func parseGPT55ModelSpec(raw string) imitateModelSpec {
	model := normalizeModelToken(raw)
	if model == "" {
		return imitateModelSpec{}
	}

	switch {
	case model == "instant" || model == "none" || strings.HasPrefix(model, "gpt-5-3") || strings.HasPrefix(model, "gpt-5.3") || model == "gpt-5-5-none" || model == "gpt-5.5-none":
		return imitateModelSpec{Family: imitateModelFamilyInstant}
	case model == "pro":
		return imitateModelSpec{Family: imitateModelFamilyPro}
	case strings.HasPrefix(model, "pro-"):
		return imitateModelSpec{Family: imitateModelFamilyPro, EffortSuffix: strings.TrimPrefix(model, "pro-")}
	case model == "thinking":
		return imitateModelSpec{Family: imitateModelFamilyThinking}
	case strings.HasPrefix(model, "thinking-"):
		return imitateModelSpec{Family: imitateModelFamilyThinking, EffortSuffix: strings.TrimPrefix(model, "thinking-")}
	}

	for _, prefix := range []string{"gpt-5-5-pro", "gpt-5.5-pro"} {
		if model == prefix {
			return imitateModelSpec{Family: imitateModelFamilyPro}
		}
		if strings.HasPrefix(model, prefix+"-") {
			return imitateModelSpec{Family: imitateModelFamilyPro, EffortSuffix: strings.TrimPrefix(model, prefix+"-")}
		}
	}

	for _, prefix := range []string{"gpt-5-5-thinking", "gpt-5.5-thinking"} {
		if model == prefix {
			return imitateModelSpec{Family: imitateModelFamilyThinking}
		}
		if strings.HasPrefix(model, prefix+"-") {
			return imitateModelSpec{Family: imitateModelFamilyThinking, EffortSuffix: strings.TrimPrefix(model, prefix+"-")}
		}
	}

	for _, prefix := range []string{"gpt-5-5", "gpt-5.5"} {
		if model == prefix {
			return imitateModelSpec{Family: imitateModelFamilyThinking}
		}
		if strings.HasPrefix(model, prefix+"-") {
			suffix := strings.TrimPrefix(model, prefix+"-")
			if suffix == "none" {
				return imitateModelSpec{Family: imitateModelFamilyInstant}
			}
			return imitateModelSpec{Family: imitateModelFamilyThinking, EffortSuffix: suffix}
		}
	}

	return imitateModelSpec{}
}

func normalizeLegacyImitateModel(raw string) string {
	model := normalizeModelToken(raw)
	switch {
	case strings.HasPrefix(model, "gpt-5.4-pro") || strings.HasPrefix(model, "gpt-5-4-pro"):
		return "gpt-5-4-pro"
	case strings.HasPrefix(model, "gpt-5.4") || strings.HasPrefix(model, "gpt-5-4"):
		return "gpt-5-4-thinking"
	case strings.HasPrefix(model, "gpt-4o-mini"):
		return "gpt-4o-mini"
	case strings.HasPrefix(model, "gpt-4o"):
		return "gpt-4o"
	case strings.HasPrefix(model, "o1-mini"):
		return "o1-mini"
	case strings.HasPrefix(model, "o1"):
		return "o1"
	case strings.HasPrefix(model, "o3"):
		return "o3"
	case strings.HasPrefix(model, "o4-mini-high"):
		return "o4-mini-high"
	case strings.HasPrefix(model, "o4-mini"):
		return "o4-mini"
	default:
		return ""
	}
}

func normalizeModelToken(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	return normalized
}
