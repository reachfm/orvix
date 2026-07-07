/* =====================================================================
   pages/acl.js — Global Access Control rules UI.

   Built on the shared form builder (modules/form.js) so the
   add/edit modals render with field grouping, inline validation,
   keyboard submit, error banner, and submit-state spinner that
   match every other admin modal.
   ===================================================================== */

import { el, confirmDanger } from '../components.js';
import { apiGet, apiPost, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { openFormModal } from '../form.js';

const PROTOCOL_OPTIONS = [
  { value: 'all',     label: 'all protocols' },
  { value: 'smtp',    label: 'smtp (25 / 587 / 465)' },
  { value: 'imap',    label: 'imap (143 / 993)' },
  { value: 'pop3',    label: 'pop3 (110 / 995)' },
  { value: 'jmap',    label: 'jmap' },
  { value: 'webmail', label: 'webmail (HTTP)' },
];
const ACTION_OPTIONS = [
  { value: 'allow', label: 'allow', desc: 'Permit connections matching the rule' },
  { value: 'deny',  label: 'deny',  desc: 'Reject connections matching the rule' },
];

// Light CIDR validator (IPv4 only — IPv6 allowlists are written
// directly in the firewall module).
function isValidCIDR(s) {
  if (!s) return false;
  if (s === '*' || s === 'any') return true;
  // Single IP
  if (/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.test(s)) {
    const [a, b, c, d] = s.split('.').map(Number);
    if ([a, b, c, d].some((n) => n < 0 || n > 255)) return false;
    return true;
  }
  // CIDR
  const m = s.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/);
  if (!m) return false;
  const [, a, b, c, d, mask] = m;
  const octs = [a, b, c, d].map(Number);
  if (octs.some((n) => n < 0 || n > 255)) return false;
  const m2 = Number(mask);
  return m2 >= 0 && m2 <= 32;
}

let _rules = [];

export async function renderACLPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Global access control' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Allow / deny rules per source CIDR or IP, per protocol. Lower priority runs first; the first match wins.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-acl-action': 'create', text: 'Add rule' }),
      el('button', { class: 'btn ghost', text: 'Refresh',
        onclick: () => renderACLPage(root) }),
    ]),
  ]));
  root.appendChild(wrap);

  const panel = el('section', { class: 'panel' });
  panel.appendChild(el('header', { class: 'panel-head is-heavy' }, [
    el('h3', { text: 'ACL rules (0)' }),
    el('span', { class: 'subtle', text: 'Rules run in ascending priority order. First match wins.' }),
  ]));
  const panelBody = el('div', { class: 'panel-body', id: 'acl-table-host' });
  panel.appendChild(panelBody);
  wrap.appendChild(panel);

  await refresh();
  wrap.addEventListener('click', (ev) => {
    const t = ev.target.closest('[data-acl-action]');
    if (!t) return;
    if (t.getAttribute('data-acl-action') === 'create') openEditor();
    if (t.getAttribute('data-acl-action') === 'delete') doDelete(Number(t.getAttribute('data-id')));
  });
  applyAutoDir(wrap);
}

async function refresh() {
  try {
    const r = await apiGet('/api/v1/admin/acl-rules');
    _rules = (r && r.rules) || [];
  } catch (e) {
    toast('Failed to load ACL: ' + (e.message || e), 'error');
    _rules = [];
  }
  paint();
}

function paint() {
  const body = document.getElementById('acl-table-host');
  if (!body) return;
  body.innerHTML = '';

  if (!_rules || !_rules.length) {
    body.appendChild(el('div', { class: 'empty-state-strong' }, [
      el('h4', { text: 'No ACL rules defined' }),
      el('p', { text: 'Without rules, the system falls back to the network-layer firewall + per-listener config. Add a rule above to override the default for a specific source.' }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'btn primary', 'data-acl-action': 'create', text: 'Add your first rule' }),
      ]),
    ]));
    return;
  }

  const tbl = el('table', { class: 'data-table dense-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: '#' }),
    el('th', { text: 'Action' }),
    el('th', { text: 'Protocol' }),
    el('th', { text: 'Source' }),
    el('th', { text: 'Note' }),
    el('th', { text: '' }),
  ])));
  const tb = el('tbody');
  _rules.forEach((r) => {
    tb.appendChild(el('tr', null, [
      el('td', { class: 'kv-v', text: String(r.priority) }),
      el('td', null, actionBadge(r.action)),
      el('td', { class: 'kv-v', text: r.protocol || 'all' }),
      el('td', { class: 'kv-v', text: r.source || '-' }),
      el('td', { class: 'subtle', text: r.note || '-' }),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn xs ghost danger', 'data-acl-action': 'delete',
          'data-id': String(r.id), text: 'Delete' }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

function actionBadge(action) {
  const a = String(action || '').toLowerCase();
  if (a === 'allow') return el('span', { class: 'badge good', text: 'allow' });
  if (a === 'deny')  return el('span', { class: 'badge bad',  text: 'deny' });
  return el('span', { class: 'badge neutral', text: action || '-' });
}

async function openEditor() {
  openFormModal({
    title: 'Add ACL rule',
    size: 'lg',
    groups: [
      {
        title: 'Match',
        subtitle: 'Defines the source and protocol this rule applies to.',
        layout: 'cols-2',
        fields: [
          { name: 'source', kind: 'text', label: 'Source', required: true,
            placeholder: '203.0.113.0/24  |  198.51.100.42  |  *',
            help: 'CIDR range, single IP, or "*" for any. Currently IPv4 only; IPv6 allowlists belong in the firewall module.',
            validate: (v) => isValidCIDR(String(v)) ? null : { error: 'Use "any" or an IPv4 CIDR (e.g. 203.0.113.0/24) or IP.' } },
          { name: 'protocol', kind: 'select', label: 'Protocol',
            options: PROTOCOL_OPTIONS, default: 'all',
            help: '"all" matches every listener protocol.' },
        ],
      },
      {
        title: 'Action',
        subtitle: 'How connections matching the rule are handled, and where this rule sits in the priority order.',
        layout: 'cols-2',
        fields: [
          { name: 'action', kind: 'select', label: 'Action', required: true,
            options: ACTION_OPTIONS, default: 'allow' },
          { name: 'priority', kind: 'number', label: 'Priority', min: 0, max: 9999,
            default: 100, required: true,
            help: 'Lower runs first. First match wins. Recommended bands: 10=blocklists, 100=greylist, 500=allowlists.' },
        ],
      },
      {
        title: 'Documentation',
        fields: [
          { name: 'note', kind: 'textarea', label: 'Note', rows: 2, maxLength: 256,
            placeholder: 'Why this rule exists; ticket / contact',
            help: 'Surface for the operator in the audit log.' },
        ],
      },
    ],
    submitLabel: 'Add rule',
    successMessage: 'ACL rule added.',
    onSubmit: async (values) => {
      try {
        await apiPost('/api/v1/admin/acl-rules', {
          source: String(values.source || '').trim(),
          protocol: String(values.protocol || 'all'),
          action: String(values.action || 'allow'),
          priority: Number(values.priority || 100),
          note: String(values.note || ''),
        });
        await refresh();
        return { ok: true };
      } catch (e) {
        return { ok: false, error: (e && (e.detail || e.message)) || 'Save failed.' };
      }
    },
  });
}

async function doDelete(id) {
  const rule = _rules.find((r) => Number(r.id) === Number(id));
  const desc = rule ? (rule.action + ' ' + (rule.protocol || 'all') + ' ' + (rule.source || '')) : ('rule #' + id);
  const ok = await confirmDanger({
    title: 'Delete ACL rule',
    message: 'Delete "' + desc.trim() + '"? Future connections matching this pattern will fall through to the next rule.',
    confirmLabel: 'Delete',
    dangerous: true,
  });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/admin/acl-rules/' + id);
    toast('Rule deleted', 'success', 1400);
    await refresh();
  } catch (e) { toast('Delete failed: ' + (e.message || e), 'error'); }
}
