// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package hostcap

// .
type Capability int

const (
	// .
	Subprocess Capability = iota
	// .
	Shell
	// .
	// .
	NativeChild
	// .
	// .
	SelfReplace
)

// .
// .
// .
type Status struct {
	Available bool
	Reason    string
}

// .
// .
// .
func Can(c Capability) Status { return can(c) }
