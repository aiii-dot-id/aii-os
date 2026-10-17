//go:build !windows

package dashboard

import "testing"

func TestThePagesMarkANoteKeptOutOfDreamAndConsolidate(t *testing.T) {
	page := `<!doctype html><div id="home-inner"></div><div id="memory-stack"></div><script type="module">
import {S} from '/state.js';import {renderHome} from '/views/home.js';import {renderMemory} from '/views/memory.js';import {run} from '/__harness.js';
run(assert=>{
 const mark='kept out of DREAM and CONSOLIDATE';
 S.stats={name:'Fixture'};S.identityExists=true;S.connected=true;S.projects=[];
 S.identity={beliefs:[],intentions:[],experiences:[{content:'an ordinary day',provenance:'self'},{content:'a very hard day',provenance:'self',unmetabolized:true}]};
 S.work={live:[],queued:0,delivered:[]};
 renderHome();
 const recent=[...document.querySelectorAll('#home-inner .recent-row')].map(e=>e.textContent);
 assert(recent.length===2,'Home lists both notes: '+JSON.stringify(recent));
 assert(recent.some(r=>r.includes('a very hard day')&&r.includes(mark)),'Home shows the marked note with its words and its mark: '+JSON.stringify(recent));
 assert(recent.filter(r=>r.includes(mark)).length===1,'Home marks the one note and no other: '+JSON.stringify(recent));
 S.view='memory';renderMemory();
 const rows=[...document.querySelectorAll('#memory-stack .item')].map(e=>e.textContent);
 assert(rows.some(r=>r.includes('a very hard day')&&r.includes(mark)),'the Memory page shows the marked note with its words and its mark: '+JSON.stringify(rows));
 assert(rows.filter(r=>r.includes(mark)).length===1,'the Memory page marks the one note and no other: '+JSON.stringify(rows));
 const chip=document.querySelector('#memory-stack .item .chip[title]');
 assert(chip&&/recorded, recalled and shown like any other/.test(chip.title),'the mark says what it is and is not');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js":  []byte(`export const sent=[];export function send(){return 'r';}export function query(){return 'r';}export function wsReady(){return true;}`),
		"/app.js": []byte(`export function go(){}`),
	})
}
