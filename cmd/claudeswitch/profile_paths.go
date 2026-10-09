package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// IMPROVEMENTS F4: a profile picked by folder. `[[profile]] paths` are globs
// (internal/config/paths.go); `cs run` with no name, `cs profile which` and
// the shell hook `cs profile hook` all pick the same way: the profile whose
// paths the folder belongs to, else `default`.

// Which rules, as `profile which --json` reports them.
const (
	whichPaths   = "paths"   // a profile's paths matched the folder
	whichDefault = "default" // none did: the default profile
	whichNone    = "none"    // none did, and there is no default profile
)

// folderProfile is the profile for a folder: the one whose paths it belongs
// to, else default (the implicit one when no [[profile]] is declared).
func folderProfile(cfg *config.Config, dir string) (in config.Profile, pattern, rule string, ok bool) {
	if name, p, matched := cfg.ProfileForDir(dir); matched {
		in, _ = cfg.ProfileNamed(name)
		return in, p, whichPaths, true
	}
	if in, found := cfg.ProfileNamed(config.DefaultProfile); found {
		return in, "", whichDefault, true
	}
	return config.Profile{}, "", whichNone, false
}

// profileWhich is `cs profile which [--dir D] [--json] [--hook]`. --hook
// prints the one line the shell hook reads: "set <dir>", "unset" or "keep".
func profileWhich(w io.Writer, cfgPath, dir string, asJSON, hook bool) error {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return wrapErr(codeFailed, "", err)
		}
		dir = wd
	}
	// The config alone: no state, no keychain. The hook runs this on every
	// cd, so it must be quick and change nothing.
	cfg, err := config.Load(cfgPath)
	if hook {
		fmt.Fprintln(w, hookLine(cfg, err, dir))
		return nil
	}
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	in, pattern, rule, ok := folderProfile(cfg, dir)
	if asJSON {
		m := map[string]any{"dir": dir, "profile": nil, "pattern": orNull(pattern), "rule": rule,
			"config_dir": nil, "from_env": false}
		if ok {
			m["profile"], m["config_dir"], m["from_env"] = in.Name, orNull(in.Dir), in.FromEnv
		}
		return emitTo(w, m)
	}
	switch rule {
	case whichPaths:
		fmt.Fprintf(w, "  %s  (%s matches %s)\n", in.Name, pattern, dir)
	case whichDefault:
		fmt.Fprintf(w, "  %s  (no profile's paths match %s)\n", in.Name, dir)
	default:
		fmt.Fprintf(w, "  none  (no profile's paths match %s, and there is no %q profile)\n",
			dir, config.DefaultProfile)
	}
	return nil
}

// hookLine is what the shell hook does in dir: set CLAUDE_CONFIG_DIR to the
// picked profile's dir as written (as `cs run` does), unset it for a profile
// without one, or keep it as it is when nothing is picked, the config does
// not load, or there are no [[profile]] blocks (the environment is the
// profile then).
func hookLine(cfg *config.Config, err error, dir string) string {
	if err != nil || cfg == nil || len(cfg.Profiles) == 0 {
		return "keep"
	}
	in, _, _, ok := folderProfile(cfg, dir)
	switch {
	case !ok:
		return "keep"
	case in.Dir == "":
		return "unset"
	case strings.ContainsAny(in.Dir, "\n\r"):
		return "keep"
	}
	return "set " + in.Dir
}

// The shell hooks. Each runs `claudeswitch profile which --hook` when the
// folder changes and only sets or unsets CLAUDE_CONFIG_DIR from its answer:
// the answer is matched, never evaluated.
var shellHooks = map[string]string{
	"zsh": `# claudeswitch: set CLAUDE_CONFIG_DIR for this folder's profile on cd.
# Add to ~/.zshrc:  eval "$(claudeswitch profile hook zsh)"
_claudeswitch_hook() {
  local out
  out="$(command claudeswitch profile which --hook 2>/dev/null)" || return 0
  case "$out" in
    "set "*) export CLAUDE_CONFIG_DIR="${out#set }" ;;
    unset) unset CLAUDE_CONFIG_DIR ;;
  esac
}
autoload -Uz add-zsh-hook
add-zsh-hook chpwd _claudeswitch_hook
_claudeswitch_hook
`,
	"bash": `# claudeswitch: set CLAUDE_CONFIG_DIR for this folder's profile on cd.
# Add to ~/.bashrc:  eval "$(claudeswitch profile hook bash)"
_claudeswitch_hook() {
  [ "$PWD" = "${_claudeswitch_pwd-}" ] && return 0
  _claudeswitch_pwd="$PWD"
  local out
  out="$(command claudeswitch profile which --hook 2>/dev/null)" || return 0
  case "$out" in
    "set "*) export CLAUDE_CONFIG_DIR="${out#set }" ;;
    unset) unset CLAUDE_CONFIG_DIR ;;
  esac
}
case ";${PROMPT_COMMAND-};" in
  *";_claudeswitch_hook;"*) ;;
  *) PROMPT_COMMAND="_claudeswitch_hook${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
esac
_claudeswitch_hook
`,
	"fish": `# claudeswitch: set CLAUDE_CONFIG_DIR for this folder's profile on cd.
# Add to ~/.config/fish/config.fish:  claudeswitch profile hook fish | source
function _claudeswitch_hook --on-variable PWD
    set -l out (command claudeswitch profile which --hook 2>/dev/null); or return 0
    switch "$out"
        case 'set *'
            set -gx CLAUDE_CONFIG_DIR (string sub --start 5 -- "$out")
        case unset
            set -e CLAUDE_CONFIG_DIR
    end
end
_claudeswitch_hook
`,
}

// profileHook is `cs profile hook zsh|bash|fish`.
func profileHook(w io.Writer, shell string) error {
	s, ok := shellHooks[shell]
	if !ok {
		return appErr(codeUsage, "the shells are zsh, bash and fish", "no hook for shell %q", shell)
	}
	_, err := io.WriteString(w, s)
	return err
}

// runProfile is the profile `cs run` starts with no name: the folder's.
func runProfile(cfg *config.Config, dir string) (config.Profile, string, error) {
	if len(cfg.Profiles) == 0 {
		return cfg.EffectiveProfiles()[0], "", nil
	}
	in, pattern, _, ok := folderProfile(cfg, dir)
	if !ok {
		return config.Profile{}, "", fmt.Errorf("no profile's paths match %s and there is no %q profile; "+
			"name one: claudeswitch run <profile> (one of %s)", dir, config.DefaultProfile,
			strings.Join(quoteAll(cfg.ProfileNames()), ", "))
	}
	return in, pattern, nil
}

// profileSetPaths is `cs profile set <p> paths <a,b|inherit>`. There is no
// global value: inherit (or "") removes the profile's paths.
func profileSetPaths(w io.Writer, cfg *config.Config, i int, name, value string, asJSON bool) error {
	unset := value == "" || value == "inherit"
	in := &cfg.Profiles[i]
	in.Paths = nil
	if !unset {
		in.Paths = splitList(value)
		if len(in.Paths) == 0 {
			unset, in.Paths = true, nil
		}
	}
	if err := cfg.Validate(); err != nil {
		return appErr(codeInvalidValue, "paths are folders, absolute or from ~/, with *, ? and ** "+
			"(a,b for several; inherit for none); no two profiles' paths may overlap",
			"profile %q: paths would make the config invalid: %v", name, err)
	}
	want := strings.Join(in.Paths, "\x00")
	ref := blockRef{"profile", name}
	err := editConfigText(cfg.Path, func(text string) (string, error) {
		if unset {
			return deleteKey(text, ref, "paths")
		}
		return setKey(text, ref, "paths", tomlList(in.Paths))
	}, func(back *config.Config) error {
		if got, ok := back.ProfileNamed(name); !ok || strings.Join(got.Paths, "\x00") != want {
			return fmt.Errorf("the edit parsed but profile %q's paths did not come out as asked; discarded", name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	paths := append([]string{}, in.Paths...)
	if asJSON {
		var override any
		if !unset {
			override = paths
		}
		return emitTo(w, map[string]any{"profile": name, "key": "paths", "override": override,
			"effective": paths, "path": cfg.Path})
	}
	if unset {
		fmt.Fprintf(w, "  profile %s: no paths; it is picked by name only\n", name)
	} else {
		fmt.Fprintf(w, "  profile %s: paths = %s\n", name, strings.Join(paths, ", "))
	}
	return nil
}
