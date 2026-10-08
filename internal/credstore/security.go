package credstore

import "sync"

// defaultSecurityBinary is the security(1) every keychain call runs, by
// absolute path. Run by bare name it would be whichever `security` comes
// first on PATH — and every write hands that program a token on stdin.
const defaultSecurityBinary = "/usr/bin/security"

var (
	securityMu  sync.RWMutex
	securityBin = defaultSecurityBinary
)

// securityBinary is the security(1) to run.
func securityBinary() string {
	securityMu.RLock()
	defer securityMu.RUnlock()
	return securityBin
}

// UseSecurityBinary points every keychain call at another program and returns
// a function that restores the default. It is a test seam, for
// internal/testshim, and nothing else may call it: production code has no way
// to change which security(1) receives the secrets, by environment or
// otherwise.
func UseSecurityBinary(path string) (restore func()) {
	securityMu.Lock()
	old := securityBin
	securityBin = path
	securityMu.Unlock()
	return func() {
		securityMu.Lock()
		securityBin = old
		securityMu.Unlock()
	}
}
