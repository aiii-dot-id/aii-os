package machine

import (
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func StoreDir(dir string) (string, error) {
	st, err := loadState(filepath.Join(dir, stateFileName))
	if err != nil {
		return "", err
	}
	if st.Store != "" {
		return st.Store, nil
	}
	return defaultStoreDir(dir), nil
}

func defaultStoreDir(dir string) string {
	dir = filepath.Clean(dir)
	if account, err := install.MachineDir(); err == nil && filepath.Clean(account) == dir {
		return filepath.Join(filepath.Dir(dir), "plugins")
	}
	return filepath.Join(dir, "store")
}

func DrainingStores(dir string) ([]string, error) {
	st, err := loadState(filepath.Join(dir, stateFileName))
	if err != nil {
		return nil, err
	}
	return append([]string(nil), st.Draining...), nil
}

func RecordedSlots(dir string) ([]string, error) {
	st, err := loadState(filepath.Join(dir, stateFileName))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(st.Identities))
	for _, r := range st.Identities {
		out = append(out, r.Slot)
	}
	return out, nil
}
