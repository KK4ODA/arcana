package hotspot

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeNM struct {
	calls  [][]string
	active bool
}

func (f *fakeNM) run(ctx context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	j := strings.Join(args, " ")
	switch {
	case j == "-t -f DEVICE,TYPE,STATE device":
		return "eth0:ethernet:connected\nwlan0:wifi:disconnected\nlo:loopback:unmanaged\n", nil
	case j == "-t -f NAME,DEVICE connection show --active":
		if f.active {
			return "Wired:eth0\narcana-hotspot:wlan0\n", nil
		}
		return "Wired:eth0\n", nil
	case strings.HasPrefix(j, "-t -g 802-11-wireless.ssid"):
		return "Arcana Library\n", nil
	case strings.HasPrefix(j, "device wifi hotspot"):
		f.active = true
		return "Device 'wlan0' successfully activated", nil
	case j == "connection down arcana-hotspot":
		if !f.active {
			return "", errors.New("Error: 'arcana-hotspot' is not an active connection.")
		}
		f.active = false
		return "", nil
	case j == "connection delete arcana-hotspot":
		return "", nil
	}
	return "", errors.New("unexpected: " + j)
}

func TestHotspot(t *testing.T) {
	f := &fakeNM{}
	m := &Manager{Run: f.run, Binary: "/usr/bin/nmcli"}
	st := m.Status(context.Background())
	if !st.Supported || st.Active || len(st.Devices) != 1 || st.Devices[0] != "wlan0" {
		t.Fatalf("status %+v", st)
	}
	if err := m.Start(context.Background(), "", "Arcana Library", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := m.Start(context.Background(), "eth0", "Arcana Library", "longenough1"); err == nil {
		t.Fatal("non-Wi-Fi device accepted")
	}
	if err := m.Start(context.Background(), "", "Arcana Library", "longenough1"); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	want := []string{"device", "wifi", "hotspot", "ifname", "wlan0", "con-name", "arcana-hotspot", "ssid", "Arcana Library", "password", "longenough1"}
	if strings.Join(last, "|") != strings.Join(want, "|") {
		t.Fatalf("nmcli args %q", last)
	}
	st = m.Status(context.Background())
	if !st.Active || st.Device != "wlan0" || st.SSID != "Arcana Library" {
		t.Fatalf("after start %+v", st)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background()); err == nil || !strings.Contains(err.Error(), "not an active") {
		t.Fatalf("second stop: %v", err)
	}
	none := &Manager{}
	if st := none.Status(context.Background()); st.Supported || st.Reason == "" {
		t.Fatalf("without nmcli: %+v", st)
	}
}

func TestValidateAndTerse(t *testing.T) {
	for _, c := range []struct {
		ssid, pw string
		ok       bool
	}{
		{"Arcana", "12345678", true},
		{"", "12345678", false},
		{strings.Repeat("x", 33), "12345678", false},
		{"Ar\ncana", "12345678", false},
		{"Arcana", "pässwörd", false},
		{"Arcana", strings.Repeat("p", 64), false},
	} {
		if err := Validate(c.ssid, c.pw); (err == nil) != c.ok {
			t.Errorf("Validate(%q, %q) = %v", c.ssid, c.pw, err)
		}
	}
	if got := splitTerse(`My\:Net:wlan0`); len(got) != 2 || got[0] != "My:Net" {
		t.Fatalf("splitTerse %q", got)
	}
}
