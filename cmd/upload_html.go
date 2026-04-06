package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

const pagedropAPI = "https://pagedrop.dev/api/v1/sites"

var uploadHTMLCmd = &cobra.Command{
	Use:     "upload-html <file>",
	Short:   "Upload an HTML file to PageDrop and return the public URL",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runUploadHTML,
}

func init() {
	rootCmd.AddCommand(uploadHTMLCmd)
}

type pagedropRequest struct {
	HTML string `json:"html"`
}

type pagedropResponse struct {
	Status string `json:"status"`
	Data   struct {
		SiteID string `json:"siteId"`
		URL    string `json:"url"`
	} `json:"data"`
}

func runUploadHTML(_ *cobra.Command, args []string) error {
	content, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	if len(content) == 0 {
		return fmt.Errorf("file is empty")
	}

	reqBody, err := json.Marshal(pagedropRequest{HTML: string(content)})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(pagedropAPI, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("PageDrop returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result pagedropResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Data.URL == "" {
		return fmt.Errorf("no URL in response: %s", string(body))
	}

	fmt.Printf("---\ncommand: upload-html\nstatus: ok\nurl: %s\nsite_id: %s\nfile: %s\nsize_bytes: %d\n---\n\n%s\n",
		result.Data.URL, result.Data.SiteID, args[0], len(content), result.Data.URL)
	return nil
}
