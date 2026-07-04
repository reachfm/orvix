/* =====================================================================
   pages/migration.js — Migration jobs UI.

   Reads /api/v1/migration/jobs (real endpoint, exists in
   internal/migration). The page renders the real job list
   with status / progress. If the endpoint returns an error
   (e.g. migration engine not initialised in dev), we render
   an honest empty state.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderMigrationPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Migration jobs' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Migrate mailboxes from an IMAP / Exchange source to Orvix.' }),
    ]),
  ]));
  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Jobs' })));
  const body = el('div', { class: 'panel-body' });
  panel.appendChild(body);
  wrap.appendChild(panel);
  root.appendChild(wrap);

  try {
    const r = await apiGet('/api/v1/migration/jobs');
    const jobs = (r && r.jobs) || (Array.isArray(r) ? r : []);
    if (!jobs.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No migration jobs. Use POST /api/v1/migration/test to validate a source first.' }));
      return;
    }
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: 'Job ID' }),
      el('th', { text: 'Source' }),
      el('th', { text: 'Status' }),
      el('th', { text: 'Created' }),
      el('th', { text: 'Updated' }),
    ])));
    const tb = el('tbody');
    jobs.forEach((j) => {
      tb.appendChild(el('tr', null, [
        el('td', { text: String(j.id || '') }),
        el('td', { text: j.source || j.source_host || '' }),
        el('td', { text: j.status || '' }),
        el('td', { text: j.created_at || '' }),
        el('td', { text: j.updated_at || '' }),
      ]));
    });
    tbl.appendChild(tb);
    body.appendChild(tbl);
  } catch (e) {
    body.appendChild(el('div', { class: 'empty',
      text: 'Migration engine not initialised in this deployment: ' + (e.message || e) }));
  }
  applyAutoDir(wrap);
}