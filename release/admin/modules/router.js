/* =====================================================================
   modules/router.js — Hash-based SPA router.

   The admin URL shape is `/#/route` so the index.html can be served
   from any path without server rewrites. Routes are flat strings
   keyed to a render function; `notFound` is the catch-all.

   Each route returns Promise<void> | void. The router handles the
   active-state highlighting by dispatching `orvix:route` events;
   sidebar.js listens and toggles its own classes. Deep links
   survive a page refresh (the hash is part of the URL).
   ===================================================================== */

import { el, $ } from './components.js';

const _routes = new Map();
let _currentRoute = null;
let _currentHandler = null;
let _notFound = null;
let _beforeEach = null;

export function register(name, handler) {
  _routes.set(name, handler);
}

export function setNotFound(fn)  { _notFound = fn; }
export function setBeforeEach(fn) { _beforeEach = fn; }

export function currentRoute() { return _currentRoute; }

export async function go(route, params) {
  let target = route;
  if (params) {
    Object.keys(params).forEach((k) => {
      target = target.replace(':' + k, encodeURIComponent(params[k]));
    });
  }
  if (location.hash !== '#/' + target) {
    location.hash = '#/' + target;
    return; // hashchange handler will pick it up
  }
  await runRoute(target);
}

async function runRoute(route) {
  _currentRoute = route;
  if (typeof _beforeEach === 'function') {
    try {
      const next = await _beforeEach(route);
      if (next === false) return;
    } catch (_) {}
  }
  const handler = _routes.get(route);
  const root = $('page-root');
  if (!root) return;
  root.innerHTML = '';
  const page = el('section', { class: 'page', 'data-route': route });
  root.appendChild(page);
  document.dispatchEvent(new CustomEvent('orvix:route', { detail: { route, leaving: _currentHandler } }));
  try {
    if (handler) {
      _currentHandler = await handler(page);
    } else if (_notFound) {
      await _notFound(page, route);
    } else {
      page.appendChild(el('div', { class: 'panel missing-route', text: 'Route not found: ' + route }));
    }
  } catch (err) {
    page.appendChild(el('div', { class: 'panel error-panel' }, [
      el('div', { class: 'panel-head' }, el('h3', { text: 'Page failed to load' })),
      el('div', { class: 'panel-body', text: (err && err.message) || String(err) }),
    ]));
  } finally {
    document.dispatchEvent(new CustomEvent('orvix:route:done', { detail: { route } }));
  }
}

function readHash() {
  const h = (location.hash || '').replace(/^#\/?/, '');
  return h || 'dashboard';
}

let _started = false;
export function startRouter() {
  if (_started) return;
  _started = true;
  window.addEventListener('hashchange', () => {
    runRoute(readHash()).catch((err) => console.error('route', err));
  });
  // Kick off.
  if (!location.hash) {
    location.hash = '#/dashboard';
  }
  runRoute(readHash()).catch((err) => console.error('route', err));
}

export async function reload() {
  await runRoute(readHash());
}
