package llm

import "fmt"

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
type Dialect string

const (
	// .
	DialectOpenAI Dialect = "openai"
	// .
	DialectResponses Dialect = "responses"
	// .
	DialectAnthropic Dialect = "anthropic"
)

// .
// .
// .
// .
var effortField = map[Dialect]string{
	DialectOpenAI:    "reasoning_effort",
	DialectResponses: "reasoning.effort",
	DialectAnthropic: "output_config.effort",
}

// .
// .
func DialectFor(provider string) Dialect {
	switch provider {
	case "chatgpt":
		return DialectResponses
	case "anthropic":
		return DialectAnthropic
	default:
		return DialectOpenAI
	}
}

// .
// .
// .
// .
// .
type WirePlan struct {
	// .
	Requested string
	// .
	Wire string
	// .
	// .
	Field string
	// .
	Sent bool
	// .
	// .
	Note string
}

// .
// .
func (p WirePlan) Summary() string {
	switch {
	case p.Requested == "":
		return "provider default (none set)"
	case p.Sent:
		return fmt.Sprintf("%s → %s", p.Requested, p.Field)
	default:
		return fmt.Sprintf("%s — NOT SENT: %s", p.Requested, p.Note)
	}
}

// .
// .
// .
// .
// .
// .
// .
func PlanEffort(d Dialect, requested string, levels []string) WirePlan {
	field, known := effortField[d]
	if !known {
		field = "reasoning_effort"
	}
	p := WirePlan{Requested: requested, Field: field}
	if requested == "" {
		return p
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if levels == nil {
		p.Wire, p.Sent = requested, true
		return p
	}
	if len(levels) == 0 {
		p.Note = "this model has no effort parameter"
		return p
	}
	for _, l := range levels {
		if l == requested {
			p.Wire, p.Sent = requested, true
			return p
		}
	}
	p.Note = fmt.Sprintf("this provider accepts %s", joinOr(levels))
	return p
}

func joinOr(levels []string) string {
	switch len(levels) {
	case 0:
		return "no effort values"
	case 1:
		return levels[0]
	}
	out := ""
	for i, l := range levels[:len(levels)-1] {
		if i > 0 {
			out += ", "
		}
		out += l
	}
	return out + " or " + levels[len(levels)-1]
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
var summaryField = map[Dialect]string{
	DialectResponses: "reasoning.summary",
	DialectAnthropic: "thinking.display",
}

// .
var summaryVocabulary = map[Dialect][]string{
	DialectResponses: {"auto", "concise", "detailed"},
	DialectAnthropic: {"summarized"},
}

// .
// .
// .
// .
// .
var summaryAliases = map[Dialect]map[string]string{
	DialectResponses: {"summarized": "auto"},
	DialectAnthropic: {"auto": "summarized", "concise": "summarized", "detailed": "summarized"},
}

// .
// .
func PlanReasoningSummary(d Dialect, requested string) WirePlan {
	field := summaryField[d]
	p := WirePlan{Requested: requested, Field: field}
	if requested == "" {
		return p
	}
	if field == "" {
		p.Note = "this dialect has no readable-reasoning parameter"
		return p
	}
	want := requested
	if alias, ok := summaryAliases[d][requested]; ok {
		want = alias
	}
	for _, v := range summaryVocabulary[d] {
		if v == want {
			p.Wire, p.Sent = want, true
			return p
		}
	}
	p.Note = fmt.Sprintf("this dialect accepts %s", joinOr(summaryVocabulary[d]))
	return p
}

// .
func (c *Client) summaryPlan() WirePlan {
	return PlanReasoningSummary(DialectFor(c.provider), c.thinkingDisplay)
}

// .
// .
// .
// .
func ReasoningSummaryField(d Dialect) string    { return summaryField[d] }
func ReasoningSummaryLevels(d Dialect) []string { return summaryVocabulary[d] }
