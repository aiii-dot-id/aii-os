package machine

import "fmt"

type MissingError struct {
	Needer   string
	Message  string
	Service  Build
	Identity Build
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("the machine service (%s) does not speak %s, which %s needs; this identity is %s",
		e.Service, e.Message, e.Needer, e.Identity)
}

func covers(s Speaks, needer string, needs []string, service, identity Build) *MissingError {
	for _, m := range needs {
		if !s.has(m) {
			return &MissingError{Needer: needer, Message: m, Service: service, Identity: identity}
		}
	}
	return nil
}
