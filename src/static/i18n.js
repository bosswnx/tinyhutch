'use strict';

/*
 * TinyHutch i18n: language selection and helpers.
 * All translations live in locales/<lang>.js so this file stays logic-only.
 */

const I18N = window.HutchLocales || {};
const I18N_LANGS = Object.keys(I18N);
const I18N_BASE = 'zh';
const HTML_LANG = { zh: 'zh-CN', en: 'en' };

let currentLang = (() => {
  const saved = localStorage.getItem('hutch-lang');
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

function localeText(lang, key) {
  const table = I18N[lang] || {};
  return table[key] != null ? table[key] : null;
}

function languageName(lang) {
  return localeText(lang, 'meta.name') || lang.toUpperCase();
}

function languageShort(lang) {
  return localeText(lang, 'meta.short') || lang.toUpperCase();
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
    if (label) label.textContent = languageShort(currentLang);
    else btn.textContent = languageShort(currentLang);
  }
}

function setLang(lang) {
  if (!I18N_LANGS.includes(lang) || lang === currentLang) return;
  currentLang = lang;
  localStorage.setItem('hutch-lang', lang);
  document.documentElement.lang = HTML_LANG[lang] || lang;
  applyI18n();
  if (langChangeHandler) langChangeHandler();
}

document.documentElement.lang = HTML_LANG[currentLang] || currentLang;

function bindLanguageButton() {
  const btn = document.getElementById('btn-lang');
  if (!btn) return;

  let picker = btn.closest('.lang-picker');
  if (!picker) {
    picker = document.createElement('div');
    picker.className = 'lang-picker';
    btn.parentNode.insertBefore(picker, btn);
    picker.appendChild(btn);
  }

  let menu = document.getElementById('lang-menu');
  if (!menu) {
    menu = document.createElement('div');
    menu.id = 'lang-menu';
    menu.className = 'lang-menu';
    menu.hidden = true;
    menu.setAttribute('role', 'menu');
    picker.appendChild(menu);
  }
  btn.setAttribute('aria-controls', 'lang-menu');

  let menuOpen = false;
  let onPointer = null;
  let onKey = null;

  function closeMenu(focusButton = false) {
    if (!menuOpen) return;
    menuOpen = false;
    menu.hidden = true;
    btn.setAttribute('aria-expanded', 'false');
    picker.classList.remove('open');
    if (onPointer) {
      document.removeEventListener('pointerdown', onPointer, true);
      onPointer = null;
    }
    if (onKey) {
      document.removeEventListener('keydown', onKey);
      onKey = null;
    }
    if (focusButton) btn.focus();
  }

  function renderMenu() {
    menu.textContent = '';
    menu.setAttribute('aria-label', t('common.language'));
    for (const lang of I18N_LANGS) {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'lang-item';
      item.setAttribute('role', 'menuitemradio');
      item.setAttribute('aria-checked', lang === currentLang ? 'true' : 'false');
      item.dataset.lang = lang;
      const name = document.createElement('span');
      name.textContent = languageName(lang);
      item.appendChild(name);
      if (lang === currentLang) {
        const check = document.createElement('span');
        check.className = 'lang-item-check';
        if (typeof icon === 'function') check.appendChild(icon('check'));
        item.appendChild(check);
      }
      item.addEventListener('click', () => {
        setLang(lang);
        closeMenu(true);
      });
      menu.appendChild(item);
    }
  }

  function openMenu() {
    if (menuOpen) return;
    renderMenu();
    menu.hidden = false;
    menuOpen = true;
    btn.setAttribute('aria-expanded', 'true');
    picker.classList.add('open');
    const selected = menu.querySelector('.lang-item[aria-checked="true"]') || menu.firstElementChild;
    if (selected) selected.focus();
    onPointer = e => {
      if (!picker.contains(e.target)) closeMenu();
    };
    onKey = e => {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeMenu(true);
      } else if (e.key === 'Tab') {
        closeMenu();
      }
    };
    document.addEventListener('pointerdown', onPointer, true);
    document.addEventListener('keydown', onKey);
  }

  btn.addEventListener('click', () => {
    if (menuOpen) closeMenu();
    else openMenu();
  });

  btn.addEventListener('keydown', e => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      openMenu();
    }
  });

  menu.addEventListener('keydown', e => {
    const items = [...menu.querySelectorAll('.lang-item')];
    if (!items.length) return;
    const i = items.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      items[(i + 1) % items.length].focus();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      items[(i - 1 + items.length) % items.length].focus();
    } else if (e.key === 'Home') {
      e.preventDefault();
      items[0].focus();
    } else if (e.key === 'End') {
      e.preventDefault();
      items[items.length - 1].focus();
    }
  });
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
