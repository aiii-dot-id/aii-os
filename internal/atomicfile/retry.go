package atomicfile

import "time"

const renameRetryBudget = 2 * time.Second

func retryContended(op func() error, contended func(error) bool) error {
	deadline := time.Now().Add(renameRetryBudget)
	delay := 500 * time.Microsecond
	for {
		err := op()
		if err == nil || !contended(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 20*time.Millisecond {
			delay *= 2
		}
	}
}
