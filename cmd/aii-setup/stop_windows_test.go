//go:build windows

package main

import "testing"

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestOnlyProcessesInsideTheInstallDirectoryAreOurs(t *testing.T) {
	const install = `C:\Users\j\AppData\Local\AII OS`

	for _, c := range []struct {
		name string
		exe  string
		want bool
	}{
		{"our binary", `C:\Users\j\AppData\Local\AII OS\aii.exe`, true},
		{"our launcher", `C:\Users\j\AppData\Local\AII OS\AII OS.exe`, true},
		{"nested under us", `C:\Users\j\AppData\Local\AII OS\bin\aii.exe`, true},
		{"case differs, same place", `c:\users\j\appdata\local\aii os\aii.exe`, true},

		// .
		{"someone else's program with our name", `C:\Tools\aii.exe`, false},
		{"another install of ours", `D:\Portable\AII OS\aii.exe`, false},
		// .
		{"a sibling whose name starts the same", `C:\Users\j\AppData\Local\AII OS Extras\aii.exe`, false},
		{"a different user's install", `C:\Users\k\AppData\Local\AII OS\aii.exe`, false},
		{"empty path", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ownsPath(c.exe, install); got != c.want {
				t.Fatalf("ownsPath(%q) = %v, want %v — the installer must stop only what it is replacing", c.exe, got, c.want)
			}
		})
	}

	// .
	if ownsPath(`C:\anything\aii.exe`, "") {
		t.Fatal("an empty install directory must claim nothing")
	}
}
