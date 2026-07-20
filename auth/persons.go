// Package auth implements web login for nagobot.
//
// The credential model has exactly two stages: a one-time login code
// (minted by the CLI, delivered as a link, 30 minutes, single use) that
// bootstraps a browser, and a passkey (WebAuthn) that is the durable
// credential for logging back in. There are no passwords and no
// self-registration: a person enters the system only through a login link.
//
// Person is the cross-channel identity: one human, many channel
// identities ("discord:1480...", ...) plus any number of passkeys.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Person is one human across all channels.
type Person struct {
	ID          string                `json:"id"`
	Username    string                `json:"username"`
	CreatedAt   time.Time             `json:"created_at"`
	Identities  []string              `json:"identities,omitempty"`
	Credentials []webauthn.Credential `json:"credentials,omitempty"`
}

// waUser adapts Person to the webauthn.User interface. The WebAuthn user
// handle is the person ID, which is how discoverable-credential login maps
// an assertion back to a person.
type waUser struct{ p *Person }

func (u waUser) WebAuthnID() []byte                         { return []byte(u.p.ID) }
func (u waUser) WebAuthnName() string                       { return u.p.Username }
func (u waUser) WebAuthnDisplayName() string                { return u.p.Username }
func (u waUser) WebAuthnCredentials() []webauthn.Credential { return u.p.Credentials }

type personsFile struct {
	Persons []*Person `json:"persons"`
}

// personStore is the file-backed person registry ({system}/persons.json).
type personStore struct {
	mu      sync.Mutex
	path    string
	persons []*Person
}

func newPersonStore(systemDir string) (*personStore, error) {
	s := &personStore{path: filepath.Join(systemDir, "persons.json")}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read persons store: %w", err)
	}
	var f personsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	s.persons = f.Persons
	return s, nil
}

// saveLocked writes the store atomically. Caller must hold mu.
func (s *personStore) saveLocked() error {
	sort.Slice(s.persons, func(i, j int) bool { return s.persons[i].CreatedAt.Before(s.persons[j].CreatedAt) })
	data, err := json.MarshalIndent(personsFile{Persons: s.persons}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func randomToken(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// create adds a new person with a unique username.
func (s *personStore) create(username string) (*Person, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}
	if len(username) > 64 {
		return nil, fmt.Errorf("username too long (max 64)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		if strings.EqualFold(p.Username, username) {
			return nil, fmt.Errorf("username %q already exists", username)
		}
	}
	p := &Person{
		ID:        "p_" + randomToken(8),
		Username:  username,
		CreatedAt: time.Now(),
	}
	s.persons = append(s.persons, p)
	if err := s.saveLocked(); err != nil {
		s.persons = s.persons[:len(s.persons)-1]
		return nil, fmt.Errorf("save persons store: %w", err)
	}
	return p, nil
}

func (s *personStore) byID(id string) *Person {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (s *personStore) list() []*Person {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Person, len(s.persons))
	copy(out, s.persons)
	return out
}

// addIdentity binds a channel identity ("discord:1480...") to a person.
// An identity can belong to at most one person; rebinding moves it.
func (s *personStore) addIdentity(personID, identity string) error {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return fmt.Errorf("identity is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var target *Person
	for _, p := range s.persons {
		if p.ID == personID {
			target = p
			break
		}
	}
	if target == nil {
		return fmt.Errorf("person %s not found", personID)
	}
	for _, p := range s.persons {
		if p == target {
			continue
		}
		for i, id := range p.Identities {
			if id == identity {
				p.Identities = append(p.Identities[:i], p.Identities[i+1:]...)
				break
			}
		}
	}
	for _, id := range target.Identities {
		if id == identity {
			return nil // already bound
		}
	}
	target.Identities = append(target.Identities, identity)
	sort.Strings(target.Identities)
	return s.saveLocked()
}

// addCredential appends a passkey to a person.
func (s *personStore) addCredential(personID string, cred webauthn.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		if p.ID == personID {
			p.Credentials = append(p.Credentials, cred)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("person %s not found", personID)
}

// updateCredential replaces the stored credential matching cred.ID (sign
// counter / clone-warning bookkeeping after a successful assertion).
func (s *personStore) updateCredential(personID string, cred webauthn.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.persons {
		if p.ID != personID {
			continue
		}
		for i := range p.Credentials {
			if string(p.Credentials[i].ID) == string(cred.ID) {
				p.Credentials[i] = cred
				return s.saveLocked()
			}
		}
	}
	return fmt.Errorf("credential not found for person %s", personID)
}
