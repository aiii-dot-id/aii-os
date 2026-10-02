package pluginhost

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const ScheduleFile = "schedule.json"

const (
	MaxScheduled = 4

	MinScheduleEvery = 600

	MaxScheduleEvery = 7 * 24 * 3600
)

type ScheduleDecl struct {
	Operation    string `json:"operation"`
	EverySeconds int    `json:"every_seconds"`
}

type ScheduleError struct {
	PluginID string
	Detail   string
}

func (e *ScheduleError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not a schedule declaration the host honors: %s", e.PluginID, ScheduleFile, e.Detail)
}

func (e *ScheduleError) WaitsOnPackage() bool { return true }

func ParseSchedule(raw []byte, methods []string) ([]ScheduleDecl, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var decls []ScheduleDecl
	if err := dec.Decode(&decls); err != nil {
		return nil, fmt.Errorf("not a list of scheduled operations: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content after the list")
	}
	if len(decls) > MaxScheduled {
		return nil, fmt.Errorf("%d scheduled operations; at most %d", len(decls), MaxScheduled)
	}
	known := map[string]bool{}
	for _, m := range methods {
		known[m] = true
	}
	named := map[string]bool{}
	for _, d := range decls {
		switch {
		case !known[d.Operation]:
			return nil, fmt.Errorf("operation %q is not a method this package declares", d.Operation)
		case named[d.Operation]:
			return nil, fmt.Errorf("operation %q is scheduled twice", d.Operation)
		case d.EverySeconds < MinScheduleEvery || d.EverySeconds > MaxScheduleEvery:
			return nil, fmt.Errorf("operation %q: every_seconds must be %d..%d (ten minutes to a week)", d.Operation, MinScheduleEvery, MaxScheduleEvery)
		}
		named[d.Operation] = true
	}
	return decls, nil
}

func loadSchedule(pkgPath string, res *packagefmt.Result, held map[string][]byte, m *packagefmt.Manifest) ([]ScheduleDecl, error) {
	if _, present := res.FileDigests[ScheduleFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, held, ScheduleFile)
	if err != nil {
		return nil, err
	}
	var methods []string
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		methods = append(methods, decl.Methods...)
	}
	decls, err := ParseSchedule(raw, methods)
	if err != nil {
		return nil, &ScheduleError{PluginID: m.ID, Detail: err.Error()}
	}
	return decls, nil
}

func holdScheduleToDescriptors(pluginID string, decls []ScheduleDecl, descs *descriptorSet) error {
	for _, d := range decls {
		desc := descs.ops[d.Operation]
		switch {
		case desc == nil:
			return &ScheduleError{PluginID: pluginID, Detail: fmt.Sprintf("operation %q has no descriptor, so what it does is not declared", d.Operation)}
		case desc.operatorConfirms:
			return &ScheduleError{PluginID: pluginID, Detail: fmt.Sprintf("operation %q declares operator_confirms: the host calls it on a timer, and nothing could confirm it", d.Operation)}
		}
	}
	return nil
}
