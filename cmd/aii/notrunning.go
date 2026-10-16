package main

import (
	"errors"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func isNotRunning(err error) bool { return errors.Is(err, install.ErrNotRunning) }
