/* theme.js — applies the operator's theme.json tokens (T0 tier).

   WHY setProperty AND NOT A <style> BLOCK. The design draft said
   "validated <style> injection". That is the weaker mechanism, and
   the difference is structural, not stylistic: building a stylesheet
   means building a STRING with `name: value;` in it, so a value that
   smuggles `;` or `}` escapes its declaration and writes rules the
   validator never saw. element.style.setProperty(name, value) cannot
   do that — it sets exactly one property, and a value the browser
   dislikes is DROPPED, not reinterpreted. So the server's allowlist
   is the policy and setProperty is the structural floor under it:
   even if the allowlist were wrong, there is no declaration to break
   out of. Two independent reasons the page cannot be rewritten by a
   theme file.

   Tokens land on :root, which is exactly where theme.css defines
   them, so a token overrides its compiled default by the ordinary
   cascade — no !important, no specificity war.

   Absent/invalid theme = the server sends null and every previously
   applied token is REMOVED, restoring the compiled defaults. That is
   the deletion-is-a-statement rule the layout already follows. */

let applied = [];   // token names currently set by us, so we can unset them

/* onTheme receives the server's already-validated payload:
   {v:1, tokens:{...}} or null. The server is the boundary; this is
   the applier. */
export function onTheme(payload) {
  const root = document.documentElement;

  // Remove what we set last time FIRST, so a token dropped from the
  // file actually disappears instead of lingering from the old apply.
  for (const name of applied) root.style.removeProperty(name);
  applied = [];

  if (!payload || !payload.tokens) return;  // absent = compiled defaults

  for (const name in payload.tokens) {
    const value = payload.tokens[name];
    // Defence in depth: the server validated these, but this module
    // must be safe on its own terms — a name that is not a custom
    // property is refused here too, so a future caller cannot use
    // this to set real CSS properties like `position`.
    if (typeof name !== 'string' || !name.startsWith('--')) continue;
    if (typeof value !== 'string') continue;
    root.style.setProperty(name, value);
    applied.push(name);
  }
}
