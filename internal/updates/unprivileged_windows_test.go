package updates

import "testing"

// On Windows a test never finds itself a user that mode bits do not bind:
// see unprivileged_test.go for the systems where it can.
func ranUnprivileged(*testing.T) bool { return false }
