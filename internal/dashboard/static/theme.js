

let applied = [];

export function onTheme(payload) {
  const root = document.documentElement;

  for (const name of applied) root.style.removeProperty(name);
  applied = [];

  if (!payload || !payload.tokens) return;

  for (const name in payload.tokens) {
    const value = payload.tokens[name];

    if (typeof name !== 'string' || !name.startsWith('--')) continue;
    if (typeof value !== 'string') continue;
    root.style.setProperty(name, value);
    applied.push(name);
  }
}

import { S } from './state.js';

const THEME_KEY = 'aii.theme';
const lightMQ = (typeof window !== 'undefined' && window.matchMedia) ? window.matchMedia('(prefers-color-scheme: light)') : null;

export function themeChoice() {
  let v = null;
  try { v = localStorage.getItem(THEME_KEY); } catch (e) { v = null; }
  return v === 'light' || v === 'dark' ? v : 'system';
}
export function currentTheme() {
  return document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
}
function applyTheme() {
  const theme = (typeof window.aiiThemeBoot === 'function') ? window.aiiThemeBoot()
    : (themeChoice() === 'system' ? ((lightMQ && lightMQ.matches) ? 'light' : 'dark') : themeChoice());
  document.documentElement.setAttribute('data-theme', theme);
  renderThemeToggle();
  if (typeof S.onThemeApplied === 'function') S.onThemeApplied();
  return theme;
}

export function setThemeChoice(choice) {
  try {
    if (choice === 'light' || choice === 'dark') localStorage.setItem(THEME_KEY, choice);
    else localStorage.removeItem(THEME_KEY);
  } catch (e) {  }
  return applyTheme();
}
function renderThemeToggle() {
  const btn = document.getElementById('theme-toggle');
  if (!btn) return;
  const light = currentTheme() === 'light';
  const label = light ? 'Switch to the dark theme' : 'Switch to the light theme';
  btn.setAttribute('aria-label', label); btn.title = label;
  btn.setAttribute('aria-pressed', light ? 'true' : 'false');
}
export function initThemeSwitch() {
  const btn = document.getElementById('theme-toggle');
  if (btn) btn.onclick = () => setThemeChoice(currentTheme() === 'light' ? 'dark' : 'light');
  if (lightMQ && lightMQ.addEventListener) lightMQ.addEventListener('change', () => { if (themeChoice() === 'system') applyTheme(); });
  renderThemeToggle();
}
S.setThemeChoice = setThemeChoice;
S.themeChoice = themeChoice;
S.initThemeSwitch = initThemeSwitch;
