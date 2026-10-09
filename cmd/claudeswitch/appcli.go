package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// The machine-readable CLI (IMPROVEMENTS M6): every action the menu-bar app
// offers has a form that takes --json, never prompts, and answers with one
// JSON object on stdout — the result, or {"error":{code,message,hint}} with
// exit status 1. The contract is docs/APP_CLI.md.

// Error codes. They are part of the contract: the app branches on them, so a
// code is never renamed, only added.
const (
	codeUsage           = "usage"                 // bad arguments
	codeNotFound        = "not_found"             // no such account, profile, setting or slot
	codeInvalidValue    = "invalid_value"         // a value the validator refuses
	codeConfigInvalid   = "config_invalid"        // the edit would not load; nothing written
	codeNoConfig        = "no_config"             // there is no config file to edit
	codeLive            = "live"                  // the account is or may be live (§3, D18); holder named
	codeInOtherPool     = "in_other_pool"         // D1: another profile's pool lists the account
	codeOutsidePool     = "outside_pool"          // D5: the account is not in this profile's pool
	codeWouldOrphan     = "would_orphan"          // removing it from its pool leaves it in none
	codeNotVaulted      = "not_vaulted"           // no credential stored under that id
	codeDaemonRunning   = "daemon_running"        // the command needs the daemon stopped
	codeConfirm         = "confirmation_required" // a prompt would be needed; pass --yes
	codeNameRequired    = "name_required"         // no account name given, none can be asked for
	codeNotInstalled    = "not_installed"         // the daemon service is not installed
	codeUnsupported     = "unsupported_platform"  // no launchd or systemd here
	codeServiceFailed   = "service_failed"        // launchctl or systemctl failed
	codeNotActive       = "not_active"            // pin: the account is not the live one
	codeNoPendingLogin  = "no_pending_login"      // login --code with no login started
	codeWrongAccount    = "wrong_account"         // a login came back as another seat
	codeAlreadyVaulted  = "already_vaulted"       // that credential is vaulted under another name
	codeFailed          = "failed"                // anything else; message says what
	codeDaemonNotLoaded = "daemon_not_loaded"     // a seed or delete timed out waiting for the daemon
	codeNotDurable      = "binary_not_durable"    // daemon install from a temporary or translocated path
	codeConfigChanged   = "config_changed"        // the config changed under a command; its edit was not undone
	codeLastProfile     = "last_profile"          // profile remove: the only profile cannot be removed
	codeExists          = "exists"                // init: a config is already there; nothing written
)

// appError is an error the app can branch on. Its human form is the
// message, then the hint on its own line.
type appError struct {
	Code    string
	Message string
	Hint    string
	Err     error
	// RetryAt, when set, is when the refusal may clear (R1: a rate-limited
	// §3 check); the JSON error object carries it as retry_at.
	RetryAt time.Time
}

func (e *appError) Error() string {
	if e.Hint == "" {
		return e.Message
	}
	return e.Message + "\n  " + e.Hint
}

func (e *appError) Unwrap() error { return e.Err }

// appErr makes an appError; hint may be "".
func appErr(code, hint, format string, a ...any) *appError {
	return &appError{Code: code, Message: fmt.Sprintf(format, a...), Hint: hint}
}

// wrapErr gives an existing error a code, keeping its text as the message.
func wrapErr(code, hint string, err error) error {
	if err == nil {
		return nil
	}
	var ae *appError
	if errors.As(err, &ae) {
		return err
	}
	return &appError{Code: code, Message: err.Error(), Hint: hint, Err: err}
}

// errorObject is the JSON form of any error: an appError as it is, the
// daemon lock refusal as daemon_running, anything else as "failed".
func errorObject(err error) map[string]any {
	code, msg, hint := codeFailed, err.Error(), ""
	var retryAt time.Time
	var ae *appError
	switch {
	case errors.As(err, &ae):
		code, msg, hint, retryAt = ae.Code, ae.Message, ae.Hint, ae.RetryAt
	case errors.Is(err, state.ErrDaemonRunning):
		code, hint = codeDaemonRunning, "stop it with: claudeswitch daemon stop"
	}
	obj := map[string]any{"code": code, "message": msg, "hint": hint, "retry_at": nil}
	if !retryAt.IsZero() {
		obj["retry_at"] = retryAt.Format(time.RFC3339)
	}
	return map[string]any{"error": obj}
}

// writeJSONError prints err as the error object.
func writeJSONError(w io.Writer, err error) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(errorObject(err))
}

// jsonRequested reports whether a command line asked for JSON, so main can
// answer a failure with the error object.
func jsonRequested(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "--json", "-json", "--json=true", "-json=true":
			return true
		}
	}
	return false
}

// exitProcess is os.Exit, a seam for the flag-error path.
var exitProcess = os.Exit

// promptsOff is set for a --json or --yes command line: nothing may prompt,
// and isTerminal says so, so every prompting path takes its refusal.
var promptsOff bool

func flagPresent(args []string, names ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == "--"+n || a == "-"+n || strings.HasPrefix(a, "--"+n+"=") || strings.HasPrefix(a, "-"+n+"=") {
				return true
			}
		}
	}
	return false
}

// humanToStderr runs fn with os.Stdout pointed at stderr when on, so a
// command's human output never mixes into the JSON a caller parses.
func humanToStderr(on bool, fn func() error) error {
	if !on {
		return fn()
	}
	saved := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = saved }()
	return fn()
}

// appFlags is a FlagSet for the app commands: errors are returned, never an
// exit, so a bad flag still answers with the error object.
func appFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseApp is parseInterleaved for an appFlags set, with the parse error
// as a usage error.
func parseApp(fs *flag.FlagSet, args []string, usage string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, appErr(codeUsage, usage, "%v", err)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// emitTo writes v as indented JSON.
func emitTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// orNull is s, or JSON null when empty.
func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}
