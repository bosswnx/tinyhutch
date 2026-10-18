'use strict';

/* Shared theme handling for both pages. */

function updateThemeIcon() {
  const btn = document.getElementById('btn-theme');
  if (!btn) return;
  const dark = document.documentElement.dataset.theme === 'dark';
  btn.replaceChildren(icon(dark ? 'sun' : 'moon'));
}

function applyTheme() {
  const saved = localStorage.getItem('tinyfm-theme');
  const dark = saved ? saved === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches;
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  updateThemeIcon();
}

function toggleTheme() {
  const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
  document.documentElement.dataset.theme = next;
  localStorage.setItem('tinyfm-theme', next);
  updateThemeIcon();
}
