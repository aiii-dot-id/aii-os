package broker

import "testing"

// AN OPERATION NO CLASS NAMES IS IN NONE OF THEM. effectsAllow answered
// true for an operation it did not list. No call reaches that today,
// because dispatch refuses an unknown operation first, but the next
// operation added without a line here would have run under every effect
// class. It is refused now, at an authority boundary.
func TestAnOperationNoClassNamesIsRefused(t *testing.T) {
	for _, effects := range []string{"", EffectsReadInternal, EffectsReadExternal, EffectsWriteLocal, EffectsWriteExternal, EffectsExec} {
		if effectsAllow(effects, "kv.rename") {
			t.Errorf("an operation no class names was allowed under %q", effects)
		}
	}
	// Control: a named operation answers as it always has.
	if !effectsAllow(EffectsReadInternal, opKVGet) || effectsAllow(EffectsReadInternal, opKVPut) {
		t.Error("the named operations changed their answers")
	}
}

// Only the two read classes are reads; the undeclared class and a class no
// contract names are not (each caller states its own answer for "" first).
func TestOnlyTheTwoReadClassesAreReads(t *testing.T) {
	for effects, want := range map[string]bool{
		EffectsReadInternal:  true,
		EffectsReadExternal:  true,
		EffectsWriteLocal:    false,
		EffectsWriteExternal: false,
		EffectsExec:          false,
		"":                   false,
		"read":               false,
	} {
		if got := IsReadEffect(effects); got != want {
			t.Errorf("IsReadEffect(%q) = %v, want %v", effects, got, want)
		}
	}
}
