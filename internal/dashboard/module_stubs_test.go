//go:build !windows

package dashboard

// browserModuleStubs is the shared inert base for pages that boot the
// REAL app.js. app.js itself, state.js, util.js, views/projects.js and
// pending.js are deliberately absent — the rig's unmapped-path
// fall-through serves the real shipped files for those, so the module
// under test is always the artifact. The base stubs everything else
// app.js imports, with each module's full export list.
//
// WHY A SHARED BASE. Four browser tests hand-copied this same map, and
// the copies forked: one had a ws.js stub with no __sent export the
// moment another test needed it; app.js gaining an import meant the
// same edit in four places, and one missed copy was the silent module
// death that cost a diagnosis turn. The base makes the fix one edit in
// one place, by construction — the fork pattern removed, not policed.
//
// The ws.js entry here is inert (throwing on accidental use) so a test
// that forgets to override it with the recording stub fails loudly at
// the exact call site rather than passing against a socket that lies.
// offerSettingsJS is what views.js does when Settings first opens (step 21):
// it offers Settings, Plugins and the cards they draw to the socket, which
// reaches them through later(). A page that draws them itself, without
// opening Settings, offers them itself with this, as the socket once
// imported them.
const offerSettingsJS = `{ const { offer } = await import('/shell/later.js');
  (await import('/state.js')).S.settingsHeld = null; // this stands in for Settings' opening, sheets and all
  for (const p of ['/views/settings.js', '/views/plugins.js', '/views/plugin-setting.js', '/sandbox.js', '/tools.js', '/views/backups.js', '/views/messages.js', '/views/mail-repair.js', '/views/expert.js']) offer(await import(p)); }
`

func browserModuleStubs() map[string][]byte {
	return map[string][]byte{
		"/ws.js": []byte(`export function send() { throw new Error('ws.js: override me with wsRecordingStub'); }
export function query() { throw new Error('ws.js: override me'); }
export function connect() {}
export function wake() {}
`),
		"/bridge.js":                     stubModule("send", "query"),
		"/firstboot.js":                  stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost", "discoveryAnswered", "discoveryFailed"),
		"/panel.js":                      stubModule("renderPanel"),
		"/presence.js":                   stubModule("renderPresence", "setThinking"),
		"/sections.js":                   stubModule("initSections", "sectionTitle", "publish", "onSections", "onLayout", "onTokensChanged"),
		"/theme.js":                      stubModule("onTheme"),
		"/overlay.js":                    stubModule("restoreDraft", "onOverlayChanged", "holdReloadWhile", "reloadForTheOverlay"),
		"/voice.js":                      stubModule("wireMic", "bindTransport", "speak", "render", "connectionLost"),
		"/views/chat.js":                 stubModule("scrollThread", "addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderInteractionPages", "renderHistory", "renderChatSubstrate", "renderComposer", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":                 stubModule("renderHome"),
		"/views/work.js":                 stubModule("renderWorkPill"),
		"/views/memory.js":               stubModule("renderMemory"),
		"/views/identity.js":             stubModule("renderIdentity"),
		"/views/identity/between.js":     stubModule("renderBetween"),
		"/views/plugins.js":              stubModule("renderPlugins", "rejectPluginAction", "applyPluginStored"),
		"/views/record.js":               stubModule("renderRecord", "readRest", "payloadBox"), // step 19\'s sent panel imports the record\'s readers
		"/views/settings.js":             stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "acceptSpeechLists", "rejectSpeechLists", "acceptAccessCode", "rejectAccessCode", "settingsConnectionLost"),
		"/views/identity/record.js":      stubModule("renderRecord", "readRest", "payloadBox"),
		"/views/identity/experiences.js": stubModule("renderExperiences"),
	}
}
