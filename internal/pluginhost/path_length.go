package pluginhost

import (
	"fmt"
	"unicode/utf16"
)

type PathLengthError struct {
	PluginID string

	Path string

	Length, Limit int

	Store string
}

func (e *PathLengthError) Error() string {
	head := fmt.Sprintf("plugin %s: a file it needs, at or under %s, has a path of at least %d characters, and Windows opens none longer than %d for the programs a plugin runs; nothing was downloaded",
		e.PluginID, e.Path, e.Length, e.Limit)

	have := pathChars(e.Store)
	fit := have - (e.Length - e.Limit)
	if fit < 3 {
		return fmt.Sprintf("%s: the package's own names are too long for any plugin store", head)
	}
	return fmt.Sprintf("%s: the account's plugin store %s is %d characters, and the plugin fits in a store of %d characters or fewer — name a shorter one with: aii machine store <dir>", head, e.Store, have, fit)
}

func pathChars(path string) int {
	n := 0
	for _, r := range path {
		n += utf16.RuneLen(r)
	}
	return n
}

func pathLengthRefusal(mat Material, opts *Options) error {
	limit := opts.PathLimit
	if limit <= 0 {
		limit = hostPathLimit
	}
	if limit <= 0 || mat.PluginID == "" {
		return nil
	}
	var worst *PathLengthError
	need := func(path string, length int) {
		if length > limit && (worst == nil || length > worst.Length) {
			worst = &PathLengthError{PluginID: mat.PluginID, Path: path, Length: length, Limit: limit, Store: mat.store}
		}
	}
	for _, m := range mat.Models {
		path := modelFile(mat.ModelsDir, m)
		need(path, pathChars(path))
	}
	if mat.Runtime != nil {
		depth := 1
		if mat.Runtime.Depth != nil {
			depth = *mat.Runtime.Depth
		}
		root := mat.runtimeRoot()
		need(root, pathChars(root)+2*depth)
	}
	if worst == nil {
		return nil
	}
	return worst
}
