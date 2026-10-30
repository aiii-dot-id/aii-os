package hold

type Hold struct {
	Reason string

	Waiting bool
}

func Safe(reason string) Hold { return Hold{Reason: reason} }

func (h Hold) Name() string {
	if h.Waiting {
		return "DEGRADED"
	}
	return "SAFE"
}

const While = "while this start waits for Ring 5, which it asks for again by itself"
