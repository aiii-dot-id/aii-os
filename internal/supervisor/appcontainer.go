package supervisor

import "fmt"

type AppContainer struct {
	Profile      string
	GrantRead    []string
	Capabilities []string
}

type ProfileLeftError struct {
	Name   string
	Reason string
	Err    error
}

func (e *ProfileLeftError) Error() string {
	return fmt.Sprintf("container profile %s could not be removed: %v", e.Name, e.Err)
}
func (e *ProfileLeftError) Unwrap() error { return e.Err }
