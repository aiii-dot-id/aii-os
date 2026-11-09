import { sendConfigChanges } from './save.js';
import { named, speakersReadback } from './voice.js';
import { S } from '../../state.js';
import { esc } from '../../util.js';
import { echoMode, echoModes } from '../../voice-echo.js';
import W from '../../strings/settings-voice.js';
export function voiceModeCard(sp, voiceChosen) {
  const m = sp.mode || { listen: 'interactive', speak: 'auto', revision: 0, name: 'interactive', set: false };
  const listen = m.listen || 'interactive';
  const speak = m.speak === 'on' || m.speak === 'off' ? m.speak : '';
  const radio = (name, value, cur, words, hint) => '<label class="settings-voice__radio"><input type="radio" name="' + name + '" id="' + name + '-' + (value || 'auto') + '" value="' + value + '"' + (cur === value ? ' checked' : '') + '>' +
    '<span class="settings-voice__radio-words"><span>' + esc(words) + '</span>' + (hint ? '<span class="settings-voice__hint">' + esc(hint) + '</span>' : '') + '</span></label>';
  return '<section class="settings-voice__part" id="sp-voice-mode" data-speech-section="speech_mode" aria-labelledby="sp-part-talk" aria-describedby="sp-talk-says">' +
    '<h3 class="settings-voice__h3" id="sp-part-talk">' + esc(W.talk) + '</h3><div class="settings-voice__choices">' +
    '<fieldset class="settings-voice__choice"><legend class="settings-voice__legend">' + esc(W.micLegend) + '</legend>' +
    radio('sp-mode-listen', 'interactive', listen, named(W.micYouSpeak), W.micYouSpeakHint) +
    radio('sp-mode-listen', 'meeting', listen, W.micMeeting, named(W.micMeetingHint)) +
    radio('sp-mode-listen', 'off', listen, W.micOff, W.micOffHint) + '</fieldset>' +
    '<fieldset class="settings-voice__choice"><legend class="settings-voice__legend">' + esc(W.spokenLegend) + '</legend>' +
    radio('sp-mode-speak', '', speak, W.spokenWhenSpoke, voiceChosen ? W.spokenWhenSpokeChosen : W.spokenWhenSpokeHint) +
    radio('sp-mode-speak', 'on', speak, W.spokenAlways) +
    radio('sp-mode-speak', 'off', speak, W.spokenNever) + '</fieldset></div></section>';
}

const ECHO = { browser: W.echoBrowser, combined: W.echoBoth, native: W.echoNative };
export function echoCard() {
  const words = ([value, label]) => ECHO[value] || label;
  return '<div class="settings-voice__group settings-voice__field"><label class="settings-voice__label" for="sp-echo-mode">' + esc(named(W.echo)) + '</label>' +
    '<select id="sp-echo-mode">' + echoModes.map(m => '<option value="' + m[0] + '"' + (echoMode() === m[0] ? ' selected' : '') + '>' + esc(words(m)) + '</option>').join('') + '</select>' +
    '<p class="settings-voice__hint">' + esc(named(W.echoHint)) + ' Push-to-talk uses browser cancellation.</p>' +
    '<p class="settings-voice__hint" id="sp-echo-readback">Saved: ' + esc(words(echoModes.find(([v]) => v === echoMode()))) + '</p></div>';
}

export function myVoiceCard(sp) {
  const voices = sp.voices || [], named_ = sp.operator_speakers || [];
  const row = v => '<div class="settings-voice__voice-row" data-my-voice-row="' + esc(v.uuid) + '">' +
    '<b data-my-voice-label>' + esc(v.label || 'no label') + '</b>' +
    '<span class="settings-voice__id">' + esc(v.uuid) + '</span>' +
    '<span class="settings-voice__hint" data-my-voice-state>' + (v.mine ? 'named as your voice' : v.enrolled ? 'an enrolled voice' : v.heard ? 'a voice it has heard' : '') + '</span>' +
    (v.mine ? '<button type="button" class="btn ghost" data-my-voice="remove" data-uuid="' + esc(v.uuid) + '">' + esc(W.notYours) + '</button>'
      : v.enrolled || v.heard ? '<button type="button" class="btn ghost" data-my-voice="name" data-uuid="' + esc(v.uuid) + '">' + esc(W.markYours) + '</button>' : '') +
    (v.why_not ? '<div class="settings-voice__hint" data-my-voice-why style="flex-basis:100%">' + esc(v.why_not) + '</div>' : '') + '</div>';
  return '<section class="settings-voice__group" id="sp-my-voice" aria-labelledby="sp-my-voice-title"><h4 class="settings-voice__label" id="sp-my-voice-title">' + esc(W.yourVoice) + '</h4>' +
    '<p class="settings-voice__hint">' + esc(named(W.yourVoiceHint)) + ' A label is what an engine or an identity called a voice; it is not proof of who spoke.</p>' +
    (named_.length ? '' : '<p class="settings-voice__hint" id="sp-my-voice-none">No voice is named as yours. Until you name it here, what a voice session hears counts as a participant\'s words, not yours. Push-to-talk keeps your role.</p>') +
    voices.map(row).join('') +
    (voices.length ? '' : '<div class="settings-voice__hint" id="sp-my-voice-empty">No speech engine has reported a speaker UUID in this identity\'s record yet.</div>') +
    (sp.voices_cut ? '<div class="settings-voice__hint">Voices heard longer ago are not listed.</div>' : '') +
    '</section>';
}

export function nameMyVoice(uuid, add) {
  const named = ((S.config && S.config.speech && S.config.speech.operator_speakers) || []).filter(id => id !== uuid);
  if (add) named.push(uuid);
  sendConfigChanges('speech_my_voice', { 'speech.operator_speakers': named.length ? named : null });
}

export function speakersCard(sp) {
  const p = sp.speakers || { mode: 'all', uids: [], unidentified: 'deliver', revision: 0 };
  const mode = p.mode || 'all';
  const opt = (v, label) => '<option value="' + v + '"' + (mode === v ? ' selected' : '') + '>' + esc(label) + '</option>';
  const unid = p.unidentified || 'deliver';
  return '<section class="settings-voice__group" id="sp-speakers" data-speech-section="speech_speakers" aria-label="' + esc(named(W.heard)) + '">' +
    '<div class="settings-voice__field"><label class="settings-voice__label" for="sp-speakers-mode">' + esc(named(W.heard)) + '</label>' +
    '<select id="sp-speakers-mode">' + opt('all', W.heardEveryone) + opt('only', W.heardOnlyListed) + opt('ignore', W.heardExceptListed) + '</select>' +
    '<p class="settings-voice__hint">' + esc(named(W.heardHint)) + '</p></div>' +
    '<div class="settings-voice__field" id="sp-speakers-list-row"' + (mode === 'all' ? ' hidden' : '') + '><label class="settings-voice__label" for="sp-speakers-uids">' + esc(W.speakers) + '</label>' +
    '<input type="text" id="sp-speakers-uids" value="' + esc((p.uids || []).join(' ')) + '"><p class="settings-voice__hint">' + esc(W.speakersHint) + ' Provisional or unresolved speakers do not match an allow-list. Identification does not grant authority.</p></div>' +
    '<div class="settings-voice__field" id="sp-speakers-unid-row"' + (mode === 'ignore' ? '' : ' hidden') + '><label class="settings-voice__label" for="sp-speakers-unid">' + esc(W.unidentified) + '</label><select id="sp-speakers-unid">' +
    '<option value="deliver"' + (unid === 'deliver' ? ' selected' : '') + '>Delivered, marked unidentified</option>' +
    '<option value="withhold"' + (unid === 'withhold' ? ' selected' : '') + '>Withheld</option></select></div>' +
    '<p class="settings-voice__hint" id="sp-speakers-readback">' + esc(speakersReadback(p)) + '</p></section>';
}
