package channel

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestRenderTableAsList_EmptyHeaders_NoColumnPrefix(t *testing.T) {
	// When ALL headers are empty, non-label columns should render as plain bullets
	// (no "Column 2" / "Column 3" technical noise).
	md := "| | |\n|---|---|\n| 🇪🇸 西班牙 | ~+2.4% |"
	got := convertTablesToLists(md)
	if strings.Contains(got, "Column 2") {
		t.Errorf("empty header should not produce 'Column 2' label:\n%s", got)
	}
	if !strings.Contains(got, "~+2.4%") {
		t.Errorf("value should still appear:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

func TestIsSeparatorRow_EmptyHeader(t *testing.T) {
	// | | | is a header row with empty cells, NOT a separator row.
	// Only rows containing --- (dashes) are separators.
	tests := []struct {
		cells    []string
		expected bool
	}{
		{[]string{" ", " "}, false},                 // empty header row
		{[]string{"", ""}, false},                   // empty header row (no space)
		{[]string{"---", "---"}, true},              // standard separator
		{[]string{":---", "---:"}, true},            // alignment separator
		{[]string{":--:", "-----"}, true},           // alignment separator
		{[]string{"Data", "Value"}, false},          // data row
		{[]string{"", "同比增速"}, false},            // first-cell-empty header
	}
	for _, tt := range tests {
		got := isSeparatorRow(tt.cells)
		if got != tt.expected {
			t.Errorf("isSeparatorRow(%v) = %v, want %v", tt.cells, got, tt.expected)
		}
	}
}

func TestConvertTablesToLists_AlignmentSeparator(t *testing.T) {
	// Alignment colons in separator row should be ignored (treated like |---|).
	md := "| Left | Center | Right |\n|:-----|:------:|------:|\n| a | b | c |"
	got := convertTablesToLists(md)
	if !strings.Contains(got, "• **Left**: a") {
		t.Errorf("missing left column:\n%s", got)
	}
	if !strings.Contains(got, "• **Center**: b") {
		t.Errorf("missing center column:\n%s", got)
	}
	if !strings.Contains(got, "• **Right**: c") {
		t.Errorf("missing right column:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

func TestConvertTablesToLists_MismatchedColumns(t *testing.T) {
	// Row with fewer cells than header pads implicitly, extra col gets normalized.
	md := "| A | B | C |\n|---|---|---|\n| 1 | 2 |\n| 3 | 4 | 5 | 6 |"
	got := convertTablesToLists(md)
	if !strings.Contains(got, "• **A**: 1") {
		t.Errorf("missing col A:\n%s", got)
	}
	if !strings.Contains(got, "• **B**: 2") {
		t.Errorf("missing col B:\n%s", got)
	}
	// Row 2 has extra cell beyond numCols — should still render existing cols.
	if !strings.Contains(got, "• **A**: 3") {
		t.Errorf("missing col A row 2:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

func TestConvertTablesToLists_MissingHeaderRow(t *testing.T) {
	// Degenerate: separator line first, no content header before it.
	// The separator is skipped (has dashes), the first data row becomes header.
	// Since there's only one row after separator, output is empty (no data rows).
	// This is a format error by the LLM — not worth special-casing.
	md := "|---|---|\n| a | b |"
	got := convertTablesToLists(md)
	// Don't crash; output empty because the only row became headers.
	if strings.Contains(got, "|") {
		t.Errorf("raw table pipes leaked:\n%s", got)
	}
	t.Logf("Output (empty expected for degenerate table):\n%s", got)
}

func TestConvertTablesToLists_EmptyCells(t *testing.T) {
	// Empty cell in data row should render as empty value.
	md := "| Country | Growth | Note |\n|---------|--------|------|\n| 🇫🇷 法国 | -0.1% | |"
	got := convertTablesToLists(md)
	if !strings.Contains(got, "• **Growth**: -0.1%") {
		t.Errorf("missing growth value:\n%s", got)
	}
	// Empty cell should still appear as a bullet (just empty value).
	t.Logf("Output:\n%s", got)
}

func TestConvertTablesToLists_RowLabelWithEmptyFirstCell(t *testing.T) {
	// When row label mode is on (headers[0]=="") but some rows have empty first cell,
	// they should render with plain numbers, not picking up previous labels.
	md := "| | Value |\n|---|---|\n| Labeled | 10 |\n| | 20 |"
	got := convertTablesToLists(md)
	if !strings.Contains(got, "**1. Labeled**") {
		t.Errorf("first row should use label:\n%s", got)
	}
	if !strings.Contains(got, "**2.**") {
		t.Errorf("second row should fall back to plain number:\n%s", got)
	}
	if !strings.Contains(got, "**2.**") {
		t.Errorf("second row should use plain number when label cell empty:\n%s", got)
	}
	if !strings.Contains(got, "**Value**: 20") {
		t.Errorf("second row value should appear with header:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

func TestConvertTablesToLists_EmptyHeaders(t *testing.T) {
	// Tables where all headers are empty — common in LLM-generated markdown.
	// | | | header with all-empty cells should render correctly.
	md := "| | |\n|---|---|\n| 🇪🇸 西班牙 | ~+2.4% |\n| 🇨🇾 塞浦路斯 | ~+2.5% |"
	got := convertTablesToLists(md)
	// Each row should be numbered, with its first cell as the bold label.
	if !strings.Contains(got, "**1. 🇪🇸 西班牙**") {
		t.Errorf("missing row label for Spain:\n%s", got)
	}
	if !strings.Contains(got, "**2. 🇨🇾 塞浦路斯**") {
		t.Errorf("missing row label for Cyprus:\n%s", got)
	}
	if !strings.Contains(got, "~+2.4%") {
		t.Errorf("missing value for Spain:\n%s", got)
	}
	t.Logf("Output:\n%s", got)
}

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

func TestBuildThreadContext_RegularChannel(t *testing.T) {
	regular := &discordgo.Channel{
		ID:   "123",
		Name: "general",
		Type: discordgo.ChannelTypeGuildText,
	}
	got := buildThreadContext(regular, nil)
	if len(got) != 0 {
		t.Errorf("expected empty map for non-thread, got %v", got)
	}
}

func TestBuildThreadContext_NilThread(t *testing.T) {
	got := buildThreadContext(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty map for nil thread, got %v", got)
	}
}

func TestBuildThreadContext_PlainThread(t *testing.T) {
	thr := &discordgo.Channel{
		ID:       "999",
		Name:     "feature-discussion",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		ParentID: "100",
	}
	parent := &discordgo.Channel{
		ID:   "100",
		Name: "general",
		Type: discordgo.ChannelTypeGuildText,
	}
	got := buildThreadContext(thr, parent)

	if got["thread_name"] != "feature-discussion" {
		t.Errorf("thread_name: want feature-discussion, got %q", got["thread_name"])
	}
	if got["thread_type"] != "thread" {
		t.Errorf("thread_type: want thread, got %q", got["thread_type"])
	}
	if _, ok := got["applied_tags"]; ok {
		t.Errorf("plain thread should not have applied_tags, got %q", got["applied_tags"])
	}
	if _, ok := got["forum_name"]; ok {
		t.Errorf("plain thread should not have forum_name, got %q", got["forum_name"])
	}
}

func TestBuildThreadContext_ForumPostWithTags(t *testing.T) {
	parent := &discordgo.Channel{
		ID:   "200",
		Name: "help-forum",
		Type: discordgo.ChannelTypeGuildForum,
		AvailableTags: []discordgo.ForumTag{
			{ID: "tag-a", Name: "Bug"},
			{ID: "tag-b", Name: "Question"},
			{ID: "tag-c", Name: "Docs"},
		},
	}
	thr := &discordgo.Channel{
		ID:          "201",
		Name:        "Can't start Docker container",
		Type:        discordgo.ChannelTypeGuildPublicThread,
		ParentID:    "200",
		AppliedTags: []string{"tag-b", "tag-a"}, // order preserved
	}
	got := buildThreadContext(thr, parent)

	if got["thread_type"] != "forum_post" {
		t.Errorf("thread_type: want forum_post, got %q", got["thread_type"])
	}
	if got["thread_name"] != "Can't start Docker container" {
		t.Errorf("thread_name mismatch: %q", got["thread_name"])
	}
	if got["forum_name"] != "help-forum" {
		t.Errorf("forum_name: want help-forum, got %q", got["forum_name"])
	}
	if got["applied_tags"] != "Question, Bug" {
		t.Errorf("applied_tags: want %q, got %q", "Question, Bug", got["applied_tags"])
	}
}

func TestBuildThreadContext_ForumPostUnknownTagID(t *testing.T) {
	parent := &discordgo.Channel{
		ID:            "200",
		Name:          "help-forum",
		Type:          discordgo.ChannelTypeGuildForum,
		AvailableTags: []discordgo.ForumTag{{ID: "tag-a", Name: "Bug"}},
	}
	thr := &discordgo.Channel{
		ID:          "201",
		Name:        "post",
		Type:        discordgo.ChannelTypeGuildPublicThread,
		AppliedTags: []string{"tag-a", "tag-unknown"},
	}
	got := buildThreadContext(thr, parent)
	if got["applied_tags"] != "Bug" {
		t.Errorf("unknown tag IDs should be dropped, got %q", got["applied_tags"])
	}
}

func TestBuildThreadContext_ForumPostNoTags(t *testing.T) {
	parent := &discordgo.Channel{
		ID:   "200",
		Name: "help-forum",
		Type: discordgo.ChannelTypeGuildForum,
	}
	thr := &discordgo.Channel{
		ID:       "201",
		Name:     "post",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		ParentID: "200",
	}
	got := buildThreadContext(thr, parent)
	if got["thread_type"] != "forum_post" {
		t.Errorf("want forum_post, got %q", got["thread_type"])
	}
	if _, ok := got["applied_tags"]; ok {
		t.Errorf("no tags applied but applied_tags present: %q", got["applied_tags"])
	}
}

func TestBuildThreadContext_ForumParentMissing(t *testing.T) {
	// Thread with ParentID but parent fetch failed (nil). Falls back to plain
	// thread handling — not a forum post because we can't confirm parent type.
	thr := &discordgo.Channel{
		ID:       "201",
		Name:     "post",
		Type:     discordgo.ChannelTypeGuildPublicThread,
		ParentID: "200",
	}
	got := buildThreadContext(thr, nil)
	if got["thread_type"] != "thread" {
		t.Errorf("parent missing → should stay thread, got %q", got["thread_type"])
	}
	if got["thread_name"] != "post" {
		t.Errorf("thread_name lost: %q", got["thread_name"])
	}
}

func TestConvertTablesToLists_EuropeEconomy(t *testing.T) {
	// Simulates the European economic tiers tables from the user's message.
	// Three tiers: 🟢 growing, 🟡 crawling, 🔴 stopped/shrinking.
	md := `## 🟢 真在跑

| | 同比增速 |
|---|---|
| 🇩🇰 丹麦 | **+5.9%** |
| 🇲🇹 马耳他 | +4.3% |
| 🇵🇱 波兰 | +3.5% |
| 🇧🇬 保加利亚 | +3.1% |
| 🇮🇸 冰岛 | +3.1% |
| 🇸🇮 斯洛文尼亚 | +3.1% |

丹麦的 +5.9% 是异常值。

## 🟡 在爬（但速度不让人放心）

| | |
|---|---|
| 🇪🇸 西班牙 | ~+2.4% |
| 🇨🇾 塞浦路斯 | ~+2.5% |
| 🇬🇧 英国 | 环比 +0.6% |
| 🇮🇹 意大利 | +0.8% |
| 🇳🇱 荷兰 | ~+1.2% |

## 🔴 停着或缩着

| | | |
|---|---|---|
| 🇩🇪 德国 | +0.3% | 连续两年衰退后，实质上停了 |
| 🇫🇷 法国 | Q1 环比 **-0.1%** | |
| 🇦🇹 奥地利 | +0.9% | 刚从最长衰退爬出来 |
| 🇸🇪 瑞典 | Q1 环比 -0.2% | |
| 🇷🇴 罗马尼亚 | **-1.1%** | 唯一同比还在缩的 |`

	got := convertTablesToLists(md)
	t.Logf("Output:\n%s", got)
}
