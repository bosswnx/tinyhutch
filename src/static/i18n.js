'use strict';

/*
 * TinyFM i18n: language selection and helpers.
 * All translations live in locales/<lang>.js so this file stays logic-only.
 */

const I18N = window.TinyFMLocales || {};
const I18N_LANGS = Object.keys(I18N);
const I18N_BASE = 'zh';
const HTML_LANG = { zh: 'zh-CN', en: 'en' };

let currentLang = (() => {
  const saved = localStorage.getItem('tinyfm-lang');
  if (saved && I18N_LANGS.includes(saved)) return saved;
  const short = (navigator.language || '').toLowerCase().split('-')[0];
  if (I18N_LANGS.includes(short)) return short;
  if (I18N_LANGS.includes('en')) return 'en';
  return I18N_LANGS[0] || I18N_BASE;
})();

let langChangeHandler = null;

function getLang() {
  return currentLang;
}

function onLangChange(fn) {
  langChangeHandler = fn;
}

function t(key, vars) {
  const table = I18N[currentLang] || {};
  let s = table[key];
  if (s == null) s = (I18N[I18N_BASE] || {})[key];
  if (s == null) return key;
  if (vars) {
    s = s.replace(/\{(\w+)\}/g, (_, k) => (vars[k] != null ? String(vars[k]) : ''));
  }
  return s;
}

function apiErrorText(err) {
  if (err && err.code) {
    const key = 'api.' + err.code;
    const table = I18N[currentLang] || {};
    if (table[key] != null) return t(key);
  }
  return (err && err.message) || t('common.error');
}

function applyI18n(root = document) {
  root.querySelectorAll('[data-i18n]').forEach(el => {
    el.textContent = t(el.dataset.i18n);
  });
  root.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
    el.placeholder = t(el.dataset.i18nPlaceholder);
  });
  root.querySelectorAll('[data-i18n-title]').forEach(el => {
    el.title = t(el.dataset.i18nTitle);
  });
  root.querySelectorAll('[data-i18n-aria-label]').forEach(el => {
    el.setAttribute('aria-label', t(el.dataset.i18nAriaLabel));
  });
  const btn = document.getElementById('btn-lang');
  if (btn) {
    const label = btn.querySelector('.lang-label');
    if (label) label.textContent = currentLang === 'zh' ? 'EN' : '中文';
    else btn.textContent = currentLang === 'zh' ? 'EN' : '中文';
  }
}

function setLang(lang) {
  if (!I18N_LANGS.includes(lang) || lang === currentLang) return;
  currentLang = lang;
  localStorage.setItem('tinyfm-lang', lang);
  document.documentElement.lang = HTML_LANG[lang] || lang;
  applyI18n();
  if (langChangeHandler) langChangeHandler();
}

document.documentElement.lang = HTML_LANG[currentLang] || currentLang;

function bindLanguageButton() {
  const btn = document.getElementById('btn-lang');
  if (!btn) return;
  btn.addEventListener('click', () => setLang(currentLang === 'zh' ? 'en' : 'zh'));
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => {
    applyI18n();
    bindLanguageButton();
  });
} else {
  applyI18n();
  bindLanguageButton();
}
