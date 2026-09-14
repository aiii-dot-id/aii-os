package pluginhost

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

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
const SubscriptionsFile = "subscriptions.json"

// .
const MaxSubscriptions = 16

// .
// .
// .
// .
// .
const (
	TopicToolCalled     = "tool.called"
	TopicTurnStarted    = "turn.started"
	TopicTurnEnded      = "turn.ended"
	TopicLedgerAppended = "ledger.appended"
	TopicAlarmFired     = "alarm.fired"
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	TopicWorkStarted   = "work.started"
	TopicWorkDelivered = "work.delivered"
	TopicWorkHarvested = "work.harvested"
	// .
	// .
	// .
	// .
	// .
	// .
	TopicWorkGraded      = "work.graded"
	TopicSubagentSpawned = "subagent.spawned"
)

// .
var Topics = []string{TopicToolCalled, TopicTurnStarted, TopicTurnEnded, TopicLedgerAppended, TopicAlarmFired,
	TopicWorkStarted, TopicWorkDelivered, TopicWorkHarvested, TopicWorkGraded, TopicSubagentSpawned}

var reFilterKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// .
// .
// .
type SubscriptionDecl struct {
	Topic     string            `json:"topic"`
	Operation string            `json:"operation"`
	Filter    map[string]string `json:"filter,omitempty"`
}

// .
// .
type SubscriptionsError struct {
	PluginID string
	Detail   string
}

func (e *SubscriptionsError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not a subscription declaration the host honors: %s", e.PluginID, SubscriptionsFile, e.Detail)
}

// .
// .
func ParseSubscriptions(raw []byte, methods []string) ([]SubscriptionDecl, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var decls []SubscriptionDecl
	if err := dec.Decode(&decls); err != nil {
		return nil, fmt.Errorf("not a list of subscriptions: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content after the list")
	}
	if len(decls) > MaxSubscriptions {
		return nil, fmt.Errorf("%d subscriptions; at most %d", len(decls), MaxSubscriptions)
	}
	known := map[string]bool{}
	for _, m := range methods {
		known[m] = true
	}
	for i, d := range decls {
		topicOK := false
		for _, t := range Topics {
			if d.Topic == t {
				topicOK = true
			}
		}
		if !topicOK {
			return nil, fmt.Errorf("subscription %d: topic %q is not one the host emits (%v)", i, d.Topic, Topics)
		}
		if !known[d.Operation] {
			return nil, fmt.Errorf("subscription %d (%s): operation %q is not a method this package declares", i, d.Topic, d.Operation)
		}
		if len(d.Filter) > 8 {
			return nil, fmt.Errorf("subscription %d (%s): at most 8 filter fields", i, d.Topic)
		}
		for k, v := range d.Filter {
			if !reFilterKey.MatchString(k) || len(v) > 256 {
				return nil, fmt.Errorf("subscription %d (%s): filter %q is not a short payload field", i, d.Topic, k)
			}
		}
	}
	return decls, nil
}

// .
// .
func (d SubscriptionDecl) Matches(payload map[string]interface{}) bool {
	for k, want := range d.Filter {
		got, ok := payload[k].(string)
		if !ok || got != want {
			return false
		}
	}
	return true
}

// .
func loadSubscriptions(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest) ([]SubscriptionDecl, error) {
	if _, present := res.FileDigests[SubscriptionsFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, SubscriptionsFile)
	if err != nil {
		return nil, err
	}
	var methods []string
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		methods = append(methods, decl.Methods...)
	}
	decls, err := ParseSubscriptions(raw, methods)
	if err != nil {
		return nil, &SubscriptionsError{PluginID: m.ID, Detail: err.Error()}
	}
	return decls, nil
}

// .
// .
// .
func (ap *ActivePlugin) hostDriven() map[string]bool {
	out := map[string]bool{}
	for _, h := range ap.Webhooks {
		out[h.Operation] = true
	}
	for _, s := range ap.Subscriptions {
		out[s.Operation] = true
	}
	return out
}
