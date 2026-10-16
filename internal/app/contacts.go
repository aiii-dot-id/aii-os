package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
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

func contactsFromChange(v any) ([]Contact, error) {
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
		var c Contact
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil || bytes.Equal(bytes.TrimSpace(line), []byte("null")) {
			return nil, &ContactsRefusal{Line: i + 1, Field: "line", Requirement: "each line is an object with name, channel, address, wake and operator"}
		}
		c = c.asKept()
		if refusal := contactRule(c, i+1, out); refusal != nil {
			return nil, refusal
		}
		out = append(out, c)
	}
	return out, nil
}

func (c Contact) asKept() Contact {
	c.Name, c.Channel, c.Address = strings.TrimSpace(c.Name), strings.TrimSpace(c.Channel), strings.TrimSpace(c.Address)
	return c
}

func contactRule(c Contact, n int, above []Contact) *ContactsRefusal {
	refuse := func(field, requirement string) *ContactsRefusal {
		return &ContactsRefusal{Line: n, Field: field, Requirement: requirement}
	}
	switch {
	case c.Name == "":
		return refuse("name", "a name is required — the name the identity sends to")
	case strings.EqualFold(c.Name, "operator"):
		return refuse("name", "the name operator is taken: sending to operator reaches this dashboard; use your own name and mark the line as you")
	case !oneLine(c.Name, maxContactName):
		return refuse("name", fmt.Sprintf("a name is one line of at most %d characters", maxContactName))
	case !channelNameRe.MatchString(c.Channel):
		return refuse("channel", "a channel is the name its adapter gives (such as telegram): lowercase letters, digits, dot, underscore or hyphen, at most 32")
	case c.Address == "":
		return refuse("address", "an address is required")
	case !oneLine(c.Address, maxContactAddress):
		return refuse("address", fmt.Sprintf("an address is one line of at most %d characters", maxContactAddress))
	}
	for j, prior := range above {
		if prior.reaches(c.Channel, c.Address) {
			return refuse("address", fmt.Sprintf("line %d has the same channel and address: an address is one person's, listed once", j+1))
		}
		if c.Operator && prior.Operator && !strings.EqualFold(prior.Name, c.Name) {
			return refuse("operator", fmt.Sprintf("line %d marks %s as you, the operator, and this line marks %s: the operator is one person", j+1, prior.Name, c.Name))
		}
	}
	return nil
}

func contactLinesTheDoorRefuses(book []Contact) []*ContactsRefusal {
	var refused []*ContactsRefusal
	above := make([]Contact, 0, len(book))
	for i, c := range book {
		c = c.asKept()
		if refusal := contactRule(c, i+1, above); refusal != nil {
			refused = append(refused, refusal)
		}
		above = append(above, c)
	}
	return refused
}

func contactLinesRefusedSaid(book []Contact) string {
	refused := contactLinesTheDoorRefuses(book)
	if len(refused) == 0 {
		return ""
	}
	var lines strings.Builder
	for _, r := range refused {
		c := book[r.Line-1]
		fmt.Fprintf(&lines, " Line %d (%q on %q): %s.", r.Line, compactText(c.Name, maxContactName), compactText(c.Channel, maxContactName), r.Requirement)
	}
	return fmt.Sprintf("[contacts] The address book in config.json has %d line(s) that Settings would refuse. It is in force as written: nothing was changed or left out.%s Correct each on Settings → Messages → Contacts or in the file; until then Settings refuses any save of the address book.",
		len(refused), lines.String())
}

func (a *App) sayContactLinesTheDoorRefuses(c Config) {
	said := contactLinesRefusedSaid(c.Contacts)
	if said == "" {
		return
	}
	logged := func() { logsink.Warn("config.decision", "%s: %s", c.SourcePath, said) }
	if a.store == nil {
		logged()
		return
	}
	sum := sha256.Sum256([]byte(said))
	added, err := a.store.AddOutboxNoticeOnce("contacts_door_"+hex.EncodeToString(sum[:8]), "operator", "", said, nil)
	if err == nil {

		if added {
			logged()
		}
		return
	}

	var held *interaction.Error
	if errors.As(err, &held) && held.Code == "INTERACTION_CONFLICT" {
		return
	}
	logged()
	logsink.Warn("config.error", "the operator's notice of the address book's lines could not be written (%v): the line above says them", err)
}

func oneLine(s string, most int) bool {
	return utf8.RuneCountInString(s) <= most && !strings.ContainsFunc(s, unicode.IsControl)
}
