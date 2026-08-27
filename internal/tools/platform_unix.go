//go:build !android && !ios && !windows

// platform_unix.go — the full desktop tool set for Unix-likes (Linux,
// macOS; Windows via its own file). The shell organ speaks bash here
// (shell_unix.go owns the host facts); the registry registers it.
package tools

// platformNoWrite gates write/edit (mobile only — store rules forbid
// shell-equivalent reach there; desktops keep the full set).
const platformNoWrite = false

// (shell availability moved to hostcap.Shell, 2026-08-26 — one truth
// with a reason instead of three constants.)
// True on unix desktops; false on mobile (both OSes forbid process
// spawning from apps — the tool simply does not exist there).
