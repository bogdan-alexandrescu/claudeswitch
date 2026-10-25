package clean

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// scanTemp judges Claude's temp files: ended sessions' folders in its temp
// folder, plugin versions no session can be using, and, with
// TranscriptsAfter, ended sessions' subagent transcripts.
func (s *scanner) scanTemp() []Item {
	var items []Item
	items = append(items, s.scratchItems()...)
	for _, dir := range s.opt.ConfigDirs {
		items = append(items, s.pluginItems(dir)...)
		if s.opt.TranscriptsAfter > 0 {
			items = append(items, s.transcriptItems(dir)...)
		}
	}
	return items
}

func (s *scanner) liveID(id string) bool {
	for _, l := range s.live {
		if l.id == id {
			return true
		}
	}
	return false
}

// scratchItems are the session folders in TempRoot: <project>/<session>.
func (s *scanner) scratchItems() []Item {
	if s.opt.TempRoot == "" {
		return nil
	}
	dirs, _ := filepath.Glob(filepath.Join(s.opt.TempRoot, "*", "*"))
	var items []Item
	for _, d := range dirs {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		it := Item{Kind: KindScratch, Path: d}
		switch {
		case s.liveID(filepath.Base(d)):
			it.Why = "its session is live"
		case s.inUse(d) != "":
			it.Why = s.inUse(d)
		default:
			if why := s.opt.recent(newestFile(d), s.opt.After); why != "" {
				it.Why = why
			} else {
				it.Remove, it.Why, it.Bytes = true, "the scratch folder of an ended session", treeSize(d)
			}
		}
		items = append(items, it)
	}
	return items
}

// pluginItems are the version folders in a config folder's plugin cache:
// cache/<marketplace>/<plugin>/<version>. Kept: the installed version, and
// for each live session the version it started with (the newest older
// than the session), since a running session keeps the hooks it loaded.
func (s *scanner) pluginItems(configDir string) []Item {
	installed := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json")); err == nil {
		var f struct {
			Plugins map[string][]struct {
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		if json.Unmarshal(b, &f) == nil {
			for _, entries := range f.Plugins {
				for _, e := range entries {
					installed[filepath.Clean(e.InstallPath)] = true
				}
			}
		}
	}
	plugins, _ := filepath.Glob(filepath.Join(configDir, "plugins", "cache", "*", "*"))
	var items []Item
	for _, p := range plugins {
		versions, _ := filepath.Glob(filepath.Join(p, "*"))
		type ver struct {
			path string
			at   time.Time
		}
		var vs []ver
		for _, v := range versions {
			if fi, err := os.Stat(v); err == nil && fi.IsDir() {
				vs = append(vs, ver{v, fi.ModTime()})
			}
		}
		sort.Slice(vs, func(i, j int) bool { return vs[i].at.Before(vs[j].at) })
		usedBy := map[string]string{}
		for _, l := range s.live {
			if l.configDir != configDir {
				continue
			}
			var pick string
			for _, v := range vs {
				if !v.at.After(l.started) {
					pick = v.path
				}
			}
			if pick != "" {
				usedBy[pick] = l.name
			}
		}
		for _, v := range vs {
			it := Item{Kind: KindPlugin, Path: v.path}
			switch {
			case installed[filepath.Clean(v.path)]:
				it.Why = "the installed version"
			case hasKey(usedBy, v.path):
				it.Why = "a live session started with it"
			default:
				if why := s.opt.recent(v.at, s.opt.After); why != "" {
					it.Why = why
				} else {
					it.Remove, it.Why, it.Bytes = true, "an old version no session uses", treeSize(v.path)
				}
			}
			items = append(items, it)
		}
	}
	return items
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// transcriptItems are ended sessions' subagents/ folders in a config
// folder's transcripts: projects/<project>/<session>/subagents.
func (s *scanner) transcriptItems(configDir string) []Item {
	dirs, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", "*", "subagents"))
	var items []Item
	for _, d := range dirs {
		it := Item{Kind: KindTranscript, Path: d}
		if s.liveID(filepath.Base(filepath.Dir(d))) {
			it.Why = "its session is live"
		} else if why := s.opt.recent(newestFile(d), s.opt.TranscriptsAfter); why != "" {
			it.Why = why
		} else {
			it.Remove, it.Bytes = true, treeSize(d)
			it.Why = fmt.Sprintf("subagent transcripts of an ended session, older than %s", ago(s.opt.TranscriptsAfter))
		}
		items = append(items, it)
	}
	return items
}
