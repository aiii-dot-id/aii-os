package updates

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type State struct {
	mu           sync.RWMutex
	availableVer string
	lastCheck    time.Time
	lastError    string
	installedVer string
	needsRestart bool

	pkg PackageUpdate

	lists string

	listsNote string

	ownWithdrawn bool
	ownUnknown   string

	stranded     []StrandedPlugin
	strandedNote string

	checking atomic.Bool
}

func (s *State) Snapshot(currentVersion string) StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StateSnapshot{
		CurrentVersion:   currentVersion,
		AvailableVersion: s.availableVer,
		LastCheck:        s.lastCheck,
		LastError:        s.lastError,
		InstalledVersion: s.installedVer,
		NeedsRestart:     s.needsRestart,
		Checking:         s.checking.Load(),
		Package:          s.pkg,
		Lists:            s.lists,
		ListsNote:        s.listsNote,
		RunningWithdrawn: s.ownWithdrawn,
		RunningUnknown:   s.ownUnknown,
		Stranded:         slices.Clone(s.stranded),
		StrandedNote:     s.strandedNote,
	}
}

type StateSnapshot struct {
	CurrentVersion   string    `json:"current_version,omitempty"`
	AvailableVersion string    `json:"available_version,omitempty"`
	LastCheck        time.Time `json:"last_check,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	InstalledVersion string    `json:"installed_version,omitempty"`
	NeedsRestart     bool      `json:"needs_restart,omitempty"`
	Checking         bool      `json:"checking,omitempty"`

	Package PackageUpdate `json:"package"`

	Lists string `json:"lists,omitempty"`

	ListsNote string `json:"lists_note,omitempty"`

	RunningWithdrawn bool   `json:"running_withdrawn,omitempty"`
	RunningUnknown   string `json:"running_unknown,omitempty"`

	Stranded     []StrandedPlugin `json:"stranded,omitempty"`
	StrandedNote string           `json:"stranded_note,omitempty"`
}

type StrandedPlugin struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

func (s *State) beginCheck() bool { return s.checking.CompareAndSwap(false, true) }

func (s *State) endCheck() { s.checking.Store(false) }

func (s *State) Available() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.availableVer
}

func (s *State) setAvailable(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availableVer = ver
}

func (s *State) setPackage(p PackageUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pkg = p
}

func (s *State) setLists(why, note string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.listsNote != note
	s.lists, s.listsNote = why, note
	return changed
}

func (s *State) setStranded(rows []StrandedPlugin, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stranded, s.strandedNote = rows, note
}

func (s *State) setOwn(withdrawn bool, unknown string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownWithdrawn, s.ownUnknown = withdrawn, unknown
}

func (s *State) setLastError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
}

func (s *State) setInstalled(ver string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installedVer = ver
	s.needsRestart = true
	s.availableVer = ""
}

func (s *State) markChecked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = ""
	s.lastCheck = time.Now()
}
