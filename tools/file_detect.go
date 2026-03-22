package tools

import (
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// FileType classifies file content for read_file dispatch.
type FileType int

const (
	FileTypeText   FileType = iota // Valid UTF-8 text
	FileTypeImage                  // Recognized image format
	FileTypeAudio                  // Recognized audio format
	FileTypeBinary                 // Non-text, non-image binary
)

// DetectFileType returns the file type and MIME type for a file.
// It reads the first 512 bytes for magic-byte and UTF-8 detection.
func DetectFileType(path string) (FileType, string) {
	// Try image detection first (extension + magic bytes).
	if m := detectImageMime(path); m != "" {
		return FileTypeImage, m
	}

	// Try audio detection (extension + magic bytes).
	if m := detectAudioMime(path); m != "" {
		return FileTypeAudio, m
	}

	// Read a sample to check if the file is valid UTF-8 text.
	f, err := os.Open(path)
	if err != nil {
		return FileTypeBinary, "application/octet-stream"
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return FileTypeText, "text/plain" // empty file treated as text
	}
	sample := buf[:n]

	// Check for null bytes (strong binary indicator) and UTF-8 validity.
	for i := 0; i < len(sample); i++ {
		if sample[i] == 0 {
			return FileTypeBinary, "application/octet-stream"
		}
	}
	// Trim trailing incomplete UTF-8 sequence that may result from fixed-size read.
	validSample := sample
	for len(validSample) > 0 && !utf8.Valid(validSample) {
		validSample = validSample[:len(validSample)-1]
	}
	if len(validSample) == 0 {
		return FileTypeBinary, "application/octet-stream"
	}

	return FileTypeText, "text/plain"
}

// detectImageMime returns the MIME type for an image file, or empty string if not an image.
// It tries extension first, then falls back to magic bytes detection.
func detectImageMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if m, ok := imageExtensions[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension(ext); strings.HasPrefix(m, "image/") {
		return m
	}
	return detectImageMimeByMagic(path)
}

// imageExtensions maps common image file extensions to MIME types.
var imageExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
}

// audioExtensions maps common audio file extensions to MIME types.
var audioExtensions = map[string]string{
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".flac": "audio/flac",
	".aac":  "audio/aac",
}

// detectAudioMime returns the MIME type for an audio file, or empty string if not audio.
func detectAudioMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if m, ok := audioExtensions[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension(ext); strings.HasPrefix(m, "audio/") {
		return m
	}
	return detectAudioMimeByMagic(path)
}

// detectAudioMimeByMagic reads the first bytes of a file to identify the audio format.
func detectAudioMimeByMagic(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	header := make([]byte, 12)
	n, err := f.Read(header)
	if err != nil || n < 4 {
		return ""
	}
	header = header[:n]

	// OGG: "OggS"
	if n >= 4 && header[0] == 'O' && header[1] == 'g' && header[2] == 'g' && header[3] == 'S' {
		return "audio/ogg"
	}
	// MP3: ID3 tag
	if n >= 3 && header[0] == 'I' && header[1] == 'D' && header[2] == '3' {
		return "audio/mpeg"
	}
	// MP3: sync word FF FB / FF FA / FF F3 / FF F2
	if n >= 2 && header[0] == 0xFF && (header[1]&0xE0) == 0xE0 {
		return "audio/mpeg"
	}
	// FLAC: "fLaC"
	if n >= 4 && header[0] == 'f' && header[1] == 'L' && header[2] == 'a' && header[3] == 'C' {
		return "audio/flac"
	}
	// RIFF/WAVE: "RIFF....WAVE"
	if n >= 12 && header[0] == 'R' && header[1] == 'I' && header[2] == 'F' && header[3] == 'F' &&
		header[8] == 'W' && header[9] == 'A' && header[10] == 'V' && header[11] == 'E' {
		return "audio/wav"
	}

	return ""
}

// detectImageMimeByMagic reads the first bytes of a file to identify the image format.
func detectImageMimeByMagic(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	header := make([]byte, 12)
	n, err := f.Read(header)
	if err != nil || n < 4 {
		return ""
	}
	header = header[:n]

	// JPEG: FF D8 FF
	if n >= 3 && header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
		return "image/jpeg"
	}
	// PNG: 89 50 4E 47
	if n >= 4 && header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47 {
		return "image/png"
	}
	// GIF: 47 49 46 38
	if n >= 4 && header[0] == 0x47 && header[1] == 0x49 && header[2] == 0x46 && header[3] == 0x38 {
		return "image/gif"
	}
	// WebP: RIFF....WEBP
	if n >= 12 && header[0] == 0x52 && header[1] == 0x49 && header[2] == 0x46 && header[3] == 0x46 &&
		header[8] == 0x57 && header[9] == 0x45 && header[10] == 0x42 && header[11] == 0x50 {
		return "image/webp"
	}
	// BMP: 42 4D
	if n >= 2 && header[0] == 0x42 && header[1] == 0x4D {
		return "image/bmp"
	}

	return ""
}
