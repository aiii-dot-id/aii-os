//go:build !windows

// .
// .

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestTheControlsRowSitsUnderTheTextBoxInBrowser(t *testing.T) {
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	start := strings.Index(html, `<div class="composer-wrap" id="composer-wrap">`)
	end := strings.Index(html, `<section class="view" id="view-home">`)
	if start < 0 || end < start {
		t.Fatal("index.html has no composer-wrap before the home view")
	}
	fragment := html[start:end]
	fragment = fragment[:strings.LastIndex(fragment, "</section>")]
	page := `<!doctype html><style>` + string(css) + `</style>
<div style="width:1200px">
  <div class="thread-inner" id="column"><div style="height:1px"></div></div>
  ` + fragment + `
</div>
<script type="module">
import { assert, run } from './__harness.js';
run(() => {
  const r = el => el.getBoundingClientRect();
  const box = r(document.querySelector('.composer'));
  const row = r(document.querySelector('.composer-row'));
  const send = r(document.getElementById('send-btn'));
  const column = document.getElementById('column');
  const inner = r(column), pad = parseFloat(getComputedStyle(column).paddingLeft);
  assert(box.height < 56, 'the text box is not one line tall: ' + box.height + 'px');
  assert(row.top >= box.bottom - 0.5 && send.top >= box.bottom - 0.5, 'the controls are inside the text box: box ends at ' + box.bottom + ', row starts at ' + row.top);
  assert(Math.abs(row.left - box.left) < 1 && Math.abs(row.width - box.width) < 1, 'the row and the box are not one width: ' + row.width + ' vs ' + box.width);
  assert(Math.abs(box.width - (inner.width - 2 * pad)) < 1, 'the box is not as wide as the conversation column: ' + box.width + ' vs ' + (inner.width - 2 * pad));
  const more = document.getElementById('mic-more');
  more.hidden = true;
  assert(getComputedStyle(more).display === 'none', 'a hidden control in the row still shows');
});
</script>`
	runPageInEngines(t, page, nil)
}
