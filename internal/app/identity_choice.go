package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type IdentityChoice struct {
	Home  string
	Why   string
	Files []IdentityFiles
}

type IdentityFiles struct {
	Key   string
	Was   string
	Found []string
}

var identityFileWhat = map[string]string{"ledger_path": "the record", "db_path": "the database", "key_path": "the key"}

func (f IdentityFiles) standard(home string) string {
	return filepath.Join(home, "data", filepath.Base(f.Was))
}

type IdentityChoiceRefusal struct {
	Choice IdentityChoice
	err    error
}

func (e *IdentityChoiceRefusal) Error() string { return e.err.Error() }
func (e *IdentityChoiceRefusal) Unwrap() error { return e.err }

func (a *App) StartAuthenticatedEmbeddedAtChoice(c IdentityChoice) (err error) {
	a.embeddedAuthentication = true
	release, err := a.claimIdentity(filepath.Join(c.Home, "data"))
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			release()
		}
	}()

	if _, err := a.prepareStart(); err != nil {
		return err
	}
	return a.startAtChoice(c)
}

func (a *App) startAtChoice(c IdentityChoice) error {
	a.identityChoice = &c
	logsink.Error("boot.refusal", "THE IDENTITY'S RECORD IS NOT WHERE THE CONFIGURATION NAMES IT — entering BOOT-SAFE (minimal, read-only) for the operator's choice: %s", c.Why)
	return a.startSafeBoot(safeIdentityChoice, c.Why)
}

func (a *App) identityChoiceView() *dashboard.IdentityChoice {
	c := a.identityChoice
	if c == nil {
		return nil
	}
	v := &dashboard.IdentityChoice{}
	for _, f := range c.Files {
		files := dashboard.IdentityChoiceFiles{Key: f.Key, What: identityFileWhat[f.Key], Was: f.Was}
		if len(f.Found) == 0 {
			files.Standard = f.standard(c.Home)
		}
		for _, p := range f.Found {
			found := dashboard.IdentityChoiceFile{Path: p}
			if st, err := os.Stat(p); err == nil {
				found.Bytes, found.Modified = st.Size(), st.ModTime().UTC().Format(time.RFC3339)
			}
			files.Found = append(files.Found, found)
		}
		v.Files = append(v.Files, files)
	}
	return v
}

func (a *App) chooseIdentityFiles(picked map[string]string) error {
	c := a.identityChoice
	if c == nil {
		return errors.New("This start is not waiting for a choice of the identity's files. Nothing was written.")
	}
	if len(c.record().Found) == 0 {
		return errors.New("No record of this identity was found here, so there is nothing to choose. Nothing was written.")
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	candidate := *a.cfg
	for _, f := range c.Files {
		to := f.standard(c.Home)
		if len(f.Found) > 0 {
			to = picked[f.Key]
			if !slices.Contains(f.Found, to) {
				return fmt.Errorf("Choose one of the files found for %s. Nothing was written.", identityFileWhat[f.Key])
			}
		}

		if rel, err := filepath.Rel(c.Home, to); err == nil && !strings.HasPrefix(rel, "..") {
			to = rel
		}
		switch f.Key {
		case "ledger_path":
			candidate.Identity.LedgerPath = to
		case "db_path":
			candidate.Identity.DBPath = to
		case "key_path":
			candidate.Identity.KeyPath = to
		}
	}
	if published, err := a.persistLocked(&candidate, saveConfig); !published {
		return fmt.Errorf("The choice could not be written (%v). Nothing else was changed.", err)
	}
	logsink.Warn("config.decision", "the operator chose the identity's files: record %s, database %s, key %s", candidate.Identity.LedgerPath, candidate.Identity.DBPath, candidate.Identity.KeyPath)
	return nil
}

func (c *IdentityChoice) record() IdentityFiles {
	for _, f := range c.Files {
		if f.Key == "ledger_path" {
			return f
		}
	}
	return IdentityFiles{Key: "ledger_path"}
}
