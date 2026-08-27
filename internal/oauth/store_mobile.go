//go:build android || ios

// store_mobile.go — adoption does not exist on mobile, and cannot.
//
// The credentials this package adopts belong to DESKTOP CLIs (Claude
// Code, codex) that do not run on a phone. Even if one did, both
// platforms confine an app to its own container: one app cannot read
// another's files by design, not by configuration, and no permission
// grant changes that. So there is nothing to adopt here — this is a
// structural absence, not a missing feature, and the honest thing is to
// say so rather than fail later on a path that could never exist.
//
// OAuth itself is NOT ruled out on mobile. A grant flow is the native
// mobile path — ASWebAuthenticationSession on iOS, Custom Tabs on
// Android, PKCE mandatory — and it would own its own token rather than
// borrow one. That is unbuilt. Until it exists, mobile authenticates
// with an API key, which works on every platform today.
package oauth

import "errors"

const platformAdopts = false

// ErrNotOnMobile is returned instead of a path error, so the operator
// reads a reason instead of a filename that was never going to be there.
var ErrNotOnMobile = errors.New(
	"adopted credentials are a desktop feature: they belong to CLIs that do not run on mobile, " +
		"and app sandboxing prevents reading another app's files — use an API key for this provider")

func credentialHome() (string, error) { return "", ErrNotOnMobile }
