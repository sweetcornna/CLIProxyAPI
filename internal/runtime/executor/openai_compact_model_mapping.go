package executor

import (
	"encoding/json"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var openAICompactModelMappingKeys = []string{
	"openai_compact_model_mapping",
	"compact_model_mapping",
}

func applyOpenAICompactModelMapping(body []byte, auth *cliproxyauth.Auth, fallbackModel string) []byte {
	upstreamModel := mappedOpenAICompactModel(auth, requestBodyModel(body, fallbackModel))
	if upstreamModel == "" {
		return body
	}
	updated, err := sjson.SetBytes(body, "model", upstreamModel)
	if err != nil {
		return body
	}
	return updated
}

func requestBodyModel(body []byte, fallback string) string {
	if model := strings.TrimSpace(gjson.GetBytes(body, "model").String()); model != "" {
		return model
	}
	return strings.TrimSpace(fallback)
}

func mappedOpenAICompactModel(auth *cliproxyauth.Auth, model string) string {
	model = strings.TrimSpace(model)
	if auth == nil || model == "" {
		return ""
	}
	for _, mapping := range openAICompactModelMappings(auth) {
		if upstream := strings.TrimSpace(mapping[model]); upstream != "" {
			return upstream
		}
	}
	return ""
}

func openAICompactModelMappings(auth *cliproxyauth.Auth) []map[string]string {
	if auth == nil {
		return nil
	}
	mappings := make([]map[string]string, 0, 2)
	if len(auth.Attributes) > 0 {
		for _, key := range openAICompactModelMappingKeys {
			if mapping := parseOpenAICompactModelMapping(auth.Attributes[key]); len(mapping) > 0 {
				mappings = append(mappings, mapping)
			}
		}
	}
	if len(auth.Metadata) > 0 {
		for _, key := range openAICompactModelMappingKeys {
			if mapping := parseOpenAICompactModelMapping(auth.Metadata[key]); len(mapping) > 0 {
				mappings = append(mappings, mapping)
			}
		}
	}
	return mappings
}

func parseOpenAICompactModelMapping(raw any) map[string]string {
	switch v := raw.(type) {
	case string:
		return parseOpenAICompactModelMappingJSON(v)
	case map[string]string:
		return sanitizeOpenAICompactModelMapping(v)
	case map[string]any:
		out := make(map[string]string, len(v))
		for key, value := range v {
			if s, ok := value.(string); ok {
				out[key] = s
			}
		}
		return sanitizeOpenAICompactModelMapping(out)
	default:
		return nil
	}
}

func parseOpenAICompactModelMappingJSON(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var direct map[string]string
	if err := json.Unmarshal([]byte(raw), &direct); err == nil {
		return sanitizeOpenAICompactModelMapping(direct)
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil
	}
	return parseOpenAICompactModelMapping(generic)
}

func sanitizeOpenAICompactModelMapping(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for from, to := range in {
		from = strings.TrimSpace(from)
		to = strings.TrimSpace(to)
		if from == "" || to == "" {
			continue
		}
		out[from] = to
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
