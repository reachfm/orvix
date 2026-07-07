/* =====================================================================
   pages/acceptance.js — Acceptance & Routing rules admin UI.

   Built on the shared form builder (modules/form.js) so the
   create/edit modal renders with field grouping, inline validation,
   keyboard submit, error banner, and submit-state spinner that
   match every other admin modal. The page also exposes a
   "Dry-run match" affordance that walks the live rule set
   without mutating state.
   ===================================================================== */

import { el, modal, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { openFormModal, openFormDrawer } from '../form.js';

const SCOPE_OPTIONS = [
  { value: 'global',  label: 'global (all domains)', desc: 'Apply across every tenant domain' },
  { value: 'domain',  label: 'domain (single)',      desc: 'Restrict to the configured scope_target domain' },
  { value: 'mailbox', label: 'mailbox (single)',     desc: 'Restrict to the configured scope_target mailbox' },
];
const ACTION_OPTIONS = [
  { value: 'accept',     label: 'accept',    desc: 'Accept normally into the recipient mailbox' },
  { value: 'reject',     label: 'reject',    desc: 'Reject during SMTP conversation (5xx)' },
  { value: 'quarantine', label: 'quarantine',desc: 'Hold in admin quarantine, do not deliver' },
];

let _rules = [];

export async function renderAcceptancePage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Acceptance & routing rules' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Priority-ordered admin rules. Lower priority is applied first; the first match wins. Use the dry-run to verify match logic without mutating state.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-acc-action': 'create', text: 'Create rule' }),
      el('button', { class: 'btn ghost', 'data-acc-action': 'test', text: 'Dry-run match' }),
    ]),
  ]));
  root.appendChild(wrap);

  const tablePanel = el('section', { class: 'panel' });
  tablePanel.appendChild(el('header', { class: 'panel-head is-heavy' }, [
    el('h3', { text: 'Rules' }),
    el('span', { class: 'subtle', id: 'acc-stamp', text: 'Loading\u2026' }),
  ]));
  const tableBody = el('div', { class: 'panel-body', id: 'acc-table-host' });
  tablePanel.appendChild(tableBody);
  wrap.appendChild(tablePanel);

  await refresh();

  wrap.addEventListener('click', (ev) => {
    const t = ev.target.closest('[data-acc-action]');
    if (!t) return;
    const a = t.getAttribute('data-acc-action');
    if (a === 'create') return openEditor(null);
    if (a === 'test') return openDryRun();
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
    const r = await apiGet('/api/v1/admin/acceptance-rules');
    _rules = (r && r.rules) || [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
    _rules = [];
  }
  paint();
}

function paint() {
  const body = document.getElementById('acc-table-host');
  if (!body) return;
  body.innerHTML = '';
  const stamp = document.getElementById('acc-stamp');
  if (stamp) stamp.textContent = 'priority-ordered; ' + _rules.length + ' rule' + (_rules.length === 1 ? '' : 's');

  if (!_rules.length) {
    body.appendChild(el('div', { class: 'empty-state-strong' }, [
      el('h4', { text: 'No acceptance / routing rules defined' }),
      el('p', { text: 'Default acceptance + the per-mailbox filter pipeline handle inbound mail. Add a rule above to override for specific senders, recipients, or source IPs.' }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'btn primary', 'data-acc-action': 'create', text: 'Create a rule' }),
      ]),
    ]));
    return;
  }

  const tbl = el('table', { class: 'data-table dense-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Prio' }),
    el('th', { text: 'Name' }),
    el('th', { text: 'Scope' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Patterns' }),
    el('th', { text: 'Enabled' }),
    el('th', { text: '' }),
  ])));
  const tb = el('tbody');
  const sorted = _rules.slice().sort((a, b) => Number(a.priority || 0) - Number(b.priority || 0));
  sorted.forEach((r) => {
    const patterns = [];
    if (r.sender_pattern)    patterns.push('from: ' + r.sender_pattern);
    if (r.recipient_pattern) patterns.push('to: ' + r.recipient_pattern);
    if (r.source_ip_cidr)    patterns.push('ip: ' + r.source_ip_cidr);
    tb.appendChild(el('tr', null, [
      el('td', { class: 'kv-v', text: String(r.priority) }),
      el('td', null, [
        el('strong', { text: r.name || ('#' + r.id) }),
        r.note ? el('div', { class: 'subtle small', style: 'margin-top:2px', text: r.note }) : null,
      ]),
      el('td', { class: 'kv-v', text: (r.scope || 'global') + (r.scope_target ? ' (' + r.scope_target + ')' : '') }),
      el('td', null, actionBadge(r.action)),
      el('td', { class: 'kv-v subtle', text: patterns.join('; ') || '\u2014' }),
      el('td', { class: 'kv-v', text: r.enabled ? 'yes' : 'no' }),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn xs ghost', 'data-acc-action': 'edit:' + r.id, text: 'Edit' }),
        el('button', { class: 'btn xs ghost danger', 'data-acc-action': 'delete:' + r.id, text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function actionBadge(action) {
  const a = String(action || '').toLowerCase();
  if (a === 'accept')     return el('span', { class: 'badge good', text: 'accept' });
  if (a === 'reject')     return el('span', { class: 'badge bad',  text: 'reject' });
  if (a === 'quarantine') return el('span', { class: 'badge warn', text: 'quarantine' });
  return el('span', { class: 'badge neutral', text: action || '-' });
}

async function openEditor(id) {
  const r = id != null ? (_rules.find((x) => Number(x.id) === Number(id)) || {}) : {};
  const isEdit = id != null;

  openFormModal({
    title: isEdit ? ('Edit acceptance rule #' + id) : 'Create acceptance rule',
    size: 'lg',
    groups: [
      {
        title: 'Identity',
        subtitle: 'Operator-facing name and priority position.',
        layout: 'cols-2',
        fields: [
          { name: 'name', kind: 'text', label: 'Name', required: true,
            default: r.name, maxLength: 128,
            help: 'Unique identifier shown in the table and audit log.' },
          { name: 'priority', kind: 'number', label: 'Priority (lower first)',
            min: 0, max: 9999, required: true,
            default: r.priority != null ? r.priority : 100,
            help: 'Lower numbers run first. Recommended bands: 10=block, 100=reroute, 500=allow.' },
        ],
      },
      {
        title: 'Scope',
        subtitle: 'Where this rule applies. "global" matches every domain unless restricted.',
        layout: 'cols-2',
        fields: [
          { name: 'scope', kind: 'select', label: 'Scope',
            options: SCOPE_OPTIONS, default: r.scope || 'global', required: true },
          { name: 'scope_target', kind: 'text', label: 'Scope target',
            default: r.scope_target || '',
            placeholder: 'example.com or postmaster@example.com',
            help: 'Required when scope is "domain" or "mailbox". Leave empty for global.' },
        ],
      },
      {
        title: 'Match conditions',
        subtitle: 'Patterns the rule applies to. Empty fields match anything.',
        layout: 'cols-2',
        fields: [
          { name: 'sender_pattern', kind: 'text', label: 'Sender pattern',
            default: r.sender_pattern || '',
            placeholder: '*@vendor.example  |  abuse@*',
            help: 'Substring or glob (use * as wildcard).' },
          { name: 'recipient_pattern', kind: 'text', label: 'Recipient pattern',
            default: r.recipient_pattern || '',
            placeholder: 'postmaster@*  |  *@sensitive.example' },
          { name: 'source_ip_cidr', kind: 'text', label: 'Source IP CIDR',
            default: r.source_ip_cidr || '',
            placeholder: '203.0.113.0/24  |  *' },
          { name: 'action', kind: 'select', label: 'Action',
            options: ACTION_OPTIONS, required: true,
            default: r.action || 'accept' },
        ],
      },
      {
        title: 'Documentation',
        fields: [
          { name: 'note', kind: 'textarea', label: 'Note',
            rows: 3, maxLength: 1024, default: r.note || '',
            placeholder: 'Ticket / contact / rationale',
            help: 'Free-form; visible in the table and audit log.' },
          { name: 'enabled', kind: 'switch', label: 'Enabled',
            default: r.enabled !== false,
            help: 'Disabling keeps the rule around but stops it from matching.' },
        ],
      },
    ],
    submitLabel: isEdit ? 'Save changes' : 'Create rule',
    successMessage: isEdit ? 'Acceptance rule updated.' : 'Acceptance rule created.',
    onSubmit: async (values) => {
      const payload = {
        name: String(values.name || '').trim(),
        priority: Number(values.priority || 100),
        scope: String(values.scope || 'global'),
        scope_target: String(values.scope_target || ''),
        sender_pattern: String(values.sender_pattern || ''),
        recipient_pattern: String(values.recipient_pattern || ''),
        source_ip_cidr: String(values.source_ip_cidr || ''),
        action: String(values.action || 'accept'),
        note: String(values.note || ''),
        enabled: !!values.enabled,
      };
      try {
        if (isEdit) await apiPatch('/api/v1/admin/acceptance-rules/' + id, payload);
        else        await apiPost('/api/v1/admin/acceptance-rules', payload);
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
    title: 'Delete acceptance rule',
    message: 'Delete rule "' + desc + '"? Future matched messages fall through to the next rule.',
    confirmLabel: 'Delete',
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/acceptance-rules/' + id);
    toast('Rule deleted', 'success', 1400);
    await refresh();
  } catch (e) { toast('Delete failed: ' + (e.message || e), 'error'); }
}

async function openDryRun() {
  // The dry-run widget is a 3-field modal that walks the live
  // rule set. We use the legacy modal so the dry-run can re-run
  // without closing / reopening.
  const root = el('div', { class: 'form-stack' });
  root.appendChild(field('Sender', 'sender', 'user@example.com'));
  root.appendChild(field('Recipient', 'recipient', 'inbox@mydomain.com'));
  root.appendChild(field('Source IP', 'source_ip', '203.0.113.5'));
  const out = el('div', { class: 'panel-body', style: 'background: rgba(0,0,0,0.18); border-radius: var(--r-md); min-height: 96px; margin-top: 10px;' });
  root.appendChild(out);

  modal({
    title: 'Dry-run acceptance / routing match',
    body: root,
    actions: [
      { label: 'Close', kind: 'ghost', value: 'cancel' },
      { label: 'Walk rules', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return true;
      out.innerHTML = '';
      out.appendChild(el('p', { class: 'subtle small', text: 'Walking rules\u2026' }));
      try {
        const resp = await apiPost('/api/v1/admin/acceptance-rules/test', {
          sender: valOf(root, 'sender'),
          recipient: valOf(root, 'recipient'),
          source_ip: valOf(root, 'source_ip'),
        });
        out.innerHTML = '';
        out.appendChild(resultRow('Result', String((resp && resp.action_label) || '-')));
        if (resp && resp.matched_rule_id != null) {
          out.appendChild(resultRow('Matched rule', '#' + resp.matched_rule_id + (resp.matched_rule_name ? ' (' + resp.matched_rule_name + ')' : '')));
        } else {
          out.appendChild(resultRow('Matched rule', 'no explicit match \u2014 default applies'));
        }
        out.appendChild(resultRow('Rules walked', String((resp && resp.rules_walked) || 0)));
        return false;
      } catch (e) {
        out.innerHTML = '';
        out.appendChild(el('div', { class: 'error', text: 'Dry-run failed: ' + (e.message || e) }));
        return false;
      }
    },
  });

  function field(label, name, placeholder) {
    return el('label', { class: 'ff-row', style: 'gap: 6px;' }, [
      el('span', { class: 'ff-label', text: label }),
      el('input', { class: 'ff-input', name, type: 'text', placeholder, autocomplete: 'off', spellcheck: 'false' }),
    ]);
  }
  function valOf(scope, name) { return (scope.querySelector('[name="' + name + '"]') || {}).value || ''; }
  function resultRow(k, v) {
    const dl = el('dl', { class: 'kv', style: 'grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 0;' });
    dl.appendChild(el('dt', { class: 'subtle', style: 'font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em;', text: k }));
    dl.appendChild(el('dd', { class: 'kv-v', text: v }));
    return dl;
  }
}
