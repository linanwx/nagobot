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

	// r2CacheControl must let the browser revalidate on every navigation, because
	// a page can be republished in place via --replace and r2.dev cannot be
	// purged (Cloudflare's purge API only covers zones, not the managed
	// pub-*.r2.dev domain). The ETag still makes an unchanged page a 304 with a
	// zero-byte body, so revalidating costs one conditional request. Measured:
	// with "immutable" a plain navigation kept rendering the pre-overwrite page.
	r2CacheControl = "public, max-age=0, must-revalidate"
)

var uploadHTMLReplace string

var uploadHTMLCmd = &cobra.Command{
	Use:     "upload-html <file>",
	Short:   "Upload an HTML file to Cloudflare R2 and return the public URL",
	GroupID: "internal",
	Args:    cobra.ExactArgs(1),
	RunE:    runUploadHTML,
}

func init() {
	uploadHTMLCmd.Flags().StringVar(&uploadHTMLReplace, "replace", "", "Republish over an already-published page, keeping its URL. Takes the URL that a previous upload-html returned. Empty publishes a new page at a new URL.")
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

	action := "created"
	key := r2KeyPrefix + r2ObjectKey(filepath.Base(path))
	if uploadHTMLReplace != "" {
		key, err = replaceTargetKey(uploadHTMLReplace, r2.PublicBaseURL)
		if err != nil {
			return err
		}
		// The page must already exist. Without this, one garbled character in the
		// URL silently publishes a phantom page nobody will ever visit, and the
		// command reports success.
		if _, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(r2.Bucket),
			Key:    aws.String(key),
		}); err != nil {
			return fmt.Errorf("--replace target %s does not exist (drop --replace to publish a new page): %w", key, err)
		}
		action = "replaced"
	}

	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(r2.Bucket),
		Key:          aws.String(key),
		Body:         f,
		ContentType:  aws.String("text/html; charset=utf-8"),
		CacheControl: aws.String(r2CacheControl),
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
		{"action", action},
		{"url", publicURL},
		{"key", key},
		{"file", path},
		{"size_bytes", fmt.Sprintf("%d", info.Size())},
	}, publicURL) + "\n")
	return nil
}

// replaceTargetKey turns the --replace value into an object key. It accepts the
// full public URL a previous upload-html returned (what the model actually has
// on hand) or the bare key it printed alongside it. Anything that does not
// resolve to this bucket's pages/ prefix is an error rather than a guess: the
// value is model-supplied and a wrong key would overwrite an unrelated page.
func replaceTargetKey(replace, publicBaseURL string) (string, error) {
	base := strings.TrimRight(publicBaseURL, "/")
	key := strings.TrimSpace(replace)

	if strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
		if !strings.HasPrefix(key, base+"/") {
			return "", fmt.Errorf("--replace URL %s is not on this bucket's public origin %s", replace, base)
		}
		key = strings.TrimPrefix(key, base+"/")
	}

	// A bare filename is unambiguous — there is only one prefix — so complete it.
	// A path under some other prefix is not, so reject it.
	if !strings.Contains(key, "/") {
		key = r2KeyPrefix + key
	}
	if !strings.HasPrefix(key, r2KeyPrefix) || strings.Contains(key, "..") || key == r2KeyPrefix {
		return "", fmt.Errorf("--replace %s does not name a page under %s", replace, r2KeyPrefix)
	}
	return key, nil
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
