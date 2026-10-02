package filelock

import "errors"

var ErrHeld = errors.New("the lock is held elsewhere")
