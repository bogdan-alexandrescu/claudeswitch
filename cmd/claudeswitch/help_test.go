package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// runCLISplit is runCLI with stdout and stderr apart: help belongs on
// stdout, a mistake on stderr.
func runCLISplit(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), cliEnv+"="+strings.Join(args, cliSep), "COLUMNS=100")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String(), code
}

// A help request is a success: the usage on stdout, exit 0. The app
// commands printed "flag: help requested" and exited 1; account, pool and
// profile took --help for a verb; help itself went to stderr with exit 2.
func TestHelpIsASuccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"help"}, {"--help"}, {"-h"},
		{"account", "--help"}, {"account", "help"}, {"account", "list", "--help"},
		{"pool", "--help"}, {"pool", "list", "-h"}, {"profile", "--help"}, {"profile", "list", "--help"},
		{"config", "--help"}, {"clean", "--help"}, {"daemon", "--help"}, {"remove", "--help"},
		{"priority", "--help"}, {"app", "--help"},
		{"status", "--help"}, {"why", "-h"}, {"audit", "--help"}, {"login", "--help"},
		{"help", "status"},
	} {
		out, errOut, code := runCLISplit(t, args...)
		if code != 0 || !strings.Contains(out, "usage") || strings.Contains(out+errOut, "help requested") ||
			strings.Contains(out+errOut, "unknown") {
			t.Errorf("%v: exit %d, want 0 with the usage on stdout\nstdout:\n%s\nstderr:\n%s", args, code, out, errOut)
		}
	}
}

// Go's bare "Usage of status:" gave the flags and not the command's shape;
// every command's help opens with its synopsis.
func TestEveryCommandsHelpHasASynopsis(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, want := range map[string]string{
		"status":  "usage: claudeswitch status [--profile NAME]",
		"why":     "usage: claudeswitch why [--profile NAME]",
		"history": "usage: claudeswitch history",
		"use":     "usage: claudeswitch use <id>",
		"login":   "usage: claudeswitch login <id>",
	} {
		out, _, _ := runCLISplit(t, name, "--help")
		if !strings.Contains(out, want) {
			t.Errorf("%s --help lacks %q:\n%s", name, want, out)
		}
	}
	// Notes for the maintainers stay out of what a user reads.
	for _, name := range []string{"doctor", "add", "login"} {
		out, _, _ := runCLISplit(t, name, "--help")
		for _, leak := range []string{"IMPROVEMENTS", "Save the login", "pin-org"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s --help shows %q:\n%s", name, leak, out)
			}
		}
	}
}

// An unknown command said nothing about being unknown and printed the
// whole command list, 130 lines.
func TestAnUnknownCommandSaysSo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, errOut, code := runCLISplit(t, "statsu")
	if code != 2 || !strings.Contains(errOut, `unknown command "statsu"`) {
		t.Errorf("exit %d, want 2 naming the command:\n%s%s", code, out, errOut)
	}
	if n := strings.Count(out+errOut, "\n"); n > 4 {
		t.Errorf("%d lines for a mistyped command; one, and where the list is, will do:\n%s%s", n, out, errOut)
	}
}

// Arguments the read commands take none of were silently dropped.
func TestReadCommandsRejectArguments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"status", "why", "plan", "top"} {
		_, errOut, code := runCLISplit(t, name, "work")
		if code != 2 || !strings.Contains(errOut, name+" takes no arguments") {
			t.Errorf("%s work: exit %d, want 2:\n%s", name, code, errOut)
		}
	}
	// A flag the command does not know: the synopsis on stderr, exit 2.
	_, errOut, code := runCLISplit(t, "status", "--bogus")
	if code != 2 || !strings.Contains(errOut, "-bogus") || !strings.Contains(errOut, "usage: claudeswitch status") {
		t.Errorf("status --bogus: exit %d, want 2 with the synopsis:\n%s", code, errOut)
	}
}

// A mistyped flag failed the parse, which left no command, and no command
// means open: `cs chrome --bogus` opened Chrome.
func TestChromeWithAMistypedFlagOpensNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testshim.ResetLaunches()
	var out, errOut bytes.Buffer
	err := runChrome(&out, &errOut, []string{"--bogus"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("chrome --bogus: err %v, want the parse error", err)
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Errorf("chrome --bogus launched %v", l)
	}
}
