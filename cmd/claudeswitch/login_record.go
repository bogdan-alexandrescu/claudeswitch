package main

import (
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// recordVaulted writes into state what a sign-in that was just vaulted tells
// us about account id. `add`, `login` and `login --code` all end here.
//
// A fresh credential answers "needs sign-in", so the error that said so goes
// with it: until 2026-11-23 it stayed until the daemon next polled the
// account, and `cs list` went on saying needs sign-in after the sign-in had
// worked. Any other error is about the account, not its credential, and is
// left for that poll to settle. The poll still owns the reading itself.
func recordVaulted(st *state.State, id string, e vault.Entry) {
	st.AddVaulted(id)
	st.SetEmail(id, e.Email)
	st.SetPlan(id, e.Plan)
	acct := st.Get(id)
	acct.OrgID, acct.RefreshExpiry = e.OrgID, e.RefreshExpiry
	if !e.Expiry.IsZero() {
		acct.TokenExpiry = e.Expiry
	}
	if state.NeedsLoginErr(acct.LastErr) {
		acct.LastErr = ""
	}
}
