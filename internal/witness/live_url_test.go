package witness

import "os"

// liveWitnessURL is the witness daemon a live test may reach, named by the
// environment; empty skips those tests. Only tests read it.
func liveWitnessURL() string { return os.Getenv("AII_TEST_WITNESSD_URL") }
