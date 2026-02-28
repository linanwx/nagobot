package channel

import (
	"strings"
	"testing"
)

func TestConvertTablesToLists_Basic(t *testing.T) {
	md := "| Name | Age |\n|------|-----|\n| Alice | 30 |\n| Bob | 25 |"
	got := convertTablesToLists(md)
	checks := []string{
		"**1.**",
		"• **Name**: Alice",
		"• **Age**: 30",
		"**2.**",
		"• **Name**: Bob",
		"• **Age**: 25",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("missing %q in output:\n%s", c, got)
		}
	}
	if strings.Contains(got, "|") {
		t.Errorf("output still contains |:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

func TestConvertTablesToLists_CJK(t *testing.T) {
	md := "| 作用 | 原理 |\n|------|------|\n| **抗氧化** | 清除自由基 |\n| **抗炎** | 抑制炎症因子 |"
	got := convertTablesToLists(md)
	if !strings.Contains(got, "• **作用**: **抗氧化**") {
		t.Errorf("missing CJK content:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

func TestConvertTablesToLists_NoTable(t *testing.T) {
	md := "Hello world\n\nNo tables here."
	got := convertTablesToLists(md)
	if got != md {
		t.Errorf("non-table text modified:\n got: %q\nwant: %q", got, md)
	}
}

func TestConvertTablesToLists_InsideCodeBlock(t *testing.T) {
	md := "```\n| Name | Age |\n|------|-----|\n| Alice | 30 |\n```"
	got := convertTablesToLists(md)
	if got != md {
		t.Errorf("table inside code block was modified:\n got: %q\nwant: %q", got, md)
	}
}

func TestConvertTablesToLists_Mixed(t *testing.T) {
	md := "Some text before.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\nSome text after."
	got := convertTablesToLists(md)
	if !strings.Contains(got, "Some text before.") {
		t.Errorf("lost text before table:\n%s", got)
	}
	if !strings.Contains(got, "Some text after.") {
		t.Errorf("lost text after table:\n%s", got)
	}
	if !strings.Contains(got, "• **A**: 1") {
		t.Errorf("table not converted:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}
