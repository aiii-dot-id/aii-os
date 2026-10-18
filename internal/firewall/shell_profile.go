package firewall

import (
	"path"
	"slices"
	"strings"
)

type ShellWallInput struct {
	Home string

	Roots []string

	Kept []*Rule

	Boundary bool

	Temp []string

	Held []string
}

var shellWriteDevices = []string{
	`(literal "/dev/null")`, `(literal "/dev/zero")`, `(literal "/dev/tty")`,
	`(literal "/dev/dtracehelper")`, `(regex #"^/dev/fd/")`, `(regex #"^/dev/ttys")`,
}

func ShellProfile(in ShellWallInput) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")

	if in.Boundary {
		b.WriteString("(deny file-write*)\n")
		if line, err := allowWrite(append([]string{in.Home}, in.Roots...)); err != nil {
			return "", err
		} else if line != "" {
			b.WriteString(line)
		}
		if line, err := allowWrite(in.Temp); err != nil {
			return "", err
		} else if line != "" {
			b.WriteString(line)
		}
		b.WriteString("(allow file-write* " + strings.Join(shellWriteDevices, " ") + ")\n")
	}

	ancestors := map[string]bool{}
	type exGroup struct {
		place  string
		except []string
		dir    bool
	}
	var groups []exGroup
	for _, r := range in.Kept {
		if r == nil || r.Path == "" {
			continue
		}
		place, err := SBPLString(r.Path)
		if err != nil {
			return "", err
		}
		ops := "file-read* file-write*"
		if r.ReadOpen {
			ops = "file-write*"
		}
		kind := "subpath"
		if r.Stem {
			kind = "prefix"
		}
		b.WriteString("(deny " + ops + " (" + kind + " " + place + "))\n")
		for _, at := range sbplAncestors(r.Path) {
			ancestors[at] = true
		}
		groups = append(groups, exGroup{place: r.Path, except: r.Except, dir: !r.Stem})
	}

	for _, g := range groups {
		for _, e := range g.except {
			ex, err := SBPLString(e)
			if err != nil {
				return "", err
			}
			b.WriteString("(allow file-read* file-write* (subpath " + ex + "))\n")
		}
	}

	metaSeen := map[string]bool{}
	for _, g := range groups {
		if !g.dir {
			continue
		}
		for _, e := range g.except {
			for dir := path.Dir(e); underOrEqual(g.place, dir); dir = path.Dir(dir) {
				if !metaSeen[dir] {
					metaSeen[dir] = true
					m, err := SBPLString(dir)
					if err != nil {
						return "", err
					}
					b.WriteString("(allow file-read-metadata (literal " + m + "))\n")
				}
				if dir == g.place {
					break
				}
			}
		}
	}

	for _, h := range in.Held {
		if h != "" {
			ancestors[h] = true
		}
	}
	anc := make([]string, 0, len(ancestors))
	for a := range ancestors {
		anc = append(anc, a)
	}
	slices.Sort(anc)
	for _, a := range anc {
		s, err := SBPLString(a)
		if err != nil {
			return "", err
		}
		b.WriteString("(deny file-write-unlink (literal " + s + "))\n")
	}

	b.WriteString("(deny lsopen) (deny appleevent-send) (deny job-creation)\n")

	prefix, err := SBPLString(shellDeniedServicePrefix)
	if err != nil {
		return "", err
	}
	b.WriteString("(deny mach-lookup (global-name-prefix " + prefix + "))\n")
	return b.String(), nil
}

const shellDeniedServicePrefix = "com.apple.metadata.mds"

func allowWrite(paths []string) (string, error) {
	var parts []string
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		s, err := SBPLString(p)
		if err != nil {
			return "", err
		}
		parts = append(parts, "(subpath "+s+")")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(allow file-write* " + strings.Join(parts, " ") + ")\n", nil
}

func underOrEqual(ancestor, p string) bool {
	a := strings.TrimSuffix(ancestor, "/")
	return p == a || strings.HasPrefix(p, a+"/")
}

func sbplAncestors(p string) []string {
	var out []string
	for at := path.Dir(p); ; at = path.Dir(at) {
		out = append(out, at)
		if path.Dir(at) == at {
			return out
		}
	}
}
