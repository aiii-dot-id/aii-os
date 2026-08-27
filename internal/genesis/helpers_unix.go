package genesis

import "os"

func readFileImpl(path string) ([]byte, error) {
	return os.ReadFile(path)
}
