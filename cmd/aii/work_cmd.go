package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

func runWork(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "export" {
		fmt.Fprintln(stderr, "Usage: aii work export [-dir directory] [-config file] [-since time] [-session ws_…] [-project id] -o file")
		return 2
	}
	return runWorkExport(args[1:], stdout, stderr)
}

func runWorkExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "Identity install directory")
	config := fs.String("config", "", "Path to config file (default: config/config.json)")
	since := fs.String("since", "", "Only sessions started or delivered at or after this time (RFC 3339, or a date: 2026-10-24)")
	session := fs.String("session", "", "Only this work session (ws_…)")
	projectID := fs.String("project", "", "Only the sessions of this project")
	out := fs.String("o", "", "The file to write: a new one, owner-only")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aii work export [-dir directory] [-config file] [-since time] [-session ws_…] [-project id] -o file")
		fmt.Fprintln(stderr, "Writes the work record, one JSON line per work session, to a new local file. "+workrecord.Warning)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *out == "" {
		fs.Usage()
		return 2
	}
	filter := workrecord.Filter{Session: strings.TrimSpace(*session), Project: strings.TrimSpace(*projectID)}
	if *since != "" {
		t, err := parseSince(*since)
		if err != nil {
			fmt.Fprintf(stderr, "work export: -since %q is neither an RFC 3339 time nor a date (2006-01-02)\n", *since)
			return 2
		}
		filter.Since = t
	}
	if _, err := os.Lstat(*out); err == nil {
		fmt.Fprintf(stderr, "work export: %s already exists; name a new file\n", *out)
		return 1
	}
	path := operatorConfigPath(*dir, *config)
	cfg, err := app.ReadConfig(path)
	if err != nil {
		fmt.Fprintf(stderr, "work export: %v\n", err)
		return 1
	}
	home := *dir
	if home == "" {
		home = "."
	}
	if home, err = filepath.Abs(home); err != nil {
		fmt.Fprintf(stderr, "work export: %v\n", err)
		return 1
	}
	n, how, err := exportWorkRecord(context.Background(), cfg, home, filter, *out)
	if err != nil {
		fmt.Fprintf(stderr, "work export: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, *out)
	fmt.Fprintf(stderr, "work export: %d work session(s) written to %s, %s. %s\n", n, *out, how, workrecord.Warning)
	return 0
}

func parseSince(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", raw)
}

func exportWorkRecord(ctx context.Context, cfg *app.Config, home string, f workrecord.Filter, out string) (int, string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(out), "."+filepath.Base(out)+".tmp-*")
	if err != nil {
		return 0, "", fmt.Errorf("create the file beside %s: %w", out, err)
	}
	staged := tmp.Name()
	published := false
	defer func() {
		tmp.Close()
		if !published {
			os.Remove(staged)
		}
	}()
	if err := fileperm.RestrictToOwner(tmp); err != nil {
		return 0, "", fmt.Errorf("restrict %s to its owner: %w", staged, err)
	}
	how, err := readWorkRecord(ctx, cfg, home, f, tmp)
	if err != nil {
		return 0, "", err
	}
	if err := tmp.Sync(); err != nil {
		return 0, "", err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return 0, "", err
	}
	n, err := workrecord.Check(tmp)
	if err != nil {
		return 0, "", fmt.Errorf("%w; nothing was written to %s", err, out)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", err
	}
	published, note, err := publishExport(staged, out)
	if err != nil {
		return 0, "", err
	}
	return n, how + note, nil
}

func publishExport(staged, out string) (published bool, note string, err error) {
	published, err = atomicfile.PublishNew(staged, out)
	switch {
	case err == nil:
		return true, "", nil
	case published:
		os.Remove(staged)
		return true, fmt.Sprintf(" (its directory entry is not proven durable: %v)", err), nil
	case errors.Is(err, os.ErrExist):
		return false, "", fmt.Errorf("%s already exists; name a new file", out)
	}
	claim, cerr := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if cerr != nil {
		return false, "", fmt.Errorf("publish %s: %v; and it could not be claimed: %w", out, err, cerr)
	}
	claim.Close()
	if rerr := atomicfile.Rename(staged, out); rerr != nil {
		os.Remove(out)
		return false, "", fmt.Errorf("publish %s: %v; and the rename over its claim failed: %w", out, err, rerr)
	}
	return true, " (named by a rename: the file system made no hard link)", nil
}

func readWorkRecord(ctx context.Context, cfg *app.Config, home string, f workrecord.Filter, w io.Writer) (string, error) {
	port := cfg.Dashboard.Port
	if port > 0 && install.WaitServing(port, home, 0) == nil {
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		if err := exportThroughHost(ctx, base, cfg.Dashboard.AccessToken, f, w); err != nil {
			return "", err
		}
		return "read through the running identity", nil
	}
	db, lockPath, projects := app.WorkRecordPlaces(cfg, home)
	lock, err := os.Open(lockPath)
	if err != nil {
		return "", fmt.Errorf("no record to read at %s (%v): this is not an identity's home, or its record was never opened", filepath.Dir(lockPath), err)
	}
	defer lock.Close()
	if err := filelock.RLock(lock); err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return "", fmt.Errorf("the identity in %s is running, and its dashboard does not answer on port %d with this slot's proof; its store is never opened by a second program, so nothing was read. Try again once its dashboard answers", home, port)
		}
		return "", fmt.Errorf("take the lock of the record in %s: %w", filepath.Dir(lockPath), err)
	}
	st, err := store.OpenReadOnly(db)
	if err != nil {
		return "", fmt.Errorf("open the stopped identity's database read-only: %w", err)
	}
	defer st.Close()
	if _, err := workrecord.Write(ctx, w, st, project.NewManager(projects), f, time.Now()); err != nil {
		return "", err
	}
	return "read from the stopped identity's home", nil
}

func exportThroughHost(ctx context.Context, base, token string, f workrecord.Filter, w io.Writer) error {
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil, ResponseHeaderTimeout: 2 * time.Minute},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var cookies []*http.Cookie
	var key string
	if token != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/auth/token", strings.NewReader(token))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("the dashboard at %s did not answer: %w", base, err)
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			return fmt.Errorf("the dashboard at %s did not sign this program in (%d): %s", base, res.StatusCode, strings.TrimSpace(string(body)))
		}
		cookies, key = res.Cookies(), res.Header.Get(dashboard.PageKeyHeader)
	}
	q := url.Values{}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if f.Session != "" {
		q.Set("session", f.Session)
	}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+dashboard.WorkExportRoute+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if key != "" {
		req.Header.Set(dashboard.PageKeyHeader, key)
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("the dashboard at %s did not answer: %w", base, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return fmt.Errorf("the dashboard at %s did not give the work record (%d): %s", base, res.StatusCode, strings.TrimSpace(string(body)))
	}
	if _, err := io.Copy(w, res.Body); err != nil {
		return fmt.Errorf("read the work record from %s: %w", base, err)
	}
	return nil
}
