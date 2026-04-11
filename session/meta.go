package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

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

var metaMu sync.Mutex
