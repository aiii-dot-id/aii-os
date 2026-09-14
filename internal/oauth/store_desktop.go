//go:build !android && !ios

// .
// .
// .
// .
// .
// .
package oauth

import "os"

// .
// .
const platformAdopts = true

func credentialHome() (string, error) { return os.UserHomeDir() }
