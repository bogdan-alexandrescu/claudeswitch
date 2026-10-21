package oauth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// startCallbackLogin opens a listener and a flow redirecting to it, closed
// when the test ends.
func startCallbackLogin(t *testing.T) (*Callback, *AuthFlow) {
	t.Helper()
	cb, err := ListenCallback()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cb.Close() })
	f, err := BeginAuthCallback(cb, Extra{})
	if err != nil {
		t.Fatal(err)
	}
	return cb, f
}

// browserReturns plays the browser landing on the redirect: path and query
// are appended to the listener's own address.
func browserReturns(t *testing.T, cb *Callback, pathAndQuery string) *http.Response {
	t.Helper()
	browser := &http.Client{Timeout: 2 * time.Second}
	resp, err := browser.Get("http://" + cb.Addr() + pathAndQuery)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func stateOf(t *testing.T, f *AuthFlow) string {
	t.Helper()
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

type waited struct {
	code string
	err  error
}

func waitInBackground(cb *Callback, f *AuthFlow, ctx context.Context) <-chan waited {
	out := make(chan waited, 1)
	go func() {
		code, err := cb.Wait(ctx, f)
		out <- waited{code, err}
	}()
	return out
}

func within(t *testing.T, ch <-chan waited) waited {
	t.Helper()
	select {
	case w := <-ch:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return")
		return waited{}
	}
}

// Claude Code's own login sends the browser back to a listener on this
// machine: redirect_uri http://localhost:<port>/callback, with the same
// client id (read from its authorize-URL builder, 2.1.306). That is what
// spares the paste.
func TestACallbackLoginRedirectsToThisMachine(t *testing.T) {
	cb, f := startCallbackLogin(t)
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := strings.Cut(cb.Addr(), ":")
	want := "http://localhost:" + port + "/callback"
	if got := u.Query().Get("redirect_uri"); got != want {
		t.Errorf("redirect_uri %q, want %q", got, want)
	}
	if !strings.HasPrefix(cb.Addr(), "127.0.0.1:") {
		t.Errorf("listening on %s; only loopback may receive the code", cb.Addr())
	}
}

func TestTheCallbackHandsBackTheCode(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	resp := browserReturns(t, cb, "/callback?code=the-code&state="+url.QueryEscape(stateOf(t, f)))
	w := within(t, got)
	if w.err != nil || w.code != "the-code" {
		t.Fatalf("Wait = %q, %v; want the-code", w.code, w.err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the browser got %d, want 200", resp.StatusCode)
	}
}

// A code whose state is not this login's belongs to another attempt, or to
// someone else's page posting at the port: it is never used, and it does not
// end the wait either, or any page could cancel a login by guessing the port.
func TestACallbackWithAnotherStateIsRefused(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	resp := browserReturns(t, cb, "/callback?code=the-code&state=not-this-one")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("the browser got %d, want 400", resp.StatusCode)
	}
	stillWaiting(t, got)
	browserReturns(t, cb, "/callback?code=c&state="+url.QueryEscape(stateOf(t, f)))
	if w := within(t, got); w.err != nil || w.code != "c" {
		t.Fatalf("Wait = %q, %v; want c", w.code, w.err)
	}
}

// A refusal without this login's state is someone else's too: an
// error=… request is refused and the wait goes on.
func TestARefusalWithoutTheStateDoesNotEndTheWait(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	for _, q := range []string{"/callback?error=access_denied", "/callback", "/callback?state=&error=x"} {
		if resp := browserReturns(t, cb, q); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s got %d, want 400", q, resp.StatusCode)
		}
	}
	stillWaiting(t, got)
	browserReturns(t, cb, "/callback?code=c&state="+url.QueryEscape(stateOf(t, f)))
	if w := within(t, got); w.code != "c" {
		t.Fatalf("Wait = %q, %v; want c", w.code, w.err)
	}
}

// The refusal's own words reach the terminal, so they lose anything that is
// not printable ASCII (an escape sequence could rewrite the screen or set the
// clipboard) and are cut short.
func TestARefusalIsSafeToPrint(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	evil := "\x1b]52;c;ZXZpbA==\x07\x1b[2Jaccess_denied"
	browserReturns(t, cb, "/callback?error="+url.QueryEscape(evil)+
		"&error_description="+strings.Repeat("A", 5000)+"&state="+url.QueryEscape(stateOf(t, f)))
	w := within(t, got)
	if w.err == nil {
		t.Fatal("a refusal with this login's state ended without an error")
	}
	msg := w.err.Error()
	for _, r := range msg {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("the error carries control character %U: %q", r, msg)
		}
	}
	if !strings.Contains(msg, "access_denied") || len(msg) > 400 {
		t.Errorf("error %q (%d bytes): want access_denied, at most 400 bytes", msg[:min(len(msg), 120)], len(msg))
	}
}

// localhost is ::1 first on most machines, and 127.0.0.1 and ::1 are
// separate sockets: holding only the first left the second free for another
// process to take, and the redirect with it. Both are held.
func TestTheListenerHoldsBothLoopbacks(t *testing.T) {
	probe, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback on this machine")
	}
	probe.Close()
	cb, _ := startCallbackLogin(t)
	_, port, _ := strings.Cut(cb.Addr(), ":")
	if other, err := net.Listen("tcp", "[::1]:"+port); err == nil {
		other.Close()
		t.Fatalf("[::1]:%s was free while the login listened; another process could take the redirect", port)
	}
}

// A request naming another host is a page that rebound a name of its own to
// this machine; it is refused and the wait goes on.
func TestAnotherHostIsRefused(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	_, port, _ := strings.Cut(cb.Addr(), ":")
	req, _ := http.NewRequest("GET", "http://"+cb.Addr()+"/callback?code=c&state="+url.QueryEscape(stateOf(t, f)), nil)
	req.Host = "evil.example:" + port
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("another host got %d, want 400", resp.StatusCode)
	}
	stillWaiting(t, got)
}

func stillWaiting(t *testing.T, ch <-chan waited) {
	t.Helper()
	select {
	case w := <-ch:
		t.Fatalf("Wait returned %q, %v; it should still be waiting", w.code, w.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestADeclinedLoginSaysWhy(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	browserReturns(t, cb, "/callback?error=access_denied&error_description=nope&state="+
		url.QueryEscape(stateOf(t, f)))
	w := within(t, got)
	if w.err == nil || !strings.Contains(w.err.Error(), "access_denied") {
		t.Fatalf("Wait error %v, want one naming access_denied", w.err)
	}
}

// A browser asks for /favicon.ico and the like; only /callback ends the wait.
func TestOtherPathsDoNotEndTheWait(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	if resp := browserReturns(t, cb, "/favicon.ico"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/favicon.ico got %d, want 404", resp.StatusCode)
	}
	select {
	case w := <-got:
		t.Fatalf("Wait returned %q, %v on another path", w.code, w.err)
	case <-time.After(50 * time.Millisecond):
	}
	browserReturns(t, cb, "/callback?code=c&state="+url.QueryEscape(stateOf(t, f)))
	if w := within(t, got); w.code != "c" {
		t.Fatalf("Wait = %q, %v; want c", w.code, w.err)
	}
}

func TestCancellingStopsTheWait(t *testing.T) {
	cb, f := startCallbackLogin(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := waitInBackground(cb, f, ctx)
	cancel()
	if w := within(t, got); !errors.Is(w.err, context.Canceled) {
		t.Fatalf("Wait error %v, want context.Canceled", w.err)
	}
}

// capture is a token endpoint that records the form it was sent.
type capture struct{ form url.Values }

func (c *capture) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	c.form, _ = url.ParseQuery(string(body))
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{},
		Body:    io.NopCloser(strings.NewReader(`{"access_token":"a","refresh_token":"r","expires_in":3600}`)),
		Request: r}, nil
}

// The token endpoint checks redirect_uri against the one the code was issued
// for, so the exchange must send the flow's own: the listener's for a
// callback login, the code page's for a pasted one.
func TestTheExchangeSendsTheRedirectTheLoginUsed(t *testing.T) {
	cb, f := startCallbackLogin(t)
	pasted, err := BeginAuthWith("", Extra{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		f    *AuthFlow
		want string
	}{
		{"callback", f, "http://" + strings.Replace(cb.Addr(), "127.0.0.1", "localhost", 1) + "/callback"},
		{"pasted", pasted, RedirectURI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ep := &capture{}
			c := &Client{HTTP: &http.Client{Transport: ep}}
			if _, err := c.Exchange(context.Background(), tc.f, "code"); err != nil {
				t.Fatal(err)
			}
			if got := ep.form.Get("redirect_uri"); got != tc.want {
				t.Errorf("redirect_uri %q, want %q", got, tc.want)
			}
		})
	}
}
