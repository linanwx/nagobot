package channel

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linanwx/nagobot/session"
	"github.com/linanwx/nagobot/skills"
	"github.com/linanwx/nagobot/thread/msg"
)

// --- GET /api/sessions/{key...}/skills[?name=<slug>] ---
//
// Without a name: every registered skill, with what this session did with it.
// The registry is global — every agent in every session is offered the same
// set — so the session-specific part is which skills it LOADED via use_skill,
// read from its own session.jsonl, and whether a loaded body is still sent.
//
// With a name: that skill's body, resolved for this session exactly as
// use_skill resolves it (skills.ResolvePrompt). It is the file on disk now,
// not the copy a past use_skill returned: one source serves loaded and
// never-loaded skills alike, and a Tier-1-compressed load has no copy left.
//
// Bodies are not in the list because together they run to hundreds of KB.

type sessionSkillEntry struct {
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	// Registered is false for a slug the session loaded that the registry no
	// longer has (the skill was deleted or renamed). Its body cannot be served.
	Registered bool `json:"registered"`
	Loaded     bool `json:"loaded"`
	// InContext: at least one load's body is still sent to the model — not
	// reduced to a header by Tier-1 compression and not in a trimmed
	// heartbeat turn.
	InContext    bool       `json:"in_context"`
	LoadCount    int        `json:"load_count,omitempty"`
	LastLoadedAt *time.Time `json:"last_loaded_at,omitempty"`
}

type sessionSkillsResponse struct {
	Key    string              `json:"key"`
	Skills []sessionSkillEntry `json:"skills"`
}

type sessionSkillBodyResponse struct {
	Key         string `json:"key"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	Dir         string `json:"dir,omitempty"`
	Prompt      string `json:"prompt"`
}

func (w *WebChannel) handleSessionSkills(rw http.ResponseWriter, r *http.Request, key string) {
	if w.skillsFn == nil {
		http.Error(rw, "skills are not available", http.StatusServiceUnavailable)
		return
	}
	path := w.resolveSessionFile(key, session.SessionFileName)
	if path == "" {
		http.Error(rw, "invalid session key", http.StatusBadRequest)
		return
	}

	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		w.serveSkillBody(rw, key, filepath.Dir(path), name)
		return
	}

	s, err := session.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(rw, "session not found", http.StatusNotFound)
			return
		}
		http.Error(rw, "failed to read session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	entries := map[string]*sessionSkillEntry{}
	for _, sk := range w.skillsFn() {
		entries[sk.Slug] = &sessionSkillEntry{Slug: sk.Slug, Description: sk.Description, Registered: true}
	}
	for _, m := range s.Messages {
		if m.Role != "tool" || m.Name != "use_skill" {
			continue
		}
		// A successful load opens with a `skill:` frontmatter header; a failed
		// one ("Error: skill … not found", the listing) has none.
		slug := msg.ExtractFrontmatterValueFromContent(m.Content, "skill")
		if slug == "" {
			continue
		}
		e, ok := entries[slug]
		if !ok {
			e = &sessionSkillEntry{Slug: slug}
			entries[slug] = e
		}
		e.Loaded = true
		e.LoadCount++
		if m.Compressed == "" && !m.HeartbeatTrim {
			e.InContext = true
		}
		if ts := m.Timestamp; !ts.IsZero() && (e.LastLoadedAt == nil || ts.After(*e.LastLoadedAt)) {
			e.LastLoadedAt = &ts
		}
	}

	list := make([]sessionSkillEntry, 0, len(entries))
	for _, e := range entries {
		list = append(list, *e)
	}
	sort.Slice(list, func(i, j int) bool { return skillEntryLess(list[i], list[j]) })

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(sessionSkillsResponse{Key: key, Skills: list})
}

// skillEntryLess orders loaded skills first, most recently loaded first, then
// everything else by slug.
func skillEntryLess(a, b sessionSkillEntry) bool {
	if a.Loaded != b.Loaded {
		return a.Loaded
	}
	if a.Loaded {
		at, bt := loadedAt(a), loadedAt(b)
		if !at.Equal(bt) {
			return at.After(bt)
		}
	}
	return a.Slug < b.Slug
}

func loadedAt(e sessionSkillEntry) time.Time {
	if e.LastLoadedAt == nil {
		return time.Time{}
	}
	return *e.LastLoadedAt
}

func (w *WebChannel) serveSkillBody(rw http.ResponseWriter, key, sessionDir, slug string) {
	var found *skills.Skill
	for _, sk := range w.skillsFn() {
		if sk.Slug == slug {
			found = sk
			break
		}
	}
	if found == nil {
		http.Error(rw, "skill not found", http.StatusNotFound)
		return
	}
	prompt := skills.ResolvePrompt(found.Prompt, skills.PromptVars{
		Workspace:  w.workspace,
		SessionDir: sessionDir,
		SessionKey: key,
		SkillDir:   found.Dir,
	})
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(sessionSkillBodyResponse{
		Key:         key,
		Slug:        found.Slug,
		Description: found.Description,
		Dir:         found.Dir,
		Prompt:      prompt,
	})
}
