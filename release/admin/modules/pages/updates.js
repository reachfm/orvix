/* =====================================================================
   pages/updates.js — Update status + preflight.

   Wires:
     GET  /api/v1/update/status
     GET  /api/v1/update/preflight
     GET  /api/v1/update/check
     POST /api/v1/update/check  (CSRF) — run a check
     POST /api/v1/update/run    (CSRF) — apply update
   ===================================================================== */

import { el, table, fmtShortDate, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderUpdatesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('updates.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'Current build and pre-flight checks.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // Current build card
  const buildCard = el('section', { class: 'panel' });
  buildCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: t('updates.current') })));
  const buildBody = el('div', { class: 'panel-body' });
  buildCard.appendChild(buildBody);
  wrap.appendChild(buildCard);

  // Preflight card
  const preCard = el('section', { class: 'panel' });
  preCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: t('updates.preflight') })));
  const preBody = el('div', { class: 'panel-body' });
  preCard.appendChild(preBody);
  wrap.appendChild(preCard);

  // Latest available update (if any)
  const upCard = el('section', { class: 'panel' });
  upCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Available update' })));
  const upBody = el('div', { class: 'panel-body' });
  upCard.appendChild(upBody);
  wrap.appendChild(upCard);

  // Actions
  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('updates.check'),
    onclick: () => doCheck(applyUpdates) }));
  actions.appendChild(el('button', { class: 'btn primary danger', type: 'button', text: t('updates.apply'),
    onclick: () => doApply() }));
  wrap.appendChild(actions);

  applyUpdates();

  function applyUpdates() {
    buildBody.innerHTML = '';
    preBody.innerHTML = '';
    upBody.innerHTML = '';
    buildBody.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    preBody.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    upBody.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    Promise.allSettled([
      apiGet('/api/v1/update/status'),
      apiGet('/api/v1/update/preflight'),
      apiGet('/api/v1/update/check'),
    ]).then(([status, preflight, latest]) => {
      // Build / current version.
      buildBody.innerHTML = '';
      if (status.status === 'fulfilled' && status.value) {
        const s = status.value;
        const dl = el('dl', { class: 'kv' });
        const items = [
          ['Version', s.current_version || s.version || '-'],
          ['Commit',  s.current_commit || s.commit || '-'],
          ['Build time', s.current_build_time || s.build_time || '-'],
          ['Channel', s.channel || 'stable'],
        ];
        items.forEach(([k, v]) => {
          dl.appendChild(el('dt', { text: k }));
          dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
        });
        buildBody.appendChild(dl);
      } else {
        buildBody.appendChild(el('div', { class: 'empty', text: t('common.empty') }));
      }
      // Preflight.
      preBody.innerHTML = '';
      if (preflight.status === 'fulfilled' && preflight.value) {
        const p = preflight.value;
        const ul = el('ul', { class: 'kv-list' });
        const items = p.checks || p.results || [];
        if (items.length) {
          items.forEach((c) => {
            const st = (c.status || c.state || 'unknown').toLowerCase();
            const k = st === 'ok' || st === 'pass' ? 'good' : (st === 'warn' ? 'warn' : (st === 'fail' || st === 'error' ? 'bad' : 'neutral'));
            ul.appendChild(el('li', { class: 'kv-row' }, [
              el('span', { class: 'kv-k', text: c.name || c.id || '-' }),
              el('span', { class: 'kv-v' }, el('span', { class: 'badge ' + k, text: st })),
              el('span', { class: 'kv-d', text: c.message || c.detail || '' }),
            ]));
          });
        } else {
          ul.appendChild(el('li', { class: 'kv-row' }, [
            el('span', { class: 'kv-k', text: 'Writable' }),
            el('span', { class: 'kv-v' }, el('span', { class: 'badge ' + (p.writable ? 'good' : 'bad'), text: p.writable ? 'yes' : 'no' })),
          ]));
          ul.appendChild(el('li', { class: 'kv-row' }, [
            el('span', { class: 'kv-k', text: 'Disk free (MB)' }),
            el('span', { class: 'kv-v', text: String(p.disk_free_mb || '-') }),
          ]));
        }
        preBody.appendChild(ul);
      } else {
        preBody.appendChild(el('div', { class: 'empty', text: t('common.empty') }));
      }
      // Latest available.
      upBody.innerHTML = '';
      if (latest.status === 'fulfilled' && latest.value) {
        const u = latest.value;
        // Backend may signal "no update feed configured" via:
        //   u.configured === false
        //   u.feed_configured === false
        //   u.not_configured === true
        //   u.error === 'not_configured'
        // We render an honest "update check not configured"
        // state for any of these. Otherwise render the latest
        // version, SHA, and release notes (keyed by release_notes,
        // notes, or changelog to be lenient across backend
        // revisions).
        const configured = (u.configured === false
          || u.feed_configured === false
          || u.not_configured === true
          || u.error === 'not_configured');
        if (configured) {
          upBody.appendChild(el('div', { class: 'empty', text: 'update check not configured' }));
        } else if (u.update_available === false || !u.latest) {
          upBody.appendChild(el('div', { class: 'empty', text: 'No update available.' }));
        } else {
          const dl = el('dl', { class: 'kv' });
          const latest = u.latest;
          const items = [
            ['Version', latest.latest_version || latest.version || '-'],
            ['Commit',  latest.latest_sha    || latest.sha     || latest.commit || '-'],
            ['Channel', latest.channel || 'stable'],
            ['Notes',   latest.release_notes || latest.notes || latest.changelog || '-'],
          ];
          items.forEach(([k, v]) => {
            dl.appendChild(el('dt', { text: k }));
            dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
          });
          upBody.appendChild(dl);
        }
      } else {
        upBody.appendChild(el('div', { class: 'empty', text: t('common.empty') }));
      }
      applyAutoDir(wrap);
    });
  }
}

async function doCheck(after) {
  try {
    await apiPost('/api/v1/update/check', {});
    toast('Update check triggered', 'success', 1800);
    setTimeout(() => after && after(), 600);
  } catch (e) {
    toast((e && e.message) || 'check failed', 'error', 6000);
  }
}

async function doApply() {
  const ok = await confirmDanger({
    title: 'Apply update',
    message: 'The runner will perform a single-flight update. A second concurrent call returns 409. Continue?',
    confirmLabel: 'Apply update',
  });
  if (!ok) return;
  try {
    await apiPost('/api/v1/update/run', {});
    toast('Update started', 'success', 4000);
  } catch (e) {
    toast((e && e.message) || 'apply failed', 'error', 6000);
  }
}
