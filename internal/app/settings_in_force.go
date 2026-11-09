package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/limits"
)

var hiddenSettings = []string{"dashboard.access_token", "dashboard.auth_token_sha256", "dashboard.ice_servers"}

func settingIsHidden(key string) bool {
	for _, h := range hiddenSettings {
		if key == h || strings.HasPrefix(key, h+".") {
			return true
		}
	}
	return false
}

func (a *App) settingsInForce() dashboard.SettingsInForce {
	c := a.configSnapshot()
	inForce, usual := flatSettings(c), flatSettings(*defaultConfig())
	stated, unread := statedInFile(c.SourcePath)
	for _, s := range cognitionSettings {
		inForce[s.key], usual[s.key] = ceilingSaid(s.value(c)), ceilingSaid(s.def(c))
	}
	keys := map[string]bool{}
	for k := range inForce {
		keys[k] = true
	}
	for k := range stated {
		keys[k] = true
	}
	for _, line := range configKeyPaths(reflect.TypeOf(Config{})) {
		if path, leaf := strings.CutPrefix(line, "L "); leaf && !strings.ContainsAny(path, "*[") {
			keys[path] = true
		}
	}
	rows := make([]dashboard.SettingInForce, 0, len(keys))
	for k := range keys {
		if head, _, _ := strings.Cut(k, "["); head != k {
			continue
		}
		_, says := stated[k]
		row := dashboard.SettingInForce{Key: k, Value: inForce[k], Usual: usual[k], Stated: says}
		if !says {
			for s := range stated {
				if strings.HasPrefix(s, k+".") {
					row.Stated = true
					break
				}
			}
		}
		if settingIsHidden(k) {
			row.Value, row.Usual, row.Hidden = "", "", true

			if says && (stated[k] == "" || stated[k] == "[]") {
				row.Stated = false
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return dashboard.SettingsInForce{Settings: rows, FileUnread: unread}
}

func statedInFile(path string) (map[string]string, string) {
	stated := map[string]string{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return stated, ""
	}
	if err != nil {
		return stated, err.Error()
	}
	var file map[string]any
	if err := canonicaljson.DecodeStrictNumbers(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &file); err != nil {
		return stated, err.Error()
	}
	flatten("", file, stated)
	return stated, ""
}

func ceilingSaid(c limits.Ceiling) string {
	if c.Unlimited {
		return limits.Word
	}
	return strconv.Itoa(c.N)
}

func flatSettings(c Config) map[string]string {
	out := map[string]string{}
	raw, err := json.Marshal(c)
	if err != nil {
		return out
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) == nil {
		flatten("", v, out)
	}
	return out
}

func flatten(path string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, m := range t {
			p := k
			if path != "" {
				p = path + "." + k
			}
			flatten(p, m, out)
		}
	case nil:
	case string:
		out[path] = t
	case json.Number:
		out[path] = t.String()
	case bool:
		out[path] = dashboard.OnOff(t)
	default:
		raw, _ := json.Marshal(t)
		out[path] = string(raw)
	}
}

func configKeyPaths(t reflect.Type) []string {
	var out []string
	unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	var walk func(t reflect.Type, path string, seen map[reflect.Type]bool)
	objectish := func(t reflect.Type) bool {
		if t.Kind() != reflect.Struct {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.IsExported() && (f.Tag.Get("json") != "" || f.Anonymous) {
				return true
			}
		}

		return !reflect.PointerTo(t).Implements(unmarshaler)
	}
	walk = func(t reflect.Type, path string, seen map[reflect.Type]bool) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch {
		case t.Kind() == reflect.Map:
			out = append(out, "O "+path)
			walk(t.Elem(), path+".*", seen)
			return
		case t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8:
			e := t.Elem()
			for e.Kind() == reflect.Pointer {
				e = e.Elem()
			}
			if objectish(e) {
				out = append(out, "L "+path)
				walk(e, path+"[]", seen)
				return
			}
			out = append(out, "L "+path)
			return
		case !objectish(t):
			out = append(out, "L "+path)
			return
		}
		if seen[t] {
			return
		}
		seen = maps1(seen, t)
		if path != "" {
			out = append(out, "O "+path)
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				walk(f.Type, path, seen)
				continue
			}
			if name == "" {
				name = f.Name
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			walk(f.Type, p, seen)
		}
	}
	walk(t, "", map[reflect.Type]bool{})
	sort.Strings(out)
	return slicesCompact(out)
}

func maps1(m map[reflect.Type]bool, t reflect.Type) map[reflect.Type]bool {
	n := make(map[reflect.Type]bool, len(m)+1)
	for k, v := range m {
		n[k] = v
	}
	n[t] = true
	return n
}

func slicesCompact(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
