package provider

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Empirical test against DeepSeek V4 (see /tmp/deepseek-empirical) showed that
// the server 400s when the reasoning_content KEY is absent from an assistant
// message's JSON, but accepts "reasoning_content": "" (empty string) on any
// assistant including tool_call rounds. This test locks the invariant so a
// future refactor cannot re-introduce the v1.4.56 regression.
func TestToDSMessagesAlwaysIncludesReasoningKey(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "q"},
		// Tool-call assistant with no stored reasoning (e.g. trimmed) — wire must
		// still carry `reasoning_content: ""`, else DeepSeek 400s.
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "",
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
		{Role: "tool", Content: "r", ToolCallID: "c1", Name: "f"},
		// Historical non-tool-call assistant with no reasoning — same requirement.
		{Role: "assistant", Content: "ok", ReasoningContent: ""},
		{Role: "user", Content: "q2"},
		// Final assistant with real reasoning.
		{Role: "assistant", Content: "final", ReasoningContent: "final thought"},
	}

	out := toDSMessages(msgs, false)
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(body)

	// Each assistant message on the wire must carry the reasoning_content key.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, m := range raw {
		var role string
		if err := json.Unmarshal(m["role"], &role); err != nil {
			t.Fatalf("index %d: role parse: %v", i, err)
		}
		if role != "assistant" {
			continue
		}
		if _, ok := m["reasoning_content"]; !ok {
			t.Errorf("assistant at index %d missing reasoning_content key; DeepSeek will 400. wire=%s", i, wire)
		}
	}

	// Last assistant with real reasoning — value must be the actual text.
	if !strings.Contains(wire, `"reasoning_content":"final thought"`) {
		t.Errorf("last assistant's real reasoning must be on wire: %s", wire)
	}
}

// Mid-chain tool-call assistants must pass their stored reasoning through to
// the wire — sending empty string on fresh (non-trimmed) messages would drop
// the model's prior thinking and degrade multi-step tool-use coherence.
func TestToDSMessagesPassesReasoningForMidChainToolCalls(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "q"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "iter1 thinking",
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
		{Role: "tool", Content: "result", ToolCallID: "c1", Name: "f"},
		{Role: "assistant", Content: "done", ReasoningContent: "iter2 thinking"},
	}

	out := toDSMessages(msgs, false)
	body, _ := json.Marshal(out)
	wire := string(body)

	if !strings.Contains(wire, `"reasoning_content":"iter1 thinking"`) {
		t.Errorf("mid-chain tool_call reasoning must be on wire, got: %s", wire)
	}
	if !strings.Contains(wire, `"reasoning_content":"iter2 thinking"`) {
		t.Errorf("final assistant reasoning must be on wire, got: %s", wire)
	}
}

// Trimmed messages (ReasoningContent cleared to "") must still include the
// reasoning_content key on the wire — just with empty value.
func TestToDSMessagesTrimmedReasoningSendsEmptyString(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "q"},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "", // trimmed
			ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "f", Arguments: `{}`},
			}},
		},
	}
	out := toDSMessages(msgs, false)
	if out[1].ReasoningContent == nil {
		t.Fatal("reasoning_content pointer must be non-nil (key must appear on wire)")
	}
	if *out[1].ReasoningContent != "" {
		t.Errorf("expected empty string, got %q", *out[1].ReasoningContent)
	}
	body, _ := json.Marshal(out[1])
	if !strings.Contains(string(body), `"reasoning_content":""`) {
		t.Errorf("expected explicit empty string in wire: %s", body)
	}
}

// -instant aliases must strip the suffix from the wire model name and send
// `thinking: {type: "disabled"}` explicitly — DeepSeek's API defaults to
// thinking-on when the field is omitted, so an explicit disabled value is
// required to actually suppress reasoning.
func TestDeepSeekInstantSuffix(t *testing.T) {
	tests := []struct {
		modelType    string
		wantWire     string
		wantThinking bool
		wantWireType string
	}{
		{"deepseek-v4-flash", "deepseek-v4-flash", true, "enabled"},
		{"deepseek-v4-pro", "deepseek-v4-pro", true, "enabled"},
		{"deepseek-v4-flash-instant", "deepseek-v4-flash", false, "disabled"},
		{"deepseek-v4-pro-instant", "deepseek-v4-pro", false, "disabled"},
	}
	for _, tc := range tests {
		p := newDeepSeekProvider("k", "", tc.modelType, tc.modelType, 0, 0)
		if p.effort != "" {
			t.Errorf("%s: effort = %q, want empty (server default)", tc.modelType, p.effort)
		}
		if p.modelName != tc.wantWire {
			t.Errorf("%s: wire modelName = %q, want %q", tc.modelType, p.modelName, tc.wantWire)
		}
		if p.thinking != tc.wantThinking {
			t.Errorf("%s: thinking = %v, want %v", tc.modelType, p.thinking, tc.wantThinking)
		}
		r := p.buildRequest(&Request{Messages: []Message{{Role: "user", Content: "q"}}}, p.thinking, true)
		if r.Model != tc.wantWire {
			t.Errorf("%s: dsRequest.Model = %q, want %q", tc.modelType, r.Model, tc.wantWire)
		}
		if r.Thinking == nil {
			t.Errorf("%s: thinking field must be present on wire", tc.modelType)
			continue
		}
		if r.Thinking.Type != tc.wantWireType {
			t.Errorf("%s: thinking.type = %q, want %q", tc.modelType, r.Thinking.Type, tc.wantWireType)
		}
	}
}

// A [bracket] suffix on the model name selects DeepSeek's reasoning depth:
// the bracket is stripped from the wire model and re-emitted as
// thinking.reasoning_effort. Bare aliases send no effort field at all so the
// server applies its own default — writing "high" explicitly would be the same
// value today but would silently pin us if DeepSeek moves that default.
func TestDeepSeekReasoningEffortSuffix(t *testing.T) {
	tests := []struct {
		modelType  string
		wantWire   string
		wantEffort string
	}{
		{"deepseek-v4-pro[high]", "deepseek-v4-pro", "high"},
		{"deepseek-v4-pro[max]", "deepseek-v4-pro", "max"},
		{"deepseek-v4-flash[high]", "deepseek-v4-flash", "high"},
		{"deepseek-v4-flash[max]", "deepseek-v4-flash", "max"},
		{"deepseek-v4-pro", "deepseek-v4-pro", ""},
		// Unregistered tiers borrowed from other providers must not reach the
		// wire — DeepSeek 400s on anything outside high/max.
		{"deepseek-v4-pro[low]", "deepseek-v4-pro", ""},
		{"deepseek-v4-pro[xhigh]", "deepseek-v4-pro", ""},
	}
	for _, tc := range tests {
		p := newDeepSeekProvider("k", "", tc.modelType, tc.modelType, 0, 0)
		if p.modelName != tc.wantWire {
			t.Errorf("%s: wire modelName = %q, want %q", tc.modelType, p.modelName, tc.wantWire)
		}
		if !p.thinking {
			t.Errorf("%s: thinking must stay on for effort variants", tc.modelType)
		}
		r := p.buildRequest(&Request{Messages: []Message{{Role: "user", Content: "q"}}}, p.thinking, true)
		if r.Thinking == nil || r.Thinking.Type != "enabled" {
			t.Fatalf("%s: thinking = %+v, want type=enabled", tc.modelType, r.Thinking)
		}
		if r.Thinking.ReasoningEffort != tc.wantEffort {
			t.Errorf("%s: reasoning_effort = %q, want %q", tc.modelType, r.Thinking.ReasoningEffort, tc.wantEffort)
		}
		body, _ := json.Marshal(r.Thinking)
		if tc.wantEffort == "" && strings.Contains(string(body), "reasoning_effort") {
			t.Errorf("%s: reasoning_effort must be omitted from the wire: %s", tc.modelType, body)
		}
	}
}

// Every registered effort variant must be a first-class model type: whitelisted
// and carrying a context window, or resolveProvider rejects it at config time.
func TestDeepSeekEffortVariantsRegistered(t *testing.T) {
	reg, ok := GetProviderRegistration("deepseek")
	if !ok {
		t.Fatal("deepseek not registered")
	}
	for _, base := range []string{"deepseek-v4-pro", "deepseek-v4-flash"} {
		for _, e := range dsReasoningEfforts {
			name := base + "[" + e + "]"
			if !slices.Contains(reg.Models, name) {
				t.Errorf("%s missing from Models whitelist", name)
			}
			if reg.ContextWindows[name] == 0 {
				t.Errorf("%s missing a context window", name)
			}
		}
	}
}

// --- vision ---

// dsTestImage writes a tiny real PNG and returns its path plus the marker that
// references it.
func dsTestImage(t *testing.T) (path, marker string) {
	t.Helper()
	// 1x1 PNG.
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	path = filepath.Join(t.TempDir(), "pixel.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path, "<<media:image/png:" + path + ">>"
}

// dsPartsOf returns the message's content as parts, or nil when it is a plain
// string. Marshalling first is deliberate: the wire shape is what DeepSeek
// judges, not the Go type.
func dsPartsOf(t *testing.T, m dsMessage) []dsContentPart {
	t.Helper()
	body, err := json.Marshal(m.Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	var parts []dsContentPart
	if err := json.Unmarshal(body, &parts); err != nil {
		return nil // plain string
	}
	return parts
}

// The gate is not cosmetic: deepseek-v4-flash answers an image part with
// 400 "This model does not support image", so an ungated attach would break
// every turn that carries a screenshot on the default model.
func TestToDSMessagesAttachesImagesOnlyWhenVisionCapable(t *testing.T) {
	_, marker := dsTestImage(t)
	msgs := []Message{{Role: "user", Content: "what is this?", Media: []string{marker}}}

	blind := toDSMessages(msgs, false)
	if parts := dsPartsOf(t, blind[0]); parts != nil {
		t.Fatalf("non-vision model got image parts: %+v", parts)
	}

	seeing := toDSMessages(msgs, true)
	parts := dsPartsOf(t, seeing[0])
	if len(parts) != 2 {
		t.Fatalf("want text+image parts, got %+v", parts)
	}
	if parts[0].Type != "text" || parts[0].Text != "what is this?" {
		t.Errorf("first part must carry the prompt text, got %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil ||
		!strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("second part must be a data-URL image, got %+v", parts[1])
	}
}

// Verified against the live API: a system message returns 400 "Image in system
// message is unsupported" and an assistant message 400 "Image in assistant
// message is not supported". Both roles must stay plain strings even when the
// model can see.
func TestToDSMessagesNeverPutsImagesInSystemOrAssistant(t *testing.T) {
	_, marker := dsTestImage(t)
	msgs := []Message{
		{Role: "system", Content: "ref " + marker, Media: []string{marker}},
		{Role: "assistant", Content: "here " + marker, Media: []string{marker}},
	}
	out := toDSMessages(msgs, true)
	for i, m := range out {
		if parts := dsPartsOf(t, m); parts != nil {
			t.Errorf("%s message (index %d) carries image parts; DeepSeek will 400. parts=%+v",
				m.Role, i, parts)
		}
	}
}

// A tool message keeps its image inline. This is the deliberate divergence from
// the OpenAI-shaped providers, which must defer tool media into a synthetic
// user message; DeepSeek accepts it in place and the model reads it.
func TestToDSMessagesKeepsToolImagesInPlace(t *testing.T) {
	_, marker := dsTestImage(t)
	msgs := []Message{
		{Role: "user", Content: "look"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "c1", Type: "function",
			Function: FunctionCall{Name: "shot", Arguments: `{}`},
		}}},
		{Role: "tool", ToolCallID: "c1", Name: "shot", Content: "screenshot: " + marker},
	}
	out := toDSMessages(msgs, true)
	if len(out) != 3 {
		t.Fatalf("no message may be injected or dropped, got %d", len(out))
	}
	parts := dsPartsOf(t, out[2])
	if len(parts) != 2 || parts[1].Type != "image_url" {
		t.Fatalf("tool message must carry its image, got %+v", parts)
	}
	// The marker is consumed, not echoed alongside the image it produced.
	if strings.Contains(parts[0].Text, "<<media:") {
		t.Errorf("marker left in attached text: %q", parts[0].Text)
	}
}

// A non-vision model keeps the literal marker in its tool output on purpose:
// that text is how it learns the file name and delegates to imagereader.
func TestToDSMessagesLeavesMarkersForBlindModels(t *testing.T) {
	_, marker := dsTestImage(t)
	msgs := []Message{{Role: "tool", ToolCallID: "c1", Content: "shot: " + marker}}
	out := toDSMessages(msgs, false)
	got, _ := out[0].Content.(string)
	if !strings.Contains(got, "<<media:") {
		t.Errorf("blind model lost the marker it needs to delegate: %q", got)
	}
}

// DeepSeek's vision model takes images only — no audio, no PDF. A non-image
// marker must fall through to the text path rather than become a part the API
// cannot accept.
func TestToDSMessagesIgnoresNonImageMedia(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "transcribe",
		Media:   []string{"<<media:audio/ogg:/tmp/does-not-matter.ogg>>"},
	}}
	out := toDSMessages(msgs, true)
	if parts := dsPartsOf(t, out[0]); parts != nil {
		t.Fatalf("audio must not become a content part, got %+v", parts)
	}
}

// SupportsVision is keyed on the nagobot-facing modelType, which still carries
// the -instant / [effort] suffix. Registering only the bare id would leave
// "deepseek-v4-flash-vision-exp[max]" silently blind.
func TestEveryDeepSeekVisionAliasIsVisionCapable(t *testing.T) {
	aliases := []string{
		"deepseek-v4-flash-vision-exp",
		"deepseek-v4-flash-vision-exp-instant",
		"deepseek-v4-flash-vision-exp[high]",
		"deepseek-v4-flash-vision-exp[max]",
	}
	for _, a := range aliases {
		if !slices.Contains(providerModelTypes["deepseek"], a) {
			t.Errorf("%s is not registered", a)
			continue
		}
		if !SupportsVision("deepseek", a) {
			t.Errorf("%s registered but not vision-capable", a)
		}
	}
	// The text models must NOT be: they 400 on an image part.
	for _, blind := range []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-instant"} {
		if SupportsVision("deepseek", blind) {
			t.Errorf("%s must not be vision-capable — the API rejects images on it", blind)
		}
	}
}
