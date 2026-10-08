package state

import (
	"sync"
	"testing"
)

// Load materialises every profile it is told about, so Profile(name) for a
// configured name only reads the map. The daemon reads state from more than
// one goroutine, and a first Profile() that inserted would be a map write.
func TestLoadMaterialisesConfiguredProfiles(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"missing file": func(t *testing.T, path string) {},
		"old file":     func(t *testing.T, path string) { writeRaw(t, path, oldFile) },
		"corrupt file": func(t *testing.T, path string) { writeRaw(t, path, "{not json") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			path := isolate(t)
			setup(t, path)
			st, _ := Load(path, "default", "work", "side")
			for _, n := range []string{DefaultProfile, "work", "side"} {
				if st.Profiles[n] == nil {
					t.Fatalf("profile %q not materialised: %v", n, st.Profiles)
				}
			}
			before := len(st.Profiles)
			w := st.Profile("work")
			if w != st.Profiles["work"] || len(st.Profiles) != before {
				t.Fatal("Profile on a materialised name must not insert")
			}
		})
	}
}

// Materialising must not disturb what the file already holds.
func TestMaterialiseKeepsLoadedProfiles(t *testing.T) {
	path := isolate(t)
	writeRaw(t, path, oldFile)
	st, err := Load(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	if st.Default().Active != "personal" || st.Default().Pinned != "work" {
		t.Fatalf("migrated default lost: %+v", st.Default())
	}
	if st.Profile("work").Active != "" {
		t.Fatalf("a materialised profile starts empty: %+v", st.Profile("work"))
	}
}

// Run with -race: concurrent Profile() on materialised names is read-only.
func TestProfileOnMaterialisedNamesIsSafeConcurrently(t *testing.T) {
	path := isolate(t)
	st, _ := Load(path, "default", "work")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if i%2 == 0 {
					_ = st.Profile("work").Active
				} else {
					_ = st.Default().Pinned
				}
			}
		}(i)
	}
	wg.Wait()
}
