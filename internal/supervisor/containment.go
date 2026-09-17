package supervisor

import "fmt"

// .
// .
// .
type Containment struct {
	Description          string
	AppContainerSID      string
	NetworkDenied        bool
	FilesystemRestricted bool
}

func (c Containment) String() string { return c.Description }
func (c Containment) Isolated() bool { return c.NetworkDenied && c.FilesystemRestricted }

// .
// .
func (s *Supervisor) Containment() Containment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child == nil {
		return Containment{}
	}
	select {
	case <-s.child.exited:
		return Containment{}
	default:
		return s.child.isolation
	}
}

// .
// .
type ContainmentCleanupError struct {
	PluginID string
	Err      error
}

func (e *ContainmentCleanupError) Error() string {
	return fmt.Sprintf("supervisor: %s: containment cleanup: %v", e.PluginID, e.Err)
}
func (e *ContainmentCleanupError) Unwrap() error { return e.Err }
