package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// WHAT A RELOAD SAYS IT APPLIED IS WHAT IT APPLIED. A reload calls a
// change saved-for-next-boot when it survives blankLiveAppliable. Three
// settings the same reload puts into effect were not blanked there:
// memory.meaning_source, which is read from the configuration at every
// use, and logs.group and logs.taps, which the reload hands to the log.
// An operator who changed the meaning source in the file had memories
// going to a provider's endpoint on the next pass, under one line that
// said the change waits for a restart.
func TestAReloadDoesNotCallALiveChangeSavedForNextBoot(t *testing.T) {
	out := reloadLog(t, func(c *Config) { c.Memory.MeaningSource = meaningChoiceNone })
	if strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Fatalf("memory.meaning_source is read at every use, and the reload said it waits for a boot:\n%s", out)
	}
	if !strings.Contains(out, "memory.meaning_source applied live") {
		t.Fatalf("a changed meaning source took effect without a line that names it:\n%s", out)
	}

	// The reload hands groups and taps to the log, which is shared by the
	// whole test binary: put it back.
	t.Cleanup(func() { logsink.SetGroups(nil); logsink.SetTaps(nil) })
	for what, edit := range map[string]func(*Config){
		"logs.group": func(c *Config) { c.Logs.Group = map[string][]string{"mine": {"voice.session"}} },
		"logs.taps":  func(c *Config) { c.Logs.Taps = map[string]TapSetting{"llm.prompt": {}} },
	} {
		a, b := *defaultConfig(), *defaultConfig()
		edit(&b)
		blankLiveAppliable(&a)
		blankLiveAppliable(&b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s is applied by a reload and survives blankLiveAppliable: a reload calls it saved for next boot", what)
		}
		out := reloadLog(t, edit)
		if strings.Contains(out, "SAVED FOR NEXT BOOT") {
			t.Errorf("%s: the reload applied it and said it waits for a boot:\n%s", what, out)
		}
		if !strings.Contains(out, "logs.group and logs.taps applied live") {
			t.Errorf("%s took effect without a line that names it:\n%s", what, out)
		}
	}
}
