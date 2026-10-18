//go:build linux

package pluginhost

// SyscallFilterForTest is the native child's system-call filter, for the
// external wall tests.
var SyscallFilterForTest = syscallFilter
