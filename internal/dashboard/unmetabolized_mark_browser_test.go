//go:build !windows

package dashboard

import "testing"

// THE MARK IS SHOWN ON THE NOTE WHEREVER THE PAGE LISTS IT. A note its
// author kept out of DREAM and CONSOLIDATE is a row like its neighbours
// (About → Experiences, step 11; Home's "Recently" became About → Between
// you, step 10), with its words, and the row says what the mark is. An
// earlier page left such a note out and printed a count. Driven through
// the real modules in every engine.
func TestThePagesMarkANoteKeptOutOfDreamAndConsolidate(t *testing.T) {
	page := `<!doctype html><div id="experiences-stack"></div><script type="module">
import {S} from '/state.js';import {renderExperiences} from '/views/identity/experiences.js';import {run} from '/__harness.js';
run(assert=>{
 const mark='Kept out of their reflection by Fixture.'; // the annex's keptOut
 S.stats={name:'Fixture'};S.identityExists=true;S.connected=true;S.projects=[];
 S.identity={beliefs:[],intentions:[],experiences:[{id:'x1',content:'an ordinary day',provenance:'self'},{id:'x2',content:'a very hard day',provenance:'self',unmetabolized:true}]};
 S.work={live:[],queued:0,delivered:[]};
 S.view='experiences';renderExperiences();
 const rows=[...document.querySelectorAll('#experiences-stack .experience')].map(e=>e.textContent);
 assert(rows.some(r=>r.includes('a very hard day')&&r.includes(mark)),'Experiences shows the marked note with its words and its mark: '+JSON.stringify(rows));
 assert(rows.filter(r=>r.includes(mark)).length===1,'Experiences marks the one note and no other: '+JSON.stringify(rows));
 const kept=document.querySelector('#experiences-stack .experience__kept');
 assert(kept&&/Recorded and recalled like any other/.test(kept.textContent),'the mark says what it is and is not: '+(kept&&kept.textContent));
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js":  []byte(`export const sent=[];export function send(){return 'r';}export function query(){return 'r';}export function wsReady(){return true;}`),
		"/app.js": []byte(`export function go(){}`),
	})
}
