package broker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAPluginDoesNotMakeAKeptPlaceUnderASpellingTheVolumeFolds(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folds := func(made, asked string) bool {
		p := filepath.Join(home, made)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			return false
		}
		defer os.Remove(p)
		_, err := os.Stat(filepath.Join(home, asked))
		return err == nil
	}
	longS := folds("x.j\u017fon", "x.json")
	decomposed := folds("cafe\u0301.json", "caf\u00e9.json")
	if !longS && !decomposed {
		t.Skip("this volume folds neither \u017f nor a decomposed accent: no second spelling of a place not made yet can be made here")
	}
	config := filepath.Join(home, "config.json")
	profile := filepath.Join(home, "secrets", "google", "token.json")
	accented := filepath.Join(home, "vault", "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	var places []*firewall.Rule
	for id, path := range map[string]string{"sub.config": config, "sub.profile": profile, "sub.accent": accented} {
		places = append(places, &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path, Reason: "the host keeps it", Enforced: true})
	}
	policy.SetPlaces(places)
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "staged.txt", `{"data":"FORGED"}`)), statusSucceeded, "")
	staged := filepath.Join(home, "staged.txt")
	sum := hexSum([]byte("FORGED"))

	type attempt struct {
		what, params string
		kept         string
		left         []string
	}
	var attempts []attempt
	if longS {
		attempts = append(attempts,
			attempt{"fs.write of config.j\u017fon", fsParams("fs.write", "sandbox", "config.j\u017fon", `{"data":"FORGED"}`), config, nil},
			attempt{"fs.write of \u017fecrets/google/token.json", fsParams("fs.write", "sandbox", "\u017fecrets/google/token.json", `{"data":"FORGED"}`), profile, []string{filepath.Join(home, "secrets")}},
			attempt{"fs.publish of config.j\u017fon", fsParams("fs.publish", "sandbox", "config.j\u017fon", `{"data":"FORGED"}`), config, nil},
		)
	} else {
		t.Log("this volume does not fold \u017f to s: the long-s spellings are not asked")
	}
	if decomposed {
		attempts = append(attempts,
			attempt{"fs.write of the decomposed spelling", fsParams("fs.write", "sandbox", "vault/cafe\u0301.json", `{"data":"FORGED"}`), accented, []string{filepath.Join(home, "vault")}},
			attempt{"fs.write appending to the decomposed spelling", fsParams("fs.write", "sandbox", "vault/cafe\u0301.json", `{"data":"FORGED","append":true}`), accented, []string{filepath.Join(home, "vault")}},
			attempt{"fs.publish of the decomposed spelling", fsParams("fs.publish", "sandbox", "vault/cafe\u0301.json", `{"data":"FORGED"}`), accented, []string{filepath.Join(home, "vault")}},
			attempt{"fs.publish of a staged file to the decomposed spelling", fsParams("fs.publish", "sandbox", "vault/cafe\u0301.json", `{"from":"staged.txt","sha256":"`+sum+`"}`), accented, []string{filepath.Join(home, "vault")}},
		)
	} else {
		t.Log("this volume does not fold a decomposed accent: the decomposed spellings are not asked")
	}
	for _, a := range attempts {
		m := dispatch(t, b, a.params)
		var got struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &got)
		if got.Status != statusDenied || got.Reason != reasonFSProtected {
			t.Errorf("%s makes a kept place: refused, FS_PROTECTED: %s", a.what, raw)
		}
		for _, p := range append([]string{a.kept}, a.left...) {
			if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s left %s behind: %v", a.what, p, err)
				os.RemoveAll(p)
			}
		}
		if got, err := os.ReadFile(staged); err != nil || string(got) != "FORGED" {
			t.Errorf("%s took the staged file: %q %v", a.what, got, err)
			if err := os.WriteFile(staged, []byte("FORGED"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "notes/plain.txt", `{"data":"an ordinary note"}`)), statusSucceeded, "")
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "sandbox", "notes/published.txt", `{"data":"an ordinary publication"}`)), statusSucceeded, "")
	for _, p := range []string{"notes/plain.txt", "notes/published.txt"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("the plugin's ordinary %s did not land: %v", p, err)
		}
	}
}

func TestAPluginWriteCountsAsMadeWhatItCreatedNotWhatThePathShowed(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "cafe\u0301.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ferr := os.Stat(filepath.Join(vault, "caf\u00e9.json"))
	os.Remove(filepath.Join(vault, "cafe\u0301.json"))
	if ferr != nil {
		t.Skip("this volume does not open a decomposed accent as the composed one: nothing to make under another spelling")
	}
	accented := filepath.Join(vault, "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	h.fsWriteLook = func(osRoot *os.Root, rel string) (fs.FileInfo, error) {
		// The other call makes the file, this write looks at it, and the
		// other call takes it back.
		f, err := osRoot.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte("OTHER"))
		f.Close()
		fi, lerr := osRoot.Lstat(rel)
		if err := osRoot.Remove(rel); err != nil {
			t.Fatal(err)
		}
		return fi, lerr
	}
	for _, args := range []string{`{"data":"FORGED"}`, `{"data":"FORGED","append":true}`} {
		m := dispatch(t, b, fsParams("fs.write", "sandbox", "vault/cafe\u0301.json", args))
		var got struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &got)
		if got.Status != statusDenied || got.Reason != reasonFSProtected {
			t.Errorf("fs.write %s that made the kept place after seeing another's file: refused, FS_PROTECTED: %s", args, raw)
		}
		if bs, err := os.ReadFile(accented); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("fs.write %s left the kept place made: %q %v", args, bs, err)
			os.Remove(accented)
		}
	}
	h.fsWriteLook = nil
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "vault/plain.txt", `{"data":"an ordinary note"}`)), statusSucceeded, "")
}

func TestAPluginDoesNotWriteIntoAKeptFilePutAtItsPathAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "cafe\u0301.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ferr := os.Stat(filepath.Join(vault, "caf\u00e9.json"))
	os.Remove(filepath.Join(vault, "cafe\u0301.json"))
	if ferr != nil {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	accented := filepath.Join(vault, "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	hostMakes := func() {
		if err := os.WriteFile(accented, []byte("HOST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.fsWriteLook = func(osRoot *os.Root, rel string) (fs.FileInfo, error) {
		hostMakes()
		return osRoot.Lstat(rel)
	}
	h.fsPublishLook = func(*os.Root, string) { hostMakes() }
	for _, c := range []struct{ op, args string }{
		{"fs.write", `{"data":"FORGED"}`},
		{"fs.write", `{"data":"FORGED","append":true}`},
		{"fs.publish", `{"data":"FORGED"}`},
	} {
		m := dispatch(t, b, fsParams(c.op, "sandbox", "vault/cafe\u0301.json", c.args))
		var got struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &got)
		if got.Status != statusDenied || got.Reason != reasonFSProtected {
			t.Errorf("%s %s while the host made its place: refused, FS_PROTECTED: %s", c.op, c.args, raw)
		}
		if bs, err := os.ReadFile(accented); err != nil || string(bs) != "HOST\n" {
			t.Errorf("%s %s: the host's file is byte for byte as it made it: %q %v", c.op, c.args, bs, err)
		}
		os.Remove(accented)
	}
	h.fsWriteLook, h.fsPublishLook = nil, nil
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "vault/plain.txt", `{"data":"an ordinary note"}`)), statusSucceeded, "")
}

func TestAPluginDoesNotWriteIntoAKeptFileInAKeptDirectoryMadeAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "dossie\u0301"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ferr := os.Stat(filepath.Join(home, "dossi\u00e9"))
	os.Remove(filepath.Join(home, "dossie\u0301"))
	if ferr != nil {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	dir := filepath.Join(home, "dossi\u00e9")
	hostFile := filepath.Join(dir, "x.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.dossier", Kind: firewall.KindSubstrate, Path: dir, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	h.fsWriteLook = func(osRoot *os.Root, rel string) (fs.FileInfo, error) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hostFile, []byte("HOST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return osRoot.Lstat(rel)
	}
	for _, args := range []string{`{"data":"FORGED"}`, `{"data":"FORGED","append":true}`} {
		m := dispatch(t, b, fsParams("fs.write", "sandbox", "dossie\u0301/x.json", args))
		var got struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &got)
		// The directory the operation made holds the host's file: it is
		// left, and the answer is a failure saying so, not a clean denial.
		if got.Status != statusFailed || got.Reason != reasonFSProtected || !strings.Contains(string(raw), "is left") {
			t.Errorf("fs.write %s while the host made its directory and file: failed, FS_PROTECTED, naming the directory left: %s", args, raw)
		}
		if bs, err := os.ReadFile(hostFile); err != nil || string(bs) != "HOST\n" {
			t.Errorf("fs.write %s: the host's file is byte for byte as it made it: %q %v", args, bs, err)
		}
		os.RemoveAll(dir)
	}
}

func TestAPluginDeleteDoesNotRemoveAKeptFileSavedAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "cafe\u0301.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ferr := os.Stat(filepath.Join(vault, "caf\u00e9.json"))
	os.Remove(filepath.Join(vault, "cafe\u0301.json"))
	if ferr != nil {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	accented := filepath.Join(vault, "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	h.fsDeleteLook = func(*os.Root, string) {
		if err := os.WriteFile(accented, []byte("HOST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := dispatch(t, b, fsParams("fs.delete", "sandbox", "vault/cafe\u0301.json", ""))
	var got struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	raw, _ := json.Marshal(m)
	_ = json.Unmarshal(raw, &got)
	if got.Status != statusDenied || got.Reason != reasonFSProtected {
		t.Errorf("fs.delete of the kept file the host saved after the gate: denied, FS_PROTECTED: %s", raw)
	}
	if bs, err := os.ReadFile(accented); err != nil || string(bs) != "HOST\n" {
		t.Errorf("the host's file is byte for byte as it made it: %q %v", bs, err)
	}
	h.fsDeleteLook = nil
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "vault/plain.txt", `{"data":"x"}`)), statusSucceeded, "")
	wantResult(t, dispatch(t, b, fsParams("fs.delete", "sandbox", "vault/plain.txt", "")), statusSucceeded, "")
}

func TestAPluginDoesNotPublishAKeptFileSavedAtItsStagedPathAfterTheGate(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "cafe\u0301.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ferr := os.Stat(filepath.Join(vault, "caf\u00e9.json"))
	os.Remove(filepath.Join(vault, "cafe\u0301.json"))
	if ferr != nil {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	accented := filepath.Join(vault, "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	h.fsPublishLook = func(*os.Root, string) {
		if err := os.WriteFile(accented, []byte("HOST\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := dispatch(t, b, fsParams("fs.publish", "sandbox", "vault/pub.txt", `{"from":"vault/cafe\u0301.json","sha256":"`+hexSum([]byte("HOST\n"))+`"}`))
	var got struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	raw, _ := json.Marshal(m)
	_ = json.Unmarshal(raw, &got)
	if got.Status != statusDenied || got.Reason != reasonFSProtected {
		t.Errorf("fs.publish from the host's kept file saved at the staged path: denied, FS_PROTECTED: %s", raw)
	}
	if bs, err := os.ReadFile(accented); err != nil || string(bs) != "HOST\n" {
		t.Errorf("the host's file is byte for byte where and as it made it: %q %v", bs, err)
	}
	if _, err := os.Lstat(filepath.Join(vault, "pub.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the host's bytes were published: %v", err)
	}
}

func TestTheDirectoriesAnOperationMadeAreTakenBackWhenItFailsPartWay(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "cafe\u0301.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, ferr := os.Stat(filepath.Join(vault, "caf\u00e9.json"))
	os.Remove(filepath.Join(vault, "cafe\u0301.json"))
	if ferr != nil {
		t.Skip("this volume does not open a decomposed accent as the composed one: no other spelling opens the place")
	}
	dir := filepath.Join(home, "dossi\u00e9")
	accented := filepath.Join(vault, "caf\u00e9.json")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{
		{ID: "sub.dossier", Kind: firewall.KindSubstrate, Path: dir, Reason: "the host keeps it", Enforced: true},
		{ID: "sub.accent", Kind: firewall.KindSubstrate, Path: accented, Reason: "the host keeps it", Enforced: true},
	})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: world, MaxFilesBytes: 1 << 20})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	long := strings.Repeat("x", 300)
	for _, place := range []struct{ what, spelled, path string }{
		{"a directory place", "dossie\u0301", dir},
		{"a file place", "vault/cafe\u0301.json", accented},
	} {
		for _, op := range []string{"fs.write", "fs.publish"} {
			m := dispatch(t, b, fsParams(op, "sandbox", place.spelled+"/"+long+"/a.txt", `{"data":"FORGED"}`))
			var got struct {
				Status string `json:"status"`
			}
			raw, _ := json.Marshal(m)
			_ = json.Unmarshal(raw, &got)
			if got.Status == statusSucceeded {
				t.Errorf("%s below %s, through a name too long, does not succeed: %s", op, place.what, raw)
			}
			if _, err := os.Lstat(place.path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s below %s, failing part-way, left the place made: %v (%s)", op, place.what, err, raw)
				os.RemoveAll(place.path)
			}
		}
	}
}
