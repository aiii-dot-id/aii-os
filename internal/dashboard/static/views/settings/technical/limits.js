import { drawForm, fieldHTML, formHTML, groupHTML, hint, moreHTML, named, say, settingHTML, settingOf } from './forms.js';
import { S } from '../../../state.js';
import W from '../../../strings/settings-technical-forms.js';

const L = W.limits;
export const LIMIT_GROUPS = [
  [L.groupTurn, [['agency.turn_token_budget', 'turnTokens'], ['agency.turn_seconds_local', 'turnSecondsLocal'], ['agency.tool_schema_refusals', 'toolsLeftOut'], ['agency.turn_checkpoint_calls', 'checkpointCalls'], ['agency.turn_checkpoint_slack', 'checkpointSlack'], ['agency.turn_checkpoint_grace_rounds', 'graceRounds'], ['agency.max_turn_continuations', 'continuations'], ['agency.continuation_wall_seconds', 'continuationWall'], ['agency.alarm_wall_seconds', 'alarmWall'], ['agency.harvest_wall_seconds', 'harvestWall'], ['agency.max_tool_rounds', 'maxRounds'], ['agency.max_tool_calls', 'maxToolCalls']]],
  [L.groupHelpers, [['agency.subagent_max_tool_rounds', 'helperRounds'], ['agency.subagent_max_tool_calls', 'helperCalls'], ['agency.subagent_max_legs', 'helperLegs'], ['agency.subagent_wall_seconds', 'helperWall'], ['agency.subagent_wall_seconds_local', 'helperWallLocal']]],
  [L.groupKept, [['agency.work_line_max_chars', 'workLineMax'], ['prompt.standing_max_chars', 'standingStateMax'], ['prompt.playbook_max_bytes', 'playbookShown'], ['record.tool_result_runes', 'toolResultKept'], ['prompt.max_tool_result_chars', 'toolResultShown']]],
  [L.groupProvider, [['llm.timeout_seconds', 'requestTimeout'], ['llm.retries', 'retries'], ['llm.retry_backoff_ms', 'retryPause'], ['llm.model_list_timeout_seconds', 'discoverTimeout'], ['llm.model_list_cache_seconds', 'providerAnswerStands']]],
  [L.groupPlugins, [['tools.webfetch_max_bytes', 'webFetchMax'], ['plugins.context_providers', 'pluginsRead'], ['plugins.context_timeout_ms', 'pluginContextTime'], ['plugins.runtime.stop_ms', 'pluginStopTime'], ['plugins.runtime.hand_over_ms', 'pluginUpdateWait'], ['plugins.runtime.session_control_ms', 'speechControlWait'], ['plugins.runtime.restart_attempts', 'pluginRestarts']]],
  [L.groupUpdates, [['updates.check_interval_minutes', 'updateCheckMinutes']]],
];
const LOCAL_ONLY = new Set(['agency.turn_seconds_local', 'agency.subagent_wall_seconds_local']);
const localSetUp = c => (S.providers || []).some(p => p.local && (p.status === 'ok' || p.name === c.llm?.provider || (c.agency?.roles || []).some(r => r.provider === p.name)));

export function drawLimits(root) {
  const c = S.config;
  if (!c) { root.dataset.form = ''; root.textContent = W.loading; return; }
  const fields = rows => rows.filter(([key]) => !LOCAL_ONLY.has(key) || localSetUp(c)).map(([key, word]) => {
    const set = settingOf(c, key);
    return set ? settingHTML(set, c, named(L[word]), named(L[word + 'Hint'] || ''), 'applies from the next turn') : '';
  }).join('');
  const word = w => L[w];
  const kept = groupHTML(L.groupKept,
    fieldHTML({ id: 'cfg-ptokens', label: L.promptBudget, about: L.promptBudgetHint, value: c.prompt.max_tokens || '', short: true, type: 'number', unit: 'tokens', attrs: ' min="0" step="1"' }) +
    fieldHTML({ id: 'cfg-pturns', label: L.recentTurns, about: L.recentTurnsHint, value: c.prompt.recent_turns, short: true, type: 'number', attrs: ' min="0" step="1"' }) +
    fields(LIMIT_GROUPS[2][1]) + moreHTML(L.groupKept, [['prompt.max_tokens', 'promptBudget'], ...LIMIT_GROUPS[2][1]], c, word));
  drawForm(root, 'limits', formHTML('limits', say(L.heading),
    hint(L.unlimitedNote) +
    LIMIT_GROUPS.map(([title, rows], i) => i === 2 ? kept : groupHTML(title, fields(rows) + moreHTML(title, rows, c, word))).join(''), L.applies));
}
