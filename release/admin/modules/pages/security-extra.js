/* =====================================================================
   pages/security-extra.js — Honest "needs backend" panels for
   security features that don't have full backend support in
   this build (SSL cert manager, anti-spam control, antivirus,
   acceptance & routing, incoming rules). Each panel reads the
   existing security surface where it exists and clearly
   states where it does not.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

// renderSecurityExtraPage renders a single placeholder card
// explaining that the feature has no UI surface in this build.
// Used by app.js for the routes that don't have a dedicated
// page module. Keeps the contract honest — no fake claims.
export function renderSecurityExtraPage(root, opts) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: opts.title || 'Security' }),
      el('p', { class: 'page-subtitle subtle', text: opts.subtitle || '' }),
    ]),
  ]));

  const card = el('section', { class: 'panel panel-placeholder' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: opts.feature || 'Feature' }),
    el('span', { class: 'badge tag', text: 'Not yet exposed' }),
  ]));
  card.appendChild(el('div', { class: 'panel-body' }, [
    el('p', { text: opts.body ||
      'This feature is not yet exposed in the admin console. The backend primitives may exist (look at the relevant Go package), but the admin UI surface is not built in this release.' }),
  ]));
  if (opts.endpoint) {
    card.appendChild(el('div', { class: 'panel-foot' }, [
      el('span', { class: 'subtle', text: 'Planned endpoint: ' }),
      el('code', { class: 'code', text: opts.endpoint }),
    ]));
  }
  wrap.appendChild(card);
  root.appendChild(wrap);
  applyAutoDir(wrap);
}

// SSL certificates: read what's already wired in /etc/orvix/tls
// through a runtime snapshot. We use the runtime telemetry
// endpoint to discover the live TLS cert paths; without
// upload / issuance we only render a read-only snapshot.
export async function renderSslPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'SSL certificates' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Read-only view of TLS certs loaded by the runtime listeners.' }),
    ]),
  ]));
  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Live runtime TLS' })));
  const body = el('div', { class: 'panel-body' });
  panel.appendChild(body);
  wrap.appendChild(panel);
  root.appendChild(wrap);

  try {
    const rt = await apiGet('/api/v1/admin/runtime');
    const listeners = (rt && rt.listeners) || [];
    const tls = listeners.filter((l) => l.tls === true || l.tls === 'enabled' || l.tls === 'true');
    if (!tls.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No TLS-enabled listeners configured. Configure certificates via setup-smtp-tls.sh.' }));
    } else {
      const tbl = el('table', { class: 'data-table' });
      tbl.appendChild(el('thead', null, el('tr', null, [
        el('th', { text: 'Listener' }),
        el('th', { text: 'Address' }),
        el('th', { text: 'TLS' }),
        el('th', { text: 'Cert path' }),
      ])));
      const tb = el('tbody');
      tls.forEach((l) => {
        tb.appendChild(el('tr', null, [
          el('td', { text: l.name || l.kind || '' }),
          el('td', { text: l.address || '' }),
          el('td', { text: String(l.tls) }),
          el('td', { text: l.cert_path || '—' }),
        ]));
      });
      tbl.appendChild(tb);
      body.appendChild(tbl);
    }
  } catch (e) {
    body.appendChild(el('div', { class: 'empty',
      text: 'Could not load runtime telemetry: ' + (e.message || e) }));
  }
  applyAutoDir(wrap);
}