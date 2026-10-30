package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

func rootFlags(fs *flag.FlagSet) func() (packagefmt.TrustRoots, error) {
	platform := fs.String("platform-root", "", "platform_release root envelope (default: the shipped root)")
	reviewer := fs.String("reviewer-root", "", "plugin_reviewer root envelope (default: the shipped root)")
	certifier := fs.String("certifier-root", "", "plugin_publisher_certifier root envelope (default: the shipped root)")
	return func() (packagefmt.TrustRoots, error) {
		var r packagefmt.TrustRoots
		var err error
		if r.PlatformRelease, err = packagefmt.PinnedOrShipped(*platform, packagefmt.KeyTypePlatformRelease); err != nil {
			return r, fmt.Errorf("platform root: %w", err)
		}
		if r.Reviewer, err = packagefmt.PinnedOrShipped(*reviewer, packagefmt.KeyTypeReviewer); err != nil {
			return r, fmt.Errorf("reviewer root: %w", err)
		}
		if r.PublisherCertifier, err = packagefmt.PinnedOrShipped(*certifier, packagefmt.KeyTypePublisherCertifier); err != nil {
			return r, fmt.Errorf("certifier root: %w", err)
		}
		return r, nil
	}
}

type multi []string

func (m *multi) String() string     { return fmt.Sprint(*m) }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func runStatusPayload(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("status-payload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "the status file name, e.g. aiii_platform_release_status.json (required)")
	current := fs.String("current", "", "the list published now (required)")
	var withdraw multi
	fs.Var(&withdraw, "withdraw", "a signature envelope of an artifact to withdraw (repeatable)")
	roots := rootFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *current == "" {
		fs.Usage()
		return 2
	}
	r, err := roots()
	if err != nil {
		return fail(stderr, err)
	}
	cur, err := os.ReadFile(*current)
	if err != nil {
		return fail(stderr, err)
	}
	var sigs [][]byte
	for _, w := range withdraw {
		raw, err := os.ReadFile(w)
		if err != nil {
			return fail(stderr, err)
		}
		sigs = append(sigs, raw)
	}
	out, err := packagefmt.NextStatusPayload(r, *file, cur, sigs)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

func runCatalogPayload(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("catalog-payload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalog := fs.String("catalog", "", "the catalog index file, e.g. aiios-plugins.md (required)")
	release := fs.String("host-release", "", "the newest released version of this host line (required)")
	generated := fs.String("generated", "", "when the announcement is made, RFC 3339 (default: now, UTC)")
	roots := rootFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalog == "" || *release == "" {
		fs.Usage()
		return 2
	}
	if *generated == "" {
		*generated = time.Now().UTC().Format(time.RFC3339)
	}
	r, err := roots()
	if err != nil {
		return fail(stderr, err)
	}
	line, ok := plugincatalog.LineOfCatalogFile(filepath.Base(*catalog))
	if !ok {
		return fail(stderr, fmt.Errorf(notALineFile, filepath.Base(*catalog)))
	}
	if released := plugincatalog.HostLine(*release); released != line {
		return fail(stderr, fmt.Errorf("%s is the AII OS %s line's file, and -host-release %s is of the %s line", filepath.Base(*catalog), line, *release, released))
	}
	md, err := os.ReadFile(*catalog)
	if err != nil {
		return fail(stderr, err)
	}
	epochs := map[string]int64{}
	for _, d := range packagefmt.RevocationDomains() {
		path := plugincatalog.PublishedBeside(*catalog, d.FileName)
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "aii-release: no %s beside the catalog: the %s list is not announced\n", path, d.RootKeyType)
			continue
		}
		if err != nil {
			return fail(stderr, err)
		}
		epoch, err := packagefmt.StatusEpoch(r, d.FileName, raw)
		if err != nil {
			return fail(stderr, fmt.Errorf("%s: %w", path, err))
		}
		epochs[d.RootKeyType] = epoch
	}
	out, err := plugincatalog.SignedPayload(md, *release, *generated, epochs)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

func runCatalogVerify(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("catalog-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	catalog := fs.String("catalog", "", "the catalog index file; its signature is the file with .sig (required)")
	roots := rootFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *catalog == "" {
		fs.Usage()
		return 2
	}
	r, err := roots()
	if err != nil {
		return fail(stderr, err)
	}
	line, ok := plugincatalog.LineOfCatalogFile(filepath.Base(*catalog))
	if !ok {
		fmt.Fprintf(stderr, "NOT VERIFIED: "+notALineFile+"\n", filepath.Base(*catalog))
		return 1
	}
	md, err := os.ReadFile(*catalog)
	if err != nil {
		return fail(stderr, err)
	}
	sig, err := os.ReadFile(*catalog + ".sig")
	if err != nil {
		return fail(stderr, err)
	}

	cat, err := plugincatalog.ParseCatalogForLine(md, sig, r.PlatformRelease, line)
	if err != nil {
		fmt.Fprintf(stderr, "NOT VERIFIED: %v\n", err)
		return 1
	}
	if line != plugincatalog.LegacyLine && !slices.Contains(cat.MustUnderstand, "host_line") {
		fmt.Fprintf(stderr, "NOT VERIFIED: the AII OS %s index does not name host_line in must_understand, so a host of an earlier line pointed at it would read half of it\n", line)
		return 1
	}
	if cat.Announced.Release == "" {
		fmt.Fprintln(stderr, "NOT VERIFIED: the signature announces no release (host_release)")
		return 1
	}
	names := map[string]string{}
	for _, d := range packagefmt.RevocationDomains() {
		names[d.RootKeyType] = d.FileName
	}
	var keys []string
	for k := range cat.Announced.StatusEpochs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want := cat.Announced.StatusEpochs[k]
		name, ok := names[k]
		if !ok {
			fmt.Fprintf(stderr, "NOT VERIFIED: the announcement names %q, which is no root this host knows\n", k)
			return 1
		}
		path := plugincatalog.PublishedBeside(*catalog, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "NOT VERIFIED: the %s list announced at epoch %d is not beside the catalog: %v\n", k, want, err)
			return 1
		}
		got, err := packagefmt.StatusEpoch(r, name, raw)
		if err != nil || got != want {
			fmt.Fprintf(stderr, "NOT VERIFIED: %s is announced at epoch %d and is epoch %d (%v)\n", path, want, got, err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "VERIFIED catalog announcing %s (%s); lists %v\n", cat.Announced.Release, cat.Announced.At, cat.Announced.StatusEpochs)
	return 0
}

const notALineFile = "%s is no line's catalog file: aiios-plugins.md (the 0.1 line) or aiios-plugins-<major>.<minor>.md"

func fail(stderr *os.File, err error) int {
	fmt.Fprintf(stderr, "aii-release: %v\n", err)
	return 1
}
