package sandbox

import (
	"testing"

	"github.com/hacKarim/cadence/internal/jail"
)

func jailCreate(t *testing.T, runDir, step string, idx int) (*jail.Jail, error) {
	t.Helper()
	return jail.Create(runDir, step, idx)
}
