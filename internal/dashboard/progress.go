package dashboard

// .
// .
// .
// .
// .
type ContractProgress struct {
	HasCriteria    bool             `json:"has_criteria"`
	Items          []AcceptanceItem `json:"items,omitempty"`
	NextIndex      int              `json:"next_index"`
	Counts         map[string]int   `json:"counts,omitempty"`
	ClosureAllowed bool             `json:"closure_allowed"`
	ClosureReason  string           `json:"closure_reason,omitempty"`
}

// .
type AcceptanceItem struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	State string `json:"state"`
	Class string `json:"class,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Stale bool   `json:"stale,omitempty"`
}
