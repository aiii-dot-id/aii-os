package genesis

import "encoding/json"

// .
func readFile(path string) ([]byte, error) {
	return readFileImpl(path)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
