// Package model holds the app's own state: proxy configurations, the ordered
// rule list and UI preferences. The engine service is stateless across
// clients, so this is the source of truth that gets replayed on every connect.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"pbdeck/internal/pbproto"
)

// Proxy is one upstream proxy as the user configured it. EngineID is the ID
// the engine assigned in the current session (0 while not applied).
type Proxy struct {
	Key        string            `json:"key"`
	Name       string            `json:"name"`
	Type       pbproto.ProxyType `json:"type"`
	IP         string            `json:"ip"`
	Port       uint16            `json:"port"`
	User       string            `json:"user,omitempty"`
	Pass       string            `json:"pass,omitempty"`
	SendDomain bool              `json:"sendDomain"`
	EngineID   uint32            `json:"-"`
}

// Rule is one entry of the ordered rule list. The first enabled rule that
// matches a new connection wins; no match means direct.
type Rule struct {
	Key       string             `json:"key"`
	Process   string             `json:"process"`
	Hosts     string             `json:"hosts"`
	Ports     string             `json:"ports"`
	Domains   string             `json:"domains"`
	Protocol  pbproto.Protocol   `json:"protocol"`
	Action    pbproto.RuleAction `json:"action"`
	ProxyKey  string             `json:"proxyKey,omitempty"`
	Localhost bool               `json:"localhost"`
	Enabled   bool               `json:"enabled"`
	EngineID  uint32             `json:"-"`
	Note      string             `json:"note,omitempty"`
}

// EnginePrefs are engine switches the service cannot report back, so the app
// remembers them and re-applies them after every connect.
type EnginePrefs struct {
	Enabled    bool `json:"enabled"`
	TrafficLog bool `json:"trafficLog"`
	LogAll     bool `json:"logAll"`
	AutoStart  bool `json:"autoStart"`
}

// UIState is what the window needs to come back the way the user left it.
type UIState struct {
	Page     string `json:"page"`
	Theme    string `json:"theme"`
	ShowLogs bool   `json:"showLogs"`
	Compact  bool   `json:"compact"`
}

// Settings is the whole persisted state.
type Settings struct {
	Version int         `json:"version"`
	Proxies []Proxy     `json:"proxies"`
	Rules   []Rule      `json:"rules"`
	Engine  EnginePrefs `json:"engine"`
	UI      UIState     `json:"ui"`
}

// Store is a settings file with an in-memory copy.
type Store struct {
	mu   sync.Mutex
	path string
	s    Settings
}

// Open loads the settings file, creating a starter profile on first run.
func Open(path string) (*Store, error) {
	st := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &st.s); err != nil {
			return nil, err
		}
	case errors.Is(err, os.ErrNotExist):
		st.s = initial()
	default:
		return nil, err
	}
	st.normalize()
	return st, nil
}

// Path is the settings file this store reads and writes.
func (st *Store) Path() string { return st.path }

func (st *Store) normalize() {
	if st.s.Version == 0 {
		st.s.Version = 1
	}
	if st.s.Proxies == nil {
		st.s.Proxies = []Proxy{}
	}
	if st.s.Rules == nil {
		st.s.Rules = []Rule{}
	}
	if st.s.UI.Theme != "dark" {
		st.s.UI.Theme = "light"
	}
	for i := range st.s.Proxies {
		if st.s.Proxies[i].Key == "" {
			st.s.Proxies[i].Key = NewKey()
		}
	}
	for i := range st.s.Rules {
		if st.s.Rules[i].Key == "" {
			st.s.Rules[i].Key = NewKey()
		}
	}
}

// Snapshot returns a deep copy of the settings.
func (st *Store) Snapshot() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := st.s
	out.Proxies = append([]Proxy(nil), st.s.Proxies...)
	out.Rules = append([]Rule(nil), st.s.Rules...)
	return out
}

// Update mutates the settings under the lock and saves them. A save failure is
// returned but does not roll the in-memory change back: the UI already shows it
// and the next save will retry.
func (st *Store) Update(fn func(*Settings) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := fn(&st.s); err != nil {
		return err
	}
	st.normalize()
	return st.saveLocked()
}

func (st *Store) saveLocked() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// NewKey returns a short random identifier for a proxy or rule.
func NewKey() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "k" + hex.EncodeToString([]byte{byte(os.Getpid())})
	}
	return hex.EncodeToString(b[:])
}

// initial is the first-run profile: nothing configured yet. Rules and proxies
// are added by the user once the engine service is installed, so the app never
// presents invented hosts as if they were traffic.
func initial() Settings {
	return Settings{
		Version: 1,
		Proxies: []Proxy{},
		Rules:   []Rule{},
		Engine:  EnginePrefs{Enabled: true},
		UI:      UIState{Page: "connections", Theme: "light"},
	}
}
