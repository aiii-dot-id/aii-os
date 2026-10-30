package updates

import "path/filepath"

func resolveLinks(path string) (string, error) {
	resolved, err := evalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if final, ferr := finalPath(path); ferr == nil {
		return final, nil
	}
	return "", err
}

var (
	evalSymlinks = filepath.EvalSymlinks
	finalPath    = finalPathOf
)
