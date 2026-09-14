package dashboard

import (
	"errors"
	"time"
)

// .
// .
// .
// .
// .
// .
var ErrBusyInternal = errors.New("an internal pass holds the identity's turn")

// .
// .
// .
const queuedTurnMaxWait = 15 * time.Minute
