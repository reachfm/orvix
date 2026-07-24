/* =====================================================================
   pages/alerts.js — Active alerts + delivery audit.

   Wires:
     GET  /api/v1/monitoring/alerts
     POST /api/v1/monitoring/alerts/:id/resolve   (CSRF)
     GET  /api/v1/monitoring/alert-deliveries     (audit)

   Audit doc reference: docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md
     §2.5 Observability, alerting, telemetry   (rows 21–25)

   Honesty contract:
     - The table never fabricates counts. Empty state replaces
       a missing endpoint or an empty response.
     - The delivery audit column is read directly from the
       secret-free monitoring.DeliveryRecord table — webhook
       URLs / tokens stay on the backend.
   ===================================================================== */

import { el, table, badge, fmtShortDate, openModal, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAlertsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('alerts.heading') || 'Active alerts' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Resolve active alerts and inspect the delivery audit feed.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'ops-grid' });
  wrap.appendChild(grid);
  grid.appendChild(buildAlertsTable());
  grid.appendChild(buildDeliveryAuditTable());

  applyAutoDir(wrap);
}

function buildAlertsTable() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: t('monitoring.alerts') || 'Active alerts' }),
    el('span', { class: 'badge tag', text: 'monitoring.resolve' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/alerts').then((data) => {
    body.innerHTML = '';
    const list = (data && (data.alerts || data)) || [];
    if (!list.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No active alerts. The monitoring dispatcher reports zero open incidents.' }));
      return;
    }
    body.appendChild(table({
      columns: [
        { name: 'sev', label: 'Severity', render: (r) => {
          const sev = (r.severity || 'warn').toLowerCase();
          const k = sev === 'critical' || sev === 'high' || sev === 'error' ? 'bad'
                  : sev === 'warn' ? 'warn'
                  : 'neutral';
          return badge(sev, k);
        } },
        { name: 'msg',  label: 'Message', render: (r) => r.message || r.title || r.code || '-' },
        { name: 'src',  label: 'Source',  render: (r) => r.source || r.component || '-' },
        { name: 'when', label: 'Detected', render: (r) => fmtShortDate(r.created_at || r.detected_at || '') },
        { name: 'a', label: '', cellClass: 'actions', render: (r) => {
          if (!r.id) return null;
          return el('button', { class: 'btn xs ghost', type: 'button',
            text: t('monitoring.resolve') || 'Resolve',
            onclick: () => resolveAlert(r.id) });
        } },
      ],
      rows: list,
    }));
  }).catch((err) => {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error',
      text: 'Could not load alerts: ' + ((err && err.message) || err) }));
  });
  return card;
}

function buildDeliveryAuditTable() {
  const card = el('section', { class: 'panel panel-wide' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Delivery audit' }),
    el('span', { class: 'badge tag', text: 'monitoring.read' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  apiGet('/api/v1/monitoring/alert-deliveries').then((data) => {
    body.innerHTML = '';
    const list = (data && data.deliveries) || [];
    if (!list.length) {
      body.appendChild(el('div', { class: 'empty',
        text: 'No deliveries recorded yet. The audit is populated as the dispatcher fans alerts out across providers.' }));
      return;
    }
    body.appendChild(table({
      columns: [
        { name: 'when',  label: 'At',         render: (r) => fmtShortDate(r.createdAt || r.created_at || '') },
        { name: 'prov',  label: 'Provider',   render: (r) => r.provider || '-' },
        { name: 'stat',  label: 'Status',     render: (r) => {
          const s = (r.status || '').toLowerCase();
          const k = s === 'success' ? 'good' : s === 'failed' ? 'bad' : 'neutral';
          return badge(s || 'unknown', k);
        } },
        { name: 'sev',   label: 'Severity',   render: (r) => r.alertSeverity || '-' },
        { name: 'cat',   label: 'Category',   render: (r) => r.alertCategory || '-' },
        { name: 'title', label: 'Alert',      render: (r) => r.alertTitle || '-' },
        { name: 'detail', label: 'Detail',    render: (r) => {
          const d = r.detail || '';
          return d.length > 80 ? d.slice(0, 77) + '...' : d;
        } },
      ],
      rows: list,
    }));
  }).catch((err) => {
    // Honest: the audit endpoint may legitimately be empty on a fresh
    // install. We still surface "available / not wired" distinction.
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty',
      text: 'Delivery audit unavailable: ' + ((err && err.message) || err) }));
  });
  return card;
}

async function resolveAlert(id) {
  const ok = await confirmDanger({
    title: 'Resolve alert',
    message: 'Mark this alert as resolved?',
    confirmLabel: 'Resolve',
  });
  if (!ok) return;
  try {
    await apiPost('/api/v1/monitoring/alerts/' + encodeURIComponent(id) + '/resolve', {});
    toast('Alert resolved', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (err) {
    toast((err && err.message) || 'Resolve failed', 'error', 6000);
  }
}
