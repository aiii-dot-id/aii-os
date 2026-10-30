package promptsuite

type State struct {
	Turns []Turn `json:"turns"`

	Calls []Call `json:"calls"`

	Deliveries []Delivery `json:"deliveries"`

	Ledger []Record `json:"ledger"`

	Files      map[string]string `json:"files"`
	SetupFiles map[string]string `json:"setup_files"`

	Name      string `json:"name"`
	BirthName string `json:"birth_name"`

	PluginTools map[string]string `json:"plugin_tools"`
}

type Turn struct {
	Said  string `json:"said"`
	Reply string `json:"reply"`

	Notes []string `json:"notes,omitempty"`
}

type Call struct {
	Turn int `json:"turn"`

	Direct bool   `json:"direct"`
	Actor  string `json:"actor"`
	Tool   string `json:"tool"`

	Args     map[string]any `json:"args,omitempty"`
	ArgsText string         `json:"args_text"`
	Outcome  string         `json:"outcome"`
	Reason   string         `json:"reason,omitempty"`

	Result string `json:"result"`
}

type Delivery struct {
	Turn    int    `json:"turn"`
	Actor   string `json:"actor"`
	Session string `json:"session"`
	Result  string `json:"result"`
}

type Record struct {
	Seq     uint64 `json:"seq"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

func (s *State) reply(n int) (Turn, bool) {
	if n < 1 || n > len(s.Turns) {
		return Turn{}, false
	}
	return s.Turns[n-1], true
}
