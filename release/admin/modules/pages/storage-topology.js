/* =====================================================================
   pages/storage-topology.js — Storage topology (honest single backend).

   Wires:
     GET /api/v1/admin/storage/volumes

   Audit doc reference: docs/ORVIX_STALWART_ENTERPRISE_PARITY_AUDIT.md
     §2.6 row 28 (Storage topology)
     §3 deliverable G (Storage topology truth)

   Honesty contract:
     - We NEVER render a "replica", "shard", "primary/standby"
       control. The handler returns a single on-disk role per
       volume and we surface it as-is.
     - Each volume row reports the real TotalBytes / UsedBytes /
       FreeBytes. On platforms where statfs is wired through a
       different syscall the row reports Available=false with a
       transparent reason.
   ===================================================================== */

import { el, badge } from '../components.js';
import { t } from '../i18n.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

function fmtPct(n) {
  if (n == null || isNaN(n)) return '-';
  return (Math.round(n * 10) / 10).toFixed(1) + '%';
}

function fmtBytes_(n) {
  if (n == null || isNaN(n)) return '-';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(v >= 100 ? 0 : 1) + ' ' + units[i];
}

export async function renderStorageTopologyPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Storage topology' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Single-backend deployment. The list below reports real on-disk directories used by the orvix process.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Volumes' }),
    el('span', { class: 'badge tag', text: 'single-node' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  apiGet('/api/v1/admin/storage/volumes').then((data) => {
    body.innerHTML = '';
    const list = (data && data.volumes) || [];
    if (!list.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No volumes configured. Connect mailstore / attachments / backups in orvix.yaml and reload.' }));
    } else {
      const tbl = el('table', { class: 'data-table storage-table' });
      tbl.appendChild(el('thead', null, el('tr', null, [
        el('th', { text: 'Mounted' }),
        el('th', { text: 'Role' }),
        el('th', { text: 'Available' }),
        el('th', { text: 'Used' }),
        el('th', { text: 'Free' }),
        el('th', { text: 'Used %' }),
        el('th', { text: 'Detail' }),
      ])));
      const tb = el('tbody');
      list.forEach((v) => {
        const available = !!v.available;
        tb.appendChild(el('tr', null, [
          el('td', { class: 'kv-v', text: v.mounted || '-' }),
          el('td', { class: 'kv-v', text: v.role || '-' }),
          el('td', { class: 'kv-v' }, badge(available ? 'present' : 'missing', available ? 'good' : 'neutral')),
          el('td', { class: 'kv-v', text: fmtBytes_(v.used_bytes) }),
          el('td', { class: 'kv-v', text: fmtBytes_(v.free_bytes) }),
          el('td', { class: 'kv-v', text: fmtPct(v.used_pct) }),
          el('td', { class: 'kv-v', text: v.detail || '' }),
        ]));
      });
      tbl.appendChild(tb);
      body.appendChild(tbl);
    }
    if (data && data.honest_note) {
      body.appendChild(el('p', { class: 'subtle ops-honest-note', text: data.honest_note }));
    }
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Storage topology unavailable: ' + ((err && err.message) || err) }));
  });

  applyAutoDir(wrap);
}
