package install

import (
	"path/filepath"
)

const MachineDirName = "machine"

func MachineDir() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, MachineDirName), nil
}

type StartupSlot struct {
	Slot string
	Dir  string
}

func startupApprovedDisabled(b []byte) bool {
	return len(b) > 0 && b[0]&1 == 1
}
