/* =====================================================================
   pages/incoming-rules.js — Admin-scoped Incoming Message Rules UI.

   Distinct from per-mailbox webmail rules at /api/v1/webmail/rules:
   these are tenant-wide rules applied before any per-mailbox
   filter. Built on the shared form builder so the create/edit
   modals match every other admin modal in field grouping,
   inline validation, error banner, submit-state spinner, and
   keyboard submit.
   ===================================================================== */

import { el, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { openFormModal } from '../form.js';

const FIELD_OPTIONS = [
  { value: 'subject',    label: 'Subject' },
  { value: 'from',       label: 'From' },
  { value: 'to',         label: 'To' },
  { value: 'any_header', label: 'Any header' },
  { value: 'size',       label: 'Size' },
  { value: 'spf',        label: 'SPF result' },
  { value: 'dkim',       label: 'DKIM result' },
  { value: 'dmarc',      label: 'DMARC result' },
];
const OP_OPTIONS = [
  { value: 'contains',    label: 'contains' },
  { value: 'equals',      label: 'equals' },
  { value: 'starts_with', label: 'starts with' },
  { value: 'ends_with',   label: 'ends with' },
  { value: 'matches',     label: 'matches (regex)' },
  { value: 'gt',          label: '>' },
  { value: 'lt',          label: '<' },
];
const ACTION_OPTIONS = [
  { value: 'reject',     label: 'reject',     desc: 'Reject during SMTP conversation (5xx)' },
  { value: 'quarantine', label: 'quarantine', desc: 'Hold in admin quarantine, do not deliver' },
  { value: 'tag',        label: 'tag',        desc: 'Tag header / add label; continue delivery' },
];
const APPLY_OPTIONS = [
  { value: 'all',            label: 'all (in + out)', desc: 'Apply regardless of direction' },
  { value: 'incoming_only',  label: 'incoming only',   desc: 'Only when message is inbound' },
  { value: 'outgoing_only',  label: 'outgoing only',   desc: 'Only when message is outbound' },
];

let _rules = [];

export async function renderIncomingRulesPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Incoming message rules' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Tenant-wide filter rules applied before per-mailbox webmail rules. Priority-ordered; first match wins.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-irr-action': 'create', text: 'Create rule' }),
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderIncomingRulesPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head is-heavy' }, [
    el('h3', { text: 'Rules' }),
    el('span', { class: 'subtle', id: 'irr-stamp', text: 'Loading\u2026' }),
  ]));
  const body = el('div', { class: 'panel-body', id: 'irr-table-host' });
  panel.appendChild(body);
  wrap.appendChild(panel);

  await refresh();
  wrap.addEventListener('click', (ev) => {
    const t = ev.target.closest('[data-irr-action]');
    if (!t) return;
    const a = t.getAttribute('data-irr-action');
    if (a === 'create') return openEditor(null);
    const m = a && a.match(/^(edit|delete):(\d+)$/);
    if (m) {
      if (m[1] === 'edit') return openEditor(Number(m[2]));
      if (m[1] === 'delete') return doDelete(Number(m[2]));
    }
  });
  applyAutoDir(wrap);
}

async function refresh() {
  try {
    const r = await apiGet('/api/v1/admin/incoming-msg-rules');
    _rules = (r && r.rules) || [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
    _rules = [];
  }
  paint();
}

function paint() {
  const body = document.getElementById('irr-table-host');
  if (!body) return;
  body.innerHTML = '';
  const stamp = document.getElementById('irr-stamp');
  if (stamp) stamp.textContent = _rules.length + ' rule' + (_rules.length === 1 ? '' : 's') + ' \u2014 ordered by priority';

  if (!_rules.length) {
    body.appendChild(el('div', { class: 'empty-state-strong' }, [
      el('h4', { text: 'No admin-scoped incoming rules defined' }),
      el('p', { text: 'Per-mailbox webmail rules at /api/v1/webmail/rules cover most use cases. Add a tenant-wide rule above for cross-mailbox filtering (e.g. global Spam header tagging, quarantine).' }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'btn primary', 'data-irr-action': 'create', text: 'Create a rule' }),
      ]),
    ]));
    return;
  }

  const tbl = el('table', { class: 'data-table dense-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Prio' }),
    el('th', { text: 'Name' }),
    el('th', { text: 'Condition' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Scope' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: '' }),
  ])));
  const tb = el('tbody');
  const sorted = _rules.slice().sort((a, b) => Number(a.priority || 0) - Number(b.priority || 0));
  sorted.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { class: 'kv-v', text: String(r.priority) }),
      el('td', null, [
        el('strong', { text: r.name || ('#' + r.id) }),
        r.note ? el('div', { class: 'subtle small', style: 'margin-top:2px', text: r.note }) : null,
      ]),
      el('td', { class: 'kv-v mono small', text: (r.field || 'subject') + ' ' + (r.operator || 'contains') + ' "' + (r.value || '') + '"' }),
      el('td', null, actionBadge(r.action, r.action_target, r.stop_processing)),
      el('td', { class: 'kv-v subtle', text: (r.apply_to || 'all') }),
      el('td', { class: 'kv-v', text: r.enabled ? 'yes' : 'no' }),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn xs ghost', 'data-irr-action': 'edit:' + r.id, text: 'Edit' }),
        el('button', { class: 'btn xs ghost danger', 'data-irr-action': 'delete:' + r.id, text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function actionBadge(action, target, stop) {
  const a = String(action || '').toLowerCase();
  let kind = 'neutral';
  let label = a;
  if (a === 'reject') { kind = 'bad'; }
  else if (a === 'quarantine') { kind = 'warn'; }
  else if (a === 'tag') { kind = 'info'; }
  if (target) label += ' \u2192 ' + target;
  if (stop) label += '  [stop]';
  return el('span', { class: 'badge ' + kind, text: label });
}

async function openEditor(id) {
  const r = id != null ? (_rules.find((x) => Number(x.id) === Number(id)) || {}) : {};
  const isEdit = id != null;

  openFormModal({
    title: isEdit ? ('Edit incoming rule #' + id) : 'Create incoming message rule',
    size: 'lg',
    groups: [
      {
        title: 'Identity', subtitle: 'Operator-facing name + priority.', layout: 'cols-2',
        fields: [
          { name: 'name', kind: 'text', label: 'Name', required: true,
            default: r.name, maxLength: 128,
            help: 'Shown in audit log + table.' },
          { name: 'priority', kind: 'number', label: 'Priority (lower first)',
            min: 0, max: 9999, required: true,
            default: r.priority != null ? r.priority : 100,
            help: 'Lower runs first.' },
        ],
      },
      {
        title: 'Condition', subtitle: 'Field / operator / value triple this rule matches.',
        layout: 'cols-2',
        fields: [
          { name: 'field', kind: 'select', label: 'Field',
            options: FIELD_OPTIONS, default: r.field || 'subject', required: true },
          { name: 'operator', kind: 'select', label: 'Operator',
            options: OP_OPTIONS, default: r.operator || 'contains', required: true },
          { name: 'value', kind: 'text', label: 'Value', required: true,
            default: r.value || '',
            placeholder: 'pattern to match',
            help: 'For "matches" use a Go regex; for size use bytes.' },
          { name: 'apply_to', kind: 'select', label: 'Apply to',
            options: APPLY_OPTIONS, default: r.apply_to || 'all', required: true },
        ],
      },
      {
        title: 'Action', subtitle: 'What to do when the condition matches.',
        layout: 'cols-2',
        fields: [
          { name: 'action', kind: 'select', label: 'Action',
            options: ACTION_OPTIONS, default: r.action || 'reject', required: true },
          { name: 'action_target', kind: 'text', label: 'Action value',
            default: r.action_target || '',
            placeholder: 'tag label or quarantine reason (optional)',
            help: 'Required when tagging to specify the header/label; otherwise free-form.' },
          { name: 'stop_processing', kind: 'switch', label: 'Stop further rules on match',
            default: r.stop_processing === true,
            help: 'When on, no lower-priority rule runs after this one fires.' },
          { name: 'enabled', kind: 'switch', label: 'Enabled',
            default: r.enabled !== false },
        ],
      },
      {
        title: 'Documentation',
        fields: [
          { name: 'note', kind: 'textarea', label: 'Note',
            rows: 3, maxLength: 1024, default: r.note || '',
            placeholder: 'Why this rule exists; ticket / contact' },
        ],
      },
    ],
    submitLabel: isEdit ? 'Save changes' : 'Create rule',
    successMessage: isEdit ? 'Incoming message rule updated.' : 'Incoming message rule created.',
    onSubmit: async (values) => {
      const payload = {
        name: String(values.name || '').trim(),
        priority: Number(values.priority || 100),
        field: String(values.field || 'subject'),
        operator: String(values.operator || 'contains'),
        value: String(values.value || ''),
        action: String(values.action || 'reject'),
        action_target: String(values.action_target || ''),
        apply_to: String(values.apply_to || 'all'),
        stop_processing: !!values.stop_processing,
        enabled: !!values.enabled,
        note: String(values.note || ''),
      };
      try {
        if (isEdit) await apiPatch('/api/v1/admin/incoming-msg-rules/' + id, payload);
        else        await apiPost('/api/v1/admin/incoming-msg-rules', payload);
        await refresh();
        return { ok: true };
      } catch (e) {
        return { ok: false, error: (e && (e.detail || e.message)) || 'Save failed.' };
      }
    },
  });
}

async function doDelete(id) {
  const r = _rules.find((x) => Number(x.id) === Number(id));
  const desc = r ? (r.name || ('#' + id)) : ('rule #' + id);
  const ok = await confirmDanger({
    title: 'Delete incoming message rule',
    message: 'Delete rule "' + desc + '"? Future matched messages fall through to the next rule (or default).',
    confirmLabel: 'Delete',
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/incoming-msg-rules/' + id);
    toast('Rule deleted', 'success', 1400);
    await refresh();
  } catch (e) { toast('Delete failed: ' + (e.message || e), 'error'); }
}
