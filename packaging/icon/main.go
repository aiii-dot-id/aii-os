package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

const masterPath = "packaging/icon/aii-os.png"

var icoSizes = []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256}

type pngAsset struct {
	path string
	make func(m *fimg) *image.NRGBA
}

func pngAssets() []pngAsset {
	var out []pngAsset
	for _, s := range []int{16, 24, 32, 48, 64, 128, 256, 512} {
		out = append(out, pngAsset{fmt.Sprintf("packaging/deb/icons/hicolor/%dx%d/apps/aii-os.png", s, s), func(m *fimg) *image.NRGBA { return free(m, s) }})
	}
	out = append(out, pngAsset{"shells/ios/AIIOS/Assets.xcassets/AppIcon.appiconset/AppIcon.png", func(m *fimg) *image.NRGBA { return opaque(m, 1024) }})
	for _, d := range []struct {
		name string
		px   int
	}{{"mdpi", 108}, {"hdpi", 162}, {"xhdpi", 216}, {"xxhdpi", 324}, {"xxxhdpi", 432}} {
		out = append(out, pngAsset{"shells/android/app/src/main/res/mipmap-" + d.name + "/ic_launcher_foreground.png", func(m *fimg) *image.NRGBA { return foreground(m, d.px) }})
	}
	out = append(out,
		pngAsset{"internal/dashboard/static/icon-32.png", func(m *fimg) *image.NRGBA { return free(m, 32) }},
		pngAsset{"internal/dashboard/static/icon-192.png", func(m *fimg) *image.NRGBA { return free(m, 192) }},
		pngAsset{"internal/dashboard/static/apple-touch-icon.png", func(m *fimg) *image.NRGBA { return opaque(m, 180) }},
	)
	return out
}

const (
	icoPath  = "packaging/windows/aii-os.ico"
	icnsPath = "packaging/macos/AppIcon.icns"
)

func icoFile(m *fimg) []byte {
	var images []sizedImage
	for _, s := range icoSizes {
		images = append(images, sizedImage{s, iconImage(free(m, s))})
	}
	return ico(images)
}

func icnsFile(m *fimg) []byte {
	return icns(func(size int) []byte { return pngBytes(plate(m, size)) })
}

func loadMaster(root string) (*fimg, error) {
	f, err := os.Open(filepath.Join(root, masterPath))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", masterPath, err)
	}
	n, ok := img.(*image.NRGBA)
	if !ok || n.Bounds().Dx() != n.Bounds().Dy() {
		return nil, fmt.Errorf("%s: want a square NRGBA PNG, got %T %v", masterPath, img, img.Bounds())
	}
	return fromNRGBA(n), nil
}

func writeFile(root, rel string, data []byte) error {
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func assets(root string) error {
	m, err := loadMaster(root)
	if err != nil {
		return err
	}
	for _, a := range pngAssets() {
		if err := writeFile(root, a.path, pngBytes(a.make(m))); err != nil {
			return err
		}
	}
	if err := writeFile(root, icoPath, icoFile(m)); err != nil {
		return err
	}
	return writeFile(root, icnsPath, icnsFile(m))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: icon assets | icon syso -arch A -version V -file F -description D -o OUT")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "assets":
		err = assets(".")
	case "syso":
		fs := flag.NewFlagSet("syso", flag.ExitOnError)
		arch := fs.String("arch", "amd64", "GOARCH of the executable")
		version := fs.String("version", "0.0.0", "the release version")
		file := fs.String("file", "", "the executable's file name")
		desc := fs.String("description", product, "what the executable is, as Windows shows it")
		out := fs.String("o", "", "where to write the object")
		_ = fs.Parse(os.Args[2:])
		if *file == "" || *out == "" {
			err = errors.New("syso needs -file and -o")
			break
		}
		var icoData, obj []byte
		if icoData, err = os.ReadFile(icoPath); err != nil {
			break
		}
		if obj, err = syso(*arch, icoData, versionInfo{*version, *file, *desc}); err != nil {
			break
		}
		err = os.WriteFile(*out, obj, 0o644)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "icon:", err)
		os.Exit(1)
	}
}
