# Setup payload

`packaging/windows/build-setup.sh` drops `aii.exe` and `AII OS.exe`
here, then builds `cmd/aii-setup` so they are embedded inside the
installer.

This file exists so the directory is embeddable when the payload is
absent: `//go:embed` fails on an empty directory, and the gate builds
every package on every platform whether or not a release is being cut.
The installer writes only `.exe` files, so this never reaches anyone's
machine.
