//go:build !darwin || ios

package app

const uninstallFromApp = false

var openUninstall = func(string) error { return errUninstallNotOffered }
