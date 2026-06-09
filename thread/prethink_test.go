package thread

import (
	"strings"
	"testing"
)

func TestParsePreThinkXML_FullStructure(t *testing.T) {
	raw := `<prethink>
  <is_multi_step>true</is_multi_step>
  <search>需要联网核实最新版本</search>
  <tone>concise, technical</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	for _, want := range []string{
		"Multi-step task: plan the steps and complete all of them before responding.",
		"Search: 需要联网核实最新版本. Consider dispatching a search subagent.",
		"Tone: concise, technical",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot: %s", want, out)
		}
	}
}

func TestParsePreThinkXML_IsMultiStep(t *testing.T) {
	raw := `<prethink>
  <intent>帮我搭一个端到端流程</intent>
  <is_multi_step>true</is_multi_step>
  <tone>thorough</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "Multi-step task: plan the steps and complete all of them before responding.") {
		t.Errorf("is_multi_step should render, got: %s", out)
	}
}

func TestParsePreThinkXML_FalseBoolTreatedAsAbsent(t *testing.T) {
	raw := `<prethink>
  <intent>clear one-shot question</intent>
  <is_multi_step>false</is_multi_step>
  <tone>concise</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if strings.Contains(out, "Multi-step") {
		t.Errorf("explicit false is_multi_step must be ignored, got: %s", out)
	}
}

func TestParsePreThinkXML_ConfusingTerminologyRequiresClarification(t *testing.T) {
	raw := `<prethink>
  <intent>用户想修改模板，但措辞可能有歧义</intent>
  <confusing_terminology>"标签"既可能指 risk 也可能指 XML tag</confusing_terminology>
  <tone>careful</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	// The string content is carried so the main model knows what to clarify.
	if !strings.Contains(out, `Confusing terminology: "标签"既可能指 risk 也可能指 XML tag.`) {
		t.Errorf("confusing_terminology content should be included, got: %s", out)
	}
	if !strings.Contains(out, "ask the user to clarify via dispatch(to=user)") {
		t.Errorf("confusing_terminology should suggest clarifying via dispatch(to=user), got: %s", out)
	}
}

func TestParsePreThinkXML_NoConfusingTerminologyNoClarification(t *testing.T) {
	raw := `<prethink>
  <intent>用户的问题很清楚</intent>
  <is_multi_step>true</is_multi_step>
  <tone>concise</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if strings.Contains(out, "Confusing terminology") {
		t.Errorf("absent confusing_terminology must NOT trigger clarification, got: %s", out)
	}
}

func TestParsePreThinkXML_Hallucination(t *testing.T) {
	raw := `<prethink>
  <intent>问 XXX 型号有没有 YYY 功能</intent>
  <hallucination>XXX 型号 / YYY 功能</hallucination>
  <tone>concise</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "Possible hallucination: XXX 型号 / YYY 功能. Consider searching references before continuing.") {
		t.Errorf("hallucination content + search suggestion should be included, got: %s", out)
	}
}

func TestParsePreThinkXML_NoHallucination(t *testing.T) {
	raw := `<prethink>
  <intent>简单聊天</intent>
  <hallucination></hallucination>
  <tone>warm</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if strings.Contains(out, "Possible hallucination") {
		t.Errorf("empty hallucination must be omitted, got: %s", out)
	}
}

func TestParsePreThinkXML_InvestigatorForcesDispatch(t *testing.T) {
	raw := `<prethink>
  <intent>调查一下竞品定价</intent>
  <is_include_investigator>true</is_include_investigator>
  <tone>thorough</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "Investigator: you must call dispatch to fan out an investigator subagent before responding to the user.") {
		t.Errorf("is_include_investigator should force dispatch, got: %s", out)
	}
}

func TestParsePreThinkXML_HasWebURL(t *testing.T) {
	raw := `<prethink>
  <intent>看下这个页面 https://example.com/post</intent>
  <has_web_url>true</has_web_url>
  <tone>concise</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "Web URL present: consider using playwright to open it.") {
		t.Errorf("has_web_url should suggest playwright, got: %s", out)
	}
}

func TestParsePreThinkXML_Skill(t *testing.T) {
	raw := `<prethink>
  <skill>playwright-cli</skill>
  <tone>concise</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, `Related skill: playwright-cli. Consider use_skill("playwright-cli") to load its instructions before proceeding.`) {
		t.Errorf("skill should suggest use_skill, got: %s", out)
	}
}

func TestParsePreThinkXML_NoSkill(t *testing.T) {
	raw := `<prethink>
  <skill></skill>
  <tone>warm</tone>
</prethink>`

	if out := parsePreThinkXML(raw); strings.Contains(out, "Related skill") {
		t.Errorf("empty skill must be omitted, got: %s", out)
	}
}

func TestParsePreThinkXML_CasualMinimal(t *testing.T) {
	raw := `<prethink>
  <tone>warm, friendly</tone>
</prethink>`

	out := parsePreThinkXML(raw)
	if !strings.Contains(out, "Tone: warm, friendly") {
		t.Errorf("got: %s", out)
	}
	// Only tone — no flag phrases.
	for _, unexpected := range []string{"Multi-step", "Confusing terminology", "Investigator", "Search:", "Web URL", "Related skill", "Intent"} {
		if strings.Contains(out, unexpected) {
			t.Errorf("unexpected %q in minimal output: %s", unexpected, out)
		}
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

func TestParsePreThinkXML_NewlinesCollapsed(t *testing.T) {
	raw := `<prethink><intent>line1
line2
line3</intent></prethink>`
	out := parsePreThinkXML(raw)
	if strings.Contains(out, "\n") {
		t.Errorf("newlines should be collapsed, got: %q", out)
	}
}
