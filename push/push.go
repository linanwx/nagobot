// Package push implements Web Push notification delivery for the web
// channel: VAPID key management, a file-backed subscription store, and
// best-effort sending. Subscriptions are recorded per person (one person may
// hold several device subscriptions), but delivery fans out to every stored
// subscription — the deployment is a personal/household bot, and a message
// that reaches the web channel with nobody connected should reach every
// enrolled device.
package push

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/linanwx/nagobot/logger"
)

const sendTimeout = 10 * time.Second

// Subscription is one browser push endpoint bound to a person.
type Subscription struct {
	PersonID  string `json:"person_id,omitempty"`
	Endpoint  string `json:"endpoint"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	UserAgent string `json:"user_agent,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type vapidKeys struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// Manager owns the VAPID keypair and the subscription store.
type Manager struct {
	mu       sync.Mutex
	subsPath string
	keys     vapidKeys
	subs     []Subscription
}

// NewManager loads (or generates on first run) the VAPID keypair at
// {systemDir}/push_vapid.json and loads subscriptions from
// {systemDir}/push_subscriptions.json.
func NewManager(systemDir string) (*Manager, error) {
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		return nil, fmt.Errorf("push: create system dir: %w", err)
	}
	m := &Manager{subsPath: filepath.Join(systemDir, "push_subscriptions.json")}

	vapidPath := filepath.Join(systemDir, "push_vapid.json")
	data, err := os.ReadFile(vapidPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &m.keys); err != nil {
			return nil, fmt.Errorf("push: parse %s: %w", vapidPath, err)
		}
	case os.IsNotExist(err):
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, fmt.Errorf("push: generate VAPID keys: %w", err)
		}
		m.keys = vapidKeys{PublicKey: pub, PrivateKey: priv}
		buf, _ := json.MarshalIndent(m.keys, "", "  ")
		if err := os.WriteFile(vapidPath, buf, 0o600); err != nil {
			return nil, fmt.Errorf("push: write %s: %w", vapidPath, err)
		}
	default:
		return nil, fmt.Errorf("push: read %s: %w", vapidPath, err)
	}

	if data, err := os.ReadFile(m.subsPath); err == nil {
		if err := json.Unmarshal(data, &m.subs); err != nil {
			return nil, fmt.Errorf("push: parse %s: %w", m.subsPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("push: read %s: %w", m.subsPath, err)
	}
	return m, nil
}

// PublicKey returns the VAPID public key for pushManager.subscribe().
func (m *Manager) PublicKey() string {
	if m == nil {
		return ""
	}
	return m.keys.PublicKey
}

// Subscribe stores (or refreshes) a device subscription. The endpoint is the
// identity: re-subscribing an existing endpoint updates it in place.
func (m *Manager) Subscribe(sub Subscription) error {
	if m == nil {
		return fmt.Errorf("push disabled")
	}
	if sub.Endpoint == "" || sub.P256dh == "" || sub.Auth == "" {
		return fmt.Errorf("subscription missing endpoint or keys")
	}
	sub.CreatedAt = time.Now().Format(time.RFC3339)
	m.mu.Lock()
	defer m.mu.Unlock()
	replaced := false
	for i := range m.subs {
		if m.subs[i].Endpoint == sub.Endpoint {
			m.subs[i] = sub
			replaced = true
			break
		}
	}
	if !replaced {
		m.subs = append(m.subs, sub)
	}
	return m.saveLocked()
}

// Unsubscribe removes a device subscription by endpoint.
func (m *Manager) Unsubscribe(endpoint string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.subs[:0]
	for _, s := range m.subs {
		if s.Endpoint != endpoint {
			kept = append(kept, s)
		}
	}
	m.subs = kept
	return m.saveLocked()
}

// Subscribed reports whether an endpoint is currently enrolled.
func (m *Manager) Subscribed(endpoint string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.subs {
		if s.Endpoint == endpoint {
			return true
		}
	}
	return false
}

// HasSubscriptions reports whether any device is enrolled.
func (m *Manager) HasSubscriptions() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subs) > 0
}

// Notification is the payload sw.js receives.
type Notification struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Session string `json:"session,omitempty"` // session key to open on click
}

// Send delivers a notification to every enrolled device. Endpoints the push
// service reports as gone (404/410) are pruned. Errors are logged, never
// returned — push is best-effort by nature and callers have no recovery.
func (m *Manager) Send(n Notification) {
	m.send(n, nil)
}

// SendTo delivers a notification only to devices enrolled by the given
// persons. Returns the number of devices targeted (before delivery attempts)
// so callers can tell "nobody enrolled" apart from "sent". Subscriptions
// without a person attribution (exempt-IP or auth-off enrollments) are never
// matched by a filtered send — they only receive broadcast Send.
func (m *Manager) SendTo(n Notification, personIDs map[string]bool) int {
	if len(personIDs) == 0 {
		return 0
	}
	return m.send(n, personIDs)
}

func (m *Manager) send(n Notification, personIDs map[string]bool) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	subs := make([]Subscription, 0, len(m.subs))
	for _, s := range m.subs {
		if personIDs == nil || (s.PersonID != "" && personIDs[s.PersonID]) {
			subs = append(subs, s)
		}
	}
	keys := m.keys
	m.mu.Unlock()
	if len(subs) == 0 {
		return 0
	}

	payload, err := json.Marshal(n)
	if err != nil {
		logger.Warn("push: marshal payload failed", "err", err)
		return 0
	}

	var gone []string
	for _, s := range subs {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
			Endpoint: s.Endpoint,
			Keys:     webpush.Keys{P256dh: s.P256dh, Auth: s.Auth},
		}, &webpush.Options{
			TTL:             3600,
			Subscriber:      "https://github.com/linanwx/nagobot",
			VAPIDPublicKey:  keys.PublicKey,
			VAPIDPrivateKey: keys.PrivateKey,
		})
		cancel()
		if err != nil {
			logger.Warn("push: send failed", "endpoint", s.Endpoint, "err", err)
			continue
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			gone = append(gone, s.Endpoint)
		} else if resp.StatusCode >= 400 {
			logger.Warn("push: service rejected", "endpoint", s.Endpoint, "status", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	for _, ep := range gone {
		if err := m.Unsubscribe(ep); err != nil {
			logger.Warn("push: prune failed", "endpoint", ep, "err", err)
		} else {
			logger.Info("push: pruned dead subscription", "endpoint", ep)
		}
	}
	return len(subs)
}

func (m *Manager) saveLocked() error {
	buf, err := json.MarshalIndent(m.subs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.subsPath, buf, 0o600)
}
