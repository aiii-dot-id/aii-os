//go:build darwin

package oauth

// keychainNote names the one macOS possibility a missing file does not
// distinguish: some builds of these tools keep credentials in the login
// Keychain rather than a file, and this adoption path reads files only.
func keychainNote() string {
	return " (if that tool stores its credentials in the macOS Keychain rather than a file, this path cannot read them)"
}
