//go:build !((linux && !android) || (darwin && !ios) || windows)

package install

func MachineRegistration() (string, bool, error) { return "", false, nil }

func MachineStartCommand() string { return "aii machine" }

func ReleaseVersion(exe string) string { return "" }

func MachineRunDisabled() (bool, error) { return false, nil }

func StartFiles() (files, stems []string, err error) { return nil, nil, nil }
