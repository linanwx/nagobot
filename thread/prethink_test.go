package thread

import (
	"strings"
	"testing"
)

func TestParsePreThinkXML_FullStructure(t *testing.T) {
	raw := `<prethink>
  <intent>用户想查 2026 年最近的科技新闻</intent>
  <risk name="hallucination" level="high">具体日期和公司名容易混淆</risk>
  <risk name="misinformation" level="high">时事信息必须搜索确认</risk>
  <search>needed: 时事数据</search>
  <tools>web_search</tools>
  <tone>concise, factual</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	for _, want := range []string{
		"Intent: 用户想查",
		"High hallucination risk",
		"High misinformation risk",
		"Search: needed",
		"Tools: web_search",
		"Tone: concise, factual",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot: %s", want, out)
		}
	}
}

func TestParsePreThinkXML_FiltersLow(t *testing.T) {
	raw := `<prethink>
  <intent>chat</intent>
  <risk name="hallucination" level="low">none</risk>
  <risk name="underinvestment" level="medium">looks simple but tricky</risk>
  <tone>warm</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if strings.Contains(strings.ToLower(out), "low") {
		t.Errorf("expected no 'low' in output, got: %s", out)
	}
	if strings.Contains(out, "hallucination") {
		t.Errorf("low hallucination should be filtered, got: %s", out)
	}
	if !strings.Contains(out, "Medium underinvestment risk") {
		t.Errorf("medium underinvestment should be kept, got: %s", out)
	}
}

func TestParsePreThinkXML_CasualMinimal(t *testing.T) {
	raw := `<prethink>
  <intent>casual conversation</intent>
  <tone>warm, friendly</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "Intent: casual conversation") {
		t.Errorf("got: %s", out)
	}
	if !strings.Contains(out, "Tone: warm, friendly") {
		t.Errorf("got: %s", out)
	}
	if strings.Contains(out, "risk") {
		t.Errorf("no risks expected, got: %s", out)
	}
}

func TestParsePreThinkXML_NoTagsReturnsEmpty(t *testing.T) {
	raw := "This is just plain text without any XML tags."
	if out := parsePreThinkXML(raw); out != "" {
		t.Errorf("expected empty, got: %s", out)
	}
}

func TestParsePreThinkXML_EmptyInput(t *testing.T) {
	if out := parsePreThinkXML(""); out != "" {
		t.Errorf("expected empty, got: %s", out)
	}
}

func TestParsePreThinkXML_PartialTags(t *testing.T) {
	// Only one risk tag, no other fields — should still parse.
	raw := `Random prose <risk name="misinformation" level="high">current events</risk> more text.`
	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "High misinformation risk: current events") {
		t.Errorf("partial parse failed, got: %s", out)
	}
}

func TestParsePreThinkXML_NewlinesCollapsed(t *testing.T) {
	raw := `<prethink><intent>line1
line2
line3</intent></prethink>`
	out := parsePreThinkXML(raw)
	if strings.Contains(out, "\n") {
		t.Errorf("newlines should be collapsed, got: %q", out)
	}
}
