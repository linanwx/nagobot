package cmd

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

const (
	catboxAPI       = "https://catbox.moe/user/api.php"
	catboxURLPrefix = "https://files.catbox.moe/"
	catboxMaxBytes  = 200 << 20 // 200 MiB hard cap on the service side
)

var uploadHTMLCmd = &cobra.Command{
	Use:     "upload-html <file>",
	Short:   "Upload an HTML file to catbox.moe and return the public URL",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runUploadHTML,
}

func init() {
	rootCmd.AddCommand(uploadHTMLCmd)
}

func runUploadHTML(_ *cobra.Command, args []string) error {
	path := args[0]
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("file is empty")
	}
	if info.Size() > catboxMaxBytes {
		return fmt.Errorf("file %d bytes exceeds catbox limit of %d", info.Size(), catboxMaxBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("reqtype", "fileupload"); err != nil {
		return fmt.Errorf("multipart write reqtype: %w", err)
	}
	part, err := mw.CreateFormFile("fileToUpload", filepath.Base(path))
	if err != nil {
		return fmt.Errorf("multipart create file part: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("multipart copy file: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}

	req, err := http.NewRequest("POST", catboxAPI, &buf)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	body := strings.TrimSpace(string(bodyBytes))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catbox returned HTTP %d: %s", resp.StatusCode, body)
	}

	// Catbox always returns 200; success is signaled by the body being a
	// catbox files URL. Anything else is a plain-text error message.
	if !strings.HasPrefix(body, catboxURLPrefix) {
		return fmt.Errorf("catbox upload error: %s", body)
	}

	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "upload-html"},
		{"status", "ok"},
		{"url", body},
		{"file", path},
		{"size_bytes", fmt.Sprintf("%d", info.Size())},
	}, body) + "\n")
	return nil
}
