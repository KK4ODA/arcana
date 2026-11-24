package kolibri

import (
	"os"
	"testing"

	"github.com/KK4ODA/arcana/internal/kolibri/kolibritest"
)

// TestMain lets the test binary stand in for Kolibri (kolibritest).
func TestMain(m *testing.M) {
	kolibritest.MaybeRun()
	os.Exit(m.Run())
}
