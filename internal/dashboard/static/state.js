

export const S = {
  projectsPrimed: false,
  view: 'chat', identityExists: false, connected: false,
  stats: null, cont: null, identity: null, tools: [], config: null, sandbox: null, work: null,
  recall: null,

  projects: [], activeProject: null, viewedProject: null, dockFilter: '',
  focusDraft: null,
  providers: [], brokenProviders: [], skipSignInWithValidToken: true, providersLoaded: false,
  thinking: false, toolBusyTimer: null, reconnectTimer: null,

  tokenPrompted: false, wsEverOpened: false,
  logsList: null, logTail: null, logFile: '',
  overlays: [],
  asks: [],

  voiceSpeak: false,
};
