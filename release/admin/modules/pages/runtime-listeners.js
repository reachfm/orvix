/* =====================================================================
   pages/runtime-listeners.js — Real listener telemetry.

   Wires:
     GET /api/v1/admin/runtime  →  { listeners: [{kind,port,state,detail}, …] }

   Renders one row per listener with the normalized proof
   vocabulary from the runtime registry:
     * active    = real bind succeeded
     * skipped   = intentionally not listening (e.g. cert missing)
     * degraded  = bound but impaired (e.g. STARTTLS unavailable)
     * failed    = bind error / port-in-use
     * unknown   = state not yet reported

   The page never claims a secure port is active when the
   backend reports "skipped".
   ===================================================================== */

import { el, badge, fmtShortDate } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { setRuntime, getRuntime } from '../state.js';
import { applyAutoDir } from '../rtl.js';

const STATE_KIND = {
  active:    'good',
  skipped:   'bad',
  failed:    'bad',
  unknown:   'neutral',
  degraded:  'warn',
  starting:  'warn',
};

const STATE_LABEL = {
  active:    'active',
  skipped:   'skipped',
  failed:    'failed',
  unknown:   'unknown',
  degraded:  'degraded',
  starting:  'starting',
};

export async function renderRuntimeListenersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('runtime.heading') }),
      el('p', { class: 'page-subtitle subtle', text: t('runtime.subtitle') }),
    ]),
  ]));
  root.appendChild(wrap);

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Listener state' }),
    el('span', { class: 'subtle', id: 'runtime-stamp', text: t('common.loading') }),
  ]));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  let data, err;
  try { data = await apiGet('/api/v1/admin/runtime'); }
  catch (e) { err = e; }
  body.innerHTML = '';
  if (err) {
    body.appendChild(el('div', { class: 'error', text: (err && err.message) || 'Could not load runtime telemetry.' }));
    applyAutoDir(wrap);
    return;
  }
  setRuntime(data || {});
  const stamp = $('runtime-stamp');
  if (stamp) stamp.textContent = t('common.lastUpdated', { when: fmtShortDate((data && data.collected_at) || new Date().toISOString()) });

  const listeners = (data && data.listeners) || [];
  if (listeners.length === 0) {
    body.appendChild(el('div', { class: 'empty', text: 'No listener telemetry reported by the runtime.' }));
    applyAutoDir(wrap);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Listener' }),
    el('th', { text: 'Port' }),
    el('th', { text: 'State' }),
    el('th', { text: 'Detail' }),
  ])));
  const tb = el('tbody');
  listeners.forEach((l) => {
    const state = (l.state || 'unknown').toLowerCase();
    const kind = STATE_KIND[state] || 'neutral';
    tb.appendChild(el('tr', null, [
      el('td', { text: l.label || l.kind || '-' }),
      el('td', { text: l.port ? String(l.port) : '-' }),
      el('td', null, badge(STATE_LABEL[state] || state, kind)),
      el('td', { class: 'kv-v', text: l.detail || '-' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);

  // Honest footer about what "skipped" means.
  const note = el('p', { class: 'subtle', text: '"skipped" means the listener is intentionally not running (e.g. no TLS cert). It is NOT active and never claims to be.' });
  body.appendChild(note);

  applyAutoDir(wrap);
}
