export const PLACES = Object.freeze(['chat', 'projects', 'identity', 'settings']);

export const VIEWS = Object.freeze({
  chat: { place: 'chat' },
  projects: { place: 'projects' },
  identity: { place: 'identity', tab: 'who' },
  between: { place: 'identity', tab: 'between' },
  memory: { place: 'identity', tab: 'beliefs' },
  experiences: { place: 'identity', tab: 'experiences' },
  record: { place: 'identity', tab: 'record' },
  status: { place: 'settings', tab: 'status' },
  phone: { place: 'settings', tab: 'phone' },
  settings: { place: 'settings', tab: 'model' },
  plugins: { place: 'settings', tab: 'permissions' },
  technical: { place: 'settings', tab: 'technical' },
});

export const RAIL = Object.freeze(['chat', 'projects', 'identity', 'record', 'plugins', 'settings']);
export const railOf = v => (RAIL.includes(v) ? v : (VIEWS[v] && VIEWS[v].place) || '');

export const TABS = Object.freeze({
  identity: Object.freeze([['who', 'identity'], ['between', 'between'], ['beliefs', 'memory'], ['experiences', 'experiences'], ['record', 'record']]),
  settings: Object.freeze([['status', 'status'], ['phone', 'phone'], ['model', 'settings'], ['voice', 'settings'], ['permissions', 'plugins'],
    ['people', 'settings'], ['access', 'settings'], ['backups', 'settings'], ['app', 'settings'], ['technical', 'technical']]),
});

export const THREAD_VIEWS = Object.freeze(['chat']);
export const ownsThread = v => THREAD_VIEWS.includes(v);

export const REDIRECTS = Object.freeze({
  home: '#/chat',
  memory: '#/identity/beliefs',
  record: '#/identity/record',
  plugins: '#/settings/permissions',
});

const STATUS = '#/settings, Status';
const NOTE = 'every page, AII OS\'s note (#page-note)';
const ABOUT = '#/identity/who, About them';
const TECH = '#/settings/technical/verification, the state of their record';
const MIC = 'every page, the microphone and its menu (#mic)';
export const FIELDS = Object.freeze({
  StatsResponse: Object.freeze({ row: 'Status and continuity', fields: Object.freeze({
    name: 'every page, the top bar and their name in its words',
    belief_count: STATUS + ' · Continuity, Details', reflection_count: STATUS + ' · Continuity, Details',
    experience_count: STATUS + ' · Continuity, Details', intention_count: STATUS + ' · Continuity, Details',
    ledger_seq: STATUS + ' · Continuity', lifetime_ticks: STATUS + ' · Continuity, Details (as lifetime)',
    uptime: STATUS + ' · Flow, Details', foreground_holds: STATUS + ' · Flow, Details',
    malformed_calls: STATUS + ' · Tools, Details', suspicious_paths: STATUS + ' · Tools, Details',
    duplicate_arg_keys: STATUS + ' · Tools, Details',
    version: STATUS + ' · Trust, Details', build: STATUS + ' · Trust, Details',
    voice_max_frame_bytes: MIC + ': the bound on each frame it sends',
    update: 'every page, the update notice (#update-chip); #/settings/app',
    plugin_updates: STATUS + ' · Tools; counted beside Plugins and permissions',
    uninstall_offered: '#/settings/app, Uninstall',
    credential_warning: NOTE + '; ' + STATUS + ' · Trust, Details',
    last_turn: STATUS + ' · Context; Model and cost, Details',
    accounted: STATUS + ' · Model and cost (the month is read again when it moves)',
    voice: MIC, voice_engine: MIC, voice_state: MIC,
    voice_reason: MIC + ': why its service cannot be reached',
    voice_source: MIC, voice_listen: MIC, voice_speak: MIC, voice_mode_revision: MIC,
    reply_voice: MIC, voice_speaker: MIC, speakers: MIC,
    expert: 'retired (step 4): read only by an identity\'s own copy of views/expert.js',
    hosted_on_phone: 'every page, the state line; #/settings/access, the public name',
    notices: '#/settings/people, where your notices go',
  }) }),
  ContinuityState: Object.freeze({ row: 'Status and continuity', fields: Object.freeze({
    ledger_seq: STATUS + ' · Continuity', anchored_seq: TECH, witnessed_at: STATUS + ' · Continuity',
    unanchored: TECH, witness_url: STATUS + ' · Continuity, Details (verified by)',
    lifetime_ticks: STATUS + ' · Continuity, Details', mode: NOTE + '; ' + STATUS + ' · Trust',
    safe_reason: NOTE + '; ' + STATUS + ' · Trust', degraded_since: NOTE, degraded_why: TECH,
    safe_cleared: TECH, serving_unkept: '#/chat, the state of their record',
    review_at: TECH, review_status: TECH, review_issues: TECH, review_lines: TECH,
  }) }),
  IdentityState: Object.freeze({ row: 'The identity', fields: Object.freeze({
    beliefs: '#/identity/beliefs; ' + ABOUT + ' · What they hold most firmly',
    intentions: ABOUT + ' · Working on', experiences: '#/identity/experiences, the newest 50',
    synthesis: ABOUT + ' · How they describe themselves', brief: ABOUT + ' · Their notes to themselves for today',
    charter: ABOUT + ' · What you have agreed', approval: ABOUT + ' · What you have agreed, the words it rests on',
    trust_level: ABOUT + ' · How far they act on their own', autonomy_level: ABOUT + ' · How far they act on their own',
    relationships: '#/identity/between, Others they work with', meaning: '#/identity/beliefs, under the beliefs until Settings → Model',
    retired: '#/identity/beliefs · All: the rows "Revised" and "No longer held", each with when and what replaced it (step 9\'s follow-up)',
    retired_more: '#/identity/beliefs · All: "{n} more on the Record page" (step 9\'s follow-up)',
    note: '#/chat, their note to you at the conversation\'s head, with what did not succeed beside it',
  }) }),
  ExperienceItem: Object.freeze({ row: 'The identity', fields: Object.freeze({
    id: '#/identity/experiences (each by its id)', content: '#/identity/experiences, in words and as written',
    category: '#/identity/experiences, its kind and the Kind filter', created_at: '#/identity/experiences, when, and the Date filter',
    provenance: '#/identity/experiences, who wrote it, and its filter', operator_verbatim: '#/identity/experiences, "You, quoted" only where verbatim',
    unmetabolized: '#/identity/experiences, "Kept out of their reflection by {name}", and its filter',
    created_seq: '#/identity/experiences, "Copy link" to its record entry',
  }) }),
  IntentionItem: Object.freeze({ row: 'The identity', fields: Object.freeze({
    id: ABOUT + ' · Working on (each by its id)', statement: ABOUT + ' · Working on, in their voice',
    state: ABOUT + ' · Working on (done and abandoned ones are not)',
  }) }),
  RelationshipItem: Object.freeze({ row: 'The identity', fields: Object.freeze({
    id: '#/identity/between, each relationship', name: '#/identity/between, "with"', role: '#/identity/between, "role"',
    charter: '#/identity/between, its text', approval: '#/identity/between, the words it rests on',
    record: '#/identity/between, its entry, opening the record',
  }) }),
  CharterApproval: Object.freeze({ row: 'The identity', fields: Object.freeze({
    state: ABOUT + ' and #/identity/between, said in words', excerpt: ABOUT + ' · your words, quoted whole',
    chars: ABOUT + ' · "{chars} characters, quoted whole"', turn: ABOUT + ' · "turn {turn}"',
    said_at: ABOUT + ' · its date', record: ABOUT + ' · "Signed in record entry {seq}"', error: ABOUT + ' · why it could not be read',
  }) }),
});
