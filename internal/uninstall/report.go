package uninstall

const Format = 1

const (
	StepSlots   = "slots"
	StepService = "service"
	StepStore   = "store"
	StepApp     = "app"
)

type Report struct {
	Format int `json:"format"`

	Plan  bool     `json:"plan,omitempty"`
	Steps []string `json:"steps,omitempty"`

	Identities []Identity `json:"identities"`

	Done []Did `json:"done"`

	Stopped *Stopped `json:"stopped,omitempty"`

	Notes []string `json:"notes,omitempty"`

	Stays []Stay `json:"stays"`

	Programs []Program `json:"programs,omitempty"`

	Removed    []string `json:"removed,omitempty"`
	FreedBytes int64    `json:"freed_bytes"`
}

type Identity struct {
	Slot     string `json:"slot"`
	Path     string `json:"path"`
	RealPath string `json:"real_path"`
	Name     string `json:"name,omitempty"`

	Registered bool `json:"registered"`

	Running bool `json:"running"`

	Service string `json:"service,omitempty"`
}

type Did struct {
	Step string `json:"step"`
	Did  string `json:"did"`
}

type Stopped struct {
	Step   string `json:"step"`
	Reason string `json:"reason"`
	Remedy string `json:"remedy"`
}

const (
	StayIdentity = "identity"
	StayByName   = "by-name"
	StayOther    = "other"
	StayDevelop  = "development service"
	StayStore    = "store"
	StayService  = "service directory"
	StayLinger   = "linger"
	StayJournal  = "journal"
	StayProgram  = "program"
)

type Stay struct {
	What     string `json:"what"`
	Path     string `json:"path,omitempty"`
	RealPath string `json:"real_path,omitempty"`
	Name     string `json:"name,omitempty"`
	Why      string `json:"why"`
}

type Program struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Remove  string `json:"remove"`

	SharedWith []int    `json:"shared_with,omitempty"`
	Leftovers  []string `json:"leftovers,omitempty"`
	LeftKept   bool     `json:"leftovers_kept,omitempty"`
}
