package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// settings are the tunable values, described once so `cs config` can list
// them, set them, explain them and describe them to the app (`config schema
// --json`) without a second copy drifting out of step.
type setting struct {
	name string
	get  func(*config.Config) string
	set  func(*config.Config, string) error
	help string

	// kind is the value's type in the schema: percent, number, int,
	// duration, enum or list.
	kind string
	// min and max bound it (seconds for a duration); nil is unbounded.
	// minExclusive makes min itself refused.
	min, max     *float64
	minExclusive bool
	enum         []string
	// profile marks a setting a [[profile]] may override (`cs profile set`).
	profile bool
}

func num(f float64) *float64 { return &f }

func durSetter(f func(*config.Config) *config.Duration) func(*config.Config, string) error {
	return func(c *config.Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%q is not a duration (try 90s, 5m, 2h)", v)
		}
		*f(c) = config.Duration{Duration: d}
		return nil
	}
}

func parsePct(v string) (float64, error) {
	n, err := parseNum(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a percentage", v)
	}
	return n, nil
}

// parseNum reads a finite number, "%" allowed after it. NaN and Inf are
// refused: every range check compares false against NaN, so one would
// pass validation and reach the policy engine.
func parseNum(v string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(v), "%"), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	return n, nil
}

func pctSetter(f func(*config.Config) *float64) func(*config.Config, string) error {
	return func(c *config.Config, v string) error {
		n, err := parsePct(v)
		if err != nil {
			return err
		}
		*f(c) = n
		return nil
	}
}

func intSetter(f func(*config.Config) *int) func(*config.Config, string) error {
	return func(c *config.Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("%q is not a number", v)
		}
		*f(c) = n
		return nil
	}
}

// splitList reads "a,b" (or "a, b"); "" is the empty list.
func splitList(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func fmtPct(v float64) string { return fmt.Sprintf("%g", v) }

func settings() []setting {
	return []setting{
		{name: "switch_at", kind: "percent", min: num(0), minExclusive: true, max: num(100), profile: true,
			get:  func(c *config.Config) string { return fmtPct(c.SwitchAt) },
			set:  pctSetter(func(c *config.Config) *float64 { return &c.SwitchAt }),
			help: "rotate away at this much of the 5-hour window"},
		{name: "switch_at_weekly", kind: "percent", min: num(0), minExclusive: true, max: num(100), profile: true,
			get:  func(c *config.Config) string { return fmtPct(c.SwitchAtWeekly) },
			set:  pctSetter(func(c *config.Config) *float64 { return &c.SwitchAtWeekly }),
			help: "...and at this much of the weekly one"},
		{name: "hard_floor", kind: "percent", min: num(0), minExclusive: true, max: num(100), profile: true,
			get:  func(c *config.Config) string { return fmtPct(c.HardFloor) },
			set:  pctSetter(func(c *config.Config) *float64 { return &c.HardFloor }),
			help: "above this, swap mid-turn rather than wait for an idle gap (at or above switch_at)"},
		{name: "hot_threshold", kind: "percent", min: num(0), max: num(100),
			get:  func(c *config.Config) string { return fmtPct(c.HotThreshold) },
			set:  pctSetter(func(c *config.Config) *float64 { return &c.HotThreshold }),
			help: "poll the account in use every poll_hot above this, rather than poll_active"},
		{name: "switch_when", kind: "enum", enum: []string{"idle", "immediate"},
			get:  func(c *config.Config) string { return c.SwitchWhen },
			set:  func(c *config.Config, v string) error { c.SwitchWhen = v; return nil },
			help: `"idle" to prefer swapping between turns, or "immediate"`},
		{name: "max_switch_wait", kind: "duration", min: num(0),
			get:  func(c *config.Config) string { return c.MaxSwitchWait.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.MaxSwitchWait }),
			help: "stop waiting for an idle gap after this"},
		{name: "cooldown", kind: "duration", min: num(0),
			get:  func(c *config.Config) string { return c.Cooldown.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.Cooldown }),
			help: "minimum gap between rotations, to stop flapping"},
		{name: "poll_active", kind: "duration", min: num(config.MinPoll.Seconds()),
			get:  func(c *config.Config) string { return c.PollActive.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.PollActive }),
			help: "how often to read the account in use"},
		{name: "poll_hot", kind: "duration", min: num(0), minExclusive: true,
			get:  func(c *config.Config) string { return c.PollHot.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.PollHot }),
			help: "...and when it is near the trigger or burning fast"},
		{name: "poll_idle", kind: "duration", min: num(config.MinPoll.Seconds()),
			get:  func(c *config.Config) string { return c.PollIdle.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.PollIdle }),
			help: "how often to read the others"},
		{name: "api_budget", kind: "int", min: num(2),
			get:  func(c *config.Config) string { return strconv.Itoa(c.APIBudget) },
			set:  intSetter(func(c *config.Config) *int { return &c.APIBudget }),
			help: "usage calls per 5 minutes, across every process"},
		{name: "landing_margin", kind: "number", min: num(0), max: num(config.MaxLandingMargin), profile: true,
			get: func(c *config.Config) string { return fmtPct(c.Margin()) },
			set: func(c *config.Config, v string) error {
				n, err := parseNum(v)
				if err != nil {
					return fmt.Errorf("%q is not a number of points", v)
				}
				c.LandingMargin = &n
				return nil
			}, help: "a switch target needs this many points below its own trigger (0 = off)"},
		{name: "blind_failover_polls", kind: "int", min: num(0),
			get: func(c *config.Config) string { return strconv.Itoa(c.BlindPolls()) },
			set: func(c *config.Config, v string) error {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					return fmt.Errorf("%q is not a number", v)
				}
				c.BlindFailoverPolls = &n
				return nil
			}, help: "fail over after this many unreadable polls of the account in use (0 = hold)"},
		{name: "hot_reserve", kind: "int", min: num(0), max: num(config.MaxHotReserve),
			get: func(c *config.Config) string { return strconv.Itoa(c.HotReserveCalls()) },
			set: func(c *config.Config, v string) error {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					return fmt.Errorf("%q is not a number", v)
				}
				c.HotReserve = &n
				return nil
			}, help: "advanced: usage calls per account held back for hot polling"},
		{name: "unseen_calls_per_hour", kind: "number", min: num(0), max: num(config.MaxUnseenCallsPerHour),
			get: func(c *config.Config) string { return fmtPct(c.UnseenPerHour()) },
			set: func(c *config.Config, v string) error {
				n, err := parseNum(v)
				if err != nil {
					return err
				}
				c.UnseenCallsPerHour = &n
				return nil
			}, help: "advanced: usage calls an hour set aside on a live account for Claude Code's own reads"},
		{name: "refresh_window", kind: "duration", min: num(0), minExclusive: true,
			get:  func(c *config.Config) string { return c.RefreshWindow.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.RefreshWindow }),
			help: "renew a credential this long before it expires"},
		{name: "refresh_probe", kind: "duration", min: num(0),
			get:  func(c *config.Config) string { return c.RefreshProbe.String() },
			set:  durSetter(func(c *config.Config) *config.Duration { return &c.RefreshProbe }),
			help: "also renew idle accounts this often, to catch a dead refresh token"},
		{name: "models", kind: "list", profile: true,
			get:  func(c *config.Config) string { return strings.Join(c.Models, ",") },
			set:  func(c *config.Config, v string) error { c.Models = splitList(v); return nil },
			help: "models whose weekly limit counts like the weekly window (comma-separated; empty = none)"},
	}
}

// tomlValue is a setting's current value in c as TOML.
func (s setting) tomlValue(c *config.Config) string {
	v := s.get(c)
	switch s.kind {
	case "duration", "enum":
		return strconv.Quote(v)
	case "list":
		return tomlList(splitList(v))
	}
	return v
}

// schema is the setting's description for `config schema --json`.
func (s setting) schema(def *config.Config) map[string]any {
	scopes := []string{"global"}
	if s.profile {
		scopes = append(scopes, "profile")
	}
	m := map[string]any{
		"key": s.name, "type": s.kind, "default": s.get(def), "scopes": scopes, "description": s.help,
		"min": nil, "max": nil, "min_exclusive": s.minExclusive, "enum": s.enum,
	}
	if s.min != nil {
		m["min"] = *s.min
	}
	if s.max != nil {
		m["max"] = *s.max
	}
	if s.enum == nil {
		m["enum"] = []string{}
	}
	return m
}

func findSetting(name string) (setting, bool) {
	for _, s := range settings() {
		if s.name == name {
			return s, true
		}
	}
	return setting{}, false
}

func unknownSetting(name string) error {
	var names []string
	for _, s := range settings() {
		names = append(names, s.name)
	}
	return appErr(codeNotFound, "the settings are: "+strings.Join(names, ", "),
		"no setting called %q", name)
}

const configUsage = "usage: claudeswitch config [--json] | config schema [--json] | " +
	"config get <name> [--profile P] [--json] | config set <name> <value> [--json] | config clean [--yes] [--json]"

// cmdConfig shows the settings in force, or changes one.
//
// There are a dozen knobs now, and editing TOML by hand to change one means
// finding the file, knowing the key, and getting no validation until the daemon
// next starts. This reads them back, writes one, and refuses anything the
// validator would reject.
func cmdConfig(args []string) error {
	fs := appFlags("config")
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	profName := fs.String("profile", "", "get: the value in force in this profile")
	yes := fs.Bool("yes", false, "clean: do not ask for confirmation")
	positional, err := parseApp(fs, args, configUsage)
	if err != nil {
		return err
	}
	if len(positional) > 0 && positional[0] == "clean" {
		if len(positional) != 1 {
			return appErr(codeUsage, configUsage, "clean takes no arguments")
		}
		return configClean(os.Stdout, *cfgPath, *yes, *asJSON)
	}
	return runConfigCmd(os.Stdout, *cfgPath, positional, *asJSON, *profName)
}

func runConfigCmd(w io.Writer, cfgPath string, positional []string, asJSON bool, profName string) error {
	if len(positional) > 0 && positional[0] == "schema" {
		return configSchema(w, asJSON)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil && cfg == nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	all := settings()

	if len(positional) == 0 {
		if asJSON {
			m := map[string]any{"path": cfg.Path}
			for _, s := range all {
				m[s.name] = s.get(cfg)
			}
			return emitTo(w, m)
		}
		fmt.Fprintf(w, "\n  %s\n\n", cfg.Path)
		wd := 0
		for _, s := range all {
			wd = max(wd, len(s.name))
		}
		for _, s := range all {
			fmt.Fprintf(w, "  %-*s  %-8s  %s\n", wd, s.name, s.get(cfg), s.help)
		}
		fmt.Fprintf(w, "\n  change one with:  cs config <name> <value>\n\n")
		return nil
	}

	verb := ""
	switch positional[0] {
	case "get", "set":
		verb, positional = positional[0], positional[1:]
	}
	if len(positional) == 0 {
		return appErr(codeUsage, configUsage, "name a setting")
	}
	name := positional[0]
	target, ok := findSetting(name)
	if !ok {
		return unknownSetting(name)
	}
	switch {
	case verb == "get" && len(positional) != 1, verb == "set" && len(positional) != 2, len(positional) > 2:
		return appErr(codeUsage, configUsage, "wrong number of arguments")
	}
	if len(positional) == 1 {
		return configGet(w, cfg, target, profName, asJSON)
	}
	return configSet(w, cfg, target, positional[1], asJSON)
}

func configGet(w io.Writer, cfg *config.Config, s setting, profName string, asJSON bool) error {
	if profName == "" {
		if asJSON {
			return emitTo(w, map[string]any{"key": s.name, "value": s.get(cfg), "scope": "global"})
		}
		fmt.Fprintln(w, s.get(cfg))
		return nil
	}
	if _, ok := cfg.ProfileNamed(profName); !ok {
		return noSuchProfile(cfg, profName)
	}
	eff := s.get(cfg.ForProfile(profName))
	override := profileOverride(cfg, profName, s.name)
	if asJSON {
		return emitTo(w, map[string]any{"key": s.name, "value": eff, "scope": "profile",
			"profile": profName, "override": orNull(override)})
	}
	fmt.Fprintln(w, eff)
	return nil
}

func configSet(w io.Writer, cfg *config.Config, s setting, value string, asJSON bool) error {
	was := s.get(cfg)
	if err := s.set(cfg, value); err != nil {
		return appErr(codeInvalidValue, settingRange(s), "%s: %v", s.name, err)
	}
	cfg.MarkSet(s.name) // chosen now, so a whole-file Write keeps it even at its default
	if err := cfg.CheckSetting(s.name); err != nil {
		return appErr(codeInvalidValue, settingRange(s), "%v", err)
	}
	// Validate before writing, so a bad value is refused rather than stored and
	// discovered when the daemon next fails to start.
	if err := cfg.Validate(); err != nil {
		return appErr(codeInvalidValue, settingRange(s), "%s would make the config invalid: %v", s.name, err)
	}
	want := s.get(cfg)
	if _, statErr := os.Stat(cfg.Path); statErr != nil {
		// No file yet: write a whole one, as before.
		if err := cfg.Write(cfg.Path); err != nil {
			return err
		}
	} else {
		err := editConfigText(cfg.Path, func(text string) (string, error) {
			out, err := setKey(text, blockRef{}, s.name, s.tomlValue(cfg))
			if err != nil || s.name == "poll_active" {
				return out, err
			}
			return dropOldPinnedPoll(out)
		}, func(back *config.Config) error {
			if got := s.get(back); got != want {
				return fmt.Errorf("the edit parsed but %s read back as %s, not %s; discarded", s.name, got, want)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	running := daemonRunning()
	if asJSON {
		return emitTo(w, map[string]any{"key": s.name, "previous": was, "value": want, "path": cfg.Path,
			"daemon_running": running})
	}
	fmt.Fprintf(w, "\n  %s  %s → %s\n", s.name, was, want)
	fmt.Fprintf(w, "  written to %s\n", cfg.Path)
	if running {
		fmt.Fprintf(w, "  a running daemon picks it up without a restart\n")
	}
	fmt.Fprintln(w)
	return nil
}

// settingRange describes what a setting accepts, for a refusal's hint.
func settingRange(s setting) string {
	switch {
	case s.kind == "enum":
		return s.name + " is one of: " + strings.Join(s.enum, ", ")
	case s.kind == "list":
		return s.name + " is a comma-separated list"
	case s.min != nil && s.max != nil:
		lo := "["
		if s.minExclusive {
			lo = "("
		}
		return fmt.Sprintf("%s is a %s in %s%g, %g]", s.name, s.kind, lo, *s.min, *s.max)
	case s.min != nil && s.kind == "duration":
		if s.minExclusive {
			return s.name + " is a positive duration (90s, 5m, 2h)"
		}
		return s.name + " is a duration (90s, 5m, 2h)"
	case s.min != nil:
		return fmt.Sprintf("%s is a %s, at least %g", s.name, s.kind, *s.min)
	}
	return ""
}

func configSchema(w io.Writer, asJSON bool) error {
	def := config.Defaults()
	var list []map[string]any
	for _, s := range settings() {
		list = append(list, s.schema(def))
	}
	if asJSON {
		return emitTo(w, map[string]any{"settings": list})
	}
	fmt.Fprintln(w)
	for _, s := range settings() {
		scope := "global"
		if s.profile {
			scope = "global, profile"
		}
		fmt.Fprintf(w, "  %-21s %-8s default %-8s %s\n", s.name, s.kind, s.get(def), scope)
		if r := settingRange(s); r != "" {
			fmt.Fprintf(w, "  %-21s %s\n", "", r)
		}
	}
	fmt.Fprintln(w)
	return nil
}

// dropOldPinnedPoll removes a top-level poll_active of exactly
// config.OldPinnedPollActive ("1m0s", "60s"): the value 0.3.x-0.5.0 `cs config`
// pinned on every write, not a choice. It is removed in the same edit as the
// setting being changed, so the file returns to today's default; the edit is
// parsed back and checked like any other. Any other poll_active is left alone,
// and on load a leftover 1m0s still runs at the floor with a warning.
func dropOldPinnedPoll(text string) (string, error) {
	es := scanTOML(text)
	k := keyIn(es, -1, "poll_active")
	if k < 0 {
		return text, nil
	}
	d, err := time.ParseDuration(literal(text, es[k]))
	if err != nil || d != config.OldPinnedPollActive {
		return text, nil
	}
	return deleteKey(text, blockRef{}, "poll_active")
}
