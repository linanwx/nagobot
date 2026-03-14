package provider

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

const (
	imageMaxLongEdge      = 1568
	imageTokensPerPixel   = 750
	imageTokensFallback   = 1000
)

// EstimateImageTokens estimates the token cost of an image file.
// Reads only the file header to get dimensions, then applies:
// scale longest edge to 1568 if larger, tokens = (w*h)/750.
// Returns a conservative fallback (1000) if the file cannot be read.
func EstimateImageTokens(filePath string) int {
	f, err := os.Open(filePath)
	if err != nil {
		return imageTokensFallback
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return imageTokensFallback
	}
	return estimateImageTokensFromDimensions(cfg.Width, cfg.Height)
}

// estimateImageTokensFromDimensions computes image tokens from pixel dimensions.
// If the longest edge exceeds 1568, the image is scaled down proportionally.
func estimateImageTokensFromDimensions(width, height int) int {
	if width <= 0 || height <= 0 {
		return imageTokensFallback
	}

	w, h := width, height
	// Scale down if longest edge exceeds limit.
	longEdge := w
	if h > longEdge {
		longEdge = h
	}
	if longEdge > imageMaxLongEdge {
		scale := float64(imageMaxLongEdge) / float64(longEdge)
		w = int(float64(w) * scale)
		h = int(float64(h) * scale)
	}

	tokens := (w * h) / imageTokensPerPixel
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
