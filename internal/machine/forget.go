package machine

import (
	"net"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func (s *Server) forget(l *link, m message) (message, func()) {
	var none struct{}
	if err := decodeStrict(m.Params, &none); err != nil {
		return refusalReply(m.ID, &Refusal{Reason: ReasonBadRequest, Message: "forget takes no params; a link is its own identity: " + err.Error()}), nil
	}
	if err := s.forgetSlot(l.slot); err != nil {
		return refusalReply(m.ID, &Refusal{Reason: ReasonNotRecorded, Message: "the slot could not be let go of: " + err.Error()}), nil
	}
	logsink.Info("machine.decision", "identity %s's operator unregistered the slot %s; its record and its references are let go", short(l.identity), l.slot)
	return message{ID: m.ID, Result: mustJSON(map[string]bool{"forgotten": true})}, func() { s.closeSlotLinks(l.slot) }
}

func (s *Server) forgetSlot(slot string) error {
	return s.letGo(func(next *State) {
		next.Identities = slices.DeleteFunc(next.Identities, func(r Record) bool { return r.Slot == slot })
		next.Refs = slices.DeleteFunc(next.Refs, func(r Ref) bool { return r.Slot == slot })
		delete(next.Gone, slot)
	})
}

func (s *Server) closeSlotLinks(slot string) {
	s.mu.Lock()
	var conns []net.Conn
	for l := range s.links {
		if l.slot == slot {
			conns = append(conns, l.conn)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}
