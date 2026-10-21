package oauth

import (
	"context"
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
	ln  net.Listener
	srv *http.Server
}

// ListenCallback starts listening on a free loopback port. Only this machine
// can reach it.
func ListenCallback() (*Callback, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not listen for the browser on this machine: %w", err)
	}
	return &Callback{ln: ln}, nil
}

// Addr is the address listened on, 127.0.0.1:<port>.
func (c *Callback) Addr() string { return c.ln.Addr().String() }

// RedirectURI is where the browser is sent back to. It names localhost, as
// Claude Code's does, rather than the address listened on.
func (c *Callback) RedirectURI() string {
	return "http://localhost:" + strconv.Itoa(c.ln.Addr().(*net.TCPAddr).Port) + "/callback"
}

// Close stops listening. Safe to call after Wait, which stops on its own.
func (c *Callback) Close() error {
	if c.srv != nil {
		return c.srv.Close()
	}
	return c.ln.Close()
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
// /favicon.ico. A code is used only when the state matches f's: anything else
// belongs to another attempt, or to a page that is not ours.
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
		switch {
		case q.Get("error") != "":
			why := strings.TrimSpace(q.Get("error") + " " + q.Get("error_description"))
			page(w, http.StatusBadRequest, "Sign-in did not complete: "+why+
				". Close this tab and try again from the terminal.")
			settle(callbackResult{err: fmt.Errorf("the sign-in was not completed: %s", why)})
		case q.Get("state") != f.state:
			page(w, http.StatusBadRequest, "This sign-in belongs to another attempt. "+
				"Close this tab and try again from the terminal.")
			settle(callbackResult{err: errors.New("the browser came back from a different login attempt; " +
				"nothing was used")})
		case q.Get("code") == "":
			page(w, http.StatusBadRequest, "No authorization code came back. "+
				"Close this tab and try again from the terminal.")
			settle(callbackResult{err: errors.New("the browser came back without an authorization code")})
		default:
			page(w, http.StatusOK, "Signed in. You can close this tab and go back to the terminal.")
			settle(callbackResult{code: q.Get("code")})
		}
	})
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = c.srv.Serve(c.ln) }()
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
