# Packaging

Three scripts, one per desktop platform. Each builds from a clean
checkout and writes to `dist/`; the version comes from `VERSION` at the
repository root.

| Platform | Script | Produces |
|---|---|---|
| Ubuntu | `deb/build-deb.sh` | `dist/aii-os_<version>_<arch>.deb` |
| Windows | `windows/build-setup.sh` | `dist/aii-setup-<version>-<arch>.exe` |
| macOS | `macos/build-dmg.sh` | `dist/AII-OS-<version>.dmg` (and the `.app`) |

And one more, `archives.sh`, for the update archives: what the in-app
updater installs from, and what a person unpacks where there is no
installer (`dist/aii-os_<version>_<platform>_<arch>.tar.gz`, `.zip` on
Windows). `build-all.sh` runs all four.

**These scripts are not run by CI, and CI publishes nothing.**
`release.yml` is manual (`workflow_dispatch`, `contents: read`) and only
checks that every target still compiles. A pushed tag builds and
publishes nothing at all. The installers are built by hand and signing
requires the publisher's platform-release key. Release
assembly, verification and DRAFT staging are `release.sh`'s act.

A candidate is a closed set: `attach` (and `stage`, which runs it) refuses
unless CANDIDATE.txt lists exactly the targets `aii-release assets` names,
every target and installer is present, nothing else is in the candidate
directory, and the retained evidence, named by `EVIDENCE=` and kept
outside the candidate, verifies for the candidate's source. `stage`
uploads, and sums, only that set. It refuses a draft that already holds an
asset the candidate does not carry, and deletes nothing.

## Update archives

    sh packaging/archives.sh linux|windows|macos

**What a platform ships is written once**, in `internal/updates/contract.go`:
`ShippedPrograms` (the executables a release installs in one directory)
and `ArchiveFiles` (every file that platform's archive holds). Three
things read it: this script, which asks `aii-release programs` and
`aii-release archive-files` and holds no list of its own; the updater,
which installs every program its archive carries, and on Windows the two
licence texts, beside `aii`; and `aii-release complete`, which opens the
built archives and refuses one that does not hold every file, whose
programs are not built for its target, or from which an update would
install anything other than what the platform's installer puts on disk.
`archives.sh` ends with that check, and `release.sh prepare` runs it
again on the candidate before any payload is rendered for signing.

| Platform | The archive holds | Its programs come from |
|---|---|---|
| Linux | `aii`, `aii-app`, `aii-vulkan-probe`, the licence texts, and under `share/` the menu entry, its icons and the manual, laid out as under `/usr/share` in the package | built by `archives.sh`, for each architecture |
| Windows | `aii.exe`, `AII OS.exe`, the licence texts: what the installer writes | `dist/windows-<arch>/`, staged by `build-setup.sh` from its installer's payload: built with their resources and signed once |
| macOS | `aii`, `aii-app`, the licence texts | `dist/macos-arm64.signed/`, staged by `build-dmg.sh`: the bundle's own signed `aii`, and the launcher signed to run outside a bundle, both notarized |

So a Windows or macOS archive is made after that platform's installer,
never before it, and holds the same bytes a fresh install does. The
staging directory is used once: `archives.sh` removes it when its archive
has passed the check. A release's
check also asks that the Windows programs carry their icon and version
resources and an Authenticode signature, and that the macOS ones carry a
Developer ID signature with `aii` the very file the app archive holds.
`UNSIGNED=1` (and `-unsigned <platform>` of the tool) says a build is not
a release and its signatures are not asked for; what the archive holds
always is.


## Icons

One master, `icon/aii-os.png` (the AII OS artwork, cropped square
around the disc, its interior opaque), makes every platform's icon:

    go run ./packaging/icon assets

| Platform | File | Form |
|---|---|---|
| Windows | `windows/aii-os.ico` (16–256 px) | the disc, free-standing |
| macOS | `macos/AppIcon.icns` (16–1024 px) | on the night plate: macOS puts any icon that is not one on a grey plate of its own |
| Ubuntu | `deb/icons/hicolor/*/apps/aii-os.png` | the disc, free-standing |
| iOS | `../shells/ios/AIIOS/Assets.xcassets/AppIcon.appiconset` | opaque: iOS draws the corners |
| Android | `../shells/android/app/src/main/res/mipmap-*` | adaptive: the disc in the 66dp safe zone over the night colour |
| Dashboard | `../internal/dashboard/static/icon-*.png`, `apple-touch-icon.png` | the tab and the home screen |

The generated files are checked in, so no build needs the generator,
and `go test ./packaging/icon` fails when any of them no longer matches
the master. It is pure Go and standard library: nothing has to be
installed to reproduce them.

## Toolchain

Go 1.27+, forced by stdlib `crypto/mldsa`. Distribution Go is often
older, and a too-old toolchain is the most common way these builds fail.
Every script honours `GO=`, so point at a 1.27 toolchain rather than
rearranging your PATH:

    GO=/path/to/go1.27/bin/go sh packaging/deb/build-deb.sh

An older `go` in this module does not simply complain about its
version. It reads the `go 1.27` directive and tries to fetch a
matching toolchain, so on a host that cannot reach the toolchain
mirror the symptom is instead

    go: downloading go1.27 (linux/amd64)
    go: download go1.27 for linux/amd64: toolchain not available

which says nothing about the installed version. `GO=` sidesteps it:
the scripts never invoke a bare `go`.

The Linux and Windows builds are pure Go with CGO off and cross-compile
from any host the gate runs on. Only macOS needs macOS, and only for
Apple's own tools.

## Ubuntu

    sh packaging/deb/build-deb.sh

Needs `dpkg-deb`. `ARCH=arm64` builds the other architecture, and
`build-all.sh` builds both: each is an asset of the release contract
(`internal/updates.PackageTargets`), signed and verified like an archive,
and `aii-release complete -packages` reads each back for its
architecture, every program a Linux release installs, and the menu
entry, icons and manual.

The package installs the binary, a systemd **user** unit template, the
man page and the copyright file, and an application-menu entry: `AII OS`
runs `aii-app`, which makes a slot on first run, has systemd start the
identity when it is stopped, and opens the dashboard.

`aii` and `aii-app` are static, and link with `-d` so that a dependency
which would make them dynamic fails the build instead
(`test/static_runtime_test.go` holds the same line before every commit).
Beside them is `aii-vulkan-probe`, the bounded process `aii` runs to read a
GPU's Vulkan memory budget. It loads the system's `libvulkan.so.1`, which
only the dynamic linker can do, so it is the one dynamic executable and the
reason the package depends on `libc6`. Missing, or on a host without a
Vulkan driver, it leaves Vulkan unknown. The Linux updater archives carry
it too, at the archive root beside `aii` and `aii-app`, and the in-app
updater installs both from there: out of the same signature-verified
archive, beside the running `aii`, before it swaps `aii` — so an updated
`aii` never runs without them. An archive in which any file an update
installs is anywhere else, or is not one regular, non-empty file, is
refused whole ("Update archives", above).

The package creates no system user and requests
no capabilities — capability directives are system-unit-only, and a user
unit carrying them is unstartable. `postrm` deliberately removes no
identity data: an identity is a signed append-only ledger and its key,
there is no second copy, and `apt remove` destroying `~/.aii` would end
identities permanently and silently. The package never owns that
directory.

## Windows

    sh packaging/windows/build-setup.sh

Builds three binaries: `aii.exe` (the console CLI), `AII OS.exe`
(`-H windowsgui`, which is what autostart points at — a console window
flashing at every sign-in is what makes software feel unfinished), and
`aii-setup.exe` carrying both as payload. The payload copies are deleted
afterwards, so a stale binary cannot be embedded in the next build; what
the installer writes is first staged in `dist/windows-<arch>/`, and the
update archive is made from those files ("Update archives", above), so an
update installs the launcher too and the same signed `aii.exe`.

All three carry the icon and their name, publisher and version as
Windows resources — what Explorer, the taskbar, the Start Menu shortcut
and Startup apps show. The script makes an `rsrc_windows_<arch>.syso`
beside each main package (`go run ./packaging/icon syso`) for that
version and architecture, and removes them after.

The installer is per-user and needs no elevation. It is unsigned by
default: SmartScreen shows *Windows protected your PC* with a Run anyway
path, which is honest for a beta and costs nothing. `SIGNTOOL=` adds
Authenticode where a certificate exists.

## macOS

`codesign`, `notarytool`, `stapler` and `hdiutil` are Apple tools, so
this half must run on macOS. The Go binaries are not so constrained.
`BIN_DIR` accepts binaries cross-compiled elsewhere, which is the route
to take when the Mac has no checkout or carries an older Go:

    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
      -ldflags "-s -w" -o bin/aii ./cmd/aii
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
      -ldflags "-s -w" -o bin/aii-app ./cmd/aii-app

then, on the Mac, from a tree holding `VERSION` and `packaging/macos/`:

    BIN_DIR=./bin sh packaging/macos/build-dmg.sh

Without `BIN_DIR` the script builds the binaries itself and needs Go
1.27 on the Mac.

The signing identity is given by certificate **hash**, not by name
(`IDENTITY=`). Two Developer ID certificates bearing the same name
resolve fine, but a second, different one would make a name match
ambiguous and `codesign` would choose for us.

Notarization uses a stored keychain profile (`PROFILE=`, default
`aii-notary`), created once:

    xcrun notarytool store-credentials aii-notary \
      --key <AuthKey.p8> --key-id <key-id> --issuer <issuer-uuid>

`NOTARIZE=0` produces a development artifact without notarization; do not
treat that artifact as a qualified release. A full run checks the resulting
image and its stapled ticket:

    spctl -a -vv -t open --context context:primary-signature dist/AII-OS-<version>.dmg
    xcrun stapler validate dist/AII-OS-<version>.dmg

The app and the disk image are notarized and stapled separately.
The ticket inside the `.app` does not travel with the image, and the
image is what a person downloads and what Gatekeeper checks first.

## Environment

| | `VERSION` | `OUT` | `GO` | `ARCH` | Additional |
|---|---|---|---|---|---|
| deb | yes | yes | yes | amd64, arm64 | |
| windows | yes | yes | yes | amd64, arm64 | `SIGNTOOL` |
| macos | yes | yes | yes | arm64 only | `BIN_DIR` `NOTARIZE` `IDENTITY` `PROFILE` |

`OUT` defaults to `dist/`, which is gitignored. `VERSION` defaults to the
`VERSION` file and may only repeat it: every binary carries that file
compiled in, so a package named for another value would hold binaries
that say otherwise, and each script refuses one before it builds.

The test suite runs on the host architecture only. Every other target
here is cross-compiled and build-verified, and Windows arm64 sits
outside the gate matrix altogether — an arm64 package is a package
that compiles, not one that has been exercised.
