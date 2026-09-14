// .
// .
// .
// .
// .
package configdir

import (
	_ "embed"
)

// .

//go:embed config.json
var Config []byte

// .
// .
// .

//go:embed providers.json
var Providers []byte
