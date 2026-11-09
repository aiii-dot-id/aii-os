package dashboard

// The local-model checkbox's visibility rule, executed (node): the
// checkbox is present only when a local model is available. The rule lives in
// views/settings/local-rule.js; this runs the embedded copy, with the two
// modules it imports beside it at the same relative paths. Same skip rule
// as the overlay contract when node is absent.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const localRuleDriver = `
import { localCheckbox } from './views/settings/local-rule.js';
import L from './strings/settings-local.js';

let failures = 0;
function check(cond, msg) { if (!cond) { console.error('FAIL: ' + msg); failures++; } }

const usable = { marked: 1, provider: 'omlx', model: 'qwen3-8b' };

// a. a local model answers and routing can use it: shown, naming it.
let r = localCheckbox(usable, false);
check(r.show === true, 'a: usable local model, box off: not shown');
check(!r.warn, 'a: usable local model, box off: warned');
check(r.line.includes('omlx') && r.line.includes('qwen3-8b'), 'a: line does not name the model sub-agents would use: ' + r.line);
r = localCheckbox(usable, true);
check(r.show === true && !r.warn, 'a: usable local model, box on: not shown, or warned');
check(r.line.includes('will use') && r.line.includes('qwen3-8b'), 'a: box on does not say what sub-agents will use: ' + r.line);

// b. nothing usable, but the box is on: shown, with a warning.
r = localCheckbox(null, true);
check(r.show === true && r.warn === true, 'b: box on, the server reports no local model: hidden or not warned');

// e. no report yet (undefined): nothing is known, so no outage is claimed
// (a review 2026-11-06 13:31Z #3). Box on: shown, saying it is checking, not
// warned. Box off: not shown.
r = localCheckbox(undefined, true);
check(r.show === true && r.warn === false && r.line === L.checking, 'e: box on, no report yet: ' + JSON.stringify(r));
check(localCheckbox(undefined, false).show === false, 'e: box off, no report yet: shown');

// f. the warnings name the roles that keep their own model, as the
// healthy line does: an explicit route wins over the box (a review 13:31Z #4).
r = localCheckbox({ marked: 1 }, true, ['critic']);
check(r.warn && r.line.startsWith(L.notAnswering) && r.line.includes('The critic role keeps its own model.'), 'f: not answering, own-route role not named: ' + r.line);
r = localCheckbox({ marked: 1, provider: 'omlx', problem: 'no model chosen' }, true, ['critic', 'judge']);
check(r.warn && r.line.includes('no model chosen') && r.line.includes('Roles with their own model keep it: critic, judge.'), 'f: cannot use, own-route roles not named: ' + r.line);
// Both counts in both warnings (a review 13:52Z): one role and several.
r = localCheckbox({ marked: 1 }, true, ['critic', 'judge']);
check(r.warn && r.line.startsWith(L.notAnswering) && r.line.includes('Roles with their own model keep it: critic, judge.'), 'f: not answering, two own-route roles not named: ' + r.line);
r = localCheckbox({ marked: 1, provider: 'omlx', problem: 'no model chosen' }, true, ['critic']);
check(r.warn && r.line.includes('no model chosen') && r.line.includes('The critic role keeps its own model.'), 'f: cannot use, one own-route role not named: ' + r.line);
r = localCheckbox({ marked: 1 }, true, []);
check(r.line === L.notAnswering, 'f: no own-route roles, yet the warning says more: ' + r.line);
r = localCheckbox({ marked: 1 }, true);
check(r.show === true && r.warn === true && r.line.includes('main model'), 'b: box on, local server silent: ' + JSON.stringify(r));
r = localCheckbox({ marked: 1, provider: 'omlx', problem: 'no model chosen' }, true);
check(r.show === true && r.warn === true && r.line.includes('no model chosen'), 'b: box on, server answers but unusable: ' + JSON.stringify(r));

// c. nothing usable and the box is off: not shown.
check(localCheckbox(null, false).show === false, 'c: no answer yet, box off: shown');
check(localCheckbox({ marked: 0 }, false).show === false, 'c: no local entry, box off: shown');
check(localCheckbox({ marked: 1 }, false).show === false, 'c: local entry silent, box off: shown');
check(localCheckbox({ marked: 1, provider: 'omlx', problem: 'no model chosen' }, false).show === false, 'c: answers but unusable, box off: shown');
check(localCheckbox({ marked: 1, provider: 'omlx' }, false).show === false, 'c: answers with no model, box off: shown');

// d. roles given their own model keep it: the line names them, and never
// the file that holds them (host 0217 #5).
r = localCheckbox(usable, true, []);
check(!r.line.includes('config.json') && !r.line.includes('keep'), 'd: no own-route roles, yet the line says more: ' + r.line);
r = localCheckbox(usable, true, ['critic']);
check(r.line.includes('The critic role keeps its own model.') && !r.line.includes('config.json'), 'd: one own-route role not named: ' + r.line);
r = localCheckbox(usable, false, ['critic', 'judge']);
check(r.line.includes('Roles with their own model keep it: critic, judge.'), 'd: two own-route roles not named: ' + r.line);

if (failures) process.exit(1);
console.log('LOCAL-RULE-OK');
`

func TestLocalModelCheckboxRule(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — local-model checkbox rule check skipped")
	}
	tmp := t.TempDir()
	files := []string{"views/settings/local-rule.js", "util.js", "strings/settings-local.js"}
	for _, f := range files {
		raw, rerr := staticFS.ReadFile("static/" + f)
		if rerr != nil {
			t.Fatalf("read embedded %s: %v", f, rerr)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(tmp, "driver.js")
	if err := os.WriteFile(driver, []byte(localRuleDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, driver).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "LOCAL-RULE-OK") {
		t.Fatalf("local-model checkbox rule failed (%v):\n%s", err, out)
	}
}
