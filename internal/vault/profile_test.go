package vault

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// recordingItem is one profile's live credential, in memory.
type recordingItem struct {
	name    string
	blob    *keychain.Blob
	writes  []string // access tokens, in order
	readErr error
}

func (r *recordingItem) Name() string { return r.name }
func (r *recordingItem) Read() (*keychain.Blob, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	if r.blob == nil {
		return nil, errors.New("empty item")
	}
	cp := *r.blob
	return &cp, nil
}
func (r *recordingItem) Write(b *keychain.Blob) error {
	r.writes = append(r.writes, b.ClaudeAIOAuth.AccessToken)
	cp := *b
	r.blob = &cp
	return nil
}

func item(name, token string) *recordingItem {
	return &recordingItem{name: name, blob: &keychain.Blob{
		ClaudeAIOAuth: &keychain.OAuth{AccessToken: token, RefreshToken: "r-" + token}}}
}

// fakeEndpoints answers the usage API (an organization per token) and the
// token endpoint (a fixed renewal), with no network.
type fakeEndpoints struct {
	usage []byte
	orgOf map[string]string
	// seatOf answers the profile endpoint: token -> "account@org".
	seatOf   map[string]string
	profiles int
	// recovered is the keychain's recovery items, by name.
	recovered map[string]*keychain.Blob
	// during, when set, sees every request as it is made.
	during func(*http.Request)
}

func (f *fakeEndpoints) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.during != nil {
		f.during(r)
	}
	if r.URL.String() == usage.ProfileEndpoint {
		f.profiles++
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		seat, ok := f.seatOf[tok]
		if !ok {
			return &http.Response{StatusCode: 500, Header: http.Header{},
				Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
		}
		acct, org, _ := strings.Cut(seat, "@")
		body := `{"account":{"uuid":"` + acct + `"},"organization":{"uuid":"` + org + `"}}`
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	if r.URL.String() == oauth.TokenEndpoint {
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(
				`{"access_token":"renewed","refresh_token":"r-renewed","expires_in":28800}`)),
			Request: r}, nil
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	h := http.Header{}
	h.Set("anthropic-organization-id", f.orgOf[tok])
	return &http.Response{StatusCode: 200, Header: h,
		Body: io.NopCloser(strings.NewReader(string(f.usage))), Request: r}, nil
}

// testVault is a vault whose entries, endpoints and budget are all fakes.
func testVault(t *testing.T, entries map[string]*keychain.Blob, orgOf map[string]string) *Vault {
	v, _ := testVaultWith(t, entries, orgOf, nil)
	return v
}

func testVaultWith(t *testing.T, entries map[string]*keychain.Blob, orgOf, seatOf map[string]string) (*Vault, *fakeEndpoints) {
	t.Helper()
	body, err := os.ReadFile("../usage/testdata/usage_response.json")
	if err != nil {
		t.Fatal(err)
	}
	if orgOf == nil {
		orgOf = map[string]string{}
	}
	ep := &fakeEndpoints{usage: body, orgOf: orgOf, seatOf: seatOf,
		recovered: map[string]*keychain.Blob{}}
	v := New(quietLogger())
	v.client = &usage.Client{HTTP: &http.Client{Transport: ep}}
	v.oauth = &oauth.Client{HTTP: &http.Client{Transport: ep}}
	v.budget = usage.NewBudget()

	oldR, oldW := readEntry, writeEntry
	oldRR, oldRW := readRecovery, writeRecovery
	t.Cleanup(func() {
		readEntry, writeEntry = oldR, oldW
		readRecovery, writeRecovery = oldRR, oldRW
	})
	readRecovery = func(name string) (*keychain.Blob, error) {
		if b, ok := ep.recovered[name]; ok {
			return b, nil
		}
		return nil, keychain.ErrNotFound
	}
	writeRecovery = func(name string, b *keychain.Blob) error {
		ep.recovered[name] = b
		return nil
	}
	readEntry = func(id string) (*keychain.Blob, error) {
		if b, ok := entries[id]; ok {
			return b, nil
		}
		return nil, errors.New("not vaulted")
	}
	writeEntry = func(id string, b *keychain.Blob) error {
		entries[id] = b
		return nil
	}
	return v, ep
}

// A swap writes the target profile's item and nothing else, and verifies
// through it.
func TestSwapToInWritesThatProfilesItem(t *testing.T) {
	v := testVault(t, map[string]*keychain.Blob{
		"w2": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w2-token"}},
	}, map[string]string{"w2-token": "org-w"})
	work := item("work", "w1-token")
	other := item("default", "a-token")

	res, err := v.SwapToIn(context.Background(), work, "w2", "org-w")
	if err != nil {
		t.Fatal(err)
	}
	if res.OrgID != "org-w" {
		t.Errorf("verified org %q", res.OrgID)
	}
	if strings.Join(work.writes, ",") != "w2-token" {
		t.Errorf("work's item writes = %v, want the incoming token once", work.writes)
	}
	if len(other.writes) != 0 {
		t.Errorf("another profile's item was written: %v", other.writes)
	}
}

// A swap that installs the wrong account rolls back into the same item and
// confirms the rollback there.
func TestSwapToInRollsBackIntoTheSameItem(t *testing.T) {
	v := testVault(t, map[string]*keychain.Blob{
		"w2": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w2-token"}},
	}, map[string]string{"w2-token": "org-somebody-else"})
	work := item("work", "w1-token")

	res, err := v.SwapToIn(context.Background(), work, "w2", "org-w")
	if err == nil || res == nil || !res.RolledBack {
		t.Fatalf("got %+v, %v; want a confirmed rollback", res, err)
	}
	if strings.Join(work.writes, ",") != "w2-token,w1-token" {
		t.Fatalf("work's item writes = %v, want the swap then the snapshot", work.writes)
	}
}

// Refreshing an account that is live in a profile writes the renewed token
// back into that profile's item, the one whose session holds the old token.
func TestRefreshInWritesBackIntoTheHoldingProfile(t *testing.T) {
	entries := map[string]*keychain.Blob{
		"w1": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w1-token", RefreshToken: "r-w1"}},
	}
	v := testVault(t, entries, map[string]string{"renewed": "org-w"})
	work := item("work", "w1-token")

	if _, err := v.RefreshIn(context.Background(), "w1", "", work, true); err != nil {
		t.Fatal(err)
	}
	if entries["w1"].ClaudeAIOAuth.AccessToken != "renewed" {
		t.Errorf("vault entry not renewed: %+v", entries["w1"].ClaudeAIOAuth)
	}
	if strings.Join(work.writes, ",") != "renewed" {
		t.Errorf("work's item writes = %v, want the renewed token", work.writes)
	}

	// Not live anywhere: the vault is renewed and no item is touched.
	if _, err := v.RefreshIn(context.Background(), "w1", "", nil, false); err != nil {
		t.Fatal(err)
	}
	if len(work.writes) != 1 {
		t.Errorf("an idle refresh wrote a live item: %v", work.writes)
	}
	// Live, without consent: refused before anything happens.
	if _, err := v.RefreshIn(context.Background(), "w1", "", work, false); !errors.Is(err, ErrActiveAccount) {
		t.Errorf("got %v, want ErrActiveAccount", err)
	}
}

// The active entry is re-captured from the profile's own item: tokens equal
// there means nothing to do, without reading any other item.
func TestSyncActiveInComparesWithThatProfilesItem(t *testing.T) {
	v := testVault(t, map[string]*keychain.Blob{
		"w1": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w1-token"}},
	}, nil)
	work := item("work", "w1-token")
	changed, err := v.SyncActiveIn(context.Background(), work, "w1", "seat@org")
	if err != nil || changed {
		t.Fatalf("got %v, %v; the item already holds the vaulted token", changed, err)
	}
	if !v.IsLiveIn(work, "w1") || v.IsLiveIn(item("default", "a-token"), "w1") {
		t.Error("IsLiveIn must answer from the item it is given")
	}
}
