package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// Help for people. A help request (help, --help, -h, -help) is a success:
// the command's synopsis and flags on stdout, exit 0. A flag the command
// does not know is a mistake: the error and the same help on stderr, exit 2.

// synopses are each command's shape, keyed by its flag set's name, as
// docs/cli gives them. Go's own "Usage of status:" listed the flags and
// never the arguments.
var synopses = map[string]string{
	"status":    "claudeswitch status [--profile NAME] [--detail] [--json] [--refresh=false] [--max-age DURATION] [--config PATH] [-v]",
	"top":       "claudeswitch top [--profile NAME] [--every DURATION] [--config PATH]",
	"why":       "claudeswitch why [--profile NAME] [--json] [--config PATH]",
	"plan":      "claudeswitch plan [--profile NAME] [--json] [--refresh=false] [--config PATH]",
	"whoami":    "claudeswitch whoami [--profile NAME] [--sync] [--config PATH]",
	"watch":     "claudeswitch watch [--live] [--idle-gap DURATION] [--quiet] [--config PATH] [-v]",
	"audit":     "claudeswitch audit [-n N] [--kind KIND] [--since DURATION] [--json]",
	"history":   "claudeswitch history [--days N] [--profile NAME] [--config PATH]\n       claudeswitch history --usage [--days N] [--json] [--config PATH]",
	"session":   "claudeswitch session [--since DURATION] [--profile NAME] [--detail] [--json] [--config PATH]",
	"doctor":    "claudeswitch doctor [--verify] [--json] [--config PATH]",
	"setup":     "claudeswitch setup [--config PATH]",
	"init":      "claudeswitch init [--empty] [--json] [--config PATH]",
	"uninstall": "claudeswitch uninstall [--credentials] [--yes] [--config PATH]",
	"add":       "claudeswitch add [<id>] [--profile NAME] [--from PROFILE] [--pool NAME | --no-pool] [--force] [--json]",
	"use":       "claudeswitch use <id> [--profile NAME] [--force] [--dry-run] [--json]",
	"login": "claudeswitch login <id> --direct [--browser APP] [--no-open] [--org ID] [--profile NAME | --pool NAME | --no-pool] [--json]\n" +
		"       claudeswitch login <id> --code <code> [--json]\n" +
		"       claudeswitch login <id> [--profile NAME] [--keep] [--sso] [--browser APP]\n" +
		"       claudeswitch login   (at a terminal: asks for the name, the pool and the browser)",
	"accounts": "claudeswitch accounts [--json] [--config PATH]",
	"forget":   "claudeswitch forget <id>... | forget --stale [--config PATH]",
	"rename":   "claudeswitch rename <old> <new> [--config PATH]",
	"refresh":  "claudeswitch refresh <id> [--allow-active] [--config PATH]",
	"identify": "claudeswitch identify [<id>...] [--force] [--config PATH]",
	"recovery": "claudeswitch recovery [list] [--identify] [--json]\n" +
		"       claudeswitch recovery restore <slot> <account> [--force] [--json]\n" +
		"       claudeswitch recovery clear <slot> [--yes] [--json]",
	"run":                  "claudeswitch run [<profile>] [--config PATH] [-- claude args]",
	"statusline":           "claudeswitch statusline [--config PATH]",
	"statusline install":   "claudeswitch statusline install [--force]",
	"statusline uninstall": "claudeswitch statusline uninstall",
	"context":              "claudeswitch context [--config PATH]",
	"chrome hint":          "claudeswitch chrome hint [--config PATH]   (the plugin's hook: the failed tool call on stdin)",
	"hook":                 "claudeswitch hook pre|post [--config PATH]   (the plugin's hooks: the tool call on stdin)",
}

// hiddenFlags are accepted and never listed: kept so a script that passes
// one still runs, though it does nothing.
var hiddenFlags = map[string]bool{"pin-org": true}

// isHelp reports whether an argument asks for help.
func isHelp(a string) bool {
	switch a {
	case "help", "-h", "-help", "--help":
		return true
	}
	return false
}

// helpRequest is a help request found while parsing: main prints its text
// on stdout and exits 0.
type helpRequest struct{ text string }

func (h *helpRequest) Error() string { return h.text }

// helpFor is the help text of usage (a synopsis, or a usage line already
// starting "usage:") and the flags of fs, when it has any.
func helpFor(usage string, fs *flag.FlagSet) string {
	var b strings.Builder
	if !strings.HasPrefix(usage, "usage:") {
		usage = "usage: " + usage
	}
	for _, line := range strings.Split(usage, "\n") {
		for _, l := range wrapSynopsis(line, render.Width()) {
			b.WriteString(l + "\n")
		}
	}
	if fs != nil {
		var flags strings.Builder
		printFlags(&flags, fs, render.Width())
		if flags.Len() > 0 {
			b.WriteString("\n" + flags.String())
		}
	}
	return b.String()
}

// commandHelp is the help text for a flag set from synopses.
func commandHelp(fs *flag.FlagSet) string {
	syn, ok := synopses[fs.Name()]
	if !ok {
		syn = "claudeswitch " + fs.Name() + " [flags]"
	}
	return helpFor(syn, fs)
}

// printFlags lists fs's flags, each name on its own line and its help below,
// wrapped to width: Go's own listing ran one help line out to 240 columns.
func printFlags(w io.Writer, fs *flag.FlagSet, width int) {
	fs.VisitAll(func(f *flag.Flag) {
		if hiddenFlags[f.Name] {
			return
		}
		dash := "--"
		if len(f.Name) == 1 {
			dash = "-"
		}
		kind, usage := flag.UnquoteUsage(f)
		line := "  " + dash + f.Name
		if kind != "" {
			line += " " + kind
		}
		if !isZeroDefault(f) {
			usage += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		fmt.Fprintln(w, line)
		for _, l := range wrapWords(usage, width-6) {
			fmt.Fprintln(w, "      "+l)
		}
	})
}

// isZeroDefault reports whether a flag's default is its type's zero, which
// the listing leaves unsaid, as Go's does.
func isZeroDefault(f *flag.Flag) bool {
	switch f.DefValue {
	case "", "0", "false", "0s":
		return true
	}
	return false
}

// wrapWords breaks s into lines of at most width columns, at spaces.
func wrapWords(s string, width int) []string {
	if width < 20 {
		width = 20
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) > width:
			lines = append(lines, line)
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// wrapSynopsis breaks a synopsis line to width, never inside a bracketed
// group, continuing under the first argument: "usage: claudeswitch status"
// then its flags, aligned.
func wrapSynopsis(line string, width int) []string {
	if len([]rune(line)) <= width {
		return []string{line}
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	// Tokens: words, with a bracketed group, however many words, as one.
	var tokens []string
	depth, cur := 0, ""
	for _, r := range strings.TrimLeft(line, " ") {
		switch {
		case r == ' ' && depth == 0:
			if cur != "" {
				tokens = append(tokens, cur)
			}
			cur = ""
			continue
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		}
		cur += string(r)
	}
	if cur != "" {
		tokens = append(tokens, cur)
	}
	// Continue under the first argument: past "claudeswitch <command>".
	lead := len(indent)
	for i, tok := range tokens {
		lead += len([]rune(tok)) + 1
		if i > 0 && tokens[i-1] == "claudeswitch" {
			break
		}
	}
	if lead > width/2 {
		lead = len(indent) + 8
	}
	var lines []string
	l := indent
	for _, tok := range tokens {
		switch {
		case strings.TrimSpace(l) == "":
			l += tok
		case len([]rune(l))+1+len([]rune(tok)) > width:
			lines = append(lines, l)
			l = strings.Repeat(" ", lead) + tok
		default:
			l += " " + tok
		}
	}
	return append(lines, l)
}

// parseFlagsErr parses flags before or after positionals, returning the
// positionals, or the error: flag.ErrHelp for a help request.
func parseFlagsErr(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.Init(fs.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return positional, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// answerParseError is what parseInterleaved does with a parse error: help on
// stdout and exit 0; with --json the usage error object and exit 1; else
// the error and the help on stderr, exit 2.
func answerParseError(fs *flag.FlagSet, args []string, err error) {
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(os.Stdout, commandHelp(fs))
		exitProcess(0)
	case jsonRequested(args):
		writeJSONError(os.Stdout, appErr(codeUsage, "", "%s: %v", fs.Name(), err))
		exitProcess(1)
	default:
		fmt.Fprintf(os.Stderr, "claudeswitch %s: %v\n%s", fs.Name(), err, commandHelp(fs))
		exitProcess(2)
	}
}

// noArgs refuses positionals a command takes none of: silently dropping
// `status work` answered a question nobody asked.
func noArgs(name string, pos []string) error {
	if len(pos) == 0 {
		return nil
	}
	return badArgs(synopses[name], "%s takes no arguments; got %q", name, pos[0])
}
