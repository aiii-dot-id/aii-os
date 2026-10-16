//go:build !((linux && !android) || (darwin && !ios) || windows)

package uninstall

const OSUninstall = "AII OS is removed with the operating system's own uninstall: it removes the app and everything in its container, the identity among it. Keep the identity's escrow before you remove the app."
