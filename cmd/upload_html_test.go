package cmd

import "testing"

func TestReplaceTargetKey(t *testing.T) {
	const base = "https://pub-abc123.r2.dev"

	ok := []struct {
		name    string
		replace string
		want    string
	}{
		{"full url", base + "/pages/20260725-120000-ab12cd34.html", "pages/20260725-120000-ab12cd34.html"},
		{"trailing slash on base is tolerated", base + "/pages/x.html", "pages/x.html"},
		{"bare key", "pages/x.html", "pages/x.html"},
		{"bare filename is completed", "x.html", "pages/x.html"},
		{"surrounding whitespace", "  pages/x.html  ", "pages/x.html"},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := replaceTargetKey(tc.replace, base)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	// Every rejection here would otherwise overwrite something that is not the
	// page the caller named.
	bad := []struct {
		name    string
		replace string
	}{
		{"another origin", "https://evil.example.com/pages/x.html"},
		{"origin that merely prefixes ours", "https://pub-abc123.r2.dev.evil.example.com/pages/x.html"},
		{"another prefix", "media/x.html"},
		{"traversal out of the prefix", "pages/../media/x.html"},
		{"the prefix itself", "pages/"},
		{"absolute path", "/pages/x.html"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := replaceTargetKey(tc.replace, base); err == nil {
				t.Fatalf("expected rejection, got key %q", got)
			}
		})
	}
}
