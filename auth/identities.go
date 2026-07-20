package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Identity is one channel user the system has seen speak, recorded so the
// web login flow can offer "associate with discord:Nansen" style bindings.
type Identity struct {
	Key      string    `json:"key"`  // "discord:1480577226356559992"
	Name     string    `json:"name"` // latest display name
	LastSeen time.Time `json:"last_seen"`
}

type identitiesFile struct {
	Identities []*Identity `json:"identities"`
}

// lastSeen persistence shares the coarse granularity of the session store:
// name changes and new identities save immediately, heartbeat-like
// lastSeen-only updates save at most once per hour per identity.
type identityStore struct {
	mu         sync.Mutex
	path       string
	identities map[string]*Identity
	persisted  map[string]time.Time
}

func newIdentityStore(systemDir string) (*identityStore, error) {
	s := &identityStore{
		path:       filepath.Join(systemDir, "identities.json"),
		identities: map[string]*Identity{},
		persisted:  map[string]time.Time{},
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read identities store: %w", err)
	}
	var f identitiesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	for _, id := range f.Identities {
		s.identities[id.Key] = id
		s.persisted[id.Key] = id.LastSeen
	}
	return s, nil
}

func (s *identityStore) saveLocked() error {
	list := make([]*Identity, 0, len(s.identities))
	for _, id := range s.identities {
		list = append(list, id)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	data, err := json.MarshalIndent(identitiesFile{Identities: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	for k, id := range s.identities {
		s.persisted[k] = id.LastSeen
	}
	return nil
}

// record notes that a channel user was seen speaking.
func (s *identityStore) record(channelName, userID, displayName string) error {
	if channelName == "" || userID == "" {
		return nil
	}
	key := channelName + ":" + userID
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.identities[key]
	if id == nil {
		s.identities[key] = &Identity{Key: key, Name: displayName, LastSeen: now}
		return s.saveLocked()
	}
	nameChanged := displayName != "" && displayName != id.Name
	if nameChanged {
		id.Name = displayName
	}
	id.LastSeen = now
	if nameChanged || now.Sub(s.persisted[key]) >= lastSeenGranularity {
		return s.saveLocked()
	}
	return nil
}

// list returns all known identities, most recently seen first.
func (s *identityStore) list() []Identity {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Identity, 0, len(s.identities))
	for _, id := range s.identities {
		out = append(out, *id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}
