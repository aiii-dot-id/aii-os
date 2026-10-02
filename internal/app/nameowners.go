package app

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type nameCollision struct {
	Name, Owner, Other string
}

func (e *nameCollision) Error() string {
	if e.Owner == e.Other {
		return fmt.Sprintf("the callable name %q is declared twice by %s; this build cannot start until one is renamed", e.Name, e.Owner)
	}
	return fmt.Sprintf("the callable name %q has two owners, %s and %s; this build cannot start until one is renamed", e.Name, e.Owner, e.Other)
}

type ownedNames struct {
	owner string
	names []string
}

func staticNames(reg *tools.Registry) []ownedNames {
	verbs := make([]string, 0, len(identity.Verbs()))
	for _, v := range identity.Verbs() {
		verbs = append(verbs, v.Name)
	}
	return []ownedNames{
		{"an offered verb (identity.Verbs)", verbs},
		{"an absorbed operation (identity absorbedVerbs)", identity.AbsorbedNames()},
		{"a registry builtin (tools registerDefaults)", reg.Builtins()},
		{"a category door (tools categories)", tools.CategoryNames()},
	}
}

func oneOwnerEach(sets []ownedNames) error {
	held := map[string]string{}
	for _, s := range sets {
		for _, n := range s.names {
			if prev, ok := held[n]; ok {
				return &nameCollision{Name: n, Owner: prev, Other: s.owner}
			}
			held[n] = s.owner
		}
	}
	return nil
}

func checkNameOwners(reg *tools.Registry) error {
	return oneOwnerEach(staticNames(reg))
}
