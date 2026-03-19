package tools

import (
	"fmt"
	"io"
	"net/http"
)

// readSearchResponse checks the HTTP status and reads the response body.
// On non-200 status it reads up to 1KB of the error body for diagnostics.
func readSearchResponse(resp *http.Response, providerName string) ([]byte, error) {
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("%s API error: HTTP %d: %s", providerName, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	return body, nil
}
