'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

let rules = [];
let adminPasswordSet = false;
let pwMode = null;

async function api(path, opts = {}) {
  const headers = Object.assign({ 'X-Requested-With': 'tinyfm' }, opts.headers || {});
  if (opts.body && typeof opts.body === 'string') headers['Content-Type'] = 'application/json';
  const res = await fetch(path, Object.assign({}, opts, { headers, credentials: 'same-origin' }));
  let data = null;
  const ct = res.headers.get('content-type') || '';
  if (ct.includes('application/json')) {
    try { data = await res.json(); } catch (_) { data = null; }
  }
  if (!res.ok) {
    const err = new Error((data && data.message) || res.statusText || t('common.error'));
    err.status = res.status;
    err.code = data && data.code;
    err.data = data;
    throw err;
  }
  return data;
}

function banner(message, ok) {
  showToast(message, !!ok);
}

/* theme helpers live in theme.js */

function formatSize(n) {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + ' ' + units[i];
}

function formatUptime(sec) {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d) return t('admin.uptime_days', { d, h });
  if (h) return t('admin.uptime_hours', { h, m });
  return t('admin.uptime_minutes', { m });
}

/* ---------- rules ---------- */

function cleanRulePath(p) {
  let s = p.trim();
  if (s.length > 1) s = s.replace(/\/+$/, '');
  return s;
}

function ruleContains(parent, child) {
  if (parent === child) return true;
  if (parent === '/') return child.startsWith('/');
  return child.startsWith(parent + '/');
}

function renderRules() {
  const body = $('#rules-body');
  body.textContent = '';
  rules.forEach((r, index) => {
    const tr = el('tr');

    const pathTd = el('td', 'rule-path');
    const pathLabel = el('span', 'td-label', t('admin.rules_path'));
    pathLabel.dataset.i18n = 'admin.rules_path';
    pathTd.appendChild(pathLabel);
    const pathInput = el('input', 'input mono');
    pathInput.value = r.path || '';
    pathInput.placeholder = '/private';
    pathInput.addEventListener('input', () => { r.path = pathInput.value.trim(); });
    pathTd.appendChild(pathInput);
    tr.appendChild(pathTd);

    const pwTd = el('td', 'rule-password');
    const pwLabel = el('span', 'td-label', t('admin.rules_password'));
    pwLabel.dataset.i18n = 'admin.rules_password';
    pwTd.appendChild(pwLabel);
    const pwWrap = el('div', 'name-cell');
    const pwInput = el('input', 'input mono');
    pwInput.type = 'password';
    pwInput.value = r.password || '';
    pwInput.autocomplete = 'new-password';
    pwInput.addEventListener('input', () => { r.password = pwInput.value; });
    pwWrap.appendChild(pwInput);
    const reveal = el('button', 'icon-btn');
    reveal.type = 'button';
    reveal.appendChild(icon('eye'));
    reveal.title = t('admin.rules_show_pw');
    reveal.addEventListener('click', () => {
      const show = pwInput.type === 'password';
      pwInput.type = show ? 'text' : 'password';
      reveal.replaceChildren(icon(show ? 'eye-off' : 'eye'));
    });
    pwWrap.appendChild(reveal);
    pwTd.appendChild(pwWrap);
    tr.appendChild(pwTd);

    const actions = el('td', 'row-actions');
    const del = el('button', 'order-btn danger');
    del.type = 'button';
    del.appendChild(icon('trash'));
    del.addEventListener('click', () => {
      rules.splice(index, 1);
      renderRules();
    });
    actions.appendChild(del);
    tr.appendChild(actions);

    body.appendChild(tr);
  });
  if (rules.length === 0) {
    const tr = el('tr');
    const td = el('td', 'hint', t('admin.rules_empty'));
    td.colSpan = 3;
    tr.appendChild(td);
    body.appendChild(tr);
  }
}

function collectRules() {
  const out = [];
  for (let i = 0; i < rules.length; i++) {
    const r = rules[i];
    const p = cleanRulePath(r.path || '');
    if (!p.startsWith('/')) {
      throw new Error(t('admin.rules_err_path', { n: i + 1 }));
    }
    if (!r.password) {
      throw new Error(t('admin.rules_err_lock', { n: i + 1 }));
    }
    if (/[*?]/.test(p)) {
      throw new Error(t('admin.rules_err_glob', { n: i + 1 }));
    }
    for (let j = 0; j < out.length; j++) {
      if (ruleContains(out[j].path, p) || ruleContains(p, out[j].path)) {
        throw new Error(t('admin.rules_err_overlap', { n: i + 1, m: j + 1, path: p }));
      }
    }
    out.push({ path: p, mode: 'lock', password: r.password });
  }
  return out;
}

/* ---------- load / save ---------- */

async function loadConfig() {
  const cfg = await api('/api/admin/config');
  rules = (cfg.rules || []).map(r => ({ path: r.path, password: r.password || '' }));
  renderRules();
  $('#set-site').value = cfg.site_name || '';
  $('#set-hours').value = cfg.session_hours || 12;
  $('#set-max').value = cfg.max_upload_mb || 4096;
  $('#set-hidden').checked = !!cfg.show_hidden;
  adminPasswordSet = !!cfg.admin_password_set;
  pwMode = null;
  renderPasswordSection();
  return cfg;
}

async function loadStatus() {
  try {
    const st = await api('/api/admin/status');
    const dl = $('#status');
    dl.textContent = '';
    const rows = [
      [t('admin.status_version'), st.version],
      [t('admin.status_root'), st.root],
      [t('admin.status_config'), st.config_path],
      [t('admin.status_uptime'), formatUptime(st.uptime_sec)],
      [t('admin.status_rules'), String(st.rules)],
      [t('admin.status_disk'), formatSize(st.disk_free)],
      [t('admin.status_session'), st.session_hours + ' ' + t('admin.settings_hours_unit')],
    ];
    for (const [k, v] of rows) {
      dl.appendChild(el('dt', null, k));
      dl.appendChild(el('dd', null, v));
    }
  } catch (e) {
    $('#status').textContent = apiErrorText(e);
  }
}

function renderPasswordSection() {
  $('#pw-add').classList.toggle('hidden', adminPasswordSet);
  $('#pw-change').classList.toggle('hidden', !adminPasswordSet);
  $('#pw-delete').classList.toggle('hidden', !adminPasswordSet);
  $('#pw-status').textContent = adminPasswordSet
    ? t('admin.password_set')
    : t('admin.password_none');
  if (!pwMode) $('#pw-form').classList.add('hidden');
}

function openPasswordForm(mode) {
  pwMode = mode;
  $('#pw-form').classList.remove('hidden');
  $('#pw-current-row').classList.toggle('hidden', mode === 'add');
  $('#pw-new-row').classList.toggle('hidden', mode === 'delete');
  $('#pw-confirm-row').classList.toggle('hidden', mode === 'delete');
  $('#pw-save').textContent = mode === 'delete' ? t('admin.password_delete') : t('admin.password_save');
  $('#pw-save').classList.toggle('danger', mode === 'delete');
  $('#pw-save').classList.toggle('primary', mode !== 'delete');
  for (const id of ['#pw-current', '#pw-new', '#pw-confirm']) $(id).value = '';
  setTimeout(() => $(mode === 'add' ? '#pw-new' : '#pw-current').focus(), 0);
}

async function savePassword() {
  const current = $('#pw-current').value;
  const next = $('#pw-new').value;
  const confirm = $('#pw-confirm').value;

  if (pwMode === 'delete') {
    if (!current) return banner(t('admin.password_enter_current'));
    try {
      await api('/api/admin/password', {
        method: 'POST',
        body: JSON.stringify({ current_password: current, new_password: '' }),
      });
      adminPasswordSet = false;
      pwMode = null;
      renderPasswordSection();
      banner(t('admin.password_deleted'), true);
    } catch (e) {
      banner(apiErrorText(e));
    }
    return;
  }

  if (next.length < 6) return banner(t('admin.password_min6'));
  if (next !== confirm) return banner(t('admin.password_mismatch'));
  if (pwMode === 'change' && !current) return banner(t('admin.password_enter_current'));
  try {
    await api('/api/admin/password', {
      method: 'POST',
      body: JSON.stringify({ current_password: current, new_password: next }),
    });
    adminPasswordSet = true;
    pwMode = null;
    renderPasswordSection();
    banner(t('admin.password_saved'), true);
  } catch (e) {
    banner(apiErrorText(e));
  }
}

async function saveRules() {
  try {
    await api('/api/admin/rules', { method: 'PUT', body: JSON.stringify({ rules: collectRules() }) });
    banner(t('admin.rules_saved'), true);
    await loadConfig();
  } catch (e) {
    banner(apiErrorText(e));
  }
}

async function saveSettings() {
  try {
    await api('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({
        site_name: $('#set-site').value.trim(),
        session_hours: parseInt($('#set-hours').value, 10) || 12,
        max_upload_mb: parseInt($('#set-max').value, 10) || 4096,
        show_hidden: $('#set-hidden').checked,
      }),
    });
    banner(t('admin.settings_saved'), true);
    await loadConfig();
  } catch (e) {
    banner(apiErrorText(e));
  }
}

async function testRule() {
  const path = $('#test-path').value.trim();
  if (!path) return;
  try {
    const r = await api('/api/admin/rule-test', { method: 'POST', body: JSON.stringify({ path }) });
    const box = $('#test-result');
    box.textContent = '';
    box.appendChild(el('div', null, t('admin.test_path', { path: r.path })));
    box.appendChild(el('div', r.locked ? 'no' : 'ok', r.locked ? t('admin.test_locked') : t('admin.test_public')));
    box.appendChild(el('div', r.readable ? 'ok' : 'no', r.readable ? t('admin.test_readable') : t('admin.test_not_readable')));
    if (r.locked) box.appendChild(el('div', 'hint', t('admin.test_write_note')));
    if (r.matched_rules && r.matched_rules.length) {
      const list = r.matched_rules.map(i => '#' + (i + 1)).join(getLang() === 'zh' ? '、' : ', ');
      box.appendChild(el('div', 'hint', t('admin.test_matched', { list, index: r.rule_index + 1 })));
    } else {
      box.appendChild(el('div', 'hint', t('admin.test_none')));
    }
  } catch (e) {
    banner(apiErrorText(e));
  }
}

/* ---------- boot ---------- */

onLangChange(() => {
  document.title = t('admin.page_title');
  renderRules();
  renderPasswordSection();
  if (pwMode) {
    $('#pw-save').textContent = t(pwMode === 'delete' ? 'admin.password_delete' : 'admin.password_save');
  }
  loadStatus();
});

applyTheme();

document.addEventListener('DOMContentLoaded', async () => {
  document.title = t('admin.page_title');
  $('#btn-theme').addEventListener('click', () => {
    const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    localStorage.setItem('tinyfm-theme', next);
  });

  $('#pw-add').addEventListener('click', () => openPasswordForm('add'));
  $('#pw-change').addEventListener('click', () => openPasswordForm('change'));
  $('#pw-delete').addEventListener('click', () => openPasswordForm('delete'));
  $('#pw-cancel').addEventListener('click', () => {
    pwMode = null;
    renderPasswordSection();
  });
  $('#pw-save').addEventListener('click', savePassword);
  $('#rules-add').addEventListener('click', () => {
    rules.push({ path: '/', password: '' });
    renderRules();
  });
  $('#rules-save').addEventListener('click', saveRules);
  $('#set-save').addEventListener('click', saveSettings);
  $('#test-run').addEventListener('click', testRule);
  $('#test-path').addEventListener('keydown', e => { if (e.key === 'Enter') testRule(); });

  $('#btn-logout').addEventListener('click', async () => {
    try { await api('/api/logout', { method: 'POST' }); } catch (_) { /* ignore */ }
    location.href = '/';
  });

  $('#logout-all').addEventListener('click', async () => {
    if (!window.confirm(t('admin.danger_confirm'))) return;
    try {
      await api('/api/admin/logout-all', { method: 'POST' });
      location.href = '/';
    } catch (e) {
      banner(apiErrorText(e));
    }
  });

  try {
    await loadConfig();
  } catch (e) {
    if (e.status === 403 || e.status === 401) {
      let autoLoggedIn = false;
      try {
        const s = await api('/api/session');
        if (!s.admin_password_set) {
          await api('/api/unlock', { method: 'POST', body: JSON.stringify({ path: '/', password: '' }) });
          await loadConfig();
          autoLoggedIn = true;
        }
      } catch (_) { /* fall through */ }
      if (!autoLoggedIn) {
        location.href = '/';
        return;
      }
    } else {
      banner(apiErrorText(e));
      return;
    }
  }
  await loadStatus();
});
