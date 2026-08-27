// Package configdir owns the repository's in-tree CONFIG ASSETS — the
// operator-editable data files that ship with the source tree under
// config/ (not to be confused with a deployment's runtime config/
// directory). Embedding lives here because go:embed cannot reach parent
// directories from cmd/aii.
package configdir

import (
	_ "embed"
)

// Config is the embedded runtime config.json scaffold.
//
//go:embed config.json
var Config []byte

// Providers is the embedded provider registry (config/providers.json).
// The registry is operator-editable DATA: edit the file, rebuild — or
// place providers.json beside the runtime's config.json to override it.
//
//go:embed providers.json
var Providers []byte
