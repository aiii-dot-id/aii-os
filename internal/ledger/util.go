package ledger

import "time"

// nowUTC returns the current time as RFC 3339 UTC string.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
