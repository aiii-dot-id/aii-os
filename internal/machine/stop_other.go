//go:build !windows

package machine

import (
	"context"
	"sync"
)

const RelaunchesService = false

func watchStop(context.Context, string, *sync.WaitGroup) <-chan struct{} { return nil }

func AccountRelaunch() func() error { return nil }
