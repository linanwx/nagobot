package auth

import (
	"strings"
	"testing"
)

// setupWithPerson runs the link → redeem → create-person flow and returns the
// manager and live setup token bound to a fresh person.
func setupWithPerson(t *testing.T, username string) (*Manager, string) {
	t.Helper()
	m := newTestManager(t)
	link, _ := m.MintLoginLink()
	setup, ok := m.RedeemCode(link[len(link)-32:])
	if !ok {
		t.Fatal("redeem failed")
	}
	if _, err := m.CreatePersonForSetup(setup, username); err != nil {
		t.Fatalf("CreatePersonForSetup: %v", err)
	}
	return m, setup
}

func TestSetPasswordAndLogin(t *testing.T) {
	m, setup := setupWithPerson(t, "linan")

	// Too short is rejected; the setup session stays live for a retry.
	if _, _, err := m.SetPasswordForSetup(setup, "short", "ua"); err == nil {
		t.Fatal("7-char password must be rejected")
	}
	if !m.SetupValid(setup) {
		t.Fatal("setup session must survive a rejected password")
	}

	token, p, err := m.SetPasswordForSetup(setup, "correct-horse", "ua")
	if err != nil {
		t.Fatalf("SetPasswordForSetup: %v", err)
	}
	if p.Username != "linan" || token == "" {
		t.Fatalf("unexpected result: %v / %q", p, token)
	}
	// Setup is spent, the issued session validates.
	if m.SetupValid(setup) {
		t.Fatal("setup session must close after password set")
	}
	if got, ok := m.ValidateSession(token); !ok || got.Username != "linan" {
		t.Fatal("issued session must validate")
	}
	// The hash is bcrypt, never the plaintext.
	stored := m.persons.byUsername("linan")
	if stored.PasswordHash == "" || strings.Contains(stored.PasswordHash, "correct-horse") {
		t.Fatalf("password stored wrong: %q", stored.PasswordHash)
	}

	// Login: right password works (username case-insensitive), wrong fails.
	tok2, p2, err := m.PasswordLogin("LINAN", "correct-horse", "1.2.3.4", "ua")
	if err != nil || p2.Username != "linan" || tok2 == "" {
		t.Fatalf("password login failed: %v", err)
	}
	if _, _, err := m.PasswordLogin("linan", "wrong-password", "1.2.3.4", "ua"); err == nil {
		t.Fatal("wrong password must fail")
	}
}

func TestPasswordLoginUniformError(t *testing.T) {
	m, setup := setupWithPerson(t, "alice")
	if _, _, err := m.SetPasswordForSetup(setup, "password-alice", "ua"); err != nil {
		t.Fatalf("SetPasswordForSetup: %v", err)
	}

	_, _, errUnknown := m.PasswordLogin("nobody", "whatever-pw", "5.6.7.8", "ua")
	_, _, errWrong := m.PasswordLogin("alice", "whatever-pw", "5.6.7.8", "ua")
	if errUnknown == nil || errWrong == nil {
		t.Fatal("both must fail")
	}
	if errUnknown.Error() != errWrong.Error() {
		t.Fatalf("errors must be uniform (no user enumeration): %q vs %q", errUnknown, errWrong)
	}
	// A passkey-only person (no password set) must also read identically.
	m2, setup2 := setupWithPerson(t, "bob")
	_ = setup2 // bob has no password
	_, _, errNoPw := m2.PasswordLogin("bob", "whatever-pw", "5.6.7.8", "ua")
	if errNoPw == nil || errNoPw.Error() != errWrong.Error() {
		t.Fatalf("no-password person must read like wrong password: %q", errNoPw)
	}
}

func TestPasswordLoginRateLimit(t *testing.T) {
	m, setup := setupWithPerson(t, "carol")
	if _, _, err := m.SetPasswordForSetup(setup, "password-carol", "ua"); err != nil {
		t.Fatalf("SetPasswordForSetup: %v", err)
	}

	for i := 0; i < loginMaxFailures; i++ {
		if _, _, err := m.PasswordLogin("carol", "wrong", "9.9.9.9", "ua"); err == nil {
			t.Fatal("wrong password must fail")
		}
	}
	// Locked out now — even the RIGHT password is refused for this IP.
	if _, _, err := m.PasswordLogin("carol", "password-carol", "9.9.9.9", "ua"); err == nil {
		t.Fatal("lockout must refuse even correct password")
	}
	// A different IP is unaffected.
	if _, _, err := m.PasswordLogin("carol", "password-carol", "10.0.0.1", "ua"); err != nil {
		t.Fatalf("other IP must not be locked: %v", err)
	}
	// Success cleared that IP's counter; failures start fresh.
	if _, _, err := m.PasswordLogin("carol", "wrong", "10.0.0.1", "ua"); err == nil {
		t.Fatal("wrong password must fail")
	}
	if _, _, err := m.PasswordLogin("carol", "password-carol", "10.0.0.1", "ua"); err != nil {
		t.Fatalf("one failure must not lock: %v", err)
	}
}
