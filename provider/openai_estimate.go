package provider

import (
	"encoding/json"
	"strings"
)

// EstimatePromptTokens implements TokenEstimator for the OpenAI Responses API.
// It mirrors buildRequestBody's structure but counts tokens instead of building
// the actual request, using existing media estimators for images/audio.
func (p *OpenAIProvider) EstimatePromptTokens(messages []Message, toolDefs []ToolDef) int {
	mediaTokens := 0

	// 1. Instructions (system messages merged into a single string).
	var instructions []string
	var input []map[string]any

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			instructions = append(instructions, msg.Content)

		case "user":
			content := []map[string]any{
				{"type": "input_text", "text": msg.Content},
			}
			if len(msg.Media) > 0 {
				_, markers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
				for _, marker := range markers {
					if strings.HasPrefix(marker.MimeType, "image/") {
						mediaTokens += EstimateImageTokens(marker.FilePath)
					}
				}
			}
			input = append(input, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": content,
			})

		case "assistant":
			if msg.Content != "" {
				input = append(input, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": msg.Content},
					},
				})
			}
			// Reasoning items — same filtering as buildRequestBody.
			if !msg.ReasoningTrimmed && len(msg.ReasoningDetails) > 0 {
				var items []json.RawMessage
				if err := json.Unmarshal(msg.ReasoningDetails, &items); err == nil {
					for _, raw := range items {
						var ri map[string]any
						if err := json.Unmarshal(raw, &ri); err == nil {
							if riType, _ := ri["type"].(string); riType == "reasoning" {
								input = append(input, ri)
							}
						}
					}
				}
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}

		case "tool":
			cleanedText, markers := ParseMediaMarkers(msg.Content)
			for _, marker := range markers {
				if strings.HasPrefix(marker.MimeType, "image/") {
					mediaTokens += EstimateImageTokens(marker.FilePath)
				} else if strings.HasPrefix(marker.MimeType, "audio/") {
					mediaTokens += EstimateAudioTokens(marker.FilePath)
				}
			}
			if len(msg.Media) > 0 {
				_, mediaMarkers := ParseMediaMarkers(strings.Join(msg.Media, "\n"))
				for _, marker := range mediaMarkers {
					if strings.HasPrefix(marker.MimeType, "image/") {
						mediaTokens += EstimateImageTokens(marker.FilePath)
					}
				}
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  cleanedText,
			})
		}
	}

	// 2. Tools — same flat structure as buildRequestBody.
	var tools []map[string]any
	for _, t := range toolDefs {
		tool := map[string]any{
			"type":       "function",
			"name":       t.Function.Name,
			"parameters": t.Function.Parameters,
		}
		if t.Function.Description != "" {
			tool["description"] = t.Function.Description
		}
		tools = append(tools, tool)
	}

	// 3. Assemble into the same top-level structure the API receives.
	body := map[string]any{
		"input": input,
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	data, err := json.Marshal(body)
	if err != nil {
		return 0
	}
	return EstimateTextTokens(string(data)) + mediaTokens
}
