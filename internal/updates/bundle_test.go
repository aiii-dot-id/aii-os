package updates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestBundleRootRecognisesOnlyARealBundleLayout(t *testing.T) {
	for _, c := range []struct {
		name string
		exe  string
		want string
	}{
		{"the shipped layout", "/Applications/AII OS.app/Contents/MacOS/aii", "/Applications/AII OS.app"},
		{"installed elsewhere", "/Users/j/Apps/AII OS.app/Contents/MacOS/aii", "/Users/j/Apps/AII OS.app"},
		{"the app's other executable", "/Applications/AII OS.app/Contents/MacOS/AII OS", "/Applications/AII OS.app"},

		{"a bare unix install", "/usr/local/bin/aii", ""},
		{"a homebrew install", "/opt/homebrew/bin/aii", ""},
		{"a development build", "/home/user", ""},
		// .
		// .
		{"right suffix, wrong interior", "/Applications/AII OS.app/aii", ""},
		{"Contents but no MacOS", "/Applications/AII OS.app/Contents/aii", ""},
		{"MacOS but no .app", "/opt/AII OS/Contents/MacOS/aii", ""},
		{"a directory merely named MacOS", "/home/u/MacOS/aii", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := bundleRoot(filepath.FromSlash(c.exe))
			if c.want == "" {
				if ok {
					t.Fatalf("%s must NOT be read as a bundle, got %q", c.exe, got)
				}
				return
			}
			if !ok {
				t.Fatalf("%s is a bundle layout and was not recognised", c.exe)
			}
			if got != filepath.FromSlash(c.want) {
				t.Fatalf("bundle root = %q, want %q", got, c.want)
			}
		})
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestTheProducerEmitsTheNameTheUpdaterRequests(t *testing.T) {
	want := BundleAssetName("1.2.3", "macos", "arm64")
	if bare := AssetName("1.2.3", "macos", "arm64"); bare == want {
		t.Fatalf("the bundle and bare assets must not share a name: %q", bare)
	}

	// .
	// .
	// .
	// .
	// .
	dmg := readRepoFile(t, "packaging", "macos", "build-dmg.sh")
	m := regexp.MustCompile(`(?m)^APPZIP="\$\{APPZIP:-\$OUT/(.+)\}"$`).FindStringSubmatch(dmg)
	if m == nil {
		t.Fatal("build-dmg.sh no longer defines APPZIP from a default the test can read")
	}
	got := strings.ReplaceAll(m[1], "${VERSION}", "1.2.3")
	if got != want {
		t.Fatalf("producer emits %q, updater requests %q — a bundle install could never self-update", got, want)
	}

	// .
	// .
	for _, sc := range []struct{ file, glob string }{
		{"packaging/build-all.sh", `"$MAC_BUILDER:$rdir/out/aii-os-app_*.zip"`},
		{"packaging/release.sh", `"$OUT"/aii-os-app_*.zip`},
	} {
		body := readRepoFile(t, strings.Split(sc.file, "/")...)
		if !strings.Contains(body, sc.glob) {
			t.Fatalf("%s no longer carries the contract glob %s", sc.file, sc.glob)
		}
		pat := strings.Trim(sc.glob[strings.LastIndex(sc.glob, "/")+1:], `"`)
		if ok, _ := filepath.Match(pat, want); !ok {
			t.Fatalf("%s glob %q does not match the contract name %q", sc.file, pat, want)
		}
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// .
// .
// .
func TestAnOversizeArchiveEntryIsRefusedNotTruncated(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := bytes.Repeat([]byte("A"), int(maxDownloadSize)+4096)
	if err := tw.WriteHeader(&tar.Header{Name: "aii", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	got, err := extractFromTarGz(buf.Bytes())
	if err == nil {
		t.Fatalf("an entry larger than the limit must be refused; got %d bytes back, which is a TRUNCATED executable", len(got))
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("the refusal must name the cause: %v", err)
	}
}

// .
// .
func TestANormalArchiveEntryStillExtractsWhole(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("a perfectly ordinary binary")
	if err := tw.WriteHeader(&tar.Header{Name: "aii", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	gz.Close()

	got, err := extractFromTarGz(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("extracted %q, want %q", got, body)
	}
}
