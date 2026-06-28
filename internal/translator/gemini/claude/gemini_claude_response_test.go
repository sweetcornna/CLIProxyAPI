package claude

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertGeminiResponseToClaude_SignatureOnlyPartDoesNotOpenEmptyTextBlock(t *testing.T) {
	requestJSON := []byte(`{"model":"gemini-test","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	thinkingChunk := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "thinking text", "thought": true}]
			}
		}],
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)
	signatureChunk := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "", "thoughtSignature": "sig-test"}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"thoughtsTokenCount": 2,
			"totalTokenCount": 12
		},
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)

	var param any
	ctx := context.Background()
	output := bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-test", requestJSON, requestJSON, thinkingChunk, &param), nil)
	output = append(output, bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-test", requestJSON, requestJSON, signatureChunk, &param), nil)...)
	output = append(output, bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-test", requestJSON, requestJSON, []byte("[DONE]"), &param), nil)...)
	outputText := string(output)

	if strings.Contains(outputText, `"content_block":{"type":"text"`) {
		t.Fatalf("signature-only part must not open an empty text block: %s", outputText)
	}
	if strings.Contains(outputText, `"type":"content_block_stop","index":1`) {
		t.Fatalf("signature-only part must not produce a stop for unopened index 1: %s", outputText)
	}
	if !strings.Contains(outputText, `"type":"signature_delta"`) || !strings.Contains(outputText, `"signature":"sig-test"`) {
		t.Fatalf("signature-only part must be emitted as a thinking signature delta: %s", outputText)
	}
	if got := strings.Count(outputText, `"type":"content_block_stop","index":0`); got != 1 {
		t.Fatalf("expected exactly one stop for thinking index 0, got %d: %s", got, outputText)
	}
	if !strings.Contains(outputText, `"type":"message_delta"`) || !strings.Contains(outputText, `"output_tokens":2`) {
		t.Fatalf("finish chunk without candidatesTokenCount must still emit final message_delta: %s", outputText)
	}
	if !strings.Contains(outputText, `"type":"message_stop"`) {
		t.Fatalf("DONE chunk must still emit message_stop after final events: %s", outputText)
	}
}

func TestConvertGeminiResponseToClaudeNonStream_CachedContentUsesClaudeInputSemantics(t *testing.T) {
	requestJSON := []byte(`{"model":"gemini-test","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	responseJSON := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "ok"}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 100,
			"candidatesTokenCount": 5,
			"cachedContentTokenCount": 40,
			"totalTokenCount": 105
		},
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)

	output := ConvertGeminiResponseToClaudeNonStream(context.Background(), "gemini-test", requestJSON, requestJSON, responseJSON, nil)

	if got := gjson.GetBytes(output, "usage.input_tokens").Int(); got != 60 {
		t.Fatalf("usage.input_tokens = %d, want 60: %s", got, output)
	}
	if got := gjson.GetBytes(output, "usage.cache_read_input_tokens").Int(); got != 40 {
		t.Fatalf("usage.cache_read_input_tokens = %d, want 40: %s", got, output)
	}
}

func TestConvertGeminiResponseToClaudeStream_CachedContentUsesClaudeInputSemantics(t *testing.T) {
	requestJSON := []byte(`{"model":"gemini-test","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	responseJSON := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "ok"}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 100,
			"candidatesTokenCount": 5,
			"cachedContentTokenCount": 40,
			"totalTokenCount": 105
		},
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)

	var param any
	output := string(bytes.Join(ConvertGeminiResponseToClaude(context.Background(), "gemini-test", requestJSON, requestJSON, responseJSON, &param), nil))
	messageDelta := geminiClaudeSSEDataForEvent(t, output, "message_delta")

	if got := gjson.Get(messageDelta, "usage.input_tokens").Int(); got != 60 {
		t.Fatalf("usage.input_tokens = %d, want 60: %s", got, messageDelta)
	}
	if got := gjson.Get(messageDelta, "usage.cache_read_input_tokens").Int(); got != 40 {
		t.Fatalf("usage.cache_read_input_tokens = %d, want 40: %s", got, messageDelta)
	}
}

func TestConvertGeminiResponseToClaudeStream_MessageStartCarriesCachedInputUsage(t *testing.T) {
	requestJSON := []byte(`{"model":"gemini-test","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	responseJSON := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "ok"}]
			}
		}],
		"usageMetadata": {
			"promptTokenCount": 100,
			"candidatesTokenCount": 5,
			"cachedContentTokenCount": 40,
			"totalTokenCount": 105
		},
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)

	var param any
	output := string(bytes.Join(ConvertGeminiResponseToClaude(context.Background(), "gemini-test", requestJSON, requestJSON, responseJSON, &param), nil))
	messageStart := geminiClaudeSSEDataForEvent(t, output, "message_start")

	if got := gjson.Get(messageStart, "message.usage.input_tokens").Int(); got != 60 {
		t.Fatalf("message_start input_tokens = %d, want 60: %s", got, messageStart)
	}
	if got := gjson.Get(messageStart, "message.usage.cache_read_input_tokens").Int(); got != 40 {
		t.Fatalf("message_start cache_read_input_tokens = %d, want 40: %s", got, messageStart)
	}
}

func geminiClaudeSSEDataForEvent(t *testing.T, output string, eventName string) string {
	t.Helper()

	currentEvent := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if currentEvent == eventName && strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}

	t.Fatalf("event %q not found in:\n%s", eventName, output)
	return ""
}
