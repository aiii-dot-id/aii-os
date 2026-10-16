//go:build linux || darwin || android || ios

package ledger

import "os"

func openLedgerForRewrap(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}

func createLedgerForRewrap(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
}
