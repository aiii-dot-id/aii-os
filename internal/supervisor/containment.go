package supervisor

import (
	"fmt"
	"os"
	"os/exec"
)

type Containment struct {
	Description          string
	AppContainerSID      string
	NetworkDenied        bool
	FilesystemRestricted bool

	BuiltMark string

	Filter []byte
}

const filterLimit = 4096

func (c Containment) Attach(cmd *exec.Cmd) (release func(), err error) {
	if len(c.Filter) == 0 {
		return func() {}, nil
	}
	if len(c.Filter) > filterLimit {
		return nil, fmt.Errorf("containment filter of %d bytes exceeds the %d a pipe takes unread", len(c.Filter), filterLimit)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("containment filter pipe: %w", err)
	}
	_, werr := w.Write(c.Filter)
	if cerr := w.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		r.Close()
		return nil, fmt.Errorf("containment filter pipe: %w", werr)
	}
	cmd.ExtraFiles = append(append([]*os.File{}, cmd.ExtraFiles...), r)
	return func() { r.Close() }, nil
}

const ContainmentBuildAttempts = 8

func (c Containment) String() string { return c.Description }
func (c Containment) Isolated() bool { return c.NetworkDenied && c.FilesystemRestricted }

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

type ContainmentCleanupError struct {
	PluginID string
	Err      error
}

func (e *ContainmentCleanupError) Error() string {
	return fmt.Sprintf("supervisor: %s: containment cleanup: %v", e.PluginID, e.Err)
}
func (e *ContainmentCleanupError) Unwrap() error { return e.Err }
