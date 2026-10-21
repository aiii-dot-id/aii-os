package broker

import "strings"

type safeAnswer int

const (
	safeRefused safeAnswer = iota

	safeAnswered

	safeOwnGate
)

type effectReach int

const (
	_ effectReach = iota

	reachAnyClass

	reachLocalWrite

	reachExternalRead

	reachExternalWrite
)

type opSpec struct {
	contextRead bool
	safe        safeAnswer

	read bool

	storage bool
	reach   effectReach

	answerFirst bool

	checks bool
}

var operations = map[string]opSpec{

	opKVGet:    {contextRead: true, safe: safeAnswered, read: true, storage: true, reach: reachAnyClass},
	opKVList:   {contextRead: true, safe: safeAnswered, read: true, storage: true, reach: reachAnyClass},
	opKVPut:    {storage: true, reach: reachLocalWrite},
	opKVDelete: {storage: true, reach: reachLocalWrite},

	opMemoryRecall:   {contextRead: true, safe: safeAnswered, read: true, storage: true, reach: reachAnyClass},
	opMemoryRemember: {storage: true, reach: reachLocalWrite},

	opSettingsGet: {contextRead: true, safe: safeAnswered, read: true, reach: reachAnyClass, answerFirst: true, checks: true},

	opInteractionQuery: {contextRead: true, safe: safeAnswered, read: true, reach: reachAnyClass},
	opInteractionRead:  {contextRead: true, safe: safeAnswered, read: true, reach: reachAnyClass},

	opVoiceObserve: {safe: safeOwnGate, reach: reachLocalWrite},

	opEmbeddingsCreate: {read: true, reach: reachExternalRead},

	opHTTPGet: {read: true, reach: reachExternalRead},

	opHTTPPost:   {reach: reachExternalWrite},
	opHTTPPut:    {reach: reachExternalWrite},
	opHTTPPatch:  {reach: reachExternalWrite},
	opHTTPDelete: {reach: reachExternalWrite},

	opHTTPRead:  {read: true, reach: reachAnyClass},
	opHTTPClose: {read: true, reach: reachAnyClass},

	opFSList:    {contextRead: true, safe: safeOwnGate, read: true, storage: true, reach: reachAnyClass, answerFirst: true},
	opFSRead:    {contextRead: true, safe: safeOwnGate, read: true, storage: true, reach: reachAnyClass, answerFirst: true},
	opFSWrite:   {safe: safeOwnGate, storage: true, reach: reachLocalWrite},
	opFSDelete:  {safe: safeOwnGate, storage: true, reach: reachLocalWrite},
	opFSPublish: {safe: safeOwnGate, storage: true, reach: reachLocalWrite},

	opToolsPublish:  {read: true, reach: reachLocalWrite},
	opToolsWithdraw: {read: true, reach: reachLocalWrite},
}

func passesSAFEGate(op string) bool {
	if spec, ok := operations[op]; ok {
		return spec.safe != safeRefused
	}
	return strings.HasPrefix(op, "fs.")
}

func opMutates(op string) bool {
	spec, ok := operations[op]
	return ok && !spec.read
}

func answersChecks(op string) bool { return operations[op].checks }

func answersFirst(op string) bool { return operations[op].answerFirst }

const DefaultMaxInflightReceipts = 4096

func (c Config) maxInflightReceipts() int64 {
	if c.MaxInflightReceipts > 0 {
		return int64(c.MaxInflightReceipts)
	}
	return DefaultMaxInflightReceipts
}

func (h *Host) holdReceipt() bool {
	if h.inflightReceipts.Add(1) > h.cfg.maxInflightReceipts() {
		h.inflightReceipts.Add(-1)
		return false
	}
	return true
}

func usesStorage(op string) bool { return operations[op].storage }

func effectsAllow(effects, op string) bool {
	return classReaches(effects, operations[op].reach)
}

func classReaches(effects string, reach effectReach) bool {
	switch reach {
	case reachAnyClass:
		return true
	case reachLocalWrite:
		return effects == "" || effects == EffectsWriteLocal || effects == EffectsWriteExternal || effects == EffectsExec
	case reachExternalRead:
		return effects == "" || effects == EffectsReadExternal || effects == EffectsWriteExternal || effects == EffectsExec
	case reachExternalWrite:
		return effects == "" || effects == EffectsWriteExternal || effects == EffectsExec
	}
	return false
}
