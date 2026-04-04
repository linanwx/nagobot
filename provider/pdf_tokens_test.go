package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEstimatePDFTokens_ByPageCount(t *testing.T) {
	dir := t.TempDir()
	content := []byte("%PDF-1.4\n/Type /Pages /Count 3\n/Type /Page\n/Type /Page\n/Type /Page\nendobj\n")
	path := filepath.Join(dir, "test.pdf")
	os.WriteFile(path, content, 0644)

	tokens := EstimatePDFTokens(path)
	if tokens != 3*pdfTokensPerPage {
		t.Errorf("EstimatePDFTokens = %d, want %d", tokens, 3*pdfTokensPerPage)
	}
}

func TestEstimatePDFTokens_NoSpaceVariant(t *testing.T) {
	dir := t.TempDir()
	content := []byte("%PDF-1.4\n/Type/Pages\n/Type/Page\n/Type/Page\nendobj\n")
	path := filepath.Join(dir, "nospace.pdf")
	os.WriteFile(path, content, 0644)

	tokens := EstimatePDFTokens(path)
	if tokens != 2*pdfTokensPerPage {
		t.Errorf("EstimatePDFTokens = %d, want %d", tokens, 2*pdfTokensPerPage)
	}
}

func TestEstimatePDFTokens_CompressedFallback(t *testing.T) {
	dir := t.TempDir()
	content := make([]byte, 100*1024)
	copy(content, []byte("%PDF-1.4"))
	path := filepath.Join(dir, "compressed.pdf")
	os.WriteFile(path, content, 0644)

	tokens := EstimatePDFTokens(path)
	expected := 100 * pdfTokensPerKB
	if tokens != expected {
		t.Errorf("EstimatePDFTokens = %d, want %d", tokens, expected)
	}
}

func TestEstimatePDFTokens_FileNotFound(t *testing.T) {
	tokens := EstimatePDFTokens("/nonexistent/file.pdf")
	if tokens != pdfTokensFallback {
		t.Errorf("EstimatePDFTokens = %d, want %d", tokens, pdfTokensFallback)
	}
}
