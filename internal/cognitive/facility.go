package cognitive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func evidenceText(e rows.Experience) string {
	text := e.Content

	if e.Provenance == "external" {
		text = untrusted.Wrap("", text)
	}
	return whoseWords(e.Provenance, e.OperatorVerbatim) + ": " + text
}

const shownInPart = " [shown in part; recall (source=experiences) reaches the whole of it]"

func evidenceLine(e rows.Experience, excerptChars int) string {
	whole := strings.Join(strings.Fields(e.Content), " ")
	e.Content = excerpt(e.Content, excerptChars)
	line := strings.ReplaceAll(evidenceText(e), "\n", " ")
	if e.Content != whole {
		line += shownInPart
	}
	return line
}

type AuthoritySource interface {
	AuthorityPreamble() (string, error)
}

type prefixAuthority interface{ AuthorityPrefix() (string, int, error) }

type reservingAuthority interface {
	AuthorityPrefixWithin(reserve int) (string, int, error)
}

const preambleJoin = "\n\n---\n\n"

func withPreamble(ctx context.Context, src AuthoritySource, base string, reserve int) (context.Context, string, error) {
	if src == nil {
		return ctx, base, nil
	}
	var pre string
	var seam int
	var err error
	switch source := src.(type) {
	case reservingAuthority:
		pre, seam, err = source.AuthorityPrefixWithin(reserve)
	case prefixAuthority:
		pre, seam, err = source.AuthorityPrefix()
	default:
		pre, err = src.AuthorityPreamble()
	}
	if err != nil {
		return ctx, "", err
	}
	if pre == "" {
		return ctx, base, nil
	}
	return wire.WithStablePrefix(ctx, seam), pre + preambleJoin + base, nil
}

func requestReserve(base, user string, tools []wire.ToolDefinition) int {
	n := messageTokens("system", preambleJoin+base) + messageTokens("user", user)
	if len(tools) > 0 {
		if b, err := json.Marshal(struct {
			Tools []wire.ToolDefinition `json:"tools"`
		}{tools}); err == nil {
			n += tokenestimate.Estimate(string(b))
		}
	}
	return n
}

func messageTokens(role, content string) int {
	b, err := json.Marshal(wire.Message{Role: role, Content: content})
	if err != nil {
		return tokenestimate.EstimateWire(content)
	}
	return tokenestimate.Estimate(string(b))
}

type admissionCheck func(ctx context.Context, system, user string) error

func simpleCheck(model any) admissionCheck {
	if c, ok := model.(InputChecker); ok {
		return c.CheckSimple
	}
	return nil
}

func toolCheck(model any, tools []wire.ToolDefinition) admissionCheck {
	if c, ok := model.(ToolInputChecker); ok {
		return func(ctx context.Context, system, user string) error {
			return c.CheckWithTools(ctx, system, user, tools)
		}
	}
	return simpleCheck(model)
}

type ToolInputChecker interface {
	CheckWithTools(ctx context.Context, systemPrompt, userMessage string, tools []wire.ToolDefinition) error
}

func fittedPreamble(ctx context.Context, src AuthoritySource, base, smallest string, tools []wire.ToolDefinition, check admissionCheck) (context.Context, string, error) {
	reserve := requestReserve(base, smallest, tools)
	callCtx, system, err := withPreamble(ctx, src, base, reserve)
	if err != nil || check == nil || src == nil {
		return callCtx, system, err
	}
	var limit *wire.ContextLimitError
	if cerr := check(callCtx, system, smallest); errors.As(cerr, &limit) && limit.Required > limit.Limit {
		return withPreamble(ctx, src, base, reserve+limit.Required-limit.Limit)
	}
	return callCtx, system, nil
}

func refusedForWhatWasAsked(err error) bool {
	var limit *wire.ContextLimitError
	var unfinished *wire.IncompleteResponseError
	return errors.As(err, &limit) ||
		errors.As(err, &unfinished) && (unfinished.Reason == "length" || unfinished.Reason == "refusal")
}

type admission struct {
	check  admissionCheck
	ctx    context.Context
	system string
	limit  int
	err    error
}

func (a *admission) admits(user string) bool {
	if a.err != nil {
		return false
	}
	if a.check == nil {
		return true
	}
	var over *wire.ContextLimitError
	switch err := a.check(a.ctx, a.system, user); {
	case err == nil:
		return true
	case errors.As(err, &over):
		a.limit = over.Limit
	default:
		a.err = fmt.Errorf("admission check: %w", err)
	}
	return false
}
