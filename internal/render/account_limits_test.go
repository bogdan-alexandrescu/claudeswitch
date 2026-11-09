package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

func TestStatusJudgesAnAccountByItsOwnLimit(t *testing.T) {
	cfg, st := judgedWorld()
	idle := st.Accounts["idle"]
	idle.Last.FiveHour.Utilization, idle.Last.SevenDay.Utilization = ptr(5), ptr(70)

	if row, _ := judgedRow(t, cfg, st, "idle", nil); !strings.Contains(row, "available") {
		t.Fatalf("70%% of a week under a 98%% trigger is available:\n%s", row)
	}
	cfg.Accounts[1].IgnoreWeeklyGTE = 65
	if row, _ := judgedRow(t, cfg, st, "idle", nil); strings.Contains(row, "available") {
		t.Errorf("70%% of a week over the account's own 65%% is not available:\n%s", row)
	}
}

func TestStatusDetailFlagsAWindowOverTheAccountsOwnLimit(t *testing.T) {
	cfg, st := judgedWorld()
	idle := st.Accounts["idle"]
	idle.Last.FiveHour.Utilization, idle.Last.SevenDay.Utilization = ptr(5), ptr(70)
	cfg.Accounts[1].IgnoreWeeklyGTE = 65

	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: true})
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "idle") {
			if !strings.Contains(line, " over") {
				t.Errorf("the week is over the account's limit:\n%s", line)
			}
			return
		}
	}
	t.Fatalf("no row:\n%s", b.String())
}

func TestTheHeadlineCountsRoomToTheAccountsOwnLimit(t *testing.T) {
	cfg, st := judgedWorld()
	cfg.Accounts[0].IgnoreWeeklyGTE = 50 // live is at 20% of its week
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Decision: &policy.Decision{Kind: policy.Stay}})
	if !strings.Contains(b.String(), "30 points before") {
		t.Errorf("room must be counted to the account's own limit:\n%s", b.String())
	}
}
