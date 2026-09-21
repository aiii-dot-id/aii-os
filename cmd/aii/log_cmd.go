package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func runLog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "Identity install directory")
	config := fs.String("config", "", "Path to config file (default: config/config.json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := *config
	switch {
	case path == "":
		path = app.ConfigPathIn(*dir)
	case *dir != "" && !filepath.IsAbs(path):
		path = filepath.Join(*dir, path)
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "log: read %s: %v\n", path, err)
		return 1
	}
	control := app.LogControlPathIn(*dir)

	show := func() int {
		cfg, err := app.LoadConfig(path)
		if err != nil {
			fmt.Fprintf(stderr, "log: %v\n", err)
			return 1
		}
		ctl, err := app.ReadLogControl(control)
		if err != nil {
			fmt.Fprintf(stderr, "log: %s is unreadable: %v\n", control, err)
			return 1
		}
		fmt.Fprint(stdout, app.DescribeLogging(*cfg, ctl, control))
		return 0
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return show()
	}

	switch rest[0] {
	case "level":
		if len(rest) == 1 {
			fmt.Fprintln(stderr, "log level: name a level (trace, debug, info, warn, error), a category=level, or both")
			return 2
		}
		level := ""
		cats := map[string]string{}
		for _, arg := range rest[1:] {
			name, value, found := strings.Cut(arg, "=")
			if !found {
				level = arg
				continue
			}
			cats[name] = value
		}
		if err := app.SetLogControl(control, level, cats); err != nil {
			fmt.Fprintf(stderr, "log level: %v\n", err)
			return 1
		}
		if rc := show(); rc != 0 {
			return rc
		}
		fmt.Fprintf(stderr, "log: written to %s; a running identity picks it up on its next watch, and the operator's config is untouched\n", control)
		return 0
	case "details":
		for _, d := range logsink.Details() {
			mark := " "
			if d.Payload {
				mark = "*"
			}
			fmt.Fprintf(stdout, "%s %-24s %s\n", mark, d.Name(), d.What)
		}
		fmt.Fprintf(stdout, "\naspects: %s\n", joinAspects())
		fmt.Fprintln(stdout, "* carries an artifact, not a sentence: reached only by its own name, never by a group")
		return 0
	case "groups":
		cfg, err := app.LoadConfig(path)
		if err != nil {
			fmt.Fprintf(stderr, "log: %v\n", err)
			return 1
		}
		ctl, err := app.ReadLogControl(control)
		if err != nil {
			fmt.Fprintf(stderr, "log: %s is unreadable: %v\n", control, err)
			return 1
		}
		fmt.Fprintln(stdout, "derived — every detail carries these without anyone defining them:")
		printGroups(stdout, logsink.DerivedGroups())
		defined := map[string][]string{}
		for n, m := range cfg.Logs.Group {
			defined[n] = m
		}
		for n, m := range ctl.Group {
			defined[n] = m
		}
		if len(defined) == 0 {
			fmt.Fprintln(stdout, "\ndefined: none — add them under logs.group")
		} else {
			fmt.Fprintln(stdout, "\ndefined:")
			printGroups(stdout, defined)
		}
		return 0
	case "tap":
		if len(rest) == 1 {
			return show()
		}
		cat := strings.ToLower(strings.TrimSpace(rest[1]))
		verb := "on"
		if len(rest) > 2 {
			verb = strings.ToLower(strings.TrimSpace(rest[2]))
		}
		switch verb {
		case "off":
			if err := app.SetLogTap(control, cat, app.TapOff); err != nil {
				fmt.Fprintf(stderr, "log tap: %v\n", err)
				return 1
			}
			// .
			// .
			// .
			// .
			// .
			// .
			fmt.Fprintf(stdout, "tap %s: stop recorded in the overlay, over the configuration.\n", cat)
			fmt.Fprintf(stdout, "  a running identity applies it within seconds; an exchange already under way still finishes its capture\n")
			fmt.Fprintf(stdout, "  to let the configuration decide again: aii log tap %s clear\n", cat)
			return 0
		case "clear":
			if err := app.SetLogTap(control, cat, app.TapClear); err != nil {
				fmt.Fprintf(stderr, "log tap: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "tap %s: the overlay says nothing about it now — the configuration decides\n", cat)
			fmt.Fprintf(stdout, "  see what that means with: aii log\n")
			return 0
		case "on":
			until := ""
			for i := 3; i < len(rest); i++ {
				var val string
				switch {
				case rest[i] == "--for" && i+1 < len(rest):
					val, i = rest[i+1], i+1
				case strings.HasPrefix(rest[i], "--for="):
					val = strings.TrimPrefix(rest[i], "--for=")
				default:
					fmt.Fprintf(stderr, "log tap: %q is not a tap option (try: --for 20m)\n", rest[i])
					return 2
				}
				d, err := time.ParseDuration(val)
				if err != nil || d <= 0 {
					fmt.Fprintf(stderr, "log tap: --for wants a duration like 20m or 2h\n")
					return 2
				}
				until = time.Now().Add(d).UTC().Format(time.RFC3339)
			}
			if err := app.SetLogTap(control, cat, until); err != nil {
				fmt.Fprintf(stderr, "log tap: %v\n", err)
				return 1
			}
			// .
			// .
			// .
			// .
			where := filepath.Join(app.LogControlDirName, logsink.CaptureDirName)
			if until == "" {
				fmt.Fprintf(stdout, "tap %s is RECORDING, with no expiry.\n", cat)
			} else {
				fmt.Fprintf(stdout, "tap %s is RECORDING until %s.\n", cat, until)
			}
			fmt.Fprintf(stdout, "  the raw payload of every %s goes to %s (0600, kept %d days)\n", cat, where, logsink.CaptureDays)
			fmt.Fprintf(stdout, "  stop it with: aii log tap %s off\n", cat)
			return 0
		default:
			fmt.Fprintf(stderr, "log tap: say on, off or clear (aii log tap %s on --for 20m)\n", cat)
			return 2
		}
	case "clear":
		if err := app.ClearLogControl(control); err != nil {
			fmt.Fprintf(stderr, "log clear: %v\n", err)
			return 1
		}
		if rc := show(); rc != 0 {
			return rc
		}
		fmt.Fprintln(stderr, "log: the overlay is gone; the operator's configuration stands")
		return 0
	default:
		fmt.Fprintf(stderr, "log: %q is not a log command (try: aii log, aii log level info, aii log details, aii log groups, aii log tap, aii log clear)\n", rest[0])
		return 2
	}
}

// .
// .
func printGroups(w io.Writer, g map[string][]string) {
	names := make([]string, 0, len(g))
	for n := range g {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		members := append([]string(nil), g[n]...)
		sort.Strings(members)
		fmt.Fprintf(w, "  %-16s %s\n", n, strings.Join(members, " "))
	}
}

func joinAspects() string {
	parts := make([]string, 0)
	for _, a := range logsink.Aspects() {
		parts = append(parts, string(a))
	}
	return strings.Join(parts, ", ")
}
