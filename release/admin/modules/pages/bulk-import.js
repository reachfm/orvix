/* =====================================================================
   pages/bulk-import.js — Bulk mailbox CSV import UI.

   Wires the two real endpoints:
     POST /api/v1/mailboxes/import/dry-run
     POST /api/v1/mailboxes/import

   Flow:
     1. Operator pastes or uploads a CSV.
     2. Dry-run button parses + validates everything without writing.
        The UI surfaces:
          - created count
          - skipped (validation error) count
          - row-level errors with line + email + reason
        No password is ever rendered to the operator; the dry-run
        result only carries error metadata.
     3. Import button is enabled only if:
          - dry-run produced 0 errors, OR
          - the operator explicitly enabled "allow partial" mode
            and accepts the warning dialog.
     4. After a successful import the textarea is cleared. Passwords
        are not echoed, logged to the console, or stored.

   Security:
     * Every mutating call goes through csrfFetch (auto CSRF header).
     * No password is ever displayed in the response.
     * The local component state holds a single `csv` string; the
       password column is not retained in any client-side cache.
   ===================================================================== */

import { el, table, badge, statusKind, copyToClipboard, openModal, confirmDanger, $ } from '../components.js';
import { t } from '../i18n.js';
import { apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir, withAutoDir } from '../rtl.js';

const TEMPLATE = 'email,password,name,quota_mb\nalice@example.com,Password1,Alice,512\nbob@example.com,Password2,Bob,1024\n';

let _lastDryRun = null;     // server response of the last dry run
let _csv = '';              // current textarea value
let _allowPartial = false;  // user toggle

export async function renderBulkImportPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('bulk.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'CSV upload — validate, dry-run, then commit.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // Card 1: input + actions.
  wrap.appendChild(buildInputCard());
  // Card 2: dry-run results (empty initially).
  wrap.appendChild(buildDryRunCard());
  // Card 3: import result.
  wrap.appendChild(buildImportCard());

  applyAutoDir(wrap);
}

function buildInputCard() {
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'CSV input' }),
    el('div', { class: 'panel-head-actions' }, [
      el('button', { class: 'btn xs ghost', type: 'button', text: t('bulk.template'),
        onclick: () => downloadTemplate() }),
    ]),
  ]));
  const body = el('div', { class: 'panel-body' });
  body.appendChild(el('p', { class: 'subtle', text: 'Required columns: email, password, name, quota_mb. Header row required. UTF-8 only.' }));

  // File upload.
  const fileInput = el('input', { type: 'file', accept: '.csv,text/csv', 'aria-label': 'CSV file' });
  fileInput.addEventListener('change', (ev) => {
    const f = ev.target.files && ev.target.files[0];
    if (!f) return;
    if (f.size > 8 * 1024 * 1024) {
      toast('CSV file too large (8 MiB max)', 'error');
      ev.target.value = '';
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      _csv = String(reader.result || '');
      ta.value = _csv;
      _lastDryRun = null;
      paintDryRun();
    };
    reader.readAsText(f);
  });
  body.appendChild(el('div', { class: 'form-row' }, [
    el('label', { for: 'bulk-file', text: t('bulk.upload') + ':' }),
    fileInput,
  ]));

  // Paste area.
  const ta = el('textarea', { id: 'bulk-csv', rows: 12, spellcheck: 'false', autocomplete: 'off',
    placeholder: 'email,password,name,quota_mb\nalice@example.com,Password1,Alice,512\n',
    style: 'width:100%;font-family:var(--font-mono);font-size:12px;' });
  ta.value = _csv;
  ta.addEventListener('input', () => {
    _csv = ta.value;
    _lastDryRun = null;
    paintDryRun();
  });
  body.appendChild(el('div', { class: 'form-row' }, [
    el('label', { for: 'bulk-csv', text: t('bulk.paste') + ':' }),
    ta,
  ]));

  // Allow partial toggle.
  const partialRow = el('div', { class: 'form-row' });
  const partialCb = el('input', { type: 'checkbox', id: 'bulk-partial' });
  partialCb.addEventListener('change', () => {
    _allowPartial = partialCb.checked;
    paintDryRun();
  });
  partialRow.appendChild(el('label', { for: 'bulk-partial', class: 'inline' }, [
    partialCb, ' ', t('bulk.partial'),
  ]));
  body.appendChild(partialRow);
  body.appendChild(el('p', { class: 'subtle', text: t('bulk.partialWarn') }));

  // Action row.
  const actions = el('div', { class: 'form-actions' });
  const dryBtn  = el('button', { class: 'btn ghost',   type: 'button', id: 'bulk-dry',   text: t('bulk.dryRun'),
    onclick: () => doDryRun() });
  const impBtn  = el('button', { class: 'btn primary', type: 'button', id: 'bulk-imp',   text: t('bulk.import'),
    onclick: () => doImport() });
  actions.appendChild(dryBtn);
  actions.appendChild(impBtn);
  body.appendChild(actions);

  card.appendChild(body);
  return card;
}

function buildDryRunCard() {
  const card = el('section', { class: 'panel', id: 'bulk-dry-card' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Dry run' }),
    el('span', { class: 'subtle', text: 'Not yet run' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  body.appendChild(el('div', { class: 'empty', text: 'Run a dry run to see row-level errors and counts.' }));
  card.appendChild(body);
  return card;
}

function buildImportCard() {
  const card = el('section', { class: 'panel', id: 'bulk-imp-card' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Import result' })));
  const body = el('div', { class: 'panel-body' });
  body.appendChild(el('div', { class: 'empty', text: 'No import run yet.' }));
  card.appendChild(body);
  return card;
}

function paintDryRun() {
  const card = $('bulk-dry-card');
  if (!card) return;
  const body = card.querySelector('.panel-body');
  body.innerHTML = '';
  if (!_lastDryRun) {
    body.appendChild(el('div', { class: 'empty', text: 'Run a dry run to see row-level errors and counts.' }));
    return;
  }
  const r = _lastDryRun;
  const head = el('div', { class: 'kv-cards' }, [
    el('div', { class: 'kv-cell' }, [el('dt', { text: 'Created' }),  el('dd', { class: 'kv-v', text: String(r.created || 0) })]),
    el('div', { class: 'kv-cell' }, [el('dt', { text: 'Skipped' }),  el('dd', { class: 'kv-v', text: String(r.skipped || 0) })]),
    el('div', { class: 'kv-cell' }, [el('dt', { text: 'Errors' }),   el('dd', { class: 'kv-v', text: String((r.errors || []).length) })]),
    el('div', { class: 'kv-cell' }, [el('dt', { text: 'Dry run' }),  el('dd', { class: 'kv-v', text: r.dryRun ? 'yes' : 'no' })]),
  ]);
  body.appendChild(head);

  const errs = r.errors || [];
  if (errs.length) {
    body.appendChild(el('h4', { text: 'Row-level errors' }));
    const tbl = table({
      columns: [
        { name: 'line',  label: 'Line',  render: (row) => row.line ? '#' + row.line : '-' },
        { name: 'email', label: 'Email', render: (row) => row.email || '-' },
        { name: 'error', label: 'Error', render: (row) => row.error || '-' },
      ],
      rows: errs,
      empty: 'No row-level errors.',
    });
    body.appendChild(tbl);
  }
  // Toggle import button enablement
  const impBtn = $('bulk-imp');
  if (impBtn) {
    const canAllOrNothing = errs.length === 0;
    const canPartial = errs.length > 0 && _allowPartial;
    impBtn.disabled = (canAllOrNothing || canPartial) ? null : 'disabled';
  }
}

function paintImportResult(r) {
  const card = $('bulk-imp-card');
  if (!card) return;
  const body = card.querySelector('.panel-body');
  body.innerHTML = '';
  if (!r) return;
  const head = el('div', { class: 'kv-cards' }, [
    el('div', { class: 'kv-cell' }, [el('dt', { text: 'Created' }),  el('dd', { class: 'kv-v', text: String(r.created || 0) })]),
    el('div', { class: 'kv-cell' }, [el('dt', { text: 'Skipped' }),  el('dd', { class: 'kv-v', text: String(r.skipped || 0) })]),
  ]);
  body.appendChild(head);
  if (r.errors && r.errors.length) {
    body.appendChild(el('h4', { text: 'Row-level errors' }));
    body.appendChild(table({
      columns: [
        { name: 'line',  label: 'Line',  render: (row) => row.line ? '#' + row.line : '-' },
        { name: 'email', label: 'Email', render: (row) => row.email || '-' },
        { name: 'error', label: 'Error', render: (row) => row.error || '-' },
      ],
      rows: r.errors,
    }));
  }
}

async function doDryRun() {
  if (!_csv.trim()) { toast('CSV body is empty', 'error'); return; }
  try {
    const r = await apiPost('/api/v1/mailboxes/import/dry-run', { csv: _csv });
    _lastDryRun = r || {};
    paintDryRun();
    toast('Dry run complete', 'success', 1800);
  } catch (err) {
    toast((err && err.message) || 'Dry run failed', 'error', 6000);
  }
}

async function doImport() {
  if (!_lastDryRun) {
    // Allow import without a prior dry run only if CSV is syntactically
    // obvious; the backend will still validate.
    await doDryRun();
    if (!_lastDryRun) return;
  }
  const errs = _lastDryRun.errors || [];
  if (errs.length > 0 && !_allowPartial) {
    toast('Dry run produced errors. Enable "allow partial" to continue.', 'error', 6000);
    return;
  }
  if (_allowPartial && errs.length > 0) {
    const ok = await confirmDanger({
      title: 'Import in partial mode',
      message: `${errs.length} row(s) will be skipped. The rest will be committed. Continue?`,
      confirmLabel: 'Continue with partial import',
    });
    if (!ok) return;
  } else {
    const ok = await confirmDanger({
      title: 'Import mailboxes',
      message: 'This will create mailboxes. Continue?',
      confirmLabel: 'Import',
    });
    if (!ok) return;
  }
  try {
    const r = await apiPost('/api/v1/mailboxes/import', { csv: _csv, allow_partial: !!_allowPartial });
    paintImportResult(r || {});
    // Wipe local CSV state — passwords cleared, textarea reset.
    _csv = '';
    _lastDryRun = null;
    const ta = $('bulk-csv');
    if (ta) ta.value = '';
    paintDryRun();
    toast('Import complete: ' + (r.created || 0) + ' created, ' + (r.skipped || 0) + ' skipped', 'success', 4000);
  } catch (err) {
    toast((err && err.message) || 'Import failed', 'error', 6000);
  }
}

function downloadTemplate() {
  const blob = new Blob([TEMPLATE], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = el('a', { href: url, download: 'mailbox-import-template.csv' });
  document.body.appendChild(a);
  a.click();
  setTimeout(() => { URL.revokeObjectURL(url); a.remove(); }, 200);
  toast('Template downloaded', 'success', 1800);
}
