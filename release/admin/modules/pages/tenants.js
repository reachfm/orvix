/* =====================================================================
   pages/tenants.js — Tenants (read-only).

   Wires:
     GET /api/v1/admin/tenants/current

   Audit doc reference: docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md
     §2.3 row 9 (Multi-tenant scoping)
     §3 deliverable D (Tenants read-only)

   Honesty contract:
     - The page only reads the JWT-tenant row. We do NOT render
       a "create tenant", "switch tenant", or "delete tenant"
       button because those APIs are not exposed in this build.
     - The Branding editor is reachable on this page only so
       a super-admin operator can find it; it lives on a
       separate route (#/branding) for clarity.
   ===================================================================== */

import { el, fmtShortDate } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderTenantsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Tenants' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Read-only view of the active tenant row. Multi-tenant write APIs are not exposed in this build.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Current tenant' }),
  ]));
  const body = el('div', { class: 'panel-body', text: 'Loading...' });
  card.appendChild(body);
  wrap.appendChild(card);

  apiGet('/api/v1/admin/tenants/current').then((data) => {
    body.innerHTML = '';
    if (!data || data.exists === false) {
      body.appendChild(el('div', { class: 'empty',
        text: 'Tenant row not provisioned for this install. ' +
              'Multi-tenant write API is not exposed in this build.' }));
      return;
    }
    const tbl = el('table', { class: 'kv-table' });
    const rows = [
      ['Tenant ID',       data.id],
      ['Name',            data.name || '-'],
      ['Slug',            data.slug || '-'],
      ['Domain',          data.domain || '-'],
      ['Plan',            data.plan || '-'],
      ['Max domains',     data.max_domains || 0],
      ['Max mailboxes',   data.max_mailboxes || 0],
      ['Logo URL',        data.logo_url || '(none)'],
      ['Primary color',   data.primary_color || '(default)'],
      ['Active',          data.active ? 'yes' : 'no'],
      ['Created',         fmtShortDate(data.created_at || '')],
      ['Updated',         fmtShortDate(data.updated_at || '')],
    ];
    rows.forEach(([k, v]) => {
      tbl.appendChild(el('tr', null, [
        el('th', { text: k }),
        el('td', { class: 'kv-v', text: String(v) }),
      ]));
    });
    body.appendChild(tbl);
    // Quick links to the pages that operate on the same data.
    const nav = el('div', { class: 'page-links' }, [
      el('a', { class: 'btn ghost', href: '#/branding',
        text: 'Edit branding' }),
    ]);
    body.appendChild(nav);
    if (data.honest_note) {
      body.appendChild(el('p', { class: 'subtle ops-honest-note',
        text: data.honest_note }));
    }
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Tenant lookup failed: ' + ((err && err.message) || err) }));
  });

  applyAutoDir(wrap);
}
