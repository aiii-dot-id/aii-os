//go:build darwin || ios

package pluginhost

var DefaultNativeView = []string{
	"/System", "/usr", "/bin", "/sbin", "/private/var/select/sh",
	"/private/etc/localtime", "/private/var/db/timezone",
	"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom",
}
