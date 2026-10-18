//go:build !darwin

package firewall

const volfs = ""

func byIdentity(string) bool { return false }
