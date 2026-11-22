// Package hotspot starts and stops a Wi-Fi access point through
// NetworkManager's command-line tool (nmcli) on Linux, so phones can join
// Arcana where there is no existing network. Arcana never needs
// administrator rights for this: NetworkManager's own policy decides whether
// the signed-in user may create a hotspot. Other systems are not supported;
// Status explains the alternatives.
package hotspot

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// ConnectionName is the NetworkManager connection Arcana manages.
const ConnectionName = "arcana-hotspot"

// Status describes hotspot support and state.
type Status struct {
	Supported bool     `json:"supported"`
	Reason    string   `json:"reason,omitempty"`
	Active    bool     `json:"active"`
	Device    string   `json:"device,omitempty"`
	SSID      string   `json:"ssid,omitempty"`
	Devices   []string `json:"devices,omitempty"` // Wi-Fi devices
}

// Runner runs nmcli; tests replace it.
type Runner func(ctx context.Context, args ...string) (string, error)

// Manager controls the hotspot.
type Manager struct {
	Run Runner
	// Binary is the nmcli path (found on PATH by default).
	Binary string
}

// New finds nmcli.
func New() *Manager {
	m := &Manager{}
	if runtime.GOOS == "linux" {
		if p, err := exec.LookPath("nmcli"); err == nil {
			m.Binary = p
		}
	}
	m.Run = func(ctx context.Context, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, m.Binary, args...).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return "", errors.New(msg)
		}
		return string(out), nil
	}
	return m
}

func unsupportedReason() string {
	switch runtime.GOOS {
	case "windows":
		return "Use Windows Settings → Network & internet → Mobile hotspot, then turn on network sharing in Arcana."
	case "darwin":
		return "macOS cannot create a hotspot without an internet connection to share. Join an existing Wi-Fi network or use a travel router."
	}
	return "NetworkManager (nmcli) was not found. Create the access point with your system's tools, or use the Raspberry Pi appliance setup."
}

// splitTerse splits nmcli -t output, honouring escaped colons.
func splitTerse(line string) []string {
	var out []string
	var cur strings.Builder
	esc := false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == ':':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

// Status reports the current state.
func (m *Manager) Status(ctx context.Context) Status {
	if m.Binary == "" {
		return Status{Reason: unsupportedReason()}
	}
	st := Status{Supported: true}
	out, err := m.Run(ctx, "-t", "-f", "DEVICE,TYPE,STATE", "device")
	if err != nil {
		st.Supported, st.Reason = false, "NetworkManager is not running: "+err.Error()
		return st
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := splitTerse(line)
		if len(f) >= 2 && f[1] == "wifi" {
			st.Devices = append(st.Devices, f[0])
		}
	}
	if len(st.Devices) == 0 {
		st.Supported, st.Reason = false, "No Wi-Fi adapter found."
		return st
	}
	out, err = m.Run(ctx, "-t", "-f", "NAME,DEVICE", "connection", "show", "--active")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if f := splitTerse(line); len(f) >= 2 && f[0] == ConnectionName {
				st.Active, st.Device = true, f[1]
			}
		}
	}
	if out, err := m.Run(ctx, "-t", "-g", "802-11-wireless.ssid", "connection", "show", ConnectionName); err == nil {
		st.SSID = strings.TrimSpace(out)
	}
	return st
}

// Validate checks a network name and password.
func Validate(ssid, password string) error {
	if ssid == "" || len(ssid) > 32 || !utf8.ValidString(ssid) {
		return errors.New("the network name must be 1 to 32 bytes")
	}
	for _, r := range ssid {
		if r < 0x20 || r == 0x7f {
			return errors.New("the network name contains control characters")
		}
	}
	if n := len(password); n < 8 || n > 63 {
		return errors.New("the Wi-Fi password must be 8 to 63 characters")
	}
	for _, r := range password {
		if r < 0x20 || r > 0x7e {
			return errors.New("use only printable ASCII characters in the Wi-Fi password")
		}
	}
	return nil
}

// Start creates (or replaces) and activates the hotspot.
func (m *Manager) Start(ctx context.Context, device, ssid, password string) error {
	if m.Binary == "" {
		return errors.New(unsupportedReason())
	}
	if err := Validate(ssid, password); err != nil {
		return err
	}
	st := m.Status(ctx)
	if !st.Supported {
		return errors.New(st.Reason)
	}
	if device == "" {
		device = st.Devices[0]
	}
	found := false
	for _, d := range st.Devices {
		found = found || d == device
	}
	if !found {
		return fmt.Errorf("unknown Wi-Fi device %q", device)
	}
	// Starting replaces the previous Arcana hotspot so settings apply.
	_, _ = m.Run(ctx, "connection", "delete", ConnectionName)
	if _, err := m.Run(ctx, "device", "wifi", "hotspot", "ifname", device, "con-name", ConnectionName,
		"ssid", ssid, "password", password); err != nil {
		return fmt.Errorf("start hotspot: %w", err)
	}
	return nil
}

// Stop deactivates the hotspot.
func (m *Manager) Stop(ctx context.Context) error {
	if m.Binary == "" {
		return errors.New(unsupportedReason())
	}
	if _, err := m.Run(ctx, "connection", "down", ConnectionName); err != nil {
		return fmt.Errorf("stop hotspot: %w", err)
	}
	return nil
}
