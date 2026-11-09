//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls the board's helpers, so it shares its build row.

package dashboard

import "testing"

// (i) A PROVIDER'S ROW STAYS INSIDE A PHONE'S WIDTH (host 1405, 1413; a review
// 1403: Model's page at 390 scrolled sideways, span.td 331 px wide at x
// 459.8). The row's address is one unbroken run in mono; .tool-row .td is a
// flex item, and a flex item's min-width:auto keeps it as wide as that run,
// so the row pushed past the page. At 390, on #/settings/model, a provider
// whose address is longer than the screen draws its row, and neither the row,
// its address, nor the page reaches past the screen's right edge.
//
// The provider comes from the server's answer, as on a real page: written
// into the page by hand, the next answer to "providers" (the local-model
// part asks once when it is first drawn) replaced it.
func TestModelProviderRowAtPhoneWidthStaysInside(t *testing.T) {
	// No hyphen or space in the address: nothing the engines break at unless told.
	const long = "https://inferencegatewayinternalexampleresearchcluster.test:8443/v1/chat/completions"
	dir := &ProviderDirectory{Providers: []ProviderInfo{
		{Name: "gateway", Chat: true, APIType: "openai", Endpoint: long, Models: []string{"m1", "m2"}, DefaultModel: "m1", Status: "ok"},
	}}
	runBoardProviders(t, 390, 844, dir, `
  const LONG = '`+long+`';
  await go('#/settings/model', () => shown(document.getElementById('view-settings')));
  if (!shown(document.getElementById('view-settings'))) return fail('#/settings/model at 390 drew no page');
  const row = () => document.querySelector('[data-prov-row="0"]');
  await until(() => shown(row()));
  if (!shown(row())) return fail('the provider row was not drawn on #/settings/model');
  const td = row().querySelector('.td');
  if (!td || !td.textContent.includes(LONG)) return fail('the row does not carry the address: ' + (td && td.textContent));
  const edge = window.innerWidth;
  const r = row().getBoundingClientRect(), d = td.getBoundingClientRect();
  if (r.right > edge + 0.5) return fail('the row reaches ' + r.right + ' px, past the screen at ' + edge);
  if (d.right > edge + 0.5) return fail('the address reaches ' + d.right + ' px (' + d.width + ' wide at x ' + d.left + '), past the screen at ' + edge);
  const page = document.scrollingElement, view = document.querySelector('.view.on');
  if (page.scrollWidth > page.clientWidth) return fail('the page scrolls sideways: ' + page.scrollWidth + ' wide in ' + page.clientWidth);
  if (view && view.scrollWidth > view.clientWidth) return fail(view.id + ' scrolls sideways: ' + view.scrollWidth + ' wide in ' + view.clientWidth);
`)
}
