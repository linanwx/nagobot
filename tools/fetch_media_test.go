package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pdfBytes is a minimal PDF: the %PDF- magic http.DetectContentType keys on.
var pdfBytes = []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n%%EOF\n")

// serveBytes starts a test server returning body under contentType. An empty
// contentType sends no header at all, which is the unlabelled case.
func serveBytes(t *testing.T, contentType string, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		} else {
			// Go sniffs and sets one otherwise; suppress it so the provider
			// really sees a response with no declared type.
			w.Header()["Content-Type"] = nil
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchCtx(t *testing.T) (context.Context, string) {
	t.Helper()
	ws := t.TempDir()
	return WithRuntimeContext(context.Background(), RuntimeContext{Workspace: ws}), ws
}

// TestFetchDivertsFilesAndKeepsPages is the core property: content HTML
// extraction would destroy goes to disk, and everything readable keeps taking
// the existing path. The regression direction that matters most is the second
// one — this predicate must never divert a page.
func TestFetchDivertsFilesAndKeepsPages(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        []byte
		urlPath     string
		wantFile    bool
		wantExt     string
	}{
		{name: "pdf", contentType: "application/pdf", body: pdfBytes, urlPath: "/paper.pdf", wantFile: true, wantExt: ".pdf"},
		{name: "png", contentType: "image/png", body: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 1, 2, 3}, wantFile: true, wantExt: ".png"},
		{name: "zip", contentType: "application/zip", body: []byte("PK\x03\x04binary\x00stuff"), wantFile: true, wantExt: ".zip"},
		{name: "xlsx", contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", body: []byte("PK\x03\x04\x00sheet"), wantFile: true, wantExt: ".xlsx"},

		{name: "html", contentType: "text/html; charset=utf-8", body: []byte("<html><body><p>hello</p></body></html>")},
		{name: "plain text", contentType: "text/plain", body: []byte("just words")},
		{name: "json", contentType: "application/json", body: []byte(`{"a":1}`)},
		{name: "csv", contentType: "text/csv", body: []byte("a,b\n1,2\n")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveBytes(t, tc.contentType, tc.body)
			ctx, ws := fetchCtx(t)

			got, err := (&DirectFetchProvider{}).Fetch(ctx, srv.URL+tc.urlPath)

			var nonPage *NonPageContent
			isFile := errors.As(err, &nonPage)
			if isFile != tc.wantFile {
				t.Fatalf("diverted to file = %v, want %v (err=%v, content=%q)", isFile, tc.wantFile, err, got)
			}
			if !tc.wantFile {
				if err != nil {
					t.Fatalf("page fetch failed: %v", err)
				}
				if got != string(tc.body) {
					t.Errorf("page content = %q, want %q", got, tc.body)
				}
				// Nothing may be written for a page.
				if entries, _ := os.ReadDir(filepath.Join(ws, "media")); len(entries) != 0 {
					t.Errorf("page fetch wrote %d file(s) to media/", len(entries))
				}
				return
			}

			if got != "" {
				t.Errorf("file fetch returned content %q, want empty", got)
			}
			if ext := strings.ToLower(filepath.Ext(nonPage.Path)); ext != tc.wantExt {
				t.Errorf("saved extension = %q, want %q (path %s)", ext, tc.wantExt, nonPage.Path)
			}
			if nonPage.Size != int64(len(tc.body)) {
				t.Errorf("saved size = %d, want %d", nonPage.Size, len(tc.body))
			}
			onDisk, readErr := os.ReadFile(nonPage.Path)
			if readErr != nil {
				t.Fatalf("saved file unreadable: %v", readErr)
			}
			if string(onDisk) != string(tc.body) {
				t.Errorf("saved bytes differ from what was served")
			}
			// The file must land inside the workspace media directory, which is
			// what read_file, the web console, and ![](media/…) all resolve to.
			if want := filepath.Join(ws, "media"); filepath.Dir(nonPage.Path) != want {
				t.Errorf("saved into %s, want %s", filepath.Dir(nonPage.Path), want)
			}
		})
	}
}

// TestReadabilityDivertsFilesToo pins the second provider that owns its HTTP.
// Without the diversion go-readability reports a generic extraction failure,
// which grades as ContentError and tells the model to try another source — for
// a PDF no source will ever work.
func TestReadabilityDivertsFilesToo(t *testing.T) {
	srv := serveBytes(t, "application/pdf", pdfBytes)
	ctx, _ := fetchCtx(t)

	_, err := (&ReadabilityFetchProvider{}).Fetch(ctx, srv.URL+"/doc.pdf")

	var nonPage *NonPageContent
	if !errors.As(err, &nonPage) {
		t.Fatalf("err = %v, want *NonPageContent", err)
	}
	var contentErr *ContentError
	if errors.As(err, &contentErr) {
		t.Error("a file must not be reported as an extraction failure")
	}
}

// TestUnlabelledResponsesAreDecidedByTheirBytes covers the case that motivated
// sniffing at all: hosts routinely serve a PDF download as octet-stream or with
// no type at all, and a type-header-only rule would hand those to the HTML
// parser.
func TestUnlabelledResponsesAreDecidedByTheirBytes(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        []byte
		wantFile    bool
	}{
		{name: "octet-stream carrying a pdf", contentType: "application/octet-stream", body: pdfBytes, wantFile: true},
		{name: "octet-stream carrying text", contentType: "application/octet-stream", body: []byte("plain words, mislabelled"), wantFile: false},
		{name: "octet-stream carrying NUL bytes", contentType: "application/octet-stream", body: []byte("bin\x00\x01\x02ary"), wantFile: true},
		{name: "no content type at all, text", contentType: "", body: []byte("hello there"), wantFile: false},
		{name: "no content type at all, pdf", contentType: "", body: pdfBytes, wantFile: true},
		{name: "empty body", contentType: "application/octet-stream", body: nil, wantFile: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveBytes(t, tc.contentType, tc.body)
			ctx, _ := fetchCtx(t)

			_, err := (&DirectFetchProvider{}).Fetch(ctx, srv.URL+"/download")

			var nonPage *NonPageContent
			if got := errors.As(err, &nonPage); got != tc.wantFile {
				t.Fatalf("diverted to file = %v, want %v (err=%v)", got, tc.wantFile, err)
			}
		})
	}
}

// TestSavedFileDispatchesCorrectlyInReadFile is the coupling that makes the
// whole feature useful: web_fetch's answer is "call read_file on this path", so
// the saved name must send the file down read_file's intended branch.
// DetectFileType consults the extension before magic bytes, which is why the
// extension is chosen from the content type rather than the URL.
func TestSavedFileDispatchesCorrectlyInReadFile(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        []byte
		// A URL extension that contradicts the content type — the shape that
		// breaks a URL-first rule.
		urlPath  string
		wantType FileType
	}{
		{name: "pdf behind a script url", contentType: "application/pdf", body: pdfBytes, urlPath: "/download.php", wantType: FileTypePDF},
		{name: "png behind a query endpoint", contentType: "image/png", body: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, urlPath: "/img", wantType: FileTypeImage},
		{name: "mp3 behind a redirect path", contentType: "audio/mpeg", body: []byte("ID3\x03\x00\x00\x00audio"), urlPath: "/stream", wantType: FileTypeAudio},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveBytes(t, tc.contentType, tc.body)
			ctx, _ := fetchCtx(t)

			_, err := (&DirectFetchProvider{}).Fetch(ctx, srv.URL+tc.urlPath)
			var nonPage *NonPageContent
			if !errors.As(err, &nonPage) {
				t.Fatalf("err = %v, want *NonPageContent", err)
			}

			if got, _ := DetectFileType(nonPage.Path); got != tc.wantType {
				t.Errorf("read_file would classify %s as %v, want %v", nonPage.Path, got, tc.wantType)
			}
		})
	}
}

// TestFileWithoutWorkspaceFailsLoud: silently returning the bytes as page text
// is the exact defect this feature removes, so the no-workspace path must be an
// error rather than a fallback to it.
func TestFileWithoutWorkspaceFailsLoud(t *testing.T) {
	srv := serveBytes(t, "application/pdf", pdfBytes)

	content, err := (&DirectFetchProvider{}).Fetch(context.Background(), srv.URL+"/paper.pdf")

	if err == nil {
		t.Fatalf("want an error, got content %q", content)
	}
	if content != "" {
		t.Errorf("content = %q, want empty — binary must never reach the caller", content)
	}
	var nonPage *NonPageContent
	if errors.As(err, &nonPage) {
		t.Error("must not claim a file was saved when none was")
	}
	if !strings.Contains(err.Error(), "workspace") {
		t.Errorf("error %q should name the missing workspace", err)
	}
}

// TestOversizedFileIsRefusedNotTruncated: a file cut off at the cap is a
// corrupt file, and read_file would accept it — the failure would surface much
// later, as a pdftotext/unzip error naming neither the limit nor the cause.
func TestOversizedFileIsRefusedNotTruncated(t *testing.T) {
	huge := make([]byte, webFetchMaxFileBytes+1024)
	copy(huge, pdfBytes)
	srv := serveBytes(t, "application/pdf", huge)
	ctx, ws := fetchCtx(t)

	content, err := (&DirectFetchProvider{}).Fetch(ctx, srv.URL+"/big.pdf")

	if err == nil {
		t.Fatalf("want an error, got %d chars of content", len(content))
	}
	var nonPage *NonPageContent
	if errors.As(err, &nonPage) {
		t.Fatal("an oversized file must not be reported as a successful save")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error %q should name the size limit", err)
	}
	// The partial write must not survive — a leftover truncated file would be
	// found by a later glob/read and taken for a real download.
	entries, _ := os.ReadDir(filepath.Join(ws, "media"))
	if len(entries) != 0 {
		t.Errorf("truncated file left behind: %d entr(ies) in media/", len(entries))
	}
}

// TestNonPageResultPointsAtTheRightTool pins the two branches of the guidance:
// types read_file dispatches on defer to read_file, and everything else is sent
// to exec. Restating read_file's own per-type instructions here is what this
// split exists to avoid.
func TestNonPageResultPointsAtTheRightTool(t *testing.T) {
	tool := &WebFetchTool{}
	dir := t.TempDir()

	cases := []struct {
		name string
		file string
		body []byte
		want string
	}{
		{name: "pdf defers to read_file", file: "a.pdf", body: pdfBytes, want: "read_file"},
		{name: "image defers to read_file", file: "a.png", body: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, want: "read_file"},
		{name: "archive goes to exec", file: "a.zip", body: []byte("PK\x03\x04\x00\x00"), want: "exec"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			if err := os.WriteFile(path, tc.body, 0o644); err != nil {
				t.Fatal(err)
			}

			got := tool.nonPageResult("https://example.com/x", "raw",
				&NonPageContent{Path: path, MIME: "application/octet-stream", Size: int64(len(tc.body))})

			if !strings.Contains(got, tc.want) {
				t.Errorf("result should point at %s:\n%s", tc.want, got)
			}
			if !strings.Contains(got, path) {
				t.Errorf("result must carry the saved path:\n%s", got)
			}
		})
	}
}
