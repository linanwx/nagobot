package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/linanwx/nagobot/config"
	"github.com/linanwx/nagobot/tools"
	"github.com/spf13/cobra"
)

const (
	r2EndpointTemplate = "https://%s.r2.cloudflarestorage.com"
	r2KeyPrefix        = "pages/"
	r2MaxBytes         = 200 << 20 // sensible 200 MiB cap, matches typical CDN limits
)

var uploadHTMLCmd = &cobra.Command{
	Use:     "upload-html <file>",
	Short:   "Upload an HTML file to Cloudflare R2 and return the public URL",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runUploadHTML,
}

func init() {
	rootCmd.AddCommand(uploadHTMLCmd)
}

func runUploadHTML(cmd *cobra.Command, args []string) error {
	path := args[0]
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("file is empty")
	}
	if info.Size() > r2MaxBytes {
		return fmt.Errorf("file %d bytes exceeds upload limit of %d", info.Size(), r2MaxBytes)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	r2 := cfg.R2
	if r2 == nil || r2.AccountID == "" || r2.AccessKeyID == "" || r2.SecretAccessKey == "" || r2.Bucket == "" || r2.PublicBaseURL == "" {
		return fmt.Errorf("r2 not configured: need accountId, accessKeyId, secretAccessKey, bucket, publicBaseURL in config.yaml")
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	key := r2KeyPrefix + r2ObjectKey(filepath.Base(path))

	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(r2.AccessKeyID, r2.SecretAccessKey, "")),
	)
	if err != nil {
		return fmt.Errorf("aws config: %w", err)
	}
	endpoint := fmt.Sprintf(r2EndpointTemplate, r2.AccountID)
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r2.Bucket),
		Key:         aws.String(key),
		Body:        f,
		ContentType: aws.String("text/html; charset=utf-8"),
		CacheControl: aws.String("public, max-age=31536000, immutable"),
	}); err != nil {
		return fmt.Errorf("r2 PutObject: %w", err)
	}

	publicURL := strings.TrimRight(r2.PublicBaseURL, "/") + "/" + key

	if err := verifyURL(ctx, publicURL); err != nil {
		return fmt.Errorf("uploaded but URL not reachable: %w", err)
	}

	fmt.Print(tools.CmdOutput([][2]string{
		{"command", "upload-html"},
		{"status", "ok"},
		{"url", publicURL},
		{"key", key},
		{"file", path},
		{"size_bytes", fmt.Sprintf("%d", info.Size())},
	}, publicURL) + "\n")
	return nil
}

// r2ObjectKey builds a unique key like "20260502-143012-ab12cd.html" from the
// source filename. Keeps the original extension; falls back to .html.
func r2ObjectKey(srcBase string) string {
	ext := strings.ToLower(filepath.Ext(srcBase))
	if ext == "" {
		ext = ".html"
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rb[:]) + ext
}

// verifyURL does a HEAD request to confirm the object is reachable. Public R2
// propagation is normally instant but a 5s window catches transient hiccups.
func verifyURL(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HEAD %s returned %s", url, resp.Status)
	}
	return nil
}
