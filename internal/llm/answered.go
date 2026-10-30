package llm

import (
	"encoding/json"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type reportedName string

func (n *reportedName) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*n = reportedName(s)
	}
	return nil
}

const maxReportedModelBytes = 256

var answeredModelSaid sync.Map

func (c *Client) answeredModel(reported string) string {
	if strings.TrimSpace(reported) == "" || reported == c.model {
		return c.model
	}
	if !carriable(reported) {
		if _, said := answeredModelSaid.LoadOrStore(c.refusalKey+"\x00", true); !said {
			logsink.Warn("llm.return", "%s %s named the model that answered with a name the record cannot carry (%d bytes; at most %d bytes of printable text) — the record names the configured model %q. Said once for this provider while the host runs",
				c.providerLabel(), c.model, len(reported), maxReportedModelBytes, c.model)
		}
		return c.model
	}
	if _, said := answeredModelSaid.LoadOrStore(c.refusalKey+"\x00"+reported, true); !said {
		logsink.Info("llm.return", "%s answered as model %q where %q is configured — the record names %q, the model the provider says answered. Said once for this provider and name while the host runs",
			c.providerLabel(), reported, c.model, reported)
	}
	return reported
}

func carriable(name string) bool {
	if len(name) > maxReportedModelBytes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if !unicode.IsGraphic(r) {
			return false
		}
	}
	return true
}
