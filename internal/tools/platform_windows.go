//go:build windows

// platform_windows.go — the Windows tool set: the pure-Go six
// (read, write, edit, grep, ls, web_fetch) plus the shell organ,
// which is Windows PowerShell here (R79, 2026-08-27; hostcap.Shell
// says so, shell_windows.go owns the host facts). History: the old
// build shipped the Unix bash tool on Windows and it failed at
// runtime on every call, so from 2026-08-18 the organ was honestly
// absent until a real need picked the Windows shape — Beta 1 journey
// 5 and Aster's signed gap report (2026-08-27) were that need.
package tools

const platformNoWrite = false
