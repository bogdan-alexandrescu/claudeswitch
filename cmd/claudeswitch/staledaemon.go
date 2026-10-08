package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// buildStamp identifies a build of this binary: the ldflags version ("dev"
// for install.sh builds, which set none) and the VCS revision and commit time
// Go embeds when building inside a git checkout.
type buildStamp struct {
	Version  string
	Revision string // "+dirty" appended when the tree was modified
	Time     time.Time
}

// thisBuild reads this binary's stamp.
func thisBuild() buildStamp {
	b := buildStamp{Version: version}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.time":
			b.Time, _ = time.Parse(time.RFC3339, s.Value)
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && b.Revision != "" {
		b.Revision += "+dirty"
	}
	return b
}

// Seams, so tests never depend on the real daemon or build.
var (
	currentBuild            = thisBuild
	daemonRunning           = state.DaemonRunning
	staleWarnOut  io.Writer = os.Stderr
)

func (b buildStamp) String() string {
	s := b.Version
	if b.Revision != "" {
		rev := b.Revision
		if len(rev) > 12 {
			rev = rev[:12]
		}
		s += " " + rev
	}
	return s
}

// daemonIsOlder reports whether the running daemon's recorded build is older
// than me. A daemon that recorded nothing predates the field, so it is older.
// Two builds are ordered by their commit time when both have one, else by
// release version; dev builds with no VCS information cannot be ordered and
// are not called older, since a warning on every command that may be false
// is worse than none.
func daemonIsOlder(st *state.State, me buildStamp) bool {
	if st.DaemonVersion == "" && st.DaemonBuild == "" {
		return true
	}
	if st.DaemonBuild != "" && st.DaemonBuild == me.Revision {
		return false
	}
	if !st.DaemonBuildTime.IsZero() && !me.Time.IsZero() {
		return st.DaemonBuildTime.Before(me.Time)
	}
	dv, dok := semver(st.DaemonVersion)
	mv, mok := semver(me.Version)
	if dok && mok {
		for i := range dv {
			if dv[i] != mv[i] {
				return dv[i] < mv[i]
			}
		}
	}
	return false
}

// semver parses "0.4.8" or "v0.4.8".
func semver(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.SplitN(strings.TrimPrefix(s, "v"), ".", 3)
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(strings.SplitN(p, "-", 2)[0])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// restartHint is how the installed service is restarted.
func restartHint(goos string) string {
	if goos == "linux" {
		return "systemctl --user restart claudeswitch.service"
	}
	return "launchctl kickstart -k gui/$UID/xyz.claudeswitch.daemon"
}

// staleDaemonLine is the warning for a running daemon older than me, or "".
func staleDaemonLine(st *state.State, me buildStamp, running bool, goos string) string {
	if !running || st == nil || !daemonIsOlder(st, me) {
		return ""
	}
	ver := "a version from before it recorded one"
	if st.DaemonVersion != "" || st.DaemonBuild != "" {
		ver = buildStamp{Version: st.DaemonVersion, Revision: st.DaemonBuild}.String()
	}
	started := "at an unknown time"
	if !st.DaemonSince.IsZero() {
		started = st.DaemonSince.Local().Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("! the running daemon (%s, started %s) is older than this cs (%s); restart it: %s",
		ver, started, me, restartHint(goos))
}

// warnStaleDaemon prints the stale-daemon line, if any, to stderr.
func warnStaleDaemon(st *state.State) {
	if line := staleDaemonLine(st, currentBuild(), daemonRunning(), runtime.GOOS); line != "" {
		fmt.Fprintln(staleWarnOut, line)
	}
}
