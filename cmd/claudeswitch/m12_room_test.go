package main

import (
	"encoding/json"
	"testing"
	"time"
)

// M12 (owner, 2026-10-08): why --json reports, per profile, on_best,
// active_room and best_room: policy.Choose's figures, so the app's
// "Already on the best" and the CLI never disagree.

func TestWhyJSONReportsRooms(t *testing.T) {
	// a at 80% session against 85 (5 points), b at 10/5 against 85/95
	// (75 points by the session window): b is roomier.
	_, cfg, st := pinWorld(t, 80)
	out := whyJSON(cfg, st, time.Now(), "")
	if out["on_best"] != false || out["active_room"] != 5.0 || out["best_room"] != 75.0 {
		t.Fatalf("on_best %v, active_room %v, best_room %v", out["on_best"], out["active_room"], out["best_room"])
	}
}

func TestWhyJSONOnBest(t *testing.T) {
	// a at 5/20 against 85/95 has 75 points (its week binds); b has 75 too.
	_, cfg, st := pinWorld(t, 5)
	out := whyJSON(cfg, st, time.Now(), "")
	if out["best"] != "b" || out["on_best"] != true || out["active_room"] != 75.0 || out["best_room"] != 75.0 {
		t.Fatalf("best %v, on_best %v, active_room %v, best_room %v",
			out["best"], out["on_best"], out["active_room"], out["best_room"])
	}
}

func TestWhyJSONRoomsPerProfile(t *testing.T) {
	_, cfg, st := multiWorld(t)
	out := whyJSON(cfg, st, time.Now(), "")
	list, _ := out["profiles"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("profiles = %v", out)
	}
	for _, m := range list {
		for _, k := range []string{"on_best", "active_room", "best_room"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%v: %s missing", m["profile"], k)
			}
		}
		switch m["profile"] {
		case "work":
			// w1 at 80% against 75 is 5 points past it; w2 at 5% has 70.
			if m["on_best"] != false || m["active_room"] != -5.0 || m["best_room"] != 70.0 {
				t.Errorf("work: on_best %v, active_room %v, best_room %v", m["on_best"], m["active_room"], m["best_room"])
			}
		case "default":
			// No other account: no best room, and nothing to be on.
			if m["on_best"] != false || m["best_room"] != nil || m["active_room"] == nil {
				t.Errorf("default: on_best %v, active_room %v, best_room %v", m["on_best"], m["active_room"], m["best_room"])
			}
		}
	}
	// Unknown is null in the JSON, never 0.
	b, err := json.Marshal(list[0])
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back["best_room"]; !ok {
		t.Error("best_room must be present when null")
	}
}
