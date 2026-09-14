package app

import (
	"log"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// .
// .
// .
// .
const exitRestart = 3

// .
// .
// .
// .
// .
// .
func relaunch() {
	if sr := hostcap.Can(hostcap.SelfReplace); !sr.Available {
		log.Printf("restart: this host cannot replace its own process (%s); leaving with exit %d for its shell to start the identity again", sr.Reason, exitRestart)
		os.Exit(exitRestart)
	}
	log.Printf("restart: handing over to the binary at this executable's path")
	if err := reexecSelf(); err != nil {
		log.Printf("restart: hand-over failed (%v); leaving with exit %d for the service manager", err, exitRestart)
		os.Exit(exitRestart)
	}
}
