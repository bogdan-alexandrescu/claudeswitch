//go:build darwin

package credstore

import (
	"errors"
	"os/exec"
	"testing"
)

// security(1) exits 44 for errSecItemNotFound: that read is a definite
// "holds nothing", while any other failure says nothing about the item.
func TestDarwinReadClassifiesAMissingItem(t *testing.T) {
	missing := exec.Command("sh", "-c", "exit 44").Run()
	if err := readError("svc", missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("exit 44 -> %v, want ErrNotFound", err)
	}
	other := exec.Command("sh", "-c", "exit 51").Run()
	if err := readError("svc", other); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("exit 51 -> %v, want a plain error", err)
	}
}
