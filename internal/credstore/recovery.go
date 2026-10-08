package credstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// recoveryPrefix starts every recovery item's name. A recovery item holds a
// live credential a swap overwrote and could not file under an account
// (vault.SwapToWith). It is neither a vault entry nor a live item, and the
// store never treats it as either: on Linux, where every other non-vault name
// means the live credential file, it has a directory of its own.
const recoveryPrefix = "claudeswitch-recovery-"

// RecoveryService names a profile's recovery item in a slot:
// claudeswitch-recovery-<profile>-<slot>.
//
// A swap that names no profile ("") gets slots of its own,
// claudeswitch-recovery-<slot>, rather than borrowing "default"'s: a declared
// profile may be called "default" while the unnamed swap wrote another
// Claude Code's item, and `cs recovery` would then show that credential under
// the wrong profile. Names parse back unambiguously: the slot is always the
// number after the last '-', and an unnamed slot's name has no '-' after the
// prefix.
func RecoveryService(profile string, slot int) string {
	return recoveryPrefix + RecoverySlotID(profile, slot)
}

// RecoverySlotID is how a person names a recovery slot: "<profile>-<slot>",
// or "<slot>" alone for the unnamed slots.
func RecoverySlotID(profile string, slot int) string {
	if profile == "" {
		return strconv.Itoa(slot)
	}
	return profile + "-" + strconv.Itoa(slot)
}

// ParseRecoverySlotID reads a slot id back: the slot is the number after the
// last '-', the profile everything before it ("" when there is no '-').
func ParseRecoverySlotID(id string) (profile string, slot int, ok bool) {
	num := id
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		profile, num = id[:i], id[i+1:]
		if profile == "" {
			return "", 0, false
		}
	}
	if num == "" || strings.TrimLeft(num, "0123456789") != "" || num[0] == '0' {
		return "", 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 {
		return "", 0, false
	}
	return profile, n, true
}

// ParseRecoveryService reads a recovery item's name back into its profile
// and slot.
func ParseRecoveryService(name string) (profile string, slot int, ok bool) {
	rest, found := strings.CutPrefix(name, recoveryPrefix)
	if !found {
		return "", 0, false
	}
	return ParseRecoverySlotID(rest)
}

// IsRecoveryService reports whether a name is a recovery item's.
func IsRecoveryService(name string) bool { return strings.HasPrefix(name, recoveryPrefix) }

// recoveryPath is a recovery item's file where items are files (Linux):
// ~/.claude/claudeswitch/recovery/<profile>-<slot>.json, beside the vault
// and, like it, independent of CLAUDE_CONFIG_DIR.
func recoveryPath(name string) (string, error) {
	if !IsRecoveryService(name) {
		return "", fmt.Errorf("%q is not a recovery item's name", name)
	}
	base := name[len(recoveryPrefix):]
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) ||
		filepath.Base(base) != base || strings.HasPrefix(base, "-") || strings.Contains(base, "..") {
		return "", fmt.Errorf("refusing a recovery item name that is not a plain name: %q", name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "claudeswitch", "recovery", base+".json"), nil
}
