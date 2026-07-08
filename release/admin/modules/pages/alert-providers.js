/* =====================================================================
   pages/alert-providers.js — Alert provider status.

   Wires GET /api/v1/monitoring/alert-providers. Renders the
   configured providers with enabled/disabled state, the redacted
   target URL (we strip the embedded token before display), and
   last delivery status if the backend exposes it.

   No secret is ever sent back to the operator: the page calls
   redactTarget() on every target string before rendering.
   ===================================================================== */

import { el, badge } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

function redactTarget(s) {
  if (s == null) return '';
  const str = String(s);
  // Replace the userinfo portion of any URL.
  return str.replace(/([a-zA-Z][a-zA-Z0-9+.-]*:\/\/)[^@\s]+@/g, '$1***@');
}

export async function renderAlertProvidersPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('monitoring.alertProviders') }),
      el('p', { class: 'page-subtitle subtle', text: 'Provider status and redacted destinations. Secrets are never returned.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Providers' })));
  const body = el('div', { class: 'panel-body', text: t('common.loading') });
  card.appendChild(body);
  wrap.appendChild(card);

  let data, err;
  try { data = await apiGet('/api/v1/monitoring/alert-providers'); }
  catch (e) { err = e; }
  body.innerHTML = '';
  if (err) {
    body.appendChild(el('div', { class: 'error', text: (err && err.message) || 'Could not load alert providers.' }));
    applyAutoDir(wrap);
    return;
  }
  const providers = (data && (data.providers || data)) || [];
  if (!providers.length) {
    body.appendChild(el('div', { class: 'empty', text: 'No alert providers configured.' }));
    applyAutoDir(wrap);
    return;
  }

  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Provider' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: 'Target' }),
    el('th', { text: 'Last delivery' }),
  ])));
  const tb = el('tbody');
  providers.forEach((p) => {
    const enabled = p.enabled !== false && p.disabled !== true;
    tb.appendChild(el('tr', null, [
      el('td', { text: p.name || p.id || p.kind || '-' }),
      el('td', null, badge(enabled ? 'enabled' : 'disabled', enabled ? 'good' : 'bad')),
      el('td', { class: 'kv-v', text: redactTarget(p.target || p.url || p.endpoint || '-') }),
      el('td', { class: 'kv-v', text: p.last_status || p.last_delivery || p.last_error || '-' }),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
  applyAutoDir(wrap);
}
