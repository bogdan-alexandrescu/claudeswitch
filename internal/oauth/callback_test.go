package oauth

import (
	"context"
	"errors"
	"io"
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
// someone else's page posting at the port: it is never used.
func TestACallbackWithAnotherStateIsRefused(t *testing.T) {
	cb, f := startCallbackLogin(t)
	got := waitInBackground(cb, f, context.Background())
	resp := browserReturns(t, cb, "/callback?code=the-code&state=not-this-one")
	w := within(t, got)
	if w.err == nil || w.code != "" {
		t.Fatalf("Wait = %q, %v; want a refusal", w.code, w.err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("the browser got %d, want 400", resp.StatusCode)
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
