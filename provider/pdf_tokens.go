package provider

import (
	"bytes"
	"os"
)

const (
	pdfTokensPerPage  = 534  // Gemini average from benchmarks.
	pdfTokensPerKB    = 300  // Fallback for compressed PDFs without page markers.
	pdfTokensFallback = 5000 // Conservative fallback for unreadable files.
)

// EstimatePDFTokens estimates the token cost of a PDF file.
// Uses page count from byte scanning; falls back to file size for compressed PDFs.
func EstimatePDFTokens(filePath string) int {
	return cachedEstimate(filePath, computePDFTokens)
}

func computePDFTokens(filePath string) int {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return pdfTokensFallback
	}
	if len(data) == 0 {
		return pdfTokensFallback
	}

	// Count pages: "/Type /Page" and "/Type/Page" variants, minus "/Type /Pages" directory nodes.
	pages := bytes.Count(data, []byte("/Type /Page")) - bytes.Count(data, []byte("/Type /Pages"))
	pages += bytes.Count(data, []byte("/Type/Page")) - bytes.Count(data, []byte("/Type/Pages"))

	if pages > 0 {
		return pages * pdfTokensPerPage
	}

	// Compressed PDF: estimate from file size.
	kb := len(data) / 1024
	if kb < 1 {
		kb = 1
	}
	return kb * pdfTokensPerKB
}
