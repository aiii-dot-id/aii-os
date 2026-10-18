package app

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (a *App) importLegacyContacts(persist func(*Config) (bool, error)) error {
	a.cfgMu.RLock()
	explicit := a.cfg.contactsPresent || a.cfg.Contacts != nil
	a.cfgMu.RUnlock()
	if explicit {
		return nil
	}
	contacts, issues, err := a.store.LegacyContacts()
	if err != nil {
		return err
	}
	for _, issue := range issues {
		logsink.Warn("config.decision", "%s", issue)
	}
	if len(contacts) == 0 {
		return nil
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg.contactsPresent || a.cfg.Contacts != nil {
		return nil
	}
	if a.cfg.SourcePath == "" {
		return &store.SchemaError{Phase: "contact import", Cause: fmt.Errorf("no config source path; historical contacts retained, affected mail remains queued")}
	}

	fresh, err := ReadConfig(a.cfg.SourcePath)
	if err != nil {
		return &store.SchemaError{Phase: "contact import", Cause: err}
	}
	if fresh.contactsPresent {
		a.cfg.Contacts = fresh.Contacts
		a.cfg.contactsPresent = true
		return nil
	}
	fresh.Contacts = make([]Contact, 0, len(contacts))
	for _, c := range contacts {
		fresh.Contacts = append(fresh.Contacts, Contact{Name: c.Name, Channel: c.Channel, Address: c.Address, Wake: c.Wake})
	}
	fresh.contactsPresent = true
	published, err := persist(fresh)
	if published || err == nil {
		a.cfg.Contacts = fresh.Contacts
		a.cfg.contactsPresent = true
	}
	if err != nil {
		return &store.SchemaError{Phase: "contact import", Cause: err}
	}
	logsink.Info("config.decision", "imported %d historical contact routes into the previously absent contacts field", len(contacts))
	return nil
}
