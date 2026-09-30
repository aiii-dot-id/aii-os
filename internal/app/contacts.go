package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxContactName = 100

	maxContactAddress = 320
)

type ContactsRefusal struct {
	Line        int
	Field       string
	Requirement string
}

func (e *ContactsRefusal) Error() string {
	if e.Line == 0 {
		return "contacts: " + e.Requirement
	}
	return fmt.Sprintf("contacts, line %d: %s", e.Line, e.Requirement)
}

func contactsFromChange(v interface{}) ([]Contact, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, &ContactsRefusal{Field: "line", Requirement: "the list cannot be read: " + err.Error()}
	}
	var lines []json.RawMessage
	if err := json.Unmarshal(raw, &lines); err != nil || lines == nil {
		return nil, &ContactsRefusal{Field: "line", Requirement: "a list of contact lines is required (an empty list removes every contact)"}
	}
	out := make([]Contact, 0, len(lines))
	for i, line := range lines {
		n := i + 1
		refuse := func(field, requirement string) error {
			return &ContactsRefusal{Line: n, Field: field, Requirement: requirement}
		}
		var c Contact
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil || bytes.Equal(bytes.TrimSpace(line), []byte("null")) {
			return nil, refuse("line", "each line is an object with name, channel, address, wake and operator")
		}
		c.Name, c.Channel, c.Address = strings.TrimSpace(c.Name), strings.TrimSpace(c.Channel), strings.TrimSpace(c.Address)
		switch {
		case c.Name == "":
			return nil, refuse("name", "a name is required — the name the identity sends to")
		case strings.EqualFold(c.Name, "operator"):
			return nil, refuse("name", "the name operator is taken: sending to operator reaches this dashboard; use your own name and mark the line as you")
		case !oneLine(c.Name, maxContactName):
			return nil, refuse("name", fmt.Sprintf("a name is one line of at most %d characters", maxContactName))
		case !channelNameRe.MatchString(c.Channel):
			return nil, refuse("channel", "a channel is the name its adapter gives (such as telegram): lowercase letters, digits, dot, underscore or hyphen, at most 32")
		case c.Address == "":
			return nil, refuse("address", "an address is required")
		case !oneLine(c.Address, maxContactAddress):
			return nil, refuse("address", fmt.Sprintf("an address is one line of at most %d characters", maxContactAddress))
		}
		for j, prior := range out {
			if prior.reaches(c.Channel, c.Address) {
				return nil, refuse("address", fmt.Sprintf("line %d has the same channel and address: an address is one person's, listed once", j+1))
			}
			if c.Operator && prior.Operator && !strings.EqualFold(prior.Name, c.Name) {
				return nil, refuse("operator", fmt.Sprintf("line %d marks %s as you, the operator, and this line marks %s: the operator is one person", j+1, prior.Name, c.Name))
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func oneLine(s string, most int) bool {
	return utf8.RuneCountInString(s) <= most && !strings.ContainsFunc(s, unicode.IsControl)
}
