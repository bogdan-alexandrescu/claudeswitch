package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// `cs daemon …` (IMPROVEMENTS M3): the daemon service, managed the way
// install.sh manages it — a launchd agent on macOS, a systemd user unit on
// Linux — so the menu-bar app can install it, start and stop it, and switch
// it between live and dry-run. It builds nothing: install registers the
// binary that is running. install.sh keeps working unchanged; the plist and
// unit written here are the ones it writes (TestServiceFilesMatchInstallSh).

const (
	serviceLabel = "xyz.claudeswitch.daemon"
	serviceUnit  = "claudeswitch.service"
	// launchctlPath is run by absolute path: a PATH entry ahead of /bin
	// must not decide what reloads the daemon.
	launchctlPath = "/bin/launchctl"
)

// systemctlPath is systemctl by absolute path, where the distribution
// keeps it.
func systemctlPath() string {
	for _, p := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/bin/systemctl"
}

// checkServicePath refuses a binary path no plist or unit can hold
// faithfully: control characters (XML 1.0 cannot carry most of them, and a
// newline would end ExecStart).
func checkServicePath(p string) error {
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return appErr(codeInvalidValue, "install the binary somewhere with a plain path, e.g. ~/.local/bin",
				"the binary's path %q has a control character in it", p)
		}
	}
	return nil
}

// nonDurable says why a binary path would not survive, or "": a path
// macOS translocated, a temporary directory, or a `go run` build cache.
// A service pointing there breaks on the next reboot or cache clean. A seam.
var nonDurable = func(p string) string {
	clean := filepath.Clean(p)
	if strings.Contains(clean, "/AppTranslocation/") {
		return "macOS runs it from a translocated copy"
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if goBuildDir.MatchString(part) {
			return "it is a `go run` build in Go's cache"
		}
	}
	tmps := []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", "/var/tmp", "/private/var/tmp"}
	if t := os.Getenv("TMPDIR"); t != "" {
		tmps = append(tmps, filepath.Clean(t))
	}
	for _, t := range tmps {
		if clean == t || strings.HasPrefix(clean, strings.TrimSuffix(t, "/")+"/") {
			return "it is in a temporary directory (" + t + ")"
		}
	}
	return ""
}

// serviceDeps are the service commands' reaches outside the process. A
// seam: tests never run launchctl or systemctl.
type serviceDeps struct {
	goos string
	// run runs launchctl or systemctl, with combined output.
	run func(name string, args ...string) (string, error)
	exe func() (string, error)
}

var serviceSeams = serviceDeps{
	goos: runtime.GOOS,
	run: func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	},
	exe: os.Executable,
}

type servicePaths struct {
	file     string // the plist or the unit
	stateDir string
	log      string
}

func servicePathsFor(goos string) (servicePaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return servicePaths{}, err
	}
	sd := filepath.Join(home, ".local", "state", "claudeswitch")
	p := servicePaths{stateDir: sd, log: filepath.Join(sd, "daemon.log")}
	switch goos {
	case "darwin":
		p.file = filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	case "linux":
		cfgHome := os.Getenv("XDG_CONFIG_HOME")
		if cfgHome == "" {
			cfgHome = filepath.Join(home, ".config")
		}
		p.file = filepath.Join(cfgHome, "systemd", "user", serviceUnit)
		p.log = "journalctl --user -u claudeswitch"
	default:
		return p, appErr(codeUnsupported, "run `claudeswitch watch` yourself",
			"no launchd or systemd on %s", goos)
	}
	return p, nil
}

// renderPlist is install.sh's plist, for bin.
func renderPlist(bin, stateDir string, live bool) string {
	liveArg := ""
	if live {
		liveArg = "    <string>--live</string>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + serviceLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEscape(bin) + `</string>
    <string>watch</string>
` + liveArg + `
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>StandardOutPath</key><string>` + xmlEscape(stateDir) + `/daemon.log</string>
  <key>StandardErrorPath</key><string>` + xmlEscape(stateDir) + `/daemon.log</string>
  <!-- No ProcessType. "Background" looks right for a poller and is a trap: it
       puts the job in launchd's background QoS band, where a ` + "`security`" + `
       child never gets far enough to read the keychain. Measured on macOS
       15: a read that takes 0.1s from a shell and 2.1s from a plain agent
       never completes at all under Background. -->
</dict>
</plist>
`
}

// renderUnit is install.sh's systemd unit, for bin.
func renderUnit(bin string, live bool) string {
	flag := ""
	if live {
		flag = " --live"
	}
	return `[Unit]
Description=claudeswitch — quota-aware Claude account rotation
After=network-online.target

[Service]
Type=simple
ExecStart=` + systemdQuote(bin) + ` watch` + flag + `
Restart=always
RestartSec=30
# The daemon holds credentials; keep its files to itself.
UMask=0077

[Install]
WantedBy=default.target
`
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// systemdQuote writes a path for ExecStart: "%" and "$" doubled, so
// neither a specifier nor a variable is expanded, and quoted when it holds
// a space, a quote or a backslash. Control characters are refused before
// (checkServicePath).
func systemdQuote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if strings.ContainsAny(s, " \t\"\\'") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return s
}

var (
	// goBuildDir is a directory `go run` or Go's build cache makes:
	// go-build, or go-build followed by digits.
	goBuildDir = regexp.MustCompile(`^go-build[0-9]*$`)
	plistBin   = regexp.MustCompile(`<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)
	unitStart  = regexp.MustCompile(`(?m)^ExecStart=("(?:[^"\\]|\\.)*"|\S+)(.*)$`)
)

// installedService reads the installed file: the binary it runs and
// whether it is live.
func installedService(goos, file string) (bin string, live, ok bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false, false
	}
	text := string(b)
	if goos == "darwin" {
		if m := plistBin.FindStringSubmatch(text); m != nil {
			bin = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(m[1])
		}
		return bin, strings.Contains(text, "<string>--live</string>"), true
	}
	if m := unitStart.FindStringSubmatch(text); m != nil {
		bin = m[1]
		if strings.HasPrefix(bin, `"`) {
			bin = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(bin[1 : len(bin)-1])
		}
		bin = strings.NewReplacer("%%", "%", "$$", "$").Replace(bin)
		return bin, strings.Contains(" "+m[2]+" ", " --live "), true
	}
	return "", false, true
}

// thisBinary is the binary to register: the running one, through a `cs`
// link to the claudeswitch it names.
func thisBinary() (string, error) {
	p, err := serviceSeams.exe()
	if err != nil {
		return "", err
	}
	if filepath.Base(p) != "claudeswitch" {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
	}
	return filepath.Abs(p)
}

const daemonUsage = "usage: claudeswitch daemon status|start|stop|restart|live|dry-run|install|uninstall " +
	"[--live|--dry-run] [--json]"

// cmdDaemon is `cs daemon <verb>`.
func cmdDaemon(args []string) error {
	fs := appFlags("daemon")
	asJSON := fs.Bool("json", false, "machine-readable output")
	live := fs.Bool("live", false, "install: in live mode (it acts)")
	dry := fs.Bool("dry-run", false, "install: in dry-run mode (it reports, changes nothing)")
	fs.Bool("yes", false, "accepted for symmetry; nothing here asks")
	p, err := parseApp(fs, args, daemonUsage)
	if err != nil {
		return err
	}
	if len(p) != 1 {
		return appErr(codeUsage, daemonUsage, "name one daemon command")
	}
	if *live && *dry {
		return appErr(codeUsage, daemonUsage, "--live and --dry-run together")
	}
	mode := ""
	switch {
	case *live:
		mode = "live"
	case *dry:
		mode = "dry-run"
	}
	return runDaemonCmd(os.Stdout, p[0], mode, *asJSON)
}

func runDaemonCmd(w io.Writer, verb, mode string, asJSON bool) error {
	goos := serviceSeams.goos
	paths, err := servicePathsFor(goos)
	if err != nil {
		return err
	}
	if verb == "status" {
		return emitDaemonStatus(w, goos, paths, "status", asJSON)
	}
	bin, isLive, installed := installedService(goos, paths.file)
	need := func() error {
		if !installed {
			return appErr(codeNotInstalled, "install it: claudeswitch daemon install", "the daemon service is not installed (%s)", paths.file)
		}
		return nil
	}
	switch verb {
	case "install":
		self, err := thisBinary()
		if err != nil {
			return err
		}
		if why := nonDurable(self); why != "" {
			return appErr(codeNotDurable, "install the binary first (./install.sh puts it in ~/.local/bin), "+
				"then run that one's `daemon install`",
				"not installing the service for %s: %s, so it would not be there after a restart", self, why)
		}
		liveMode := isLive && installed
		if mode != "" {
			liveMode = mode == "live"
		}
		if err := writeService(goos, paths, self, liveMode); err != nil {
			return err
		}
		if err := serviceReload(goos, paths, true); err != nil {
			return err
		}
	case "uninstall":
		if goos == "darwin" {
			_, _ = serviceSeams.run(launchctlPath, "unload", paths.file)
		} else {
			_, _ = serviceSeams.run(systemctlPath(), "--user", "disable", "--now", serviceUnit)
		}
		if err := os.Remove(paths.file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if goos != "darwin" {
			_, _ = serviceSeams.run(systemctlPath(), "--user", "daemon-reload")
		}
	case "start":
		if err := need(); err != nil {
			return err
		}
		if goos == "darwin" {
			if !launchdLoaded() {
				if err := svc(launchctlPath, "load", paths.file); err != nil {
					return err
				}
			}
		} else if err := svc(systemctlPath(), "--user", "start", serviceUnit); err != nil {
			return err
		}
	case "stop":
		if err := need(); err != nil {
			return err
		}
		if goos == "darwin" {
			if launchdLoaded() {
				if err := svc(launchctlPath, "unload", paths.file); err != nil {
					return err
				}
			}
		} else if err := svc(systemctlPath(), "--user", "stop", serviceUnit); err != nil {
			return err
		}
	case "restart":
		if err := need(); err != nil {
			return err
		}
		if err := serviceReload(goos, paths, false); err != nil {
			return err
		}
	case "live", "dry-run":
		if err := need(); err != nil {
			return err
		}
		if bin == "" {
			if bin, err = thisBinary(); err != nil {
				return err
			}
		}
		if err := writeService(goos, paths, bin, verb == "live"); err != nil {
			return err
		}
		if err := serviceReload(goos, paths, false); err != nil {
			return err
		}
	default:
		return appErr(codeUsage, daemonUsage, "unknown daemon command %q", verb)
	}
	return emitDaemonStatus(w, goos, paths, verb, asJSON)
}

func svc(name string, args ...string) error {
	out, err := serviceSeams.run(name, args...)
	if err != nil {
		return appErr(codeServiceFailed, strings.TrimSpace(out), "%s %s failed: %v", name, strings.Join(args, " "), err)
	}
	return nil
}

// writeService writes the plist or unit, 0644 as install.sh's are.
func writeService(goos string, paths servicePaths, bin string, live bool) error {
	if err := checkServicePath(bin); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.file), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.stateDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(paths.stateDir, 0o700)
	body := renderUnit(bin, live)
	if goos == "darwin" {
		body = renderPlist(bin, paths.stateDir, live)
	}
	tmp := paths.file + ".claudeswitch-new"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, paths.file); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// serviceReload makes the service manager run the file as it now is.
// enable also turns a systemd unit on at login, as install.sh does.
func serviceReload(goos string, paths servicePaths, enable bool) error {
	if goos == "darwin" {
		_, _ = serviceSeams.run(launchctlPath, "unload", paths.file)
		return svc(launchctlPath, "load", paths.file)
	}
	if err := svc(systemctlPath(), "--user", "daemon-reload"); err != nil {
		return err
	}
	if enable {
		return svc(systemctlPath(), "--user", "enable", "--now", serviceUnit)
	}
	return svc(systemctlPath(), "--user", "restart", serviceUnit)
}

func launchdLoaded() bool {
	_, err := serviceSeams.run(launchctlPath, "list", serviceLabel)
	return err == nil
}

func emitDaemonStatus(w io.Writer, goos string, paths servicePaths, action string, asJSON bool) error {
	bin, isLive, installed := installedService(goos, paths.file)
	loaded := false
	platform := "launchd"
	if goos == "darwin" {
		loaded = installed && launchdLoaded()
	} else {
		platform = "systemd"
		out, err := serviceSeams.run(systemctlPath(), "--user", "is-active", serviceUnit)
		loaded = err == nil && strings.TrimSpace(out) == "active"
	}
	self, _ := thisBinary()
	running := daemonRunning()
	m := map[string]any{
		"action": action, "platform": platform, "file": paths.file, "log": paths.log,
		"installed": installed, "loaded": loaded, "running": running,
		"mode": nil, "binary": orNull(bin), "this_binary": orNull(self),
		"binary_is_this": installed && bin != "" && bin == self,
	}
	if installed {
		m["mode"] = "dry-run"
		if isLive {
			m["mode"] = "live"
		}
	}
	if st, err := state.Load(""); err == nil && running {
		m["daemon_live"] = st.DaemonLive
		m["daemon_version"] = orNull(st.DaemonVersion)
		if !st.DaemonSince.IsZero() {
			m["since"] = st.DaemonSince
		}
	}
	if asJSON {
		return emitTo(w, m)
	}
	fmt.Fprintf(w, "\n  daemon (%s)\n", platform)
	if !installed {
		fmt.Fprintf(w, "    not installed; install it with: claudeswitch daemon install\n\n")
		return nil
	}
	fmt.Fprintf(w, "    file     %s\n", paths.file)
	fmt.Fprintf(w, "    binary   %s\n", nonEmpty(bin, "?"))
	fmt.Fprintf(w, "    mode     %s\n", m["mode"])
	fmt.Fprintf(w, "    loaded   %t\n    running  %t\n\n", loaded, running)
	return nil
}
