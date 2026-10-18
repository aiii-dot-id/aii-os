//go:build !windows

package dashboard

import "testing"

const utilHelpersPage = `<!doctype html>
<script type="module">
import { bytes, composing, b64, loadingCard } from './util.js';
import { assert, run } from './__harness.js';
run(() => {
  const cases = [[0, '0 B'], [512, '512 B'], [1023, '1023 B'], [1024, '1.0 KB'], [2048, '2.0 KB'], [10240, '10 KB'],
    [530611, '518 KB'], [5033165, '4.8 MB'], [214000000, '204 MB'], [3113851290, '2.9 GB'], [32212254720, '30 GB']];
  for (const [n, want] of cases) assert(bytes(n) === want, 'bytes(' + n + ') is ' + JSON.stringify(bytes(n)) + ', want ' + JSON.stringify(want));
  assert(composing({ isComposing: true }) && composing({ keyCode: 229 }) && !composing({ key: 'Enter', keyCode: 13 }), 'composing does not tell an IME confirm from Enter');
  const buf = new Uint8Array([0, 1, 2, 250, 255, 104, 105]).buffer;
  assert(b64(buf) === 'AAEC+v9oaQ==', 'b64 is ' + b64(buf));
  assert(b64(new ArrayBuffer(0)) === '', 'b64 of nothing is ' + JSON.stringify(b64(new ArrayBuffer(0))));
  assert(loadingCard('backups') === '<div class="card"><div class="empty">loading backups…</div></div>', 'loadingCard is ' + loadingCard('backups'));
});
</script>`

func TestThePagesSmallHelpersAnswerOnceInBrowser(t *testing.T) {
	runPageInEngines(t, utilHelpersPage, map[string][]byte{})
}
