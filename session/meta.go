package session

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxTokenRatioSamples bounds the per-(provider, model) ratio history.
const MaxTokenRatioSamples = 10

const metaFileName = "meta.json"

// RephraseSessionSuffix is the session key suffix for rephrase sibling sessions.
const RephraseSessionSuffix = ":rephrase"

// ForkSessionInfix is the infix used in fork session keys: {parent}:fork:{purpose}.
const ForkSessionInfix = ":fork:"

// Meta holds per-session metadata persisted to {sessionDir}/meta.json.
type Meta struct {
	Agent     string          `json:"agent,omitempty"`      // Explicitly assigned agent name.
	Rephrase  bool            `json:"rephrase,omitempty"`   // Enable rephrase agent for this session.
	DiscordDM *DiscordDMMeta  `json:"discord_dm,omitempty"` // Discord DM routing.
	WeCom     *WeComMeta      `json:"wecom,omitempty"`      // WeCom routing.

	// TokenEstimateRatios records the last MaxTokenRatioSamples observations of
	// (real total tokens) / (estimated total tokens) per "provider/model" key.
	// Used for calibrating estimation accuracy and (eventually) compression
	// trigger correction.
	TokenEstimateRatios map[string][]TokenRatioSample `json:"tokenEstimateRatios,omitempty"`
}

// TokenRatioSample is one observation of estimation accuracy for a given
// provider/model: the ratio of real tokens to our estimated tokens.
type TokenRatioSample struct {
	Ratio     float64   `json:"ratio"`
	CreatedAt time.Time `json:"created_at"`
}

// DiscordDMMeta holds Discord DM routing metadata.
type DiscordDMMeta struct {
	ReplyTo string `json:"reply_to"`
	UserID  string `json:"user_id,omitempty"`
}

// WeComMeta holds WeCom routing metadata.
type WeComMeta struct {
	ReqID string `json:"req_id"`
}

// ReadMeta loads meta.json from the session directory.
// Returns zero Meta if the file doesn't exist or is unreadable.
func ReadMeta(sessionDir string) Meta {
	if sessionDir == "" {
		return Meta{}
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, metaFileName))
	if err != nil {
		return Meta{}
	}
	var m Meta
	_ = json.Unmarshal(data, &m)
	return m
}

// WriteMeta atomically writes meta.json to the session directory.
func WriteMeta(sessionDir string, m Meta) {
	if sessionDir == "" {
		return
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(sessionDir, 0755)
	_ = os.WriteFile(filepath.Join(sessionDir, metaFileName), raw, 0644)
}

// UpdateMeta reads, applies fn, and writes back meta.json.
// Safe for concurrent use via a package-level lock.
func UpdateMeta(sessionDir string, fn func(*Meta)) {
	metaMu.Lock()
	defer metaMu.Unlock()

	m := ReadMeta(sessionDir)
	fn(&m)
	WriteMeta(sessionDir, m)
}

// MetaAgent is a convenience to read just the agent field.
func MetaAgent(sessionDir string) string {
	return strings.TrimSpace(ReadMeta(sessionDir).Agent)
}

// AppendTokenRatioSample appends a ratio observation for the given
// provider+model bucket and trims the bucket to MaxTokenRatioSamples (FIFO).
// Skips silently when sessionDir/provider/model is empty or ratio is non-finite.
func AppendTokenRatioSample(sessionDir, providerName, modelName string, ratio float64) {
	if sessionDir == "" || providerName == "" || modelName == "" {
		return
	}
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return
	}
	key := providerName + "/" + modelName
	UpdateMeta(sessionDir, func(m *Meta) {
		if m.TokenEstimateRatios == nil {
			m.TokenEstimateRatios = map[string][]TokenRatioSample{}
		}
		samples := append(m.TokenEstimateRatios[key], TokenRatioSample{
			Ratio:     ratio,
			CreatedAt: time.Now(),
		})
		if len(samples) > MaxTokenRatioSamples {
			samples = samples[len(samples)-MaxTokenRatioSamples:]
		}
		m.TokenEstimateRatios[key] = samples
	})
}

var metaMu sync.Mutex
