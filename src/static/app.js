'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

const state = {
  path: '/',
  parent: '/',
  entries: [],
  admin: false,
  adminPasswordSet: true,
  siteName: 'TinyHutch',
  filter: '',
  sort: { key: 'name', dir: 1 },
  queue: [],
  activeUploads: 0,
  truncated: false,
  selected: new Set(),
  dragPaths: [],
};

let uploadSeq = 0;

/* ---------- api ---------- */

async function api(path, opts = {}) {
  const headers = Object.assign({ 'X-Requested-With': 'tinyhutch' }, opts.headers || {});
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

/* theme helpers live in theme.js */

/* ---------- helpers ---------- */

function absPath(entry) {
  return state.path === '/' ? '/' + entry.name : state.path + '/' + entry.name;
}

function formatSize(n) {
  if (n == null) return '—';
  if (n < 1024) return n + ' B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (v >= 10 ? v.toFixed(0) : v.toFixed(1)) + ' ' + units[i];
}

function formatTime(s) {
  const d = new Date(s);
  if (isNaN(d)) return '';
  const p = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function extOf(name) {
  const i = name.lastIndexOf('.');
  return i < 0 ? '' : name.slice(i + 1).toLowerCase();
}

function fileIcon(name, type) {
  if (type === 'dir') return 'folder';
  const ext = extOf(name);
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'svg', 'bmp', 'heic'].includes(ext)) return 'image';
  if (['mp4', 'm4v', 'webm', 'ogv', 'mov', 'mkv', 'avi'].includes(ext)) return 'film';
  if (['mp3', 'wav', 'ogg', 'oga', 'm4a', 'flac', 'aac'].includes(ext)) return 'music';
  if (['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'rar', '7z'].includes(ext)) return 'archive';
  if (['js', 'mjs', 'ts', 'tsx', 'jsx', 'go', 'py', 'rb', 'rs', 'java', 'c', 'h', 'cpp', 'hpp', 'sh', 'bash', 'zsh', 'html', 'htm', 'css', 'scss', 'json', 'yml', 'yaml', 'toml', 'xml', 'sql'].includes(ext)) return 'file-code';
  if (['txt', 'md', 'markdown', 'log', 'csv', 'tsv', 'ini', 'conf', 'pdf'].includes(ext)) return 'file-text';
  return 'file';
}

function isExactRule(p) {
  return !/[*?]/.test(p);
}

function showBanner(message, ok) {
  showToast(message, !!ok);
}

/* ---------- session ---------- */

function updateLoginLabel() {
  const label = $('#login-label');
  if (label) label.textContent = t('common.login');
}

async function refreshSession() {
  const s = await api('/api/session');
  state.admin = !!s.admin;
  state.adminPasswordSet = !!s.admin_password_set;
  state.siteName = s.site_name || 'TinyHutch';
  $('#site-name').textContent = state.siteName;
  document.title = state.siteName;
  $('#btn-upload').classList.toggle('hidden', !state.admin);
  $('#btn-mkdir').classList.toggle('hidden', !state.admin);
  $('#btn-admin').classList.toggle('hidden', !state.admin);
  $('#btn-logout').classList.toggle('hidden', !state.admin);
  $('#btn-login').classList.toggle('hidden', state.admin);
  document.body.classList.toggle('is-admin', state.admin);
  if (!state.admin) state.selected.clear();
  updateLoginLabel();
  return s;
}

/* ---------- listing ---------- */

async function loadList(path, push = true) {
  try {
    const data = await api('/api/list?path=' + encodeURIComponent(path));
    state.path = data.path;
    state.parent = data.parent || '/';
    state.entries = data.entries || [];
    state.admin = !!data.admin;
    state.truncated = !!data.truncated;
    state.selected.clear();
    const url = data.path === '/' ? '/' : '/?path=' + encodeURIComponent(data.path);
    if (push) history.pushState(null, '', url);
    else history.replaceState(null, '', url);
    render();
  } catch (e) {
    if (e.status === 401) {
      const ok = await unlockFlow(path, t('index.auth_dir_locked'));
      if (ok) {
        await refreshSession();
        return loadList(path);
      }
      if (path !== '/') return loadList('/');
      renderError(t('index.need_password'));
      return;
    }
    renderError(apiErrorText(e));
  }
}

function renderError(message) {
  $('#rows').textContent = '';
  $('#empty').classList.remove('hidden');
  $('#empty').textContent = message;
}

function visibleEntries() {
  let list = state.entries.slice();
  if (state.filter) {
    const f = state.filter.toLowerCase();
    list = list.filter(e => e.name.toLowerCase().includes(f));
  }
  const { key, dir } = state.sort;
  list.sort((a, b) => {
    if ((a.type === 'dir') !== (b.type === 'dir')) return a.type === 'dir' ? -1 : 1;
    if (key === 'size') return ((a.size || 0) - (b.size || 0)) * dir;
    if (key === 'mtime') return (new Date(a.mtime) - new Date(b.mtime)) * dir;
    return a.name.localeCompare(b.name, 'zh-CN') * dir;
  });
  return list;
}

function render() {
  renderCrumbs();
  renderSort();
  const rows = $('#rows');
  rows.textContent = '';
  const list = visibleEntries();
  if (state.path !== '/') rows.appendChild(renderParentRow());
  for (const entry of list) rows.appendChild(renderRow(entry));

  $('#empty').classList.toggle('hidden', list.length > 0);
  if (list.length === 0) {
    $('#empty-text').textContent = state.filter ? t('index.empty_filter') : t('index.empty');
  }
  $('#truncated').classList.toggle('hidden', !state.truncated);

  let files = 0;
  let dirs = 0;
  let total = 0;
  for (const e of state.entries) {
    if (e.type === 'dir') dirs++;
    else { files++; total += e.size || 0; }
  }
  const n = state.entries.length;
  $('#count').textContent = t('index.count', {
    n,
    item: n === 1 ? t('index.item_one') : t('index.item_many'),
    d: dirs,
    folder: dirs === 1 ? t('index.folder_one') : t('index.folder_many'),
    f: files,
    file: files === 1 ? t('index.file_one') : t('index.file_many'),
  });
  $('#total').textContent = total ? formatSize(total) : '';
  document.title = state.siteName + (state.path === '/' ? '' : ' · ' + state.path);
  updateSelectionUI();
}

function renderSort() {
  document.querySelectorAll('.sortbtn').forEach(btn => {
    const arrow = btn.querySelector('.arrow');
    arrow.replaceChildren();
    if (btn.dataset.sort === state.sort.key) {
      arrow.appendChild(icon(state.sort.dir > 0 ? 'chevron-up' : 'chevron-down'));
    }
  });
}

function renderCrumbs() {
  const nav = $('#crumbs');
  nav.textContent = '';
  const parts = state.path.split('/').filter(Boolean);
  const links = [{ name: t('nav.home'), path: '/' }];
  let acc = '';
  for (const p of parts) {
    acc += '/' + p;
    links.push({ name: p, path: acc });
  }
  links.forEach((link, i) => {
    if (i > 0) nav.appendChild(el('span', 'sep', '/'));
    if (i === links.length - 1) {
      nav.appendChild(el('span', 'current', link.name));
    } else {
      const a = el('a', null, link.name);
      a.href = link.path === '/' ? '/' : '/?path=' + encodeURIComponent(link.path);
      a.addEventListener('click', ev => { ev.preventDefault(); loadList(link.path); });
      nav.appendChild(a);
    }
  });
}

function rowAction(name, label, title) {
  const b = el('button', 'rowbtn');
  b.type = 'button';
  b.appendChild(icon(name));
  b.title = title || label;
  b.setAttribute('aria-label', label);
  return b;
}

// Clone the actual selected rows so the drag image is the rows themselves,
// not a summary card.
function buildDragGhost(e, grabbedTr) {
  const table = grabbedTr.closest('table');
  if (!table) return null;
  const tableRect = table.getBoundingClientRect();
  const header = table.tHead && table.tHead.rows[0];
  const ghost = el('div', 'drag-ghost-rows');
  ghost.style.width = tableRect.width + 'px';

  const cloneTable = document.createElement('table');
  cloneTable.className = 'filetable';
  cloneTable.style.width = '100%';
  cloneTable.style.tableLayout = 'fixed';
  if (header) {
    const colgroup = document.createElement('colgroup');
    for (const th of [...header.cells]) {
      const col = document.createElement('col');
      col.style.width = th.getBoundingClientRect().width + 'px';
      colgroup.appendChild(col);
    }
    cloneTable.appendChild(colgroup);
  }

  const tbody = document.createElement('tbody');
  let offsetY = 0;
  let offsetX = 0;
  let reached = false;
  for (const row of table.querySelectorAll('tbody tr[data-path]')) {
    if (!state.selected.has(row.dataset.path)) continue;
    const rect = row.getBoundingClientRect();
    if (!reached) {
      if (row === grabbedTr) {
        offsetX = e.clientX - rect.left;
        offsetY += e.clientY - rect.top;
        reached = true;
      } else {
        offsetY += rect.height;
      }
    }
    const clone = row.cloneNode(true);
    clone.classList.remove('dragging', 'drop-target');
    tbody.appendChild(clone);
  }
  cloneTable.appendChild(tbody);
  ghost.appendChild(cloneTable);
  return { el: ghost, x: offsetX, y: offsetY };
}

function startDrag(e, entry, tr) {
  const p = absPath(entry);
  if (!state.selected.has(p)) {
    state.selected.clear();
    updateSelectionUI();
  }
  const paths = state.selected.size ? [...state.selected] : [p];
  state.dragPaths = paths;
  e.dataTransfer.setData('text/plain', paths[0]);
  e.dataTransfer.effectAllowed = 'move';
  tr.classList.add('dragging');

  if (paths.length > 1) {
    const ghost = buildDragGhost(e, tr);
    if (ghost) {
      document.body.appendChild(ghost.el);
      e.dataTransfer.setDragImage(ghost.el, ghost.x, ghost.y);
      setTimeout(() => ghost.el.remove(), 0);
    }
  }
}

function endDrag() {
  state.dragPaths = [];
  document.querySelectorAll('tr.dragging, tr.drop-target').forEach(tr => {
    tr.classList.remove('dragging', 'drop-target');
  });
}

function isInside(path, dir) {
  return path === dir || path.startsWith(dir + '/');
}

// Moving/deleting a parent already covers anything selected inside it.
function topLevelPaths(paths) {
  return paths.filter(p => !paths.some(q => q !== p && isInside(p, q)));
}

function updateSelectionUI() {
  const n = state.selected.size;
  const bar = $('#selection-bar');
  if (bar) bar.classList.toggle('hidden', n === 0);
  const count = $('#selection-count');
  if (count) count.textContent = t('index.selected_count', { n });
  document.querySelectorAll('#rows tr[data-path]').forEach(tr => {
    const cb = tr.querySelector('.row-select');
    if (cb) cb.checked = state.selected.has(tr.dataset.path);
    tr.querySelectorAll('.rowbtn').forEach(btn => {
      btn.disabled = n > 0 || btn.dataset.inherited === '1';
    });
  });
  const all = $('#select-all');
  if (all) {
    const visible = visibleEntries().map(absPath);
    const chosen = visible.filter(p => state.selected.has(p)).length;
    all.checked = visible.length > 0 && chosen === visible.length;
    all.indeterminate = chosen > 0 && chosen < visible.length;
  }
}

async function downloadSelected() {
  const paths = topLevelPaths([...state.selected]);
  if (!paths.length) return;
  let stats;
  try {
    stats = await api('/api/stat', { method: 'POST', body: JSON.stringify({ paths }) });
  } catch (e) {
    showBanner(apiErrorText(e));
    return;
  }
  const ok = await dialog({
    title: t('index.download_title'),
    body: t('index.download_selected_body', {
      items: paths.length,
      files: stats.files,
      size: formatSize(stats.bytes),
    }),
    buttons: [
      { label: t('common.cancel'), value: false },
      { label: t('common.download'), value: true, primary: true },
    ],
  });
  if (!ok) return;
  const qs = paths.map(p => 'path=' + encodeURIComponent(p)).join('&');
  triggerDownload('/api/zip?' + qs);
}

async function deleteSelected() {
  const paths = topLevelPaths([...state.selected]);
  if (!paths.length) return;
  const ok = await dialog({
    title: t('index.delete_title'),
    body: t('index.delete_selected_body', { n: paths.length }),
    buttons: [
      { label: t('common.cancel'), value: false },
      { label: t('common.delete'), value: true, danger: true, primary: true },
    ],
  });
  if (!ok) return;
  try {
    const res = await api('/api/delete', { method: 'POST', body: JSON.stringify({ paths, recursive: true }) });
    state.selected.clear();
    await loadList(state.path, false);
    if (res && res.undo) {
      showUndoToast(t('index.deleted'), async () => {
        await api('/api/undo-delete', { method: 'POST', body: JSON.stringify({ token: res.undo }) });
        await loadList(state.path, false);
      });
    }
  } catch (e) {
    state.selected.clear();
    await loadList(state.path, false);
    showBanner(apiErrorText(e));
  }
}

async function moveEntries(paths, targetDir) {
  const list = topLevelPaths(paths);
  const done = [];
  let firstErr = null;
  for (const from of list) {
    const name = from.slice(from.lastIndexOf('/') + 1);
    const to = (targetDir === '/' ? '' : targetDir) + '/' + name;
    try {
      await api('/api/move', { method: 'POST', body: JSON.stringify({ from, to }) });
      done.push({ from, to });
    } catch (e) {
      if (!firstErr) firstErr = e;
    }
  }
  state.selected.clear();
  await loadList(state.path, false);
  if (firstErr) showBanner(apiErrorText(firstErr));
  if (done.length) {
    showUndoToast(t('index.move_done'), async () => {
      for (const m of done.slice().reverse()) {
        await api('/api/move', { method: 'POST', body: JSON.stringify({ from: m.to, to: m.from }) });
      }
      await loadList(state.path, false);
    });
  }
}

function renderParentRow() {
  const tr = el('tr', 'parent-row');
  const nameTd = el('td', 'col-name');
  const wrap = el('div', 'name-cell');
  const tile = el('span', 'icon-tile');
  tile.appendChild(icon('corner-left-up'));
  wrap.appendChild(tile);
  const btn = el('button', 'fname', '..');
  btn.title = t('index.parent');
  btn.addEventListener('click', () => loadList(state.parent || '/'));
  wrap.appendChild(btn);
  nameTd.appendChild(wrap);
  tr.append(el('td', 'col-select'), nameTd, el('td', 'col-size', '—'), el('td', 'col-time', ''), el('td', 'col-actions'));

  // The ".." row is also a drop target: drop here to move into the parent.
  if (state.admin) {
    tr.addEventListener('dragover', e => {
      if (!state.dragPaths.length) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      tr.classList.add('drop-target');
    });
    tr.addEventListener('dragleave', e => {
      if (!tr.contains(e.relatedTarget)) tr.classList.remove('drop-target');
    });
    tr.addEventListener('drop', e => {
      e.preventDefault();
      tr.classList.remove('drop-target');
      const paths = state.dragPaths.length
        ? [...state.dragPaths]
        : (e.dataTransfer.getData('text/plain') ? [e.dataTransfer.getData('text/plain')] : []);
      const target = state.parent || '/';
      endDrag();
      if (!paths.length) return;
      if (paths.some(p => isInside(target, p))) return;
      moveEntries(paths, target);
    });
  }
  return tr;
}

function renderRow(entry) {
  const tr = el('tr');
  const p = absPath(entry);
  tr.dataset.path = p;
  const selectTd = el('td', 'col-select');
  if (state.admin) {
    const cb = el('input');
    cb.type = 'checkbox';
    cb.className = 'row-select';
    cb.checked = state.selected.has(p);
    cb.setAttribute('aria-label', t('index.select_row'));
    cb.addEventListener('change', () => {
      if (cb.checked) state.selected.add(p);
      else state.selected.delete(p);
      updateSelectionUI();
    });
    selectTd.appendChild(cb);
  }
  const nameTd = el('td', 'col-name');
  const wrap = el('div', 'name-cell');
  if (state.admin) {
    const handle = el('span', 'drag-handle');
    handle.title = t('index.drag_hint');
    handle.setAttribute('aria-label', t('index.drag_hint'));
    handle.appendChild(icon('grip-vertical'));
    // Only make the row draggable while the handle is held down, so the
    // browser uses the whole row as the drag image and normal clicks still work.
    handle.addEventListener('mousedown', () => {
      tr.draggable = true;
      const reset = () => {
        tr.draggable = false;
        window.removeEventListener('mouseup', reset);
      };
      window.addEventListener('mouseup', reset, { once: true });
    });
    wrap.appendChild(handle);
    tr.addEventListener('dragstart', e => startDrag(e, entry, tr));
    tr.addEventListener('dragend', () => {
      tr.draggable = false;
      endDrag();
    });
  }
  const tile = el('span', 'icon-tile' + (entry.type === 'dir' ? ' is-dir' : ''));
  tile.appendChild(icon(fileIcon(entry.name, entry.type)));
  wrap.appendChild(tile);

  const nameBtn = el('button', 'fname', entry.name);
  nameBtn.title = entry.name;
  nameBtn.addEventListener('click', () => {
    if (entry.type === 'dir') loadList(absPath(entry));
    else downloadEntry(entry);
  });
  wrap.appendChild(nameBtn);

  const protectedNow = entry.protected || entry.locked;
  if (protectedNow) {
    const lock = el('span', 'lockbadge');
    lock.appendChild(icon('lock'));
    lock.title = entry.inherited ? t('index.lock_inherited_title') : t('index.locked_title');
    wrap.appendChild(lock);
  }
  nameTd.appendChild(wrap);

  const sizeTd = el('td', 'col-size', entry.type === 'dir' ? '—' : formatSize(entry.size));
  const timeTd = el('td', 'col-time', formatTime(entry.mtime));
  const actions = el('td', 'col-actions');

  if (entry.type === 'file') {
    const dl = rowAction('download', t('common.download'));
    dl.disabled = state.selected.size > 0;
    dl.addEventListener('click', () => downloadEntry(entry));
    actions.appendChild(dl);

    const linkBtn = rowAction('link', t('index.copy_link'));
    linkBtn.disabled = state.selected.size > 0;
    linkBtn.addEventListener('click', () => copyLink(absPath(entry), entry));
    actions.appendChild(linkBtn);
  }
  if (state.admin) {
    const rn = rowAction('pencil', t('common.rename'));
    rn.classList.add('act-rename');
    rn.disabled = state.selected.size > 0;
    rn.addEventListener('click', () => renameEntry(entry));
    actions.appendChild(rn);

    const directLock = entry.protected && !entry.inherited;
    const lockBtn = rowAction(
      directLock ? 'unlock' : 'lock',
      directLock ? t('common.unlock') : t('common.lock'),
      entry.inherited
        ? t('index.lock_inherited_title')
        : directLock ? t('index.unlock_action_title') : t('index.lock_action_title'));
    lockBtn.classList.add('act-lock');
    if (entry.inherited) lockBtn.dataset.inherited = '1';
    lockBtn.disabled = state.selected.size > 0 || !!entry.inherited;
    lockBtn.addEventListener('click', () => toggleLock(entry));
    actions.appendChild(lockBtn);

    const del = rowAction('trash', t('common.delete'));
    del.classList.add('danger');
    del.disabled = state.selected.size > 0;
    del.addEventListener('click', () => deleteEntry(entry));
    actions.appendChild(del);
  }

  tr.append(selectTd, nameTd, sizeTd, timeTd, actions);

  if (state.admin && entry.type === 'dir') {
    tr.addEventListener('dragover', e => {
      if (!state.dragPaths.length) return;
      const target = absPath(entry);
      if (state.dragPaths.some(p => isInside(target, p))) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      tr.classList.add('drop-target');
    });
    tr.addEventListener('dragleave', e => {
      if (!tr.contains(e.relatedTarget)) tr.classList.remove('drop-target');
    });
    tr.addEventListener('drop', e => {
      e.preventDefault();
      tr.classList.remove('drop-target');
      const paths = state.dragPaths.length
        ? [...state.dragPaths]
        : (e.dataTransfer.getData('text/plain') ? [e.dataTransfer.getData('text/plain')] : []);
      endDrag();
      if (!paths.length) return;
      const target = absPath(entry);
      if (paths.some(p => isInside(target, p))) return;
      moveEntries(paths, target);
    });
  }

  return tr;
}

/* ---------- unlocking ---------- */

async function unlockFlow(path, reason) {
  let lastError = '';
  for (;;) {
    const pw = await askPassword(t('index.auth_title'), reason, lastError);
    if (pw === null) return false;
    try {
      await api('/api/unlock', { method: 'POST', body: JSON.stringify({ path, password: pw }) });
      return true;
    } catch (e) {
      if (e.status === 429) lastError = apiErrorText(e);
      else if (e.status === 401) lastError = t('index.auth_wrong');
      else lastError = apiErrorText(e);
    }
  }
}

async function ensureReadable(entry) {
  if (!entry.locked) return true;
  const ok = await unlockFlow(absPath(entry), t('index.auth_file_locked'));
  if (ok) {
    await refreshSession();
    await loadList(state.path, false);
  }
  return ok;
}

/* ---------- file actions ---------- */

async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (_) { /* fall through */ }
  }
  const input = el('input', 'input');
  input.value = text;
  input.readOnly = true;
  await dialog({
    title: t('index.copy_link'),
    body: input,
    buttons: [{ label: t('common.close'), value: true, primary: true }],
  });
  return false;
}

async function copyLink(abs, entry) {
  if (!(await ensureReadable(entry))) return;
  let url = location.origin + '/api/download?path=' + encodeURIComponent(abs);
  let withPassword = false;
  if (entry.protected) {
    try {
      const info = await api('/api/rule-password?path=' + encodeURIComponent(abs));
      if (info && info.password) {
        url += '&password=' + encodeURIComponent(info.password);
        withPassword = true;
      }
    } catch (e) {
      showToast(apiErrorText(e), false);
      return;
    }
  }
  if (await copyText(url)) {
    showToast(withPassword ? t('index.link_copied_with_password') : t('index.link_copied'), true);
  }
}

function triggerDownload(href) {
  const a = document.createElement('a');
  a.href = href;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

async function downloadEntry(entry) {
  if (!(await ensureReadable(entry))) return;
  const ok = await dialog({
    title: t('index.download_title'),
    body: t('index.download_body', { name: entry.name, size: formatSize(entry.size) }),
    buttons: [
      { label: t('common.cancel'), value: false },
      { label: t('common.download'), value: true, primary: true },
    ],
  });
  if (!ok) return;
  triggerDownload('/api/download?path=' + encodeURIComponent(absPath(entry)));
}

async function renameEntry(entry) {
  const name = await askText(t('index.rename_title'), entry.name);
  if (name === null || name === entry.name) return;
  try {
    const res = await api('/api/rename', { method: 'POST', body: JSON.stringify({ path: absPath(entry), name }) });
    await loadList(state.path, false);
    if (res && res.path) {
      showUndoToast(t('index.renamed'), async () => {
        await api('/api/rename', { method: 'POST', body: JSON.stringify({ path: res.path, name: entry.name }) });
        await loadList(state.path, false);
      });
    }
  } catch (e) {
    showBanner(apiErrorText(e));
  }
}

async function deleteEntry(entry) {
  const ok = await dialog({
    title: t('index.delete_title'),
    body: entry.type === 'dir'
      ? t('index.delete_body_dir', { name: entry.name })
      : t('index.delete_body', { name: entry.name }),
    buttons: [
      { label: t('common.cancel'), value: false },
      { label: t('common.delete'), value: true, danger: true, primary: true },
    ],
  });
  if (!ok) return;
  try {
    const res = await api('/api/delete', { method: 'POST', body: JSON.stringify({ path: absPath(entry), recursive: true }) });
    await loadList(state.path, false);
    if (res && res.undo) {
      showUndoToast(t('index.deleted'), async () => {
        await api('/api/undo-delete', { method: 'POST', body: JSON.stringify({ token: res.undo }) });
        await loadList(state.path, false);
      });
    }
  } catch (e) {
    showBanner(apiErrorText(e));
  }
}

async function toggleLock(entry) {
  if (entry.inherited) {
    showBanner(t('index.lock_inherited_title'));
    return;
  }
  try {
    const cfg = await api('/api/admin/config');
    const abs = absPath(entry);
    const rules = (cfg.rules || []).filter(r => !(isExactRule(r.path) && r.path === abs));
    if (entry.protected) {
      await saveRules(rules);
      await loadList(state.path, false);
      showBanner(t('index.unlocked_done'), true);
      return;
    }
    const pw = await askPassword(t('index.auth_lock_title'), t('index.auth_lock_hint', { name: entry.name }));
    if (pw === null || pw === '') {
      if (pw === '') showBanner(t('index.auth_empty'));
      return;
    }
    rules.push({ path: abs, mode: 'lock', password: pw });
    await saveRules(rules);
    await loadList(state.path, false);
    showBanner(t('index.locked_done'), true);
  } catch (e) {
    showBanner(apiErrorText(e));
  }
}

async function saveRules(rules) {
  await api('/api/admin/rules', { method: 'PUT', body: JSON.stringify({ rules }) });
}

async function createFolder() {
  const name = await askText(t('index.mkdir_title'), '');
  if (name === null || name === '') return;
  try {
    await api('/api/mkdir', { method: 'POST', body: JSON.stringify({ path: state.path, name }) });
    await loadList(state.path, false);
  } catch (e) {
    showBanner(apiErrorText(e));
  }
}

/* ---------- upload ---------- */

function stageFiles(files) {
  if (!state.admin || !files.length) return;
  const names = new Set(state.entries.map(e => e.name));
  const items = [];
  for (const file of files) {
    const conflict = names.has(file.name);
    names.add(file.name);
    items.push({ file, conflict, overwrite: false });
  }
  openUploadDialog(items);
}

async function openUploadDialog(items) {
  const conflicts = items.filter(i => i.conflict);
  const total = items.reduce((sum, i) => sum + i.file.size, 0);
  const body = el('div');
  body.appendChild(el('div', 'upload-summary',
    t('index.upload_summary', { n: items.length, size: formatSize(total) }) +
    (conflicts.length ? ' · ' + t('index.upload_conflicts', { n: conflicts.length }) : '')));

  const list = el('div', 'upload-list');
  const boxes = new Map();
  for (const item of items) {
    const row = el('div', 'upload-row');
    const name = el('span', 'uname', item.file.name);
    name.title = item.file.name;
    row.appendChild(name);
    row.appendChild(el('span', 'usize', formatSize(item.file.size)));
    if (item.conflict) {
      const label = el('label', 'uconflict');
      const cb = el('input');
      cb.type = 'checkbox';
      boxes.set(item, cb);
      label.appendChild(cb);
      label.appendChild(el('span', null, t('index.upload_overwrite')));
      row.appendChild(label);
    } else {
      row.appendChild(el('span', 'unew', t('index.upload_new')));
    }
    list.appendChild(row);
  }
  body.appendChild(list);
  if (conflicts.length) {
    body.appendChild(el('div', 'upload-summary', t('index.upload_hint')));
  }

  const ok = await dialog({
    title: t('index.upload_title'),
    body,
    wide: true,
    buttons: [
      { label: t('common.cancel'), value: false },
      { label: t('common.upload'), value: true, primary: true },
    ],
  });
  if (!ok) return;

  const selected = [];
  for (const item of items) {
    const box = boxes.get(item);
    if (item.conflict && !box?.checked) continue;
    selected.push({ file: item.file, overwrite: !!box?.checked });
  }
  if (!selected.length) {
    showToast(t('index.upload_nothing'), false);
    return;
  }
  enqueue(selected);
}

function enqueue(items) {
  if (!state.admin || !items.length) return;
  for (const item of items) {
    state.queue.push({
      id: ++uploadSeq,
      file: item.file,
      name: item.file.name,
      dir: state.path,
      overwrite: !!item.overwrite,
      progress: 0,
      status: 'waiting',
      error: '',
      xhr: null,
    });
  }
  $('#upload-panel').classList.remove('hidden');
  renderQueue();
  pump();
}

function pump() {
  while (state.activeUploads < 2) {
    const next = state.queue.find(q => q.status === 'waiting');
    if (!next) break;
    startUpload(next);
  }
  if (state.activeUploads === 0 && !state.queue.some(q => q.status === 'waiting')) {
    setTimeout(() => {
      if (state.activeUploads === 0) loadList(state.path, false);
    }, 300);
  }
}

function startUpload(item) {
  item.status = 'uploading';
  state.activeUploads++;
  renderQueue();
  runUpload(item).finally(() => {
    state.activeUploads--;
    renderQueue();
    pump();
  });
}

async function runUpload(item) {
  let overwrite = !!item.overwrite;
  for (;;) {
    try {
      await putFile(item, overwrite);
      item.status = 'done';
      item.progress = 1;
      renderQueue();
      return;
    } catch (e) {
      if (e.aborted) {
        item.status = 'canceled';
        renderQueue();
        return;
      }
      if (e.status === 409 && !overwrite) {
        const choice = await conflictDialog(item.name);
        if (choice === 'skip') { item.status = 'skipped'; renderQueue(); return; }
        if (choice === 'rename') { item.name = nextName(item.name); continue; }
        if (choice === 'overwrite') { overwrite = true; continue; }
        item.status = 'canceled';
        renderQueue();
        return;
      }
      item.error = apiErrorText(e);
      item.status = 'failed';
      renderQueue();
      return;
    }
  }
}

function putFile(item, overwrite) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    item.xhr = xhr;
    xhr.open('PUT', '/api/file?path=' + encodeURIComponent(item.dir) +
      '&name=' + encodeURIComponent(item.name) + '&overwrite=' + (overwrite ? 1 : 0));
    xhr.setRequestHeader('X-Requested-With', 'tinyhutch');
    xhr.upload.onprogress = e => {
      if (e.lengthComputable) {
        item.progress = e.loaded / e.total;
        renderQueue();
      }
    };
    xhr.onload = () => {
      item.xhr = null;
      if (xhr.status >= 200 && xhr.status < 300) return resolve();
      let data = null;
      try { data = JSON.parse(xhr.responseText); } catch (_) { /* ignore */ }
      const err = new Error((data && data.message) || t('index.upload_http_error', { status: xhr.status }));
      err.status = xhr.status;
      err.code = data && data.code;
      reject(err);
    };
    xhr.onerror = () => { item.xhr = null; reject(new Error(t('index.network_error'))); };
    xhr.onabort = () => { item.xhr = null; reject(Object.assign(new Error(t('index.upload_canceled')), { aborted: true })); };
    xhr.send(item.file);
  });
}

function nextName(name) {
  const dot = name.lastIndexOf('.');
  const base = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : '';
  const m = base.match(/^(.*) \((\d+)\)$/);
  if (m) return `${m[1]} (${parseInt(m[2], 10) + 1})${ext}`;
  return `${base} (1)${ext}`;
}

function statusLabel(item) {
  switch (item.status) {
    case 'waiting': return t('index.upload_waiting');
    case 'uploading': return t('index.upload_uploading');
    case 'done': return t('index.upload_done');
    case 'skipped': return t('index.upload_skipped');
    case 'canceled': return t('index.upload_canceled');
    case 'failed': return t('index.upload_failed', { msg: item.error || '' });
    default: return item.status;
  }
}

function renderQueue() {
  const list = $('#upload-list');
  list.textContent = '';
  for (const item of state.queue) {
    const li = el('li');
    const row = el('div', 'u-name');
    row.appendChild(el('span', null, item.name));
    const st = el('span', 'u-status' + (item.status === 'failed' ? ' error' : ''), statusLabel(item));
    row.appendChild(st);
    if (item.status === 'waiting' || item.status === 'uploading') {
      const cancel = el('button', 'icon-btn');
      cancel.appendChild(icon('x'));
      cancel.title = t('index.upload_cancel');
      cancel.addEventListener('click', () => {
        if (item.xhr) item.xhr.abort();
        item.status = 'canceled';
        renderQueue();
      });
      row.appendChild(cancel);
    } else if (item.status === 'failed') {
      const retry = el('button', 'icon-btn');
      retry.appendChild(icon('rotate-cw'));
      retry.title = t('index.upload_retry');
      retry.addEventListener('click', () => {
        item.status = 'waiting';
        item.progress = 0;
        renderQueue();
        pump();
      });
      row.appendChild(retry);
    }
    li.appendChild(row);
    const progress = el('div', 'progress');
    const bar = el('i');
    bar.style.width = Math.round((item.progress || 0) * 100) + '%';
    progress.appendChild(bar);
    li.appendChild(progress);
    list.appendChild(li);
  }
}

/* ---------- dialogs ---------- */

function dialog({ title, body, buttons, wide, bodyClass }) {
  return new Promise(resolve => {
    const overlay = el('div', 'overlay');
    const box = el('div', 'dialog' + (wide ? ' wide' : ''));
    box.appendChild(el('div', 'dialog-head', title));
    const bodyEl = el('div', 'dialog-body' + (bodyClass ? ' ' + bodyClass : ''));
    if (typeof body === 'string') bodyEl.appendChild(el('p', null, body));
    else if (body) bodyEl.appendChild(body);
    box.appendChild(bodyEl);

    const foot = el('div', 'dialog-foot');
    let settled = false;
    const close = value => {
      if (settled) return;
      settled = true;
      overlay.remove();
      document.removeEventListener('keydown', onKey);
      resolve(value);
    };
    for (const b of buttons) {
      const btn = el('button', 'btn' + (b.primary ? ' primary' : '') + (b.danger ? ' danger' : ''), b.label);
      btn.addEventListener('click', () => close(b.value));
      foot.appendChild(btn);
    }
    box.appendChild(foot);
    overlay.appendChild(box);
    $('#modal-root').appendChild(overlay);

    const onKey = e => {
      if (e.key === 'Tab') {
        const focusable = [...box.querySelectorAll('button, input, select, textarea, a[href]')].filter(n => !n.disabled);
        if (!focusable.length) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
        return;
      }
      if (e.key === 'Escape') close(null);
      if (e.key === 'Enter' && !(e.target instanceof HTMLTextAreaElement)) {
        const primary = foot.querySelector('.primary');
        if (primary) primary.click();
      }
    };
    document.addEventListener('keydown', onKey);
    overlay.addEventListener('click', e => { if (e.target === overlay) close(null); });

    const input = box.querySelector('input');
    if (input) { input.focus(); input.select(); }
    else { const primary = foot.querySelector('.primary'); if (primary) primary.focus(); }
  });
}

function askPassword(title, hint, errorText) {
  return new Promise(resolve => {
    const overlay = el('div', 'overlay');
    const box = el('div', 'dialog');
    box.appendChild(el('div', 'dialog-head', title));
    const body = el('div', 'dialog-body');
    if (hint) body.appendChild(el('p', null, hint));
    const input = el('input', 'input');
    input.type = 'password';
    input.autocomplete = 'current-password';
    body.appendChild(input);
    const err = el('div', 'form-error', errorText || '');
    body.appendChild(err);
    box.appendChild(body);

    const foot = el('div', 'dialog-foot');
    const cancel = el('button', 'btn', t('common.cancel'));
    const ok = el('button', 'btn primary', t('common.ok'));
    let settled = false;
    const close = value => {
      if (settled) return;
      settled = true;
      overlay.remove();
      document.removeEventListener('keydown', onKey);
      resolve(value);
    };
    cancel.addEventListener('click', () => close(null));
    ok.addEventListener('click', () => close(input.value));
    foot.append(cancel, ok);
    box.appendChild(foot);
    overlay.appendChild(box);
    $('#modal-root').appendChild(overlay);

    const onKey = e => {
      if (e.key === 'Tab') {
        const focusable = [...box.querySelectorAll('button, input, select, textarea, a[href]')].filter(n => !n.disabled);
        if (!focusable.length) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
        return;
      }
      if (e.key === 'Escape') close(null);
      if (e.key === 'Enter') ok.click();
    };
    document.addEventListener('keydown', onKey);
    overlay.addEventListener('click', e => { if (e.target === overlay) close(null); });
    setTimeout(() => input.focus(), 0);
  });
}

function askText(title, initial, hint) {
  return new Promise(resolve => {
    const overlay = el('div', 'overlay');
    const box = el('div', 'dialog');
    box.appendChild(el('div', 'dialog-head', title));
    const body = el('div', 'dialog-body');
    if (hint) body.appendChild(el('p', null, hint));
    const input = el('input', 'input');
    input.type = 'text';
    input.value = initial || '';
    body.appendChild(input);
    box.appendChild(body);

    const foot = el('div', 'dialog-foot');
    const cancel = el('button', 'btn', t('common.cancel'));
    const ok = el('button', 'btn primary', t('common.ok'));
    let settled = false;
    const close = value => {
      if (settled) return;
      settled = true;
      overlay.remove();
      document.removeEventListener('keydown', onKey);
      resolve(value);
    };
    cancel.addEventListener('click', () => close(null));
    ok.addEventListener('click', () => close(input.value.trim()));
    foot.append(cancel, ok);
    box.appendChild(foot);
    overlay.appendChild(box);
    $('#modal-root').appendChild(overlay);

    const onKey = e => {
      if (e.key === 'Tab') {
        const focusable = [...box.querySelectorAll('button, input, select, textarea, a[href]')].filter(n => !n.disabled);
        if (!focusable.length) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
        return;
      }
      if (e.key === 'Escape') close(null);
      if (e.key === 'Enter') ok.click();
    };
    document.addEventListener('keydown', onKey);
    overlay.addEventListener('click', e => { if (e.target === overlay) close(null); });
    setTimeout(() => { input.focus(); input.select(); }, 0);
  });
}

function conflictDialog(name) {
  return dialog({
    title: t('index.conflict_title'),
    body: t('index.conflict_body', { name }),
    buttons: [
      { label: t('index.conflict_skip'), value: 'skip' },
      { label: t('index.conflict_rename'), value: 'rename' },
      { label: t('index.conflict_overwrite'), value: 'overwrite', primary: true },
    ],
  });
}

/* ---------- bindings ---------- */

function bind() {
  $('#btn-theme').addEventListener('click', toggleTheme);
  $('#filter').addEventListener('input', e => {
    state.filter = e.target.value.trim();
    render();
  });

  document.querySelectorAll('.sortbtn').forEach(btn => {
    btn.addEventListener('click', () => {
      const key = btn.dataset.sort;
      if (state.sort.key === key) state.sort.dir *= -1;
      else { state.sort.key = key; state.sort.dir = 1; }
      render();
    });
  });

  $('#btn-upload').addEventListener('click', () => $('#file-input').click());
  $('#file-input').addEventListener('change', e => {
    stageFiles(Array.from(e.target.files || []));
    e.target.value = '';
  });
  $('#btn-mkdir').addEventListener('click', createFolder);

  $('#select-all').addEventListener('change', e => {
    const paths = visibleEntries().map(absPath);
    if (e.target.checked) paths.forEach(p => state.selected.add(p));
    else paths.forEach(p => state.selected.delete(p));
    updateSelectionUI();
  });
  $('#selection-clear').addEventListener('click', () => {
    state.selected.clear();
    updateSelectionUI();
  });
  $('#selection-download').addEventListener('click', downloadSelected);
  $('#selection-delete').addEventListener('click', deleteSelected);

  $('#btn-login').addEventListener('click', async () => {
    if (!state.adminPasswordSet) {
      try {
        await api('/api/unlock', { method: 'POST', body: JSON.stringify({ path: state.path, password: '' }) });
        await refreshSession();
        await loadList(state.path, false);
      } catch (e) {
        showBanner(apiErrorText(e));
      }
      return;
    }
    if (await unlockFlow(state.path, t('index.auth_login_hint'))) {
      await refreshSession();
      await loadList(state.path, false);
    }
  });

  $('#btn-logout').addEventListener('click', async () => {
    try { await api('/api/logout', { method: 'POST' }); } catch (_) { /* ignore */ }
    await refreshSession();
    await loadList(contextPath() || '/', false);
  });

  $('#upload-close').addEventListener('click', () => {
    if (state.activeUploads === 0) {
      state.queue = [];
      renderQueue();
    }
    $('#upload-panel').classList.add('hidden');
  });

  window.addEventListener('popstate', () => loadList(contextPath() || '/', false));

  let dragDepth = 0;
  window.addEventListener('dragenter', e => {
    if (!state.admin) return;
    if (e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files')) {
      dragDepth++;
      $('#dropzone').classList.remove('hidden');
    }
  });
  window.addEventListener('dragover', e => e.preventDefault());
  window.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) $('#dropzone').classList.add('hidden');
  });
  window.addEventListener('drop', e => {
    e.preventDefault();
    dragDepth = 0;
    $('#dropzone').classList.add('hidden');
    if (!state.admin) return;
    stageFiles(Array.from(e.dataTransfer.files || []));
  });
}

function contextPath() {
  const params = new URLSearchParams(location.search);
  return params.get('path');
}

/* ---------- boot ---------- */

onLangChange(() => {
  updateLoginLabel();
  render();
  renderQueue();
});

applyTheme();
document.addEventListener('DOMContentLoaded', async () => {
  bind();
  try {
    await refreshSession();
    await loadList(contextPath() || '/', false);
  } catch (e) {
    renderError(apiErrorText(e));
  }
});
