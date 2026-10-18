package app

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// THE HANDLERS' WIRING, PINNED (T32). What a dashboard handler carries is
// recorded field by field: every non-zero field of the WSHandler, and every
// non-zero field of a nested hook struct as Outer.Field, each with what it
// is bound to — the method a method value names, "closure" for a func
// literal, a string's value, an interface's dynamic type. The pin is the
// wiring's inventory, so a hook dropped, added, or bound to another method
// of the same type (CancelSignIn and CancelProfileSignIn are both
// func(string) error) fails here by its name, whichever file assigns it.
//
// SAFE serves the live handler (safeboot.go), so the live pin is SAFE's
// too. FIRSTBOOT builds its own (ceremony.go) and keeps its own subset.

func TestTheLiveHandlerCarriesItsPinnedHooks(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	checkHookPin(t, "live", a.buildLiveHandler(), liveHookPin)
}

func TestTheFirstbootHandlerCarriesItsPinnedHooks(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	checkHookPin(t, "firstboot", a.buildFirstbootHandler(), firstbootHookPin)
}

var liveHookPin = map[string]string{
	"AccessCodeOrigin":           "closure",
	"AccessCodeSeconds":          "closure",
	"AcquireTurn":                "app.(*App).acquireTurn",
	"AdmitChat":                  "app.(*App).admitOperatorChat",
	"AdmitFile":                  "app.(*App).admitShownFile",
	"AdmitRequest":               "app.(*App).admitConstrainedChat",
	"AnswerAsk":                  "app.(*App).answerAsk",
	"ApproveOverlayScript":       "app.(*App).approveOverlayScript",
	"AudioPlane":                 "app.(*App).AudioPlane",
	"Background":                 "app.(*App).runBackground",
	"CancelProfileSignIn":        "app.(*App).CancelProfileSignIn",
	"CancelSignIn":               "app.(*App).CancelSignIn",
	"CancelTurn":                 "app.(*App).CancelTurn",
	"CatalogRefresh":             "closure",
	"Channels":                   "closure",
	"ClearPluginKey":             "app.(*App).ClearPluginSecret",
	"CompleteProfileSignIn":      "app.(*App).CompleteProfileSignIn",
	"CompleteSignIn":             "app.(*App).CompleteSignIn",
	"Continuity":                 "*dashboard.ContinuityHooks",
	"Continuity.Background":      "app.(*App).runBackground",
	"Continuity.EscrowCheck":     "continuity.(*Runtime).escrowCheckHere",
	"Continuity.EscrowCreate":    "continuity.(*Runtime).escrowCreateHere",
	"Continuity.KeyBack":         "continuity.(*Runtime).putBackKey",
	"Continuity.Restore":         "continuity.(*Runtime).requestRestore",
	"Continuity.RestorePlan":     "closure",
	"Continuity.SnapshotKeyBack": "continuity.(*Runtime).putBackSnapshotKey",
	"Continuity.Take":            "closure",
	"Continuity.Verify":          "closure",
	"Continuity.View":            "continuity.(*Runtime).continuityView",
	"DashboardToken":             "app.(*App).dashboardToken",
	"DatabaseExport":             "continuity.(*Runtime).ExportDatabase",
	"DeleteAuthProfile":          "app.(*App).DeleteAuthProfile",
	"DeleteProvider":             "app.(*App).deleteProvider",
	"DeviceSignInProfile":        "app.(*App).DeviceSignInProfile",
	"DisconnectProfile":          "app.(*App).DisconnectProfile",
	"DiscoverModels":             "app.(*App).discoverModels",
	"Files":                      "app.(*App).filePolicy",
	"GetAsks":                    "app.(*App).askViews",
	"GetConfig":                  "app.(*App).configState",
	"GetContinuity":              "app.(*App).continuityState",
	"GetIdentity":                "app.(*App).identityState",
	"GetProjectRoot":             "app.(*App).getProjectRoot",
	"GetProjects":                "app.(*App).projectsState",
	"GetProviders":               "app.(*App).providerDirectory",
	"GetSandbox":                 "app.(*App).sandboxState",
	"GetStats":                   "app.(*App).statsState",
	"GetTools":                   "closure",
	"GetWork":                    "app.(*App).workQueueState",
	"GetWorkspace":               "app.(*App).getProjectWorkspace",
	"GradeResult":                "app.(*App).gradeResult",
	"HandleMessage":              "app.(*App).handleMessage",
	"HearUtterance":              "app.(*App).HearUtterance",
	"HeardSpeech":                "app.(*App).heardSpeechPage",
	"HeldMail":                   "app.(*App).heldMail",
	"InspectMail":                "app.(*App).inspectHeldMail",
	"Interactions":               "*app.App",
	"ListLogs":                   "app.(*App).listLogs",
	"Messages":                   "app.(*App).messagesPage",
	"OAuthCallback":              "app.(*App).OAuthCallback",
	"ObserveChat":                "app.(*App).observeChat",
	"PagesClosed":                "app.(*App).pokeOutbox",
	"PendingSteers":              "app.(*App).PendingSteers",
	"PluginAct":                  "closure",
	"ProjectAct":                 "closure",
	"PublicNameClaim":            "publicname.(*Runtime).Claim",
	"PublicNameLook":             "publicname.(*Runtime).Look",
	"PublicNameMove":             "publicname.(*Runtime).Move",
	"PublicNameRetry":            "publicname.(*Runtime).Retry",
	"PublicNameState":            "publicname.(*Runtime).State",
	"Recall":                     "app.(*App).recallForDashboard",
	"RecentTurns":                "app.(*App).recentTurnViews",
	"Record":                     "app.recordSource",
	"RecordSettings":             "app.(*App).recordSettings",
	"RefusedPauses":              "closure",
	"ReleaseTurn":                "app.(*App).EndTurn",
	"RemoveBrokenProvider":       "app.(*App).removeBrokenProvider",
	"RepairMail":                 "app.(*App).repairMail",
	"RepairProvider":             "app.(*App).repairBrokenProvider",
	"ReplyVoice":                 "app.(*App).replyVoice",
	"RequireDashboardToken":      "app.(*App).requireDashboardAccess",
	"Restart":                    "app.(*App).Restart",
	"RotateDashboardToken":       "app.(*App).rotateDashboardAccess",
	"Sent":                       "*llm.SentRequests",
	"SessionServers":             "publicname.(*Runtime).sessionServers",
	"SetAuthProfile":             "app.(*App).SetAuthProfile",
	"SetConfig":                  "app.(*App).applyConfigChange",
	"SetEffort":                  "app.(*App).setActiveEffort",
	"SetPluginKey":               "app.(*App).SetPluginSecret",
	"SetProvider":                "app.(*App).setProviderInfo",
	"SetSandbox":                 "app.(*App).setSandboxRoots",
	"SetSandboxOff":              "app.(*App).setSandboxOff",
	"SetSpeechService":           "app.(*App).setSpeechService",
	"SetToolFunc":                "app.(*App).setToolEnabled",
	"SettingChoices":             "app.(*App).PluginSettingChoices",
	"SignInProfile":              "app.(*App).SignInProfile",
	"SignInProvider":             "app.(*App).SignInProvider",
	"SpeakAhead":                 "app.(*App).speakAhead",
	"SpeakMint":                  "app.(*App).speakMint",
	"SpeakPlay":                  "app.(*App).speakPlay",
	"Speaker":                    "\"identity\"",
	"SpeakerPolicy":              "app.(*App).speakerPolicyState",
	"SpeechLists":                "app.(*App).speechLists",
	"Steer":                      "app.(*App).Steer",
	"TailLogs":                   "app.(*App).tailLogs",
	"TakeOutbox":                 "app.(*App).takeOutbox",
	"Thinking":                   "app.(*App).thinkingOfTurn",
	"ToldPath":                   "app.(*App).toldPath",
	"TurnActive":                 "app.(*App).TurnActive",
	"Uninstall":                  "app.(*App).openUninstallApp",
	"UnsignedRequests":           "closure",
	"UpdateCheck":                "app.(*App).checkForUpdateNow",
	"UploadDir":                  "app.(*App).uploadDir",
	"VoiceConfigured":            "app.(*App).VoiceConfigured",
	"VoiceEngine":                "app.(*App).VoiceEngine",
	"VoiceMode":                  "app.(*App).VoiceMode",
	"VoiceSessionOpen":           "app.(*App).OpenVoiceSession",
	"VoiceSpeaker":               "app.(*App).voiceSpeakerCarried",
	"VoiceStatus":                "app.(*App).VoiceStatus",
}

var firstbootHookPin = map[string]string{
	"Background":           "app.(*App).runBackground",
	"CancelProfileSignIn":  "app.(*App).CancelProfileSignIn",
	"CancelSignIn":         "app.(*App).CancelSignIn",
	"CompleteSignIn":       "app.(*App).CompleteSignIn",
	"DeleteProvider":       "app.(*App).deleteProvider",
	"DiscoverModels":       "app.(*App).discoverModels",
	"GetProviders":         "app.(*App).providerDirectory",
	"GetStats":             "closure",
	"HandleGenesis":        "app.(*App).handleGenesis",
	"HandleMessage":        "app.(*App).handleBootstrapMessage",
	"OAuthCallback":        "app.(*App).OAuthCallback",
	"RemoveBrokenProvider": "app.(*App).removeBrokenProvider",
	"RepairProvider":       "app.(*App).repairBrokenProvider",
	"Restart":              "app.(*App).Restart",
	"RestoreNew":           "*dashboard.RestoreNewHooks",
	"RestoreNew.Keys":      "continuity.(*Runtime).NewMachineKeys",
	"RestoreNew.Restore":   "continuity.(*Runtime).NewMachineRestore",
	"RestoreNew.Upload":    "continuity.(*Runtime).NewMachineUpload",
	"SetProvider":          "app.(*App).setProviderInfo",
	"SignInProvider":       "app.(*App).SignInProvider",
	"Speaker":              "\"system\"",
	"UpdateCheck":          "closure",
}

func checkHookPin(t *testing.T, which string, h *dashboard.WSHandler, pin map[string]string) {
	t.Helper()
	got := handlerWiring(t, h)
	names := map[string]bool{}
	for n := range pin {
		names[n] = true
	}
	for n := range got {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		want, pinned := pin[n]
		have, wired := got[n]
		switch {
		case pinned && !wired:
			t.Errorf("the %s handler lost %s (pinned to %s)", which, n, want)
		case !pinned && wired:
			t.Errorf("the %s handler carries %s (bound to %s), which the pin does not", which, n, have)
		case want != have:
			t.Errorf("the %s handler binds %s to %s, pinned to %s", which, n, have, want)
		}
	}
	if t.Failed() {
		var b strings.Builder
		for _, n := range sortedKeys(got) {
			fmt.Fprintf(&b, "\t%q: %q,\n", n, got[n])
		}
		t.Logf("the %s handler as wired, for a deliberate change to its pin:\n%s", which, b.String())
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// handlerWiring records what h carries; see the file's head.
func handlerWiring(t *testing.T, h *dashboard.WSHandler) map[string]string {
	t.Helper()
	got := map[string]string{}
	var walk func(prefix string, v reflect.Value)
	walk = func(prefix string, v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			fv := v.Field(i)
			if fv.IsZero() {
				continue
			}
			name := prefix + v.Type().Field(i).Name
			switch fv.Kind() {
			case reflect.Func:
				got[name] = funcTarget(fv)
			case reflect.Pointer:
				got[name] = fv.Type().String()
				if fv.Elem().Kind() == reflect.Struct {
					walk(name+".", fv.Elem()) // a nested hook struct
				}
			case reflect.Interface:
				got[name] = fv.Elem().Type().String()
			case reflect.String:
				got[name] = strconv.Quote(fv.String())
			default:
				t.Fatalf("%s is a %s field, which this record does not know how to name", name, fv.Kind())
			}
		}
	}
	walk("", reflect.ValueOf(h).Elem())
	return got
}

// closureName matches a func literal's symbol, wherever it was declared or
// inlined: …buildLiveHandler.func3, …startLive.func1.2.
var closureName = regexp.MustCompile(`\.func\d+(\.\d+)*$`)

// funcTarget names what a func value runs: a method value is its method's
// wrapper (…(*App).setActiveEffort-fm), named without the suffix; a func
// literal is only "closure", since its symbol names the function that
// declares it, and moving a literal between wire funcs changes no hook.
func funcTarget(v reflect.Value) string {
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return "unnamed func"
	}
	name := fn.Name()
	name = name[strings.LastIndex(name, "/")+1:]
	if m, ok := strings.CutSuffix(name, "-fm"); ok {
		return m
	}
	if closureName.MatchString(name) {
		return "closure"
	}
	return name
}
