package channel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// A reply to an image-only post used to produce no reply_context at all,
// because the quoted message's Content is empty: the model saw neither the
// quote nor the image it pointed at.
func TestDiscordReplyContextNamesQuotedAttachments(t *testing.T) {
	img := &discordgo.MessageAttachment{Filename: "map.png", ContentType: "image/png"}
	author := &discordgo.User{Username: "nansen"}

	cases := []struct {
		name string
		ref  *discordgo.Message
		want string
	}{
		{"image only", &discordgo.Message{Author: author, Attachments: []*discordgo.MessageAttachment{img}},
			"[Reply to nansen]: [Image: map.png]"},
		{"text and image", &discordgo.Message{Author: author, Content: "look", Attachments: []*discordgo.MessageAttachment{img}},
			"[Reply to nansen]: look [Image: map.png]"},
		{"text only", &discordgo.Message{Author: author, Content: "look"},
			"[Reply to nansen]: look"},
		{"empty", &discordgo.Message{Author: author}, ""},
		{"no referenced message", nil, ""},
	}
	for _, c := range cases {
		if got := discordReplyContext(c.ref); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The quoted image must land in media_summary as a downloaded image_path, the
// shape the dispatcher's preview step and read_file both act on, and be marked
// as coming from the quote rather than from the new message.
func TestDiscordQuotedImageIsDownloadedIntoMedia(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	}))
	defer srv.Close()

	d := &DiscordChannel{mediaDir: t.TempDir()}
	got := d.attachmentSummaries([]*discordgo.MessageAttachment{
		{Filename: "map.png", ContentType: "image/png", URL: srv.URL + "/map.png"},
	}, "quoted message")
	if len(got) != 1 {
		t.Fatalf("want 1 summary, got %d: %q", len(got), got)
	}
	s := got[0]
	if !strings.Contains(s, "from: quoted message") {
		t.Errorf("summary does not mark its origin: %q", s)
	}
	m := regexp.MustCompile(`(?m)^image_path:\s*(.+)$`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no image_path line: %q", s)
	}
	if _, err := os.Stat(strings.TrimSpace(m[1])); err != nil {
		t.Errorf("image_path does not exist on disk: %v", err)
	}
}
