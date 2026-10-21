package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// D31: named pools, subscribed by profiles. Two profiles that list one pool
// share its accounts; a profile's candidates are its pools in order (D33).
const sharedPools = profileAccounts + `
[[pool]]
name     = "own"
accounts = ["personal", "a4"]

[[pool]]
name     = "acme"
accounts = ["work-1", "work-2"]

[[profile]]
name  = "default"
pools = ["own", "acme"]

[[profile]]
name  = "work"
dir   = "~/.claude-work"
pools = ["acme"]
`

func TestProfilesShareTheNamedPoolsTheyList(t *testing.T) {
	c := mustLoad(t, sharedPools)
	def, _ := c.ProfileNamed("default")
	work, _ := c.ProfileNamed("work")
	if want := []string{"personal", "a4", "work-1", "work-2"}; !reflect.DeepEqual(def.Pool, want) {
		t.Errorf("default's accounts %v, want %v: its pools in the order it lists them", def.Pool, want)
	}
	if want := [][]string{{"personal", "a4"}, {"work-1", "work-2"}}; !reflect.DeepEqual(def.Tiers, want) {
		t.Errorf("default's tiers %v, want %v", def.Tiers, want)
	}
	if want := []string{"own", "acme"}; !reflect.DeepEqual(def.PoolNames, want) {
		t.Errorf("default's pool names %v, want %v", def.PoolNames, want)
	}
	if want := []string{"work-1", "work-2"}; !reflect.DeepEqual(work.Pool, want) {
		t.Errorf("work's accounts %v, want %v", work.Pool, want)
	}
	if got := c.ProfilesOf("work-1"); !reflect.DeepEqual(got, []string{"default", "work"}) {
		t.Errorf("ProfilesOf(work-1) = %v, want both profiles", got)
	}
	if got := c.ProfilesOf("personal"); !reflect.DeepEqual(got, []string{"default"}) {
		t.Errorf("ProfilesOf(personal) = %v", got)
	}
	if !c.InPool("work", "work-2") || c.InPool("work", "personal") {
		t.Error("InPool disagrees with the pools work lists")
	}
	if p, ok := c.PoolOf("work-1"); !ok || p != "acme" {
		t.Errorf("PoolOf(work-1) = %q, %v", p, ok)
	}
	if got := c.SubscribersOf("acme"); !reflect.DeepEqual(got, []string{"default", "work"}) {
		t.Errorf("SubscribersOf(acme) = %v", got)
	}
}

// D35: `pool = [...]` on a profile is a pool named after it, used by it alone.
func TestALegacyPoolIsAPrivatePoolNamedAfterItsProfile(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"
pool = ["personal", "a4"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["work-1", "work-2"]
`)
	work, _ := c.ProfileNamed("work")
	if !reflect.DeepEqual(work.PoolNames, []string{"work"}) || !reflect.DeepEqual(work.Tiers, [][]string{{"work-1", "work-2"}}) {
		t.Errorf("work: names %v tiers %v", work.PoolNames, work.Tiers)
	}
	if p, ok := c.PoolOf("a4"); !ok || p != "default" {
		t.Errorf("PoolOf(a4) = %q, %v; want default's own pool", p, ok)
	}
}

// D6 still holds: accounts no pool lists are default's, after its pools,
// as a last tier with no name.
func TestUnlistedAccountsAreDefaultsLastTier(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[pool]]
name     = "acme"
accounts = ["work-1"]

[[profile]]
name  = "default"
pools = ["acme"]
`)
	def, _ := c.ProfileNamed("default")
	if want := [][]string{{"work-1"}, {"personal", "a4", "work-2"}}; !reflect.DeepEqual(def.Tiers, want) {
		t.Errorf("tiers %v, want %v", def.Tiers, want)
	}
	if want := []string{"acme", ""}; !reflect.DeepEqual(def.PoolNames, want) {
		t.Errorf("pool names %v, want %v", def.PoolNames, want)
	}
	if _, ok := c.PoolOf("personal"); ok {
		t.Error("an unlisted account is in no pool")
	}
}

func TestInvalidPoolConfigIsAnError(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"D32: an account in two named pools", `
[[pool]]
name = "a"
accounts = ["personal"]
[[pool]]
name = "b"
accounts = ["personal"]
[[profile]]
name = "default"
pools = ["a", "b"]
`, []string{"personal", `"a"`, `"b"`}},
		{"D32: an account in a named pool and a profile's own", `
[[pool]]
name = "a"
accounts = ["personal"]
[[profile]]
name = "default"
pool = ["personal"]
`, []string{"personal", `"a"`, "default"}},
		{"D35: pool and pools on one profile", `
[[pool]]
name = "a"
accounts = ["personal"]
[[profile]]
name = "default"
pool = ["a4"]
pools = ["a"]
`, []string{"default", "pool", "pools"}},
		{"a profile lists an unknown pool", `
[[profile]]
name = "default"
pools = ["nowhere"]
`, []string{"default", "nowhere"}},
		{"a profile lists a pool twice", `
[[pool]]
name = "a"
accounts = ["personal"]
[[profile]]
name = "default"
pools = ["a", "a"]
`, []string{"default", `"a"`, "twice"}},
		{"a pool declared twice", `
[[pool]]
name = "a"
[[pool]]
name = "a"
`, []string{`"a"`, "twice"}},
		{"a pool with no name", `
[[pool]]
accounts = ["personal"]
`, []string{"pool", "name"}},
		{"a pool names an unknown account", `
[[pool]]
name = "a"
accounts = ["ghost"]
`, []string{`"a"`, "ghost"}},
		{"an account twice in one pool", `
[[pool]]
name = "a"
accounts = ["personal", "personal"]
`, []string{`"a"`, "personal", "twice"}},
		{"a pool named like a profile's own pool", `
[[pool]]
name = "work"
accounts = ["work-1"]
[[profile]]
name = "default"
pools = ["work"]
[[profile]]
name = "work"
dir = "~/.claude-work"
pool = ["work-2"]
`, []string{`"work"`}},
		{"a pool name that is not a name", `
[[pool]]
name = "a b"
`, []string{"a b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, profileAccounts+tc.body)
			if err == nil {
				t.Fatal("want a config error, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %s", err, w)
				}
			}
		})
	}
}

// A pool no profile lists rotates nowhere: allowed, like an empty pool, and
// said.
func TestAPoolNoProfileListsIsAWarning(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[pool]]
name = "spare"
accounts = ["work-2"]

[[profile]]
name = "default"
pool = ["personal", "a4", "work-1"]
`)
	found := false
	for _, w := range c.Warnings() {
		if strings.Contains(w, `"spare"`) && strings.Contains(w, "work-2") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings %q do not name the unused pool and its account", c.Warnings())
	}
}

func TestWrittenPoolsRoundTrip(t *testing.T) {
	c := mustLoad(t, sharedPools)
	path := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		b, _ := os.ReadFile(path)
		t.Fatalf("written config does not load: %v\n%s", err, b)
	}
	if !reflect.DeepEqual(back.Pools, c.Pools) || !reflect.DeepEqual(back.Profiles, c.Profiles) {
		t.Fatalf("pools or profiles changed in the round trip:\n got %+v %+v\nwant %+v %+v",
			back.Pools, back.Profiles, c.Pools, c.Profiles)
	}
}
