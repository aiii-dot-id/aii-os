package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var keptFirst = []string{
	"identity",
	"tools.disabled",
	"plugins.autoload",
	"speech.speakers",
	"dashboard.require_token",
	"agency.max_subagent_depth",
	"updates.automatic",
}

var keptWhole = []string{"speech.speakers", "plugins.grants.*", "plugins.auth_profiles.*"}

type salvagePiece struct {
	path []string
	raw  json.RawMessage
	elem bool
}

type salvageWalk struct {
	keep func(salvagePiece) bool
	kept []salvagePiece
	lost []string
}

func (w *salvageWalk) visit(path []string, raw json.RawMessage) (keptAny bool) {
	if len(path) > 0 && w.try(salvagePiece{path: path, raw: raw}) {
		return true
	}
	if len(path) == 0 || !keptWholeAt(path) {
		switch firstByte(raw) {
		case '{':
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) == nil {
				mark := len(w.lost)
				for _, k := range rankedKeys(path, m) {
					if w.visit(append(path[:len(path):len(path)], k), m[k]) {
						keptAny = true
					}
				}
				if !keptAny && len(path) > 0 {
					w.lost = append(w.lost[:mark], dotted(path))
				}
				return keptAny
			}
		case '[':
			var entries []json.RawMessage
			if len(path) > 0 && json.Unmarshal(raw, &entries) == nil {
				missed := 0
				for _, e := range entries {
					if w.try(salvagePiece{path: path, raw: e, elem: true}) {
						keptAny = true
					} else {
						missed++
					}
				}
				switch {
				case !keptAny:
					w.lost = append(w.lost, dotted(path))
				case missed > 0:
					w.lost = append(w.lost, fmt.Sprintf("%s (%d of its %d entries)", dotted(path), missed, len(entries)))
				}
				return keptAny
			}
		}
	}
	if len(path) > 0 {
		w.lost = append(w.lost, dotted(path))
	}
	return false
}

func (w *salvageWalk) try(p salvagePiece) bool {
	if !w.keep(p) {
		return false
	}
	w.kept = append(w.kept, p)
	return true
}

func firstByte(raw json.RawMessage) byte {
	if t := bytes.TrimSpace(raw); len(t) > 0 {
		return t[0]
	}
	return 0
}

func dotted(path []string) string { return strings.Join(path, ".") }

func keptWholeAt(path []string) bool {
	for _, w := range keptWhole {
		parts := strings.Split(w, ".")
		if len(parts) != len(path) {
			continue
		}
		match := true
		for i, p := range parts {
			if p != "*" && p != path[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func rankedKeys(path []string, m map[string]json.RawMessage) []string {
	rank := func(k string) int {
		full := dotted(append(path[:len(path):len(path)], k))
		for i, f := range keptFirst {
			if f == full || strings.HasPrefix(f, full+".") {
				return i
			}
		}
		return len(keptFirst)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func salvageDocument(pieces []salvagePiece) []byte {
	root := map[string]any{}
	for _, p := range pieces {
		node := root
		for _, k := range p.path[:len(p.path)-1] {
			next, ok := node[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[k] = next
			}
			node = next
		}
		last := p.path[len(p.path)-1]
		if p.elem {
			list, _ := node[last].([]json.RawMessage)
			node[last] = append(list, p.raw)
		} else {
			node[last] = p.raw
		}
	}
	data, _ := json.Marshal(root)
	return data
}

const notAnObject = "all of it (it is not a JSON object)"

func SalvageConfig(path string, raw []byte) (cfg *Config, lost []string, err error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	walk := &salvageWalk{keep: func(p salvagePiece) bool {
		_, _, err := decodeConfigSaying(path, salvageDocument([]salvagePiece{p}))
		return err == nil
	}}
	if firstByte(raw) == '{' && json.Valid(raw) {
		walk.visit(nil, raw)
	} else {
		walk.lost = []string{notAnObject}
	}
	kept, lost := walk.kept, walk.lost

	if _, _, err := decodeConfigSaying(path, salvageDocument(kept)); err != nil {
		var together []salvagePiece
		for _, p := range kept {
			if _, _, err := decodeConfigSaying(path, salvageDocument(append(slices.Clip(together), p))); err == nil {
				together = append(together, p)
			} else {
				lost = append(lost, dotted(p.path))
			}
		}
		kept = together
	}
	cfg, err = decodeConfig(path, salvageDocument(kept))
	if err != nil {
		return nil, lost, err
	}

	cfg.stated = nil
	if _, err := saveConfig(cfg); err != nil {
		return nil, lost, err
	}
	return cfg, lost, nil
}

const setAsideMark = ".unreadable-"

const setAsideLayout = "20060102T150405Z"

func SetAsideName(path string, now time.Time) string {
	return path + setAsideMark + now.UTC().Format(setAsideLayout)
}

func notInForce(current Config, raw []byte) (lost []string, readable bool) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if firstByte(raw) != '{' || !json.Valid(raw) {
		return nil, false
	}
	base, err := json.Marshal(current)
	if err != nil {
		return nil, false
	}
	read := func(doc []byte) []byte {
		c, _, err := decodeConfigSaying(current.SourcePath, doc)
		if err != nil {
			return nil
		}
		out, _ := json.Marshal(c)
		return out
	}
	want := read(base)
	if want == nil {
		return nil, false
	}
	var tree map[string]any
	if json.Unmarshal(base, &tree) != nil {
		return nil, false
	}
	walk := &salvageWalk{keep: func(p salvagePiece) bool {

		node := tree
		var made []func()
		defer func() {
			for i := len(made) - 1; i >= 0; i-- {
				made[i]()
			}
		}()
		for _, k := range p.path[:len(p.path)-1] {
			next, ok := node[k].(map[string]any)
			if !ok {
				if _, there := node[k]; there {
					return false
				}
				next = map[string]any{}
				parent, key := node, k
				parent[key] = next
				made = append(made, func() { delete(parent, key) })
			}
			node = next
		}
		last := p.path[len(p.path)-1]
		if p.elem {
			list, _ := node[last].([]any)
			var e any
			if json.Unmarshal(p.raw, &e) != nil {
				return false
			}
			return slices.ContainsFunc(list, func(have any) bool { return bytes.Equal(marshalled(have), marshalled(e)) })
		}
		was, had := node[last]
		node[last] = p.raw
		got := read(marshalled(tree))
		if had {
			node[last] = was
		} else {
			delete(node, last)
		}
		return got != nil && bytes.Equal(got, want)
	}}
	walk.visit(nil, raw)
	return walk.lost, true
}

func marshalled(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

const configSetAsideNoticePrefix = "config_set_aside_"

func (a *App) sayConfigsSetAside(c Config) {
	if a.store == nil || c.SourcePath == "" {
		return
	}
	dir, base := filepath.Split(c.SourcePath)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	told, err := a.store.OutboxIDsUnder(configSetAsideNoticePrefix)
	if err != nil {
		logsink.Warn("config.error", "whether the operator was told of a configuration set aside could not be read: %v", err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, base+setAsideMark) || slices.Contains(told, configSetAsideNoticePrefix+name) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			logsink.Warn("config.error", "the configuration set aside as %s could not be read to tell the operator of it: %v", name, err)
			continue
		}
		said := configSetAsideSaid(name, strings.TrimPrefix(name, base+setAsideMark), c, raw)
		if _, err := a.store.AddOutboxNoticeOnce(configSetAsideNoticePrefix+name, "operator", "", said, nil); err != nil {
			logsink.Warn("config.error", "the operator's notice of the configuration set aside as %s could not be written (%v): %s", name, err, said)
			continue
		}
		logsink.Warn("config.decision", "%s", said)
	}
}

func configSetAsideSaid(name, stamp string, current Config, raw []byte) string {
	when := ""
	if t, err := time.Parse(setAsideLayout, stamp); err == nil {
		when = " when it started on " + t.UTC().Format("2006-01-02 at 15:04 UTC")
	}
	head := "[settings] Your settings file could not be read by this version of the platform" + when +
		". It is kept, unchanged, as " + name + ", and the settings in use were written from what could be read of it. "
	lost, readable := notInForce(current, raw)
	switch {
	case !readable:
		return head + "Nothing in it could be read, so none of it is in force. Set again in Settings what you still want."
	case len(lost) == 0:
		return head + "Every setting in it is in force now."
	}
	return head + "These settings in it are not in force now: " + strings.Join(lost, ", ") + ". Set again in Settings those you still want."
}
