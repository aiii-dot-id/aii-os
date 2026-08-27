/* state.js — the one client-side state object every surface reads.
   Owns: S (socket/status/continuity/identity/config/sandbox/work/
   projects/providers + view + transient UI timers). R66: this grows
   into the frame's projection store — the source the bridge's
   data.subscribe(topic) reads from; sections will consume projections
   of S, never S itself (UI_FRAME.md §4). */

export const S = {
  projectsPrimed: false, // first projects payload primes the dock (ws.js)
  view: 'chat', identityExists: false, connected: false,
  stats: null, cont: null, identity: null, tools: [], config: null, sandbox: null, work: null,
  /* Two variables, because they answer two different questions.
     activeProject is server truth: the project the identity is working
     in, the one that stamps turns and work sessions. viewedProject is
     this browser's own attention: which project's page is on screen.
     They were one field, and every control on the page carried the
     ACTIVE project's id while the operator believed they were looking
     at another — so an edit aimed at a new project landed on the old
     one. Browsing is free; working somewhere is an explicit act.
       viewedProject === null  -> nothing chosen yet, follow the active one
       viewedProject === ''    -> the operator asked for the new-project page
       viewedProject === '<id>'-> that project's page, active or not */
  projects: [], activeProject: null, viewedProject: null, dockFilter: '',
  focusDraft: null, // {id,val} the operator is still typing; outlives the save it belongs to (pending.js owns the wait)
  providers: [], providersLoaded: false,
  thinking: false, toolBusyTimer: null, reconnectTimer: null,
  /* R74 door state. wsEverOpened tells "server down" apart from
     "token refused": a socket that has NEVER opened this page load
     while a token cookie exists means the stored token is stale and
     the operator must be re-asked (D76). tokenPrompted keeps that
     ask to one per page load. */
  tokenPrompted: false, wsEverOpened: false,
  logsList: null, logTail: null, logFile: '', // Settings→Logs viewer state
  overlays: [], // W2: frame overlay outcomes (accepted/rejected/inert), frame furniture via ui.overlay
  /* ANSWER THE WAY YOU WERE ASKED. Set when the operator speaks, cleared
     when they type — so a spoken question gets a spoken answer and a
     typed one does not suddenly start talking. No setting to find and
     none to forget, which is why this is a fact about the last input
     rather than a preference. */
  voiceSpeak: false,
};
