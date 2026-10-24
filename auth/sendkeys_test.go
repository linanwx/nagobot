package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendKeyResolvesToPersonAndSession(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.persons.create("nansen")
	if err != nil {
		t.Fatal(err)
	}
	key, err := m.MintSendKey("Nansen", "discord:123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, sendKeyPrefix) {
		t.Fatalf("key %q lacks prefix", key)
	}

	got, session, ok := m.ResolveSendKey(key)
	if !ok || got.ID != p.ID || session != "discord:123" {
		t.Fatalf("resolve = %v %q %v", got, session, ok)
	}
	if _, _, ok := m.ResolveSendKey(key + "x"); ok {
		t.Fatal("a wrong key must not resolve")
	}

	// Only the hash is persisted, and it survives a reload.
	data, _ := os.ReadFile(filepath.Join(dir, "persons.json"))
	if strings.Contains(string(data), key) {
		t.Fatal("raw send key written to persons.json")
	}
	reloaded, err := NewManager(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := reloaded.ResolveSendKey(key); !ok {
		t.Fatal("key must survive a reload")
	}
}

func TestSendKeyRequiresKnownUser(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.MintSendKey("ghost", "discord:123"); err == nil {
		t.Fatal("minting for an unknown user must fail")
	}
}

func TestRevokeSendKeysBySession(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.persons.create("nansen"); err != nil {
		t.Fatal(err)
	}
	a, _ := m.MintSendKey("nansen", "discord:1")
	b, _ := m.MintSendKey("nansen", "discord:2")

	n, err := m.RevokeSendKeys("nansen", "discord:1")
	if err != nil || n != 1 {
		t.Fatalf("revoke one = %d %v", n, err)
	}
	if _, _, ok := m.ResolveSendKey(a); ok {
		t.Fatal("revoked key still resolves")
	}
	if _, _, ok := m.ResolveSendKey(b); !ok {
		t.Fatal("revoking one session must leave the other key alone")
	}

	n, err = m.RevokeSendKeys("nansen", "")
	if err != nil || n != 1 {
		t.Fatalf("revoke all = %d %v", n, err)
	}
	if _, _, ok := m.ResolveSendKey(b); ok {
		t.Fatal("revoke-all left a key behind")
	}
}
