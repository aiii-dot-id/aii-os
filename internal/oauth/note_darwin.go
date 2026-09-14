//go:build darwin

package oauth

// .
// .
// .
func keychainNote() string {
	return " (if that tool stores its credentials in the macOS Keychain rather than a file, this path cannot read them)"
}
