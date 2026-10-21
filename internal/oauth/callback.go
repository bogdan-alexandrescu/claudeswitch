package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Callback receives a login's authorization code on this machine, so nobody
// has to copy it out of the browser.
//
// It is how Claude Code's own login avoids the paste: it listens on
// 127.0.0.1 at a port of the system's choosing and sends the browser back to
// http://localhost:<port>/callback, with the same client id (read from its
// authorize-URL builder and callback listener, 2.1.306). The code page
// (RedirectURI) remains for a login finished somewhere else: an app, a
// script, or another machine's browser.
type Callback struct {
	// lns are 127.0.0.1:<port> and, where the machine has an IPv6 loopback,
	// [::1]:<port>: the redirect names localhost, which is ::1 first on most
	// machines, and the two are separate sockets. Holding only the first left
	// the second free for another process to take, and the redirect with it.
	lns  []net.Listener
	port int
	srv  *http.Server
}

// ListenCallback starts listening on a free port on both loopback addresses.
// Only this machine can reach it.
func ListenCallback() (*Callback, error) {
	v6 := hasIPv6Loopback()
	for try := 0; try < 20; try++ {
		v4, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("could not listen for the browser on this machine: %w", err)
		}
		port := v4.Addr().(*net.TCPAddr).Port
		if !v6 {
			return &Callback{lns: []net.Listener{v4}, port: port}, nil
		}
		ln6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
		if err == nil {
			return &Callback{lns: []net.Listener{v4, ln6}, port: port}, nil
		}
		v4.Close() // that port is taken on ::1: try another
	}
	return nil, errors.New("could not listen for the browser on both loopback addresses of this machine")
}

// hasIPv6Loopback says whether ::1 can be listened on at all.
func hasIPv6Loopback() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// Addr is the IPv4 address listened on, 127.0.0.1:<port>.
func (c *Callback) Addr() string { return c.lns[0].Addr().String() }

// RedirectURI is where the browser is sent back to. It names localhost, as
// Claude Code's does, rather than the address listened on.
func (c *Callback) RedirectURI() string {
	return "http://localhost:" + strconv.Itoa(c.port) + "/callback"
}

// Close stops listening. Safe to call after Wait, which stops on its own.
func (c *Callback) Close() error {
	if c.srv != nil {
		return c.srv.Close()
	}
	var first error
	for _, ln := range c.lns {
		if err := ln.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ours says whether a request was addressed to this listener, by one of the
// names it answers to; a page that rebound a name of its own to this machine
// is not.
func (c *Callback) ours(host string) bool {
	p := strconv.Itoa(c.port)
	switch host {
	case "localhost:" + p, "127.0.0.1:" + p, "[::1]:" + p:
		return true
	}
	return false
}

// printable is s as it may reach a terminal: printable ASCII only, cut to
// max bytes. A refusal's words come from the request, and an escape sequence
// in them could rewrite the screen or set the clipboard.
func printable(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > max {
		out = out[:max] + "..."
	}
	return out
}

// BeginAuthCallback builds an authorization URL that returns to c.
func BeginAuthCallback(c *Callback, e Extra) (*AuthFlow, error) {
	return beginAuthTo(c.RedirectURI(), "", false, e)
}

type callbackResult struct {
	code string
	err  error
}

// Wait serves the redirect until the browser comes back with f's answer, or
// ctx ends. It hands back the authorization code to Exchange with f.
//
// Only /callback ends the wait; a browser also asks for things like
// /favicon.ico. Only a request carrying f's state ends it, a refusal as much
// as a code: anything else belongs to another attempt, or to a page that is
// not ours, and is refused while the wait goes on, so no page can cancel a
// login by guessing the port.
func (c *Callback) Wait(ctx context.Context, f *AuthFlow) (string, error) {
	got := make(chan callbackResult, 1)
	settle := func(r callbackResult) {
		select {
		case got <- r:
		default: // the first answer is the one that counts
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if !c.ours(r.Host) || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.state)) != 1 {
			page(w, http.StatusBadRequest, "This sign-in belongs to another attempt. "+
				"Close this tab and try again from the terminal.")
			return
		}
		switch {
		case q.Get("error") != "":
			why := printable(q.Get("error")+" "+q.Get("error_description"), 200)
			page(w, http.StatusBadRequest, "Sign-in did not complete: "+why+
				". Close this tab and try again from the terminal.")
			settle(callbackResult{err: fmt.Errorf("the sign-in was not completed: %s", why)})
		case q.Get("code") == "":
			page(w, http.StatusBadRequest, "No authorization code came back. "+
				"Close this tab and try again from the terminal.")
			settle(callbackResult{err: errors.New("the browser came back without an authorization code")})
		default:
			// Not "signed in" yet: the terminal still exchanges the code and
			// checks whose it is.
			page(w, http.StatusOK, "Received. You can close this tab; the terminal finishes the sign-in.")
			settle(callbackResult{code: q.Get("code")})
		}
	})
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	for _, ln := range c.lns {
		go func() { _ = c.srv.Serve(ln) }()
	}
	defer func() {
		// Let the page that answered finish writing, then stop.
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.srv.Shutdown(sctx)
	}()

	select {
	case r := <-got:
		return r.code, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// page answers the browser with one line of plain text in a page.
func page(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><title>claudeswitch</title>"+
		"<p style=\"font:16px system-ui;margin:3em\">%s</p>\n", html.EscapeString(msg))
}
