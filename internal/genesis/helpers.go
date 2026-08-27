package genesis

import "encoding/json"

// Helper to avoid importing json in test directly (keeps test cleaner)
func readFile(path string) ([]byte, error) {
	return readFileImpl(path)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
