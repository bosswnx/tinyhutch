'use strict';

/* Floating, non-layout-shifting notices. */
function showToast(message, ok) {
  let host = document.getElementById('toast-host');
  if (!host) {
    host = document.createElement('div');
    host.id = 'toast-host';
    host.className = 'toast-host';
    document.body.appendChild(host);
  }
  const toast = document.createElement('div');
  toast.className = 'toast' + (ok ? ' ok' : '');
  toast.textContent = message;
  host.appendChild(toast);
  while (host.children.length > 3) host.firstElementChild.remove();
  setTimeout(() => toast.remove(), ok ? 3000 : 6000);
}

let undoCleanup = null;

function clearUndoToast() {
  if (undoCleanup) {
    undoCleanup();
    undoCleanup = null;
  }
}

// showUndoToast shows one undoable notice with a 5 second countdown.
// A new undoable action replaces the previous one (only one step is kept).
function showUndoToast(message, onUndo) {
  clearUndoToast();
  let host = document.getElementById('toast-host');
  if (!host) {
    host = document.createElement('div');
    host.id = 'toast-host';
    host.className = 'toast-host';
    document.body.appendChild(host);
  }
  const toast = document.createElement('div');
  toast.className = 'toast ok undo-toast';
  const text = document.createElement('span');
  text.textContent = message;
  toast.appendChild(text);

  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'toast-action';
  let left = 5;
  const render = () => { btn.textContent = `${t('common.undo')} (${left}s)`; };
  render();

  let timer = null;
  const stop = () => {
    if (timer) clearInterval(timer);
    timer = null;
    toast.remove();
    if (undoCleanup === stop) undoCleanup = null;
  };
  timer = setInterval(() => {
    left -= 1;
    if (left <= 0) {
      stop();
      return;
    }
    render();
  }, 1000);
  undoCleanup = stop;

  btn.addEventListener('click', async () => {
    const fn = onUndo;
    stop();
    try {
      await fn();
      showToast(t('index.undone'), true);
    } catch (e) {
      showToast(apiErrorText(e), false);
    }
  });
  toast.appendChild(btn);
  host.appendChild(toast);
}
