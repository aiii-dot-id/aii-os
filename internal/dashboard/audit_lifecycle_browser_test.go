// .
// .
// .
// .
// .
// .
// .
// .
// .

//go:build !windows

package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLifecycleFailedUpdateShowsActualBackendReason(t *testing.T) {
	dir := os.Getenv("AIII_AUDIT_LIFECYCLE_DIR")
	if dir == "" {
		dir = filepath.Join("testdata", "lifecycle")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "failed-update.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Installed []PluginView `json:"installed"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Installed) != 1 || doc.Installed[0].Lifecycle == nil || doc.Installed[0].Lifecycle.Refusal == nil {
		t.Fatal("fixture lacks actual failed update evidence")
	}
	page := `<!doctype html><div class="app"><div class="stack" id="plugins-stack"></div></div><script type="module">
 import { assert, run } from './__harness.js'; import { S } from './state.js'; import { renderPlugins } from './views/plugins.js';
 run(()=>{S.config={plugins:__BACKEND__};renderPlugins();const el=document.getElementById('plugins-stack');const text=el.textContent;
 const life=S.config.plugins.installed[0].lifecycle;assert(life.state==='active','predecessor remains active');
 assert(text.includes('generation 1 active'),'still-serving predecessor shown');
 assert(text.includes(life.refusal.cause),'actual failed replacement reason must be visible: '+text);
 assert(text.includes(life.refusal.class+' at '+life.refusal.stage),'refusal class and stage must be visible: '+text);
 assert(text.includes('tried again at'),'actual retry timing preserved');});</script>`
	page = strings.Replace(page, "__BACKEND__", string(raw), 1)
	runPageInEngines(t, page, map[string][]byte{
		"/views/settings.js": []byte(`export function saveConfigSection(){}; export function savebarHTML(){return ''}; export function sendConfigChanges(){}; export function configFeedbackHTML(){return ''}`),
		"/ws.js":             []byte(`export function send(){}`),
	})
}
