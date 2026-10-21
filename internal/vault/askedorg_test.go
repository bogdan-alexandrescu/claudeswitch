package vault

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
)

// A sign-in that asked for an organization (orgUUID) and came back in
// another is refused before anything is stored, and says which it asked for
// and which it got: that is the evidence of whether the request was honoured.
// It is checked before the duplicate check, so landing in an organization
// another name already holds reads as the request not taking, not as a
// duplicate.
func TestASignInFromAnotherOrganizationThanTheOneAskedForIsRefused(t *testing.T) {
	v, _ := testVaultWith(t, nil, map[string]string{"x-tok": "org-home"},
		map[string]string{"x-tok": "ux@org-home"})
	_, err := v.StoreTokens(context.Background(), "x", "", "org-asked",
		&oauth.Tokens{AccessToken: "x-tok", RefreshToken: "r", ExpiresIn: 3600}, nil)
	var other *OtherOrgError
	if !errors.As(err, &other) || other.Want != "org-asked" || other.Got != "org-home" {
		t.Fatalf("got %v, want an OtherOrgError from org-asked to org-home", err)
	}
	for _, want := range []string{"org-asked", "org-home", "Nothing was stored"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lost %q:\n%s", want, err)
		}
	}
}
