package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

const repoRoot = "../.."

// The adaptive icon's background is a colour resource, written by hand:
// the middle of the night the disc sits on. Nothing generates it, so
// this holds it to night(0.5), within the level a hand rounding takes.
func TestAndroidBackgroundIsTheNight(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "shells/android/app/src/main/res/values/ic_launcher_background.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var r, g, b int
	at := bytes.Index(raw, []byte(`name="ic_launcher_background">#`))
	if at < 0 {
		t.Fatal("no ic_launcher_background colour in the resource")
	}
	if _, err := fmt.Sscanf(string(raw[at+len(`name="ic_launcher_background">#`):]), "%02X%02X%02X", &r, &g, &b); err != nil {
		t.Fatal(err)
	}
	want := night(0.5)
	for i, got := range []int{r, g, b} {
		if d := got - int(to8(want[i])); d < -1 || d > 1 {
			t.Fatalf("ic_launcher_background is #%02X%02X%02X; night(0.5) is #%02X%02X%02X", r, g, b, to8(want[0]), to8(want[1]), to8(want[2]))
		}
	}
}

// Every committed icon file is what the generator makes from the
// master today. Pixels may differ by 2 levels: floating point is not
// bit-identical across architectures.
func TestCommittedIconsMatchMaster(t *testing.T) {
	m, err := loadMaster(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range pngAssets() {
		samePixels(t, a.path, readPNG(t, filepath.Join(repoRoot, a.path)), a.make(m))
	}

	icoData, err := os.ReadFile(filepath.Join(repoRoot, icoPath))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseICO(icoData)
	if err != nil {
		t.Fatalf("%s: %v", icoPath, err)
	}
	if len(entries) != len(icoSizes) {
		t.Fatalf("%s holds %d sizes, want %d", icoPath, len(entries), len(icoSizes))
	}
	for i, e := range entries {
		if e.size != icoSizes[i] {
			t.Fatalf("%s entry %d is %d px, want %d", icoPath, i, e.size, icoSizes[i])
		}
		img, err := decodeIconImage(e.data)
		if err != nil {
			t.Fatalf("%s %dpx: %v", icoPath, e.size, err)
		}
		samePixels(t, fmt.Sprintf("%s %dpx", icoPath, e.size), img, free(m, e.size))
	}

	icnsData, err := os.ReadFile(filepath.Join(repoRoot, icnsPath))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseICNS(icnsData)
	if err != nil {
		t.Fatalf("%s: %v", icnsPath, err)
	}
	for _, e := range icnsEntries {
		data, ok := parsed[e.kind]
		if !ok {
			t.Fatalf("%s lacks %s", icnsPath, e.kind)
		}
		img, err := decodeIconImage(data)
		if err != nil {
			t.Fatalf("%s %s: %v", icnsPath, e.kind, err)
		}
		samePixels(t, icnsPath+" "+e.kind, img, plate(m, e.size))
	}
}

// Platforms that draw their own corners reject transparency.
func TestOpaqueIconsAreOpaque(t *testing.T) {
	for _, p := range []string{
		"shells/ios/AIIOS/Assets.xcassets/AppIcon.appiconset/AppIcon.png",
		"internal/dashboard/static/apple-touch-icon.png",
	} {
		img := readPNG(t, filepath.Join(repoRoot, p))
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 255 {
				t.Fatalf("%s has a translucent pixel", p)
			}
		}
	}
}

// The resource object links into a real Windows executable, and the
// executable carries the icon group, every icon, and the version
// details — read back out of the linked file, not out of the object.
func TestResourcesLinkIntoAWindowsExecutable(t *testing.T) {
	icoData, err := os.ReadFile(filepath.Join(repoRoot, icoPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			dir := t.TempDir()
			obj, err := syso(arch, icoData, versionInfo{"1.2.3", "probe.exe", "Resource probe"})
			if err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string][]byte{
				"go.mod":                         []byte("module probe\n\ngo 1.21\n"),
				"main.go":                        []byte("package main\n\nfunc main() {}\n"),
				"rsrc_windows_" + arch + ".syso": obj,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			exe := filepath.Join(dir, "probe.exe")
			cmd := exec.Command("go", "build", "-o", exe, ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build with the resource object: %v\n%s", err, out)
			}
			f, err := pe.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			sec := f.Section(".rsrc")
			if sec == nil {
				t.Fatal("the executable has no .rsrc section: the object was not linked")
			}
			data, err := sec.Data()
			if err != nil {
				t.Fatal(err)
			}
			res, err := readResources(data, sec.VirtualAddress)
			if err != nil {
				t.Fatal(err)
			}
			group, ok := res[[2]uint16{rtGroupIcon, 1}]
			if !ok {
				t.Fatal("no icon group 1")
			}
			le := binary.LittleEndian
			n := int(le.Uint16(group[4:]))
			if n != len(icoSizes) {
				t.Fatalf("icon group lists %d images, want %d", n, len(icoSizes))
			}
			for i := 0; i < n; i++ {
				e := group[6+14*i:]
				want, id := int(le.Uint32(e[8:])), le.Uint16(e[12:])
				icon, ok := res[[2]uint16{rtIcon, id}]
				if !ok || len(icon) != want {
					t.Fatalf("icon %d: have %d bytes, the group says %d", id, len(icon), want)
				}
				img, err := decodeIconImage(icon)
				if err != nil || img.Bounds().Dx() != icoSizes[i] {
					t.Fatalf("icon %d does not decode to %d px: %v", id, icoSizes[i], err)
				}
			}
			ver, ok := res[[2]uint16{rtVersion, 1}]
			if !ok {
				t.Fatal("no version resource")
			}
			if !bytes.Contains(ver, []byte{0xBD, 0x04, 0xEF, 0xFE}) {
				t.Fatal("version resource lacks VS_FIXEDFILEINFO")
			}
			for _, s := range []string{"AII OS", "1.2.3", "Resource probe", "probe.exe", company} {
				if !bytes.Contains(ver, utf16le(s)) {
					t.Errorf("version resource lacks %q", s)
				}
			}
		})
	}
}

func TestVersionNumbers(t *testing.T) {
	for in, want := range map[string][4]uint32{
		"0.1.8": {0, 1, 8, 0}, "0.1.8-dev": {0, 1, 8, 0}, "1.2.3.4": {1, 2, 3, 4}, "0.0.0-dev": {}, "x": {},
	} {
		if got := versionNumbers(in); got != want {
			t.Errorf("versionNumbers(%q) = %v, want %v", in, got, want)
		}
	}
}

// readResources walks a linked .rsrc section: type, ID, first language.
func readResources(sec []byte, rva uint32) (map[[2]uint16][]byte, error) {
	le := binary.LittleEndian
	dir := func(at uint32) ([][2]uint32, error) {
		if int(at)+16 > len(sec) {
			return nil, fmt.Errorf("directory at %d is outside the section", at)
		}
		n := int(le.Uint16(sec[at+12:])) + int(le.Uint16(sec[at+14:]))
		var out [][2]uint32
		for i := 0; i < n; i++ {
			e := sec[int(at)+16+8*i:]
			out = append(out, [2]uint32{le.Uint32(e), le.Uint32(e[4:])})
		}
		return out, nil
	}
	out := map[[2]uint16][]byte{}
	types, err := dir(0)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		ids, err := dir(t[1] &^ 0x80000000)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			langs, err := dir(id[1] &^ 0x80000000)
			if err != nil || len(langs) == 0 {
				return nil, fmt.Errorf("resource %d/%d: no language (%v)", t[0], id[0], err)
			}
			entry := langs[0][1]
			addr, size := le.Uint32(sec[entry:]), le.Uint32(sec[entry+4:])
			start := addr - rva
			if addr < rva || int(start)+int(size) > len(sec) {
				return nil, fmt.Errorf("resource %d/%d points outside the section (address %#x)", t[0], id[0], addr)
			}
			out[[2]uint16{uint16(t[0]), uint16(id[0])}] = sec[start : start+size]
		}
	}
	return out, nil
}

func utf16le(s string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, utf16.Encode([]rune(s)))
	return b.Bytes()
}

func readPNG(t *testing.T, path string) *image.NRGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	out := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func samePixels(t *testing.T, name string, got, want *image.NRGBA) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("%s is %v, the master makes %v — run: go run ./packaging/icon assets", name, got.Bounds(), want.Bounds())
	}
	for i := range got.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d > 2 || d < -2 {
			// A fully transparent pixel's colour is not visible.
			p := i - i%4
			if got.Pix[p+3] == 0 && want.Pix[p+3] == 0 {
				continue
			}
			px := i / 4
			t.Fatalf("%s differs from the master at (%d,%d): %v vs %v — run: go run ./packaging/icon assets",
				name, px%got.Bounds().Dx(), px/got.Bounds().Dx(), got.Pix[p:p+4], want.Pix[p:p+4])
		}
	}
}

// parseICNS reads back what icns wrote.
func parseICNS(data []byte) (map[string][]byte, error) {
	if len(data) < 8 || string(data[:4]) != "icns" || int(binary.BigEndian.Uint32(data[4:])) != len(data) {
		return nil, fmt.Errorf("not an icns file")
	}
	out := map[string][]byte{}
	for p := 8; p < len(data); {
		if p+8 > len(data) {
			return nil, fmt.Errorf("truncated entry at %d", p)
		}
		n := int(binary.BigEndian.Uint32(data[p+4:]))
		if n < 8 || p+n > len(data) {
			return nil, fmt.Errorf("bad entry length %d at %d", n, p)
		}
		out[string(data[p:p+4])] = data[p+8 : p+n]
		p += n
	}
	return out, nil
}

// decodeIconImage turns an RT_ICON/.ico image (PNG or 32-bit DIB) back
// into pixels.
func decodeIconImage(data []byte) (*image.NRGBA, error) {
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		n, ok := img.(*image.NRGBA)
		if !ok {
			return nil, fmt.Errorf("PNG is %T, want NRGBA", img)
		}
		return n, nil
	}
	le := binary.LittleEndian
	if len(data) < 40 || le.Uint32(data) != 40 || le.Uint16(data[14:]) != 32 {
		return nil, fmt.Errorf("not a 32-bit DIB")
	}
	w, h := int(int32(le.Uint32(data[4:]))), int(int32(le.Uint32(data[8:])))/2
	if len(data) < 40+w*h*4 {
		return nil, fmt.Errorf("DIB truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	px := data[40:]
	for y := 0; y < h; y++ {
		row := px[(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			q := row[x*4:]
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = q[2], q[1], q[0], q[3]
		}
	}
	return img, nil
}
