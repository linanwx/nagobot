package channel

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
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
