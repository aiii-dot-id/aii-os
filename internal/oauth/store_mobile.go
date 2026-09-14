//go:build android || ios

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package oauth

import "errors"

const platformAdopts = false

// .
// .
var ErrNotOnMobile = errors.New(
	"adopted credentials are a desktop feature: they belong to CLIs that do not run on mobile, " +
		"and app sandboxing prevents reading another app's files — use an API key for this provider")

func credentialHome() (string, error) { return "", ErrNotOnMobile }
