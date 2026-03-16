package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFileType_Text(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte("hello world\nline 2\n"), 0644)

	ft, mime := DetectFileType(path)
	if ft != FileTypeText {
		t.Errorf("expected FileTypeText, got %d", ft)
	}
	if mime != "text/plain" {
		t.Errorf("expected text/plain, got %s", mime)
	}
}

func TestDetectFileType_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte{}, 0644)

	ft, _ := DetectFileType(path)
	if ft != FileTypeText {
		t.Errorf("expected FileTypeText for empty file, got %d", ft)
	}
}

func TestDetectFileType_JPEG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	// JPEG magic bytes: FF D8 FF
	os.WriteFile(path, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}, 0644)

	ft, mime := DetectFileType(path)
	if ft != FileTypeImage {
		t.Errorf("expected FileTypeImage, got %d", ft)
	}
	if mime != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", mime)
	}
}

func TestDetectFileType_PNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "image.png")
	// PNG magic bytes: 89 50 4E 47
	os.WriteFile(path, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0644)

	ft, mime := DetectFileType(path)
	if ft != FileTypeImage {
		t.Errorf("expected FileTypeImage, got %d", ft)
	}
	if mime != "image/png" {
		t.Errorf("expected image/png, got %s", mime)
	}
}

func TestDetectFileType_ImageByExtension(t *testing.T) {
	dir := t.TempDir()
	// File with .webp extension but no magic bytes — extension takes priority.
	path := filepath.Join(dir, "pic.webp")
	os.WriteFile(path, []byte("not real webp data"), 0644)

	ft, mime := DetectFileType(path)
	if ft != FileTypeImage {
		t.Errorf("expected FileTypeImage, got %d", ft)
	}
	if mime != "image/webp" {
		t.Errorf("expected image/webp, got %s", mime)
	}
}

func TestDetectFileType_ImageByMagicNoExtension(t *testing.T) {
	dir := t.TempDir()
	// File with .dat extension but JPEG magic bytes.
	path := filepath.Join(dir, "data.dat")
	os.WriteFile(path, []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x00}, 0644)

	ft, mime := DetectFileType(path)
	if ft != FileTypeImage {
		t.Errorf("expected FileTypeImage, got %d", ft)
	}
	if mime != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", mime)
	}
}

func TestDetectFileType_Binary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	// Contains null bytes — binary indicator.
	os.WriteFile(path, []byte{0x7F, 0x45, 0x4C, 0x46, 0x00, 0x01, 0x02}, 0644)

	ft, mime := DetectFileType(path)
	if ft != FileTypeBinary {
		t.Errorf("expected FileTypeBinary, got %d", ft)
	}
	if mime != "application/octet-stream" {
		t.Errorf("expected application/octet-stream, got %s", mime)
	}
}

func TestDetectFileType_UTF8WithSpecialChars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chinese.txt")
	os.WriteFile(path, []byte("你好世界\n第二行\n"), 0644)

	ft, _ := DetectFileType(path)
	if ft != FileTypeText {
		t.Errorf("expected FileTypeText for UTF-8 Chinese text, got %d", ft)
	}
}
