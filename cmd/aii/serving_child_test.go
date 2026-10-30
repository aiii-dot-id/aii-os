package main

import (
	"fmt"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

// slotChildEnv marks this test binary, run again as "aii -dir <slot>" by a
// test's fake service manager, as that slot's process. Its value says what
// the process does: "serve" builds the slot's dashboard as an identity does
// (its serving key written in the slot, then the dashboard on the slot's
// configured port, exiting 1 when the port cannot be bound, as an identity
// does), and "idle" runs and never serves.
const slotChildEnv = "AII_TEST_SLOT_CHILD"

func runSlotChild(mode string) int {
	dir := ""
	for i, a := range os.Args {
		if a == "-dir" && i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "slot child: no -dir")
		return 2
	}
	if mode == "serve" {
		key, err := install.WriteServingKey(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "slot child:", err)
			return 1
		}
		d := dashboard.New("127.0.0.1", install.ConfiguredPort(dir, 0), &dashboard.WSHandler{})
		d.SetServingKey(key)
		if _, err := d.Start(""); err != nil {
			fmt.Fprintln(os.Stderr, "slot child:", err)
			return 1
		}
	}
	for {
		time.Sleep(time.Hour) // until the test ends it
	}
}
