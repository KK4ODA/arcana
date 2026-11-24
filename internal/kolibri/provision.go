package kolibri

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Setup describes the school Arcana creates in Kolibri.
type Setup struct {
	// Facility is the school's name; it also names the device.
	Facility string
	// Username and Password are the administrator's.
	Username string
	Password string
	// Language is the interface language ("en", "es").
	Language string
}

// AdminUsername is the administrator account Arcana creates.
const AdminUsername = "admin"

// provisionFile is where the provisioning file waits in KOLIBRI_HOME until
// Kolibri has read it.
const provisionFile = ".arcana-provision.json"

// NewPassword makes an administrator password: four groups of four
// letters and digits, without look-alikes (0/O, 1/l/I).
func NewPassword() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	var b strings.Builder
	for i := 0; i < 16; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String()
}

// FacilityName makes a school name from the library's label.
func FacilityName(label string) string {
	name := strings.TrimSpace(label)
	if name == "" {
		name = "Arcana"
	}
	if r := []rune(name); utf8.RuneCountInString(name) > 90 {
		name = string(r[:90])
	}
	return name
}

// WriteProvisionFile writes Kolibri's automatic provisioning file into
// home and returns its path. Kolibri reads it at its first start
// (AUTOMATIC_PROVISION_FILE) and ignores it once provisioned; Arcana
// deletes it once Kolibri runs, since it holds the password.
func WriteProvisionFile(home string, s Setup) (string, error) {
	if s.Facility == "" || s.Username == "" || s.Password == "" {
		return "", errors.New("school name, administrator and password are needed")
	}
	lang := s.Language
	if lang != "es" {
		lang = "en"
	}
	data, err := json.MarshalIndent(map[string]any{
		"facility_name": s.Facility,
		// formal: students sign in with their username alone and cannot
		// change it; teachers and administrators have passwords.
		"preset": "formal",
		"device_settings": map[string]any{
			"language_id": lang,
			"name":        s.Facility,
			// Guest access lets anyone browse without an account (progress
			// needs one) and lets Arcana's search read Kolibri.
			"allow_guest_access": true,
		},
		"superuser": map[string]string{"username": s.Username, "password": s.Password},
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(home, provisionFile)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// RemoveProvisionFile deletes the provisioning file.
func RemoveProvisionFile(home string) {
	_ = os.Remove(filepath.Join(home, provisionFile))
}
