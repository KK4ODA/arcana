package apps

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// appState is kept in StateFile.
type appState struct {
	// Passwords are the administrator passwords Arcana set at an app's
	// first start, by app ID.
	Passwords map[string]string `json:"passwords,omitempty"`
	// Network lists the apps devices on the network may open.
	Network map[string]bool `json:"network,omitempty"`
}

// loadStateLocked reads the state once; the caller holds m.mu.
func (m *Manager) loadStateLocked() *appState {
	if m.state != nil {
		return m.state
	}
	st := &appState{}
	if m.StateFile != "" {
		if data, err := os.ReadFile(m.StateFile); err == nil {
			_ = json.Unmarshal(data, st)
		}
	}
	if st.Passwords == nil {
		st.Passwords = map[string]string{}
	}
	if st.Network == nil {
		st.Network = map[string]bool{}
	}
	m.state = st
	return st
}

func (m *Manager) saveStateLocked() error {
	if m.StateFile == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.StateFile), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(m.StateFile, data, 0o600)
}

// Password returns the administrator password Arcana set for an app, if
// it set one.
func (m *Manager) Password(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadStateLocked().Passwords[id]
}

// NetworkAllowed reports whether devices on the network may open a native
// app.
func (m *Manager) NetworkAllowed(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadStateLocked().Network[id]
}

// SetNetwork allows or refuses devices on the network a native app.
func (m *Manager) SetNetwork(id string, allow bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReadOnly {
		return errors.New("this library is read-only")
	}
	st := m.loadStateLocked()
	if allow {
		st.Network[id] = true
	} else {
		delete(st.Network, id)
	}
	return m.saveStateLocked()
}

// ErrNotReady means an app cannot run now; Status says why.
var ErrNotReady = errors.New("the app is not ready")

// service returns the supervisor of a ready native app, creating it.
func (m *Manager) service(id string) (*Supervisor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sv := m.services[id]; sv != nil {
		return sv, nil
	}
	e := m.apps[id]
	if e == nil || e.state != "ready" || e.spec.Service == nil || m.closed {
		return nil, ErrNotReady
	}
	env := Env{Runtime: e.dir, Data: m.dataDir(id), Files: m.Files,
		Cache: filepath.Join(m.Cache, "data", id), Platform: m.Platform, Log: m.log()}
	if e.spec.Service.AdminUser != "" {
		st := m.loadStateLocked()
		if st.Passwords[id] == "" {
			st.Passwords[id] = randomPassword()
			if err := m.saveStateLocked(); err != nil {
				return nil, err
			}
		}
		env.Password = st.Passwords[id]
	}
	sv := newSupervisor(e.spec, env, m.log())
	if m.services == nil {
		m.services = map[string]*Supervisor{}
	}
	m.services[id] = sv
	return sv, nil
}

// Start starts a native app's program.
func (m *Manager) Start(id string) error {
	sv, err := m.service(id)
	if err != nil {
		return err
	}
	sv.Start()
	return nil
}

// Stop stops a native app's program; the next visit starts it again.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	sv := m.services[id]
	m.mu.Unlock()
	if sv != nil {
		sv.Stop()
	}
}

// stopServiceLocked stops and forgets an app's program (the app was
// updated or removed); the caller holds m.mu.
func (m *Manager) stopServiceLocked(id string) {
	if sv := m.services[id]; sv != nil {
		delete(m.services, id)
		go sv.Close()
	}
}

// randomPassword returns two groups of eight letters and digits joined
// by a hyphen (about 95 bits), with lower and upper case letters and a
// digit in it: Calibre-Web refuses passwords without each of these and a
// sign, and the hyphen is easy to type on a phone.
func randomPassword() string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for {
		b := make([]byte, 17)
		for i := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				panic(err)
			}
			b[i] = alphabet[n.Int64()]
		}
		b[8] = '-'
		s := string(b)
		if strings.ContainsAny(s, "abcdefghijkmnopqrstuvwxyz") && strings.ContainsAny(s, "ABCDEFGHJKLMNPQRSTUVWXYZ") && strings.ContainsAny(s, "23456789") {
			return s
		}
	}
}
