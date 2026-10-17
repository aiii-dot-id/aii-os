//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/memoryeval"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// meaning_trial_test.go — a real package of aii.embeddings, from install
// to recall, through a real App.
//
// Every test of the meaning layer's plugin source until this one stood a
// stand-in where the package would be. No host had run a real one: its
// package through verification and selection, its runtime archive and its
// model through the host's own fetch, its carrier and worker under the
// wall, its signed probes at activation, and then a backfill and recalls
// through the application's own source.
//
// AII_MEANING_TRIAL names a directory of inputs; without it the test
// skips. It holds:
//
//	package.aiiospkg   the plugin's package, UNSIGNED
//	files/<name>       every file the package declares by URL (its runtime
//	                   archive, its models), each under the last element
//	                   of its URL's path
//
// AII_MEANING_TRIAL_REPORT, when set, is where the run's record is written.
// AII_MEANING_TRIAL_SERVE_MINUTES, when set, keeps the scratch identity's
// dashboard serving after the run, for a browser to be driven against it.
//
// THIS IS A REHEARSAL, AND EVERY RECORD OF IT SAYS SO. The host runs native
// code at the platform tier only. The unsigned package is signed here, in
// this process, by a platform root made for the run, and the App is told
// to trust that root and no other. It proves the package and the host
// against each other. It is not a run of a signed release.
func TestTheMeaningPackageFromInstallToRecall(t *testing.T) {
	inputs := os.Getenv("AII_MEANING_TRIAL")
	if inputs == "" {
		t.Skip("set AII_MEANING_TRIAL to a directory holding package.aiiospkg (unsigned) and files/")
	}
	report := map[string]any{"rehearsal": true, "signed_by": "a platform root made in the test's own process", "passed": false}
	t.Cleanup(func() {
		report["passed"] = !t.Failed()
		raw, _ := json.MarshalIndent(report, "", "  ")
		t.Logf("the run's record:\n%s", raw)
		if out := os.Getenv("AII_MEANING_TRIAL_REPORT"); out != "" {
			if err := os.WriteFile(out, append(raw, '\n'), 0o600); err != nil {
				t.Error(err)
			}
		}
	})

	// The frozen corpus is read from the tree this test was built from,
	// found before the working directory moves to the scratch identity's.
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	corpusDir := filepath.Join(pkgDir, "..", "memory", "memoryeval", "testdata", "recall-2026-10-07")

	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "MeaningTrial",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// The package, signed for the rehearsal, in the place an install puts it.
	raw, err := os.ReadFile(filepath.Join(inputs, "package.aiiospkg"))
	if err != nil {
		t.Fatal(err)
	}
	spec := unsignedPackage(t, raw)
	var manifest struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(spec.Manifest, &manifest); err != nil || manifest.ID == "" || manifest.Version == "" {
		t.Fatalf("the package's manifest names no id or version: %v", err)
	}
	report["package_id"], report["package_version"] = manifest.ID, manifest.Version
	root, status, err := packagetest.NewPlatformRelease("aiii_rehearsal_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	rootRaw, _ := json.Marshal(root.Env)
	rootPath := filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trust, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	signed := filepath.Join(t.TempDir(), manifest.ID+"-"+manifest.Version+".aiiospkg")
	if err := os.WriteFile(signed, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, manifest.ID, signed)
	t.Chdir(dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// The host's own fetch, answered from the inputs: each declared file
	// under the last element of its URL.
	var fetched atomic.Int32
	var fetchedNames []string
	serve := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		name := path.Base(strings.SplitN(url, "?", 2)[0])
		f, err := os.Open(filepath.Join(inputs, "files", name))
		if err != nil {
			return 0, fmt.Errorf("the inputs hold no file for %s: %w", url, err)
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
		fetched.Add(1)
		fetchedNames = append(fetchedNames, name)
		return io.Copy(w, f)
	}
	store := newTestStore(t)
	boot := func(fetch func(context.Context, string, int64, io.Writer) (int64, error)) *App {
		cfg := &Config{
			Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
				LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
			LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
			SourcePath: filepath.Join(dir, "config.json"),
			Dashboard:  DashboardConfig{Port: 0},
			Tools:      ToolsConfig{CWD: dir},
			Plugins:    PluginsConfig{Autoload: "T3", PlatformRoot: rootPath, WorkerBinary: exe},
			Agency:     defaultConfig().Agency,
		}
		if stored, err := LoadConfig(cfg.SourcePath); err == nil {
			cfg.Memory = stored.Memory // the choice the host recorded on the first start
		}
		store.name(cfg)
		a := New(cfg)
		a.storeMaterialize = store.materialize(fetch)
		if err := startLiveUnsettled(a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	serving := func(a *App) bool { return len(a.embeddingsPlugins()) == 1 }

	// 1. INSTALL: verified, selected, its runtime and model fetched and
	// verified, admitted, started under the wall.
	began := time.Now()
	a := boot(serve)
	defer func() { stopApp(t, a) }()
	voiceWait(t, "the package to be serving as a source of vectors (its files fetched, its child started)", 10*time.Minute, func() bool {
		if serving(a) {
			return true
		}
		for _, v := range a.pluginPendingViews() {
			if v.LastError != "" {
				report["last_error_while_preparing"] = v.LastError
			}
		}
		return false
	})
	report["install_to_serving_seconds"] = time.Since(began).Seconds()
	report["fetched"] = append([]string(nil), fetchedNames...)
	if fetched.Load() == 0 {
		t.Fatal("the package served and nothing was fetched: the inputs' files were not what it runs from")
	}
	ap := a.embeddingsPlugins()[0]
	report["basis"] = ap.Embeddings.Basis()

	// 2. THE CHOICE AND THE PROBES. Installing it is choosing it where no
	// choice is stored; its signed probes are asked before any memory.
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	card := func() string {
		state, err := a.meaningState(context.Background(), f, a.pickMeaningSource())
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(state)
		return string(raw)
	}
	voiceWait(t, "the plugin to be the recorded source, checked against its probes", 3*time.Minute, func() bool {
		pick := a.pickMeaningSource()
		return a.configSnapshot().Memory.MeaningSource == meaningChoicePlugin+manifest.ID && pick.kind == "plugin" && pick.question == ""
	})
	report["card_after_install"] = json.RawMessage(card())

	// 3. STOPPED AND STARTED WITH EVERYTHING ON DISK: NOTHING IS FETCHED.
	// What a start finds under its hashes it uses, offline as much as
	// online; that is also how a trial's files reach an identity by hand.
	// (Before the record below is written: the record is put straight
	// into the store, which a start would rightly refuse to run on.)
	stopApp(t, a)
	report["files_on_disk"] = filesBelow(t, dir, "plugins", "plugins-models", "plugins-runtime")
	var refetched atomic.Int32
	began = time.Now()
	a = boot(func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		refetched.Add(1)
		return serve(ctx, url, offset, w)
	})
	voiceWait(t, "the package to serve again from what is on disk", 5*time.Minute, func() bool {
		pick := a.pickMeaningSource()
		return serving(a) && pick.kind == "plugin" && pick.question == ""
	})
	report["restart_to_serving_seconds"] = time.Since(began).Seconds()
	if n := refetched.Load(); n != 0 {
		t.Fatalf("a start with the archive and the model on disk fetched %d times", n)
	}
	if got := a.configSnapshot().Memory.MeaningSource; got != meaningChoicePlugin+manifest.ID {
		t.Fatalf("after a restart the recorded source is %q", got)
	}
	f = memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})

	// 4. A RECORD, AND A BACKFILL UNDER THE PASS'S TIME.
	_, corpus, cues, err := memoryeval.Load(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	record, asked := trialRecord(corpus, cues, 300, 40)
	now := time.Now().UTC()
	for i, m := range record {
		seq := 1000 + i
		at := now.Add(time.Duration(i-len(record)) * time.Minute).Format(time.RFC3339Nano)
		if _, err := a.store.DB().Exec(`INSERT OR IGNORE INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'test.event', 3, '{}', '')`, seq, at); err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.DB().Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', ?, ?)`, m.ID, m.Text, seq, at); err != nil {
			t.Fatal(err)
		}
	}
	report["record_memories"], report["cues"] = len(record), len(asked)
	var passes []map[string]any
	for pass := 1; ; pass++ {
		if pass > 40 {
			t.Fatalf("forty passes did not cover a record of %d memories: %v", len(record), passes)
		}
		started := time.Now()
		rep, err := a.backfillPass(context.Background(), f)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		var left int64
		for _, s := range rep.Stores {
			left += s.Remaining
		}
		passes = append(passes, map[string]any{"embedded": rep.Embedded, "set_aside": rep.SetAside, "remaining": left,
			"worked_seconds": rep.Worked.Seconds(), "wall_seconds": time.Since(started).Seconds(), "time_up": rep.TimeUp, "line": rep.Line()})
		if rep.Unavailable != "" {
			t.Fatalf("pass %d: the source was not available: %s", pass, rep.Unavailable)
		}
		if left == 0 {
			break
		}
		if rep.Embedded == 0 {
			t.Fatalf("pass %d embedded nothing and %d are still waiting: %s", pass, left, rep.Line())
		}
	}
	report["passes"] = passes
	report["card_after_backfill"] = json.RawMessage(card())

	// 5. RECALLS WITH KNOWN ANSWERS.
	found, byMeaning := 0, 0
	var misses []string
	var pageCue *memoryeval.Cue // the first cue whose own memory the meaning layer brought back
	for _, c := range asked {
		res, err := f.Recall(context.Background(), memory.Query{Text: c.Text})
		if err != nil {
			t.Fatalf("recall %s: %v", c.ID, err)
		}
		if res.Meaning.Status != "found" && res.Meaning.Status != "found_nothing" {
			t.Fatalf("recall %s: the meaning layer answered %q (%s), not a reading of the record", c.ID, res.Meaning.Status, res.Meaning.Detail)
		}
		if res.Meaning.Status == "found" {
			byMeaning++
		}
		hit := false
		for i, h := range res.Hits {
			if i < 7 && h.Store == "experiences" && h.ID == c.Expect[0] {
				hit = true
				if pageCue == nil && h.Similarity > 0 && (h.Match == "meaning" || h.Match == "both") {
					asking := c
					pageCue = &asking
				}
			}
		}
		if hit {
			found++
		} else {
			misses = append(misses, c.ID)
		}
	}
	report["recalled_in_the_first_seven"], report["meaning_layer_found_something"], report["missed"] = found, byMeaning, misses
	if byMeaning == 0 {
		t.Fatal("no recall was answered by the meaning layer: the vectors are there and nothing read them")
	}
	if found*2 < len(asked) {
		t.Fatalf("%d of %d cues brought their memory into the first seven: under half", found, len(asked))
	}

	// 6. THE PAGE. What the Memory view shows of all this to the person
	// in front of it: the card and a recall, read from a real browser
	// signed in through the page's own form (meaning_trial_page_test.go).
	if pageCue == nil {
		t.Fatal("no cue's own memory came back marked as found by meaning: there is nothing to ask the page for")
	}
	report["page"] = meaningTrialPage(t, a, card(), pageCue.Text, pageCue.Expect[0])

	// 7. HELD OPEN FOR A BROWSER, when asked, for a look by eye: the same
	// scratch identity keeps serving its dashboard for
	// AII_MEANING_TRIAL_SERVE_MINUTES and says where, with the cue the
	// page was just asked. Signing in there needs this identity's access
	// token, which this test prints nowhere.
	if minutes, _ := strconv.Atoi(os.Getenv("AII_MEANING_TRIAL_SERVE_MINUTES")); minutes > 0 && a.dashboard != nil {
		fmt.Printf("MEANING_TRIAL_DASHBOARD %s (a rehearsal: a scratch identity, the package signed in this test's process; open for %d minutes)\n", a.dashboard.Origin(), minutes)
		fmt.Printf("MEANING_TRIAL_CUE %q should bring memory %s\n", pageCue.Text, pageCue.Expect[0])
		time.Sleep(time.Duration(minutes) * time.Minute)
	}
}

// unsignedPackage reads an assembled, unsigned package into the parts a
// signature is made over; anything but a manifest and install files in it
// means it is not an unsigned package.
func unsignedPackage(t *testing.T, raw []byte) packagetest.PackageSpec {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	spec := packagetest.PackageSpec{InstallFiles: map[string][]byte{}}
	for archive := tar.NewReader(gz); ; {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		root, rest, _ := strings.Cut(header.Name, "/")
		if spec.Root == "" {
			spec.Root = root
		}
		if root != spec.Root {
			t.Fatalf("the package has a second top-level directory %q", root)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case rest == "manifest.json":
			spec.Manifest = data
		case strings.HasPrefix(rest, "install-root/"):
			spec.InstallFiles[strings.TrimPrefix(rest, "install-root/")] = data
		default:
			t.Fatalf("the package holds %q: it is not an unsigned package", header.Name)
		}
	}
	if spec.Manifest == nil || len(spec.InstallFiles) == 0 {
		t.Fatal("the package holds no manifest or no install files")
	}
	return spec
}

// trialRecord is a record cut from the frozen corpus: the memories the
// first cues of meaning are about, and short memories beside them up to
// the size asked. The cues returned are those whose memory is in it.
func trialRecord(corpus []memoryeval.Memory, cues []memoryeval.Cue, size, ask int) ([]memoryeval.Memory, []memoryeval.Cue) {
	byID := map[string]memoryeval.Memory{}
	for _, m := range corpus {
		byID[m.ID] = m
	}
	in := map[string]bool{}
	var record []memoryeval.Memory
	var asked []memoryeval.Cue
	for _, c := range cues {
		if len(asked) == ask {
			break
		}
		if c.Type != "meaning" || len(c.Expect) == 0 {
			continue
		}
		m, ok := byID[c.Expect[0]]
		if !ok {
			continue
		}
		if !in[m.ID] {
			in[m.ID] = true
			record = append(record, m)
		}
		asked = append(asked, c)
	}
	for _, m := range corpus {
		if len(record) >= size {
			break
		}
		if !in[m.ID] && len(m.Text) < 600 {
			in[m.ID] = true
			record = append(record, m)
		}
	}
	return record, asked
}

// filesBelow lists the files under the named directories of root, with
// their sizes: where an install left the runtime and the model.
func filesBelow(t *testing.T, root string, names ...string) []string {
	t.Helper()
	var out []string
	for _, name := range names {
		_ = filepath.Walk(filepath.Join(root, name), func(p string, info os.FileInfo, err error) error {
			if err == nil && info.Mode().IsRegular() && info.Size() > 1<<20 {
				rel, _ := filepath.Rel(root, p)
				out = append(out, fmt.Sprintf("%s (%d MB)", rel, info.Size()>>20))
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}
