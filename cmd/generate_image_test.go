package cmd

import "testing"

func TestNormalizeImageFormat(t *testing.T) {
	for in, want := range map[string]string{"png": "png", " JPG ": "jpeg", "jpeg": "jpeg", "WebP": "webp"} {
		got, err := normalizeImageFormat(in)
		if err != nil || got != want {
			t.Errorf("normalizeImageFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := normalizeImageFormat("gif"); err == nil {
		t.Error("gif must be rejected")
	}
}

func TestNormalizeImageBackground(t *testing.T) {
	cases := []struct {
		raw, format, want string
		wantErr           bool
	}{
		{"auto", "png", "", false},
		{"", "jpeg", "", false},
		{"opaque", "jpeg", "opaque", false},
		{"transparent", "png", "transparent", false},
		{"transparent", "webp", "transparent", false},
		// jpeg has no alpha channel: refuse instead of returning an opaque image.
		{"transparent", "jpeg", "", true},
		{"clear", "png", "", true},
	}
	for _, c := range cases {
		got, err := normalizeImageBackground(c.raw, c.format)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("normalizeImageBackground(%q, %q) = %q, %v; want %q, err=%v", c.raw, c.format, got, err, c.want, c.wantErr)
		}
	}
}
