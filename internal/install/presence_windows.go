//go:build windows

package install

func SlotRunning(slotDir string) (bool, error) { return StopChannelPresent(slotDir) }
