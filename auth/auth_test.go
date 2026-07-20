package auth

import (
	"testing"

	"github.com/linanwx/nagobot/config"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := &config.Config{
		Channels: &config.ChannelsConfig{
			Web: &config.WebChannelConfig{Addr: "127.0.0.1:8080"},
		},
	}
	m, err := NewManager(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestLoginCodeSingleUse(t *testing.T) {
	m := newTestManager(t)
	link, _ := m.MintLoginLink()
	if link == "" {
		t.Fatal("empty link")
	}
	// Extract the code from the link.
	const marker = "login_code="
	idx := len(link) - 32
	code := link[idx:]
	if len(code) != 32 {
		t.Fatalf("unexpected code %q from link %q (marker %s)", code, link, marker)
	}

	setup, ok := m.RedeemCode(code)
	if !ok || setup == "" {
		t.Fatal("first redeem should succeed")
	}
	if _, ok := m.RedeemCode(code); ok {
		t.Fatal("second redeem must fail (single use)")
	}
	if _, ok := m.RedeemCode("nonexistent"); ok {
		t.Fatal("unknown code must fail")
	}
	if !m.SetupValid(setup) {
		t.Fatal("setup session should be live")
	}
}

func TestCreateAndAssociatePerson(t *testing.T) {
	m := newTestManager(t)
	_, _ = m.MintLoginLink()
	link, _ := m.MintLoginLink()
	code := link[len(link)-32:]
	setup, _ := m.RedeemCode(code)

	p, err := m.CreatePersonForSetup(setup, "linan")
	if err != nil {
		t.Fatalf("create person: %v", err)
	}
	if _, err := m.CreatePersonForSetup(setup, "LINAN"); err == nil {
		t.Fatal("case-insensitive duplicate username must fail")
	}
	if err := m.AddIdentities(p.ID, []string{"discord:123"}); err != nil {
		t.Fatalf("add identity: %v", err)
	}

	persons := m.Persons()
	if len(persons) != 1 || persons[0].Username != "linan" {
		t.Fatalf("unexpected persons: %+v", persons)
	}
	if len(persons[0].Identities) != 1 || persons[0].Identities[0] != "discord:123" {
		t.Fatalf("unexpected identities: %+v", persons[0].Identities)
	}

	// Associate flow binds the setup session to the existing person.
	link2, _ := m.MintLoginLink()
	setup2, _ := m.RedeemCode(link2[len(link2)-32:])
	p2, err := m.AssociateSetup(setup2, p.ID)
	if err != nil || p2.ID != p.ID {
		t.Fatalf("associate: %v person=%+v", err, p2)
	}
	if _, err := m.AssociateSetup(setup2, "p_nope"); err == nil {
		t.Fatal("associate to unknown person must fail")
	}
}

func TestIdentityRebindMoves(t *testing.T) {
	m := newTestManager(t)
	link, _ := m.MintLoginLink()
	setup, _ := m.RedeemCode(link[len(link)-32:])
	a, _ := m.CreatePersonForSetup(setup, "alice")

	link2, _ := m.MintLoginLink()
	setup2, _ := m.RedeemCode(link2[len(link2)-32:])
	b, _ := m.CreatePersonForSetup(setup2, "bob")

	if err := m.AddIdentities(a.ID, []string{"discord:1"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddIdentities(b.ID, []string{"discord:1"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Persons() {
		switch p.ID {
		case a.ID:
			if len(p.Identities) != 0 {
				t.Fatalf("identity should have moved off alice: %+v", p.Identities)
			}
		case b.ID:
			if len(p.Identities) != 1 {
				t.Fatalf("identity should be on bob: %+v", p.Identities)
			}
		}
	}
}

func TestRecordIdentity(t *testing.T) {
	m := newTestManager(t)
	m.RecordIdentity("discord", "1480", "Nansen")
	m.RecordIdentity("discord", "1480", "Nansen") // lastSeen-only update
	m.RecordIdentity("telegram", "42", "Bob")
	m.RecordIdentity("", "42", "noop")            // ignored
	m.RecordIdentity("discord", "", "noop")       // ignored
	ids := m.Identities()
	if len(ids) != 2 {
		t.Fatalf("want 2 identities, got %+v", ids)
	}
	m.RecordIdentity("discord", "1480", "Nansen2") // rename persists
	for _, id := range m.Identities() {
		if id.Key == "discord:1480" && id.Name != "Nansen2" {
			t.Fatalf("rename not applied: %+v", id)
		}
	}

	// nil manager must be a safe no-op (dispatcher in tests).
	var nilMgr *Manager
	nilMgr.RecordIdentity("discord", "1", "x")
}

func TestDeviceSessions(t *testing.T) {
	m := newTestManager(t)
	link, _ := m.MintLoginLink()
	setup, _ := m.RedeemCode(link[len(link)-32:])
	p, _ := m.CreatePersonForSetup(setup, "linan")

	token, err := m.sessions.mint(p.ID, "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.ValidateSession(token)
	if !ok || got.ID != p.ID {
		t.Fatalf("validate: ok=%v person=%+v", ok, got)
	}
	if _, ok := m.ValidateSession("bogus"); ok {
		t.Fatal("bogus token must fail")
	}
	m.RevokeSession(token)
	if _, ok := m.ValidateSession(token); ok {
		t.Fatal("revoked token must fail")
	}
}

func TestExemptIP(t *testing.T) {
	cfg := &config.Config{
		Channels: &config.ChannelsConfig{
			Web: &config.WebChannelConfig{
				Addr: "0.0.0.0:8080",
				Auth: &config.WebAuthConfig{ExemptCIDRs: []string{"100.64.0.0/10", "172.16.0.0/12"}},
			},
		},
	}
	m, err := NewManager(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"127.0.0.1:5432":     true,  // loopback always exempt
		"[::1]:5432":         true,  // v6 loopback
		"100.117.211.46:1":   true,  // tailscale CGNAT range from config
		"172.17.0.1:9":       true,  // docker bridge from config
		"192.168.1.5:1":      false, // LAN not in list
		"8.8.8.8:443":        false,
		"garbage":            false,
	}
	for addr, want := range cases {
		if got := m.ExemptIP(addr); got != want {
			t.Errorf("ExemptIP(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestPersistenceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Channels: &config.ChannelsConfig{Web: &config.WebChannelConfig{Addr: ":8080"}}}
	m1, err := NewManager(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	link, _ := m1.MintLoginLink()
	setup, _ := m1.RedeemCode(link[len(link)-32:])
	p, _ := m1.CreatePersonForSetup(setup, "linan")
	_ = m1.AddIdentities(p.ID, []string{"discord:1480"})
	m1.RecordIdentity("discord", "1480", "Nansen")
	token, _ := m1.sessions.mint(p.ID, "ua")

	// A fresh manager over the same dir sees persons, identities, sessions —
	// but not login codes (in-memory by design).
	m2, err := NewManager(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m2.ValidateSession(token); !ok || got.Username != "linan" {
		t.Fatalf("session did not survive restart: ok=%v got=%+v", ok, got)
	}
	if len(m2.Persons()) != 1 || len(m2.Identities()) != 1 {
		t.Fatalf("stores did not survive restart: %+v %+v", m2.Persons(), m2.Identities())
	}
}
