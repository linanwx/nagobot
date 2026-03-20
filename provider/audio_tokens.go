package provider

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	audioTokensPerSecond = 32   // Gemini: ~32 tokens per second of audio.
	audioTokensFallback  = 1000 // Conservative fallback for unreadable files.
)

// audioBitrateEstimates maps audio MIME types to approximate bytes per second.
// Used to estimate duration from file size when header parsing is not available.
var audioBitrateEstimates = map[string]int{
	"audio/ogg":  4000,  // Opus ~32kbps
	"audio/mpeg": 16000, // MP3 ~128kbps
	"audio/mp4":  16000, // AAC ~128kbps
	"audio/wav":  32000, // PCM 16-bit mono 16kHz
	"audio/flac": 24000, // FLAC ~192kbps
	"audio/aac":  16000, // AAC ~128kbps
}

// EstimateAudioTokens estimates the token cost of an audio file.
// Uses file size and MIME type to estimate duration, then applies 32 tokens/sec.
func EstimateAudioTokens(filePath string) int {
	info, err := os.Stat(filePath)
	if err != nil {
		return audioTokensFallback
	}
	size := int(info.Size())
	if size <= 0 {
		return audioTokensFallback
	}

	// Determine MIME type from extension for bitrate lookup.
	ext := strings.ToLower(filepath.Ext(filePath))
	mime := extensionToAudioMime(ext)
	bytesPerSec, ok := audioBitrateEstimates[mime]
	if !ok {
		bytesPerSec = 8000 // Conservative default: ~64kbps
	}

	durationSec := size / bytesPerSec
	if durationSec < 1 {
		durationSec = 1
	}

	tokens := durationSec * audioTokensPerSecond
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

func extensionToAudioMime(ext string) string {
	switch ext {
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	default:
		return ""
	}
}
