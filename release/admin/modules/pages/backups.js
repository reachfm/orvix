/* =====================================================================
   pages/backups.js — Backup & Restore UI.

   Wires:
     GET   /api/v1/admin/backups             → list
     GET   /api/v1/admin/backups/schedule     → current schedule
     GET   /api/v1/admin/backups/metrics      → counters
     GET   /api/v1/admin/backups/health       → health snapshot
     POST  /api/v1/admin/backups              → create (manual)
     POST  /api/v1/admin/backups/:id/validate → validate archive
     POST  /api/v1/admin/backups/:id/restore  → restore (dangerous)
     DELETE /api/v1/admin/backups/:id         → delete archive

   Restore is double-gated:
     1. The operator must type the backup id exactly.
     2. A second dialog confirms with a typed phrase.
   Restore is a LIVE, asynchronous, job-based operation. POST returns 202 with
   a job id; an external privileged coordinator (orvix-restore.service)
   activates the backup, restarts Orvix (this UI's connection may drop),
   verifies the restarted service's health, and automatically rolls back to a
   pre-restore safety backup on failure. The UI polls
   GET /api/v1/admin/backups/restore-jobs/:job_id and shows success ONLY after
   the coordinator reports the restarted service is healthy.
   ===================================================================== */

import { el, table, badge, fmtShortDate, fmtBytes, openModal, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderBackupsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('backups.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'Backups, validation, and dangerous restore (typed confirmation required).' }),
    ]),
  ]));
  root.appendChild(wrap);

  // Top row: 4 cards — health, count, retention, last.
  const top = el('div', { class: 'kv-cards' });
  wrap.appendChild(top);

  // Action row.
  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn primary', type: 'button', text: t('backups.create'),
    onclick: () => doCreate() }));
  wrap.appendChild(actions);

  // Table.
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Backup archive list' })));
  const body = el('div', { class: 'panel-body', text: t('common.loading') });
  card.appendChild(body);
  wrap.appendChild(card);

  // Load all in parallel.
  let list, schedule, metrics, health;
  [list, schedule, metrics, health] = await Promise.all([
    apiGet('/api/v1/admin/backups').catch((e) => { return { __err: e }; }),
    apiGet('/api/v1/admin/backups/schedule').catch(() => null),
    apiGet('/api/v1/admin/backups/metrics').catch(() => null),
    apiGet('/api/v1/admin/backups/health').catch(() => null),
  ]);

  paintTop(top, { list, schedule, metrics, health });
  body.innerHTML = '';
  if (list && list.__err) { body.appendChild(el('div', { class: 'error', text: list.__err.message || 'load failed' })); applyAutoDir(wrap); return; }
  const items = (list && (list.backups || list)) || [];
  if (!items.length) { body.appendChild(el('div', { class: 'empty', text: t('common.empty') })); applyAutoDir(wrap); return; }
  // Automatic restore notice. RestoreBackup validates, safety-snapshots,
  // activates, restarts, health-verifies, and rolls back on failure.
  body.appendChild(el('p', { class: 'subtle' }, [
    el('strong', { text: 'restore-orvix-backup' }),
    el('span', { text: ': Restore replaces ALL current mailboxes, domains, and rules. The system creates a pre-restore safety backup, activates the selected backup, restarts the service, and verifies system health. On failure the system automatically rolls back to the safety backup.' }),
  ]));
  body.appendChild(table({
    columns: [
      { name: 'id',   label: 'ID', render: (r) => r.id || r.backup_id || '-' },
      { name: 'when', label: 'Created', render: (r) => fmtShortDate(r.created_at || r.time || '') },
      { name: 'size', label: 'Size', render: (r) => fmtBytes(r.size || r.size_bytes) },
      { name: 'kind', label: 'Kind', render: (r) => r.kind || r.type || 'manual' },
      { name: 'ok',   label: 'Validated', render: (r) => r.validated ? badge('yes', 'good') : badge('no', 'neutral') },
      { name: 'a', label: 'Actions', cellClass: 'actions', render: (r) => {
        const wrap = el('div', { class: 'row-actions' });
        if (r.id) {
          wrap.appendChild(el('button', { class: 'btn xs ghost', type: 'button', text: t('backups.validate'),
            onclick: () => doValidate(r.id) }));
          wrap.appendChild(el('button', { class: 'btn xs ghost', type: 'button', text: t('backups.restore'),
            onclick: () => doRestore(r.id) }));
          wrap.appendChild(el('button', { class: 'btn xs danger', type: 'button', text: t('backups.delete'),
            onclick: () => doDelete(r.id) }));
        }
        return wrap;
      } },
    ],
    rows: items,
  }));
  applyAutoDir(wrap);
}

// runRetention is the typed-confirmation-gated retention runner.
// It is wired into the same /admin/backups/retention endpoint that
// the legacy monolithic app.js used; the contract is preserved
// here so the static-analysis test (TestAdminRetentionUsesConfirmDanger)
// keeps matching after the modular refactor.
//
// Order matters: confirmDanger must be called BEFORE the apiPost,
// requireText must be the literal 'retention', and the apiPost
// must be inside the success path (i.e. gated by the `if (!ok) return;`
// check). The retain/delete operation is irreversible and the
// typed-confirmation step is mandatory.
export async function runRetention() {
  const ok = await confirmDanger({
    title: 'Run backup retention',
    message: 'Retention will delete backup archives older than the configured retention. This is irreversible. Type retention to confirm.',
    confirmLabel: 'Run retention',
    requireText: 'retention',
  });
  if (!ok) return;
  try {
    await apiPost('/api/v1/admin/backups/retention', {});
    toast('Retention run started', 'success', 1800);
  } catch (e) {
    toast((e && e.message) || 'retention failed', 'error', 6000);
  }
}

function paintTop(host, { list, schedule, metrics, health }) {
  host.innerHTML = '';
  const cells = [
    ['Health',  (health && (health.healthy ? 'healthy' : (health.status || 'unknown'))) || '-'],
    ['Count',   list && (list.backups || list).length || 0],
    ['Last',    list && (list.backups || list)[0] && fmtShortDate((list.backups || list)[0].created_at) || '-'],
    ['Schedule', schedule && schedule.schedule || schedule.cron || '-'],
  ];
  cells.forEach(([k, v]) => {
    host.appendChild(el('div', { class: 'kv-cell' }, [
      el('dt', { text: k }),
      el('dd', { class: 'kv-v', text: String(v) }),
    ]));
  });
}

async function doCreate() {
  const ok = await confirmDanger({ title: 'Create backup', message: 'Create a backup archive now? Existing archives are NOT deleted.', confirmLabel: 'Create' });
  if (!ok) return;
  try {
    const r = await apiPost('/api/v1/admin/backups', {});
    toast('Backup created', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (err) {
    toast((err && err.message) || 'Create failed', 'error', 6000);
  }
}

async function doValidate(id) {
  try {
    await apiPost('/api/v1/admin/backups/' + encodeURIComponent(id) + '/validate', {});
    toast('Backup valid', 'success', 1800);
  } catch (err) {
    toast((err && err.message) || 'Validation failed', 'error', 6000);
  }
}

async function doDelete(id) {
  const ok = await confirmDanger({ title: 'Delete backup', message: 'Type "delete-orvix-backup" to confirm permanent deletion.', confirmLabel: 'Delete', requireText: 'delete-orvix-backup' });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/backups/' + encodeURIComponent(id), {
      headers: { 'X-Orvix-Confirm': 'delete-orvix-backup' },
      body: JSON.stringify({ confirm: 'delete-orvix-backup' })
    });
    toast('Backup deleted', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (err) {
    toast((err && err.message) || 'Delete failed', 'error', 6000);
  }
}

async function doRestore(id) {
  // Stage 1: typed id confirmation.
  const stage1 = await confirmDanger({
    title: 'Restore backup',
    message: 'Restoring a backup replaces ALL current mailboxes, domains, and rules. A pre-restore safety backup is created automatically. Restore runs as a background job: Orvix will RESTART (this page may briefly lose connection), then the restarted service is health-checked. On any failure the system automatically rolls back to the safety backup. Progress is tracked as a restore job.',
    confirmLabel: 'Continue',
    requireText: id,
  });
  if (!stage1) return;
  // Stage 2: phrase confirmation.
  const stage2 = await confirmDanger({
    title: 'Final confirmation',
    message: 'Type the phrase restore-orvix-backup to confirm.',
    confirmLabel: 'Restore',
    requireText: 'restore-orvix-backup',
  });
  if (!stage2) return;
  let job;
  try {
    // Restore is asynchronous: the API accepts the job (202) and an external
    // coordinator activates the backup, restarts Orvix, and verifies health.
    job = await apiPost('/api/v1/admin/backups/' + encodeURIComponent(id) + '/restore', { confirm: 'restore-orvix-backup' });
  } catch (err) {
    toast((err && err.message) || 'Restore could not be submitted', 'error', 6000);
    return;
  }
  if (!job || !job.job_id) {
    toast('Restore submitted, but no job id was returned', 'error', 6000);
    return;
  }
  toast('Restore started. Orvix will restart and this connection may drop; tracking job ' + job.job_id.slice(0, 12) + '…', 'info', 6000);
  pollRestoreJob(job.job_id);
}

// pollRestoreJob follows the durable restore job status. The Orvix service is
// restarted by the external coordinator, so polls will fail transiently while
// it is down; those errors are tolerated until the job reaches a terminal
// state or the poll budget is exhausted. Success is shown ONLY when the
// coordinator reports the restarted service passed its health check.
async function pollRestoreJob(jobId) {
  const url = '/api/v1/admin/backups/restore-jobs/' + encodeURIComponent(jobId);
  const deadline = Date.now() + 5 * 60 * 1000; // 5 minutes
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 3000));
    let res;
    try {
      res = await apiGet(url);
    } catch (_e) {
      // Expected while Orvix is restarting; keep polling.
      continue;
    }
    if (!res || !res.status) continue;
    if (res.status === 'succeeded') {
      toast('Restore complete — restarted service verified healthy', 'success', 6000);
      return;
    }
    if (res.status === 'failed') {
      const rb = res.rolled_back ? ' (rolled back to the pre-restore safety backup)' : '';
      toast('Restore failed' + rb + (res.error ? ': ' + res.error : ''), 'error', 9000);
      return;
    }
    // pending / activating / restarting / verifying / rolling_back — keep going.
  }
  toast('Restore is still in progress; check the restore job status shortly.', 'info', 6000);
}
