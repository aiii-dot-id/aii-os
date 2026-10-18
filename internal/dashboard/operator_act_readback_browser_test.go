//go:build !windows

package dashboard

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestOperatorActReadbackKeepsTheResultOutOfOperatorSpeech(t *testing.T) {
	s, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecordToolStart("act", 1, "operator", "", "", "pl_fixture_run", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDoneMeasured("act", 1, "pl_fixture_run", "{}", "PLUGIN_RESULT <img src=x onerror=alert(1)>", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	decision, err := s.RecordConversation(t.Context(), "operator", "confirmed run", "act", interaction.Details{Channel: "plugin", Actor: "operator", Tool: "pl_fixture_run", Reason: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	rows.Identity = "fixture"
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := json.Marshal(decision.ID)
	page := quietChatMarkup + fmt.Sprintf(`<script type="module">
import {renderInteractionPages} from '/views/chat.js';import {run} from '/__harness.js';
run(assert=>{
 const page=%s, id=%s;
 const pages=new Map([['recorded',page],['transient',{identity:'fixture',incarnation:'memory',rows:[]}]]);
 renderInteractionPages(pages,()=>{},false);
 const decision=document.querySelector('[data-turn-id="'+id+'"]');
 assert(decision && decision.textContent.includes('confirmed run'),'operator decision disappeared');
 assert(!decision.textContent.includes('PLUGIN_RESULT'),'plugin text rendered as operator speech');
 document.querySelectorAll('details').forEach(box=>box.open=true);
 const result=document.querySelector('.tool-ev');
 assert(result && result.textContent.includes('PLUGIN_RESULT'),'linked tool result not available in readback');
 const content=[...result.querySelectorAll('.record-content')].find(el=>el.textContent.includes('PLUGIN_RESULT'));
 assert(content && content.getBoundingClientRect().height>0,'result is only hidden metadata');
 assert(!result.querySelector('img'),'plugin output became executable markup');
 renderInteractionPages(pages,()=>{},false);
 assert(document.querySelectorAll('.tool-ev').length===1,'refresh duplicated the execution');
});</script>`, raw, id)
	runPageInEngines(t, page, map[string][]byte{
		"/app.js":                []byte(`export function toast(){}`),
		"/ws.js":                 []byte(`export function wsReady(){return true;}export function send(){}export function query(){}`),
		"/presence.js":           []byte(`export function setThinking(){}export function toolPulse(){}`),
		"/views/model-picker.js": []byte(`export function fillModelPicker(){}`),
	})
}
