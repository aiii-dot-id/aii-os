//go:build android || ios

// platform_mobile.go — the mobile tool set: NO shell, NO write/edit by
// policy. Both Android and iOS forbid spawning subprocesses from app
// sandboxes; the write/edit tools return honest refusals (the runtime
// itself may still persist its own state — this gates the IDENTITY's
// tool reach, not the substrate).
package tools

const platformNoWrite = true
