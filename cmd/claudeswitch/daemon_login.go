package main

import (
	"context"
	"fmt"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// loginRecheckEvery bounds how often the daemon tries to bring a profile's
// recorded login in line: a profile read that fails is retried at most this
// often.
const loginRecheckEvery = 10 * time.Minute

// reconcileLogin brings the login Claude Code in profile il records in line
// with the account live there (P1), when a swap left it on another account
// or half written (its profile read failed), or something else wrote the
// file; and records the change (P5). A dry run only says so, once per
// mismatch. It costs one profile read, at the lowest priority, and only
// when there is something to put right.
func (d *daemon) reconcileLogin(ctx context.Context, il *profileLoop) {
	active := d.profState(il).Active
	rec, differs, known := loginOf(d.cfg, il.conf, active)
	if !known || (!differs && !rec.Partial()) {
		il.loginSig = ""
		return
	}
	why := fmt.Sprintf("Claude Code was logged in as %s, not %s", nonEmpty(rec.Email, rec.AccountUUID), active)
	if !differs {
		why = fmt.Sprintf("Claude Code's recorded login for %s was incomplete", active)
	}
	if !d.live {
		if sig := rec.AccountUUID + "/" + active; differs && il.loginSig != sig {
			il.loginSig = sig
			il.log.Info("Claude Code is logged in as another account than the live one; "+
				"a live daemon would line them up", "recorded", rec.Email, "live", active)
			_ = d.aud.Write(audit.Event{Kind: "login", Profile: il.name, From: rec.Email, To: active,
				Reason: why, DryRun: true})
		}
		return
	}
	if il.live == nil || time.Since(il.loginAt) < loginRecheckEvery {
		return
	}
	il.loginAt = time.Now()
	blob, err := il.live.Read()
	if err != nil || blob.ClaudeAIOAuth == nil {
		il.log.Warn("could not read the live credential to line up Claude Code's recorded login", "err", err)
		return
	}
	full, err := d.v.SyncLogin(ctx, identityPathOf(il.conf), active, blob.ClaudeAIOAuth.AccessToken, usage.Scheduled)
	if err != nil {
		il.log.Warn("could not line up Claude Code's recorded login", "account", active, "err", err)
		return
	}
	il.log.Info("lined up Claude Code's recorded login with the live account", "was", rec.Email,
		"now", active, "complete", full)
	if differs {
		_ = d.aud.Write(audit.Event{Kind: "login", Profile: il.name, From: rec.Email, To: active, Reason: why})
	}
}
