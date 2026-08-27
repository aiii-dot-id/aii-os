package dashboard

import (
	"errors"
	"time"
)

// ErrBusyInternal reports that a metabolism/facility pass holds the
// turn gate. A facility never reads the steer queue, so steering into
// it would swallow the words (live incident 2026-08-26: an operator
// message steered into a self_model pass waited six unread hours). The
// chat path parks the message and opens its own turn when the pass
// ends; the app's steer() is what returns this.
var ErrBusyInternal = errors.New("an internal pass holds the identity's turn")

// queuedTurnMaxWait bounds how long a parked message waits for the
// gate. Facility passes run seconds to a few minutes; fifteen is
// already pathology, and the operator is told the message did NOT run.
const queuedTurnMaxWait = 15 * time.Minute
