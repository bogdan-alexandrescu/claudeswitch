package main

import (
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// The JSON forms of signing in from the app (IMPROVEMENTS M4): `login
// --direct --json` starts a browser sign-in and answers with its URL;
// `login --code --json` and `add --json` answer with the vaulted account.

// directLoginJSON is what `login <id> --direct --json` prints: the URL to
// open, when the attempt expires, and what completing it will do.
func directLoginJSON(cfg *config.Config, id string) map[string]any {
	expires := time.Now().Add(oauth.PendingTTL).UTC().Truncate(time.Second)
	flow, pend, err := oauth.LoadPending()
	if err != nil {
		return map[string]any{"url": nil, "expires_at": expires, "pending": nil}
	}
	return map[string]any{
		"url":        flow.URL,
		"expires_at": expires,
		"pending": map[string]any{
			"account": pend.AccountID, "profile": orNull(pend.Profile),
			"org_id": orNull(pend.OrgID), "new_account": !hasAccount(cfg, id),
		},
	}
}

// vaultedAccountJSON is an account just vaulted, as `add --json` and
// `login --code --json` report it. The config is read again: the login
// may have just written the account's block and pool entry.
func vaultedAccountJSON(cfgPath, id string, e *vault.Entry, isNew bool) map[string]any {
	seat := ""
	if e.AccountUUID != "" {
		seat = e.AccountUUID + "@" + e.OrgID
	}
	m := map[string]any{
		"account": id, "seat": orNull(seat), "org_id": orNull(e.OrgID),
		"email": orNull(acctSeams.describe(id)), "plan": orNull(acctSeams.plan(id)),
		"profile": nil, "pool": []string{}, "configured": false,
		"new_account": isNew, "renewable": !e.NoRefreshToken,
		"access_expires_at": nil, "refresh_expires_at": nil,
	}
	if !e.Expiry.IsZero() {
		m["access_expires_at"] = e.Expiry.UTC()
	}
	if !e.RefreshExpiry.IsZero() {
		m["refresh_expires_at"] = e.RefreshExpiry.UTC()
	}
	if cfg, err := config.Load(cfgPath); err == nil {
		if hasAccount(cfg, id) {
			m["configured"] = true
		}
		if owner, ok := cfg.ProfileOf(id); ok {
			m["profile"] = owner
			if in, ok := cfg.ProfileNamed(owner); ok {
				m["pool"] = append([]string{}, in.Pool...)
			}
		}
	}
	return m
}
