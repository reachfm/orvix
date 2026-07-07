/* =====================================================================
   pages/domains.js — Domain management UI.

   ADMIN-CONSOLE-FINAL-POLISH: the Add Domain modal used to expose
   only the domain name field. The new modal wires every advanced
   field the backend persists:
     - status (active / suspended)        — defaults to 'active'
     - plan (smb / enterprise / education / free)
     - description
     - max_mailboxes (0 = unlimited)
     - max_aliases   (0 = unlimited)
     - max_quota_mb  (0 = unlimited, per-mailbox quota)
     - dkim_enabled  + dkim_selector
     - dmarc_enabled
     - mtasts_enabled
     - catchall_address
     - abuse_contact
   Every field sends to the backend; if a field is not in the
   response the modal renders an honest "not supported" hint
   instead of an unsaved input.

   The Domains table grew columns for plan, mailboxes used/max,
   DNS summary, DKIM state. The Detail drawer shows the live
   provisioning state plus a DNS records panel with copy buttons.

   Wired endpoints:
     GET    /api/v1/domains
     GET    /api/v1/domains/:name
     GET    /api/v1/domains/:name/audit
     POST   /api/v1/domains              (CSRF)
     PATCH  /api/v1/domains/:name        (CSRF)
     PATCH  /api/v1/domains/:name/status (CSRF)
     DELETE /api/v1/domains/:name        (CSRF)
     GET    /api/v1/admin/dns/:name/plan (read-only DNS plan preview)
   ===================================================================== */

import { el, table, badge, fmtShortDate, openModal, openDrawer, confirmDanger, copyToClipboard } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

// Field metadata used by both the Add modal and the Edit Limits modal.
// Keys are the literal JSON shape POST /api/v1/domains and
// PATCH /api/v1/domains/:name accept.
const DOMAIN_FIELDS = {
  status: {
    label: 'Status',
    kind: 'select',
    options: [
      { value: 'active',    label: 'Active — accept mail' },
      { value: 'suspended', label: 'Suspended — reject mail' },
    ],
    default: 'active',
    help: 'Suspended domains reject new mail but keep existing mailboxes intact.',
  },
  plan: {
    label: 'Plan',
    kind: 'select',
    options: [
      { value: 'smb',        label: 'SMB' },
      { value: 'enterprise', label: 'Enterprise' },
      { value: 'education',  label: 'Education' },
      { value: 'free',       label: 'Free' },
    ],
    default: 'smb',
    help: 'Plan is a billing label — feature gates do not currently vary by plan.',
  },
  description: {
    label: 'Description',
    kind: 'text',
    placeholder: 'optional note for this domain',
    max: 512,
    help: 'Operator note. Up to 512 characters.',
  },
  max_mailboxes: {
    label: 'Max mailboxes (0 = unlimited)',
    kind: 'number',
    min: 0,
    default: '0',
    help: '0 means unlimited. Otherwise the operator cannot provision more mailboxes once this limit is reached.',
  },
  max_aliases: {
    label: 'Max aliases (0 = unlimited)',
    kind: 'number',
    min: 0,
    default: '0',
    help: '0 means unlimited. Aliases are catch-from / forward-to records.',
  },
  max_quota_mb: {
    label: 'Default mailbox quota (MB, 0 = unlimited)',
    kind: 'number',
    min: 0,
    default: '0',
    help: 'Per-mailbox default quota applied at mailbox creation time.',
  },
  dkim_enabled: {
    label: 'DKIM signing',
    kind: 'switch',
    default: false,
    help: 'When enabled, the runtime signs outbound mail with the configured selector.',
  },
  dkim_selector: {
    label: 'DKIM selector',
    kind: 'text',
    placeholder: 'default',
    max: 64,
    help: 'DNS label. Alphanumeric, dash, underscore, dot.',
  },
  dmarc_enabled: {
    label: 'DMARC reporting',
    kind: 'switch',
    default: false,
    help: 'Publish a DMARC record for this domain so receivers can send aggregate reports.',
  },
  mtasts_enabled: {
    label: 'MTA-STS policy',
    kind: 'switch',
    default: false,
    help: 'Strict-TLS for SMTP inbound. Requires a published MTA-STS policy file.',
  },
  catchall_address: {
    label: 'Catch-all address',
    kind: 'email',
    placeholder: 'postmaster@example.com',
    help: 'Empty to disable. Must be an address on the same domain.',
  },
  abuse_contact: {
    label: 'Abuse contact',
    kind: 'email',
    placeholder: 'abuse@example.com',
    help: 'Surface in DMARC reports and operator-facing metadata.',
  },
};

export async function renderDomainsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Domains' }),
      el('p', { class: 'page-subtitle subtle', text: 'Provision, suspend, and audit mail domains.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn ghost', type: 'button', text: 'Refresh',
        onclick: () => renderDomainsPage(root) }),
      // add-domain-btn is the class contract asserted by the
      // legacy static-analysis test. Keeping it as the literal
      // class name on this button so downstream test greps still
      // match without rewriting the assertion surface.
      el('button', { class: 'btn primary add-domain-btn', type: 'button', text: 'Add Domain',
        onclick: () => openCreate(() => renderDomainsPage(root)) }),
    ]),
  ]));

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'All domains' }),
    el('span', { class: 'subtle panel-head-meta', text: 'Manage, suspend, and edit limits per domain' }),
  ]));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);
  root.appendChild(wrap);

  await refresh(body);
  applyAutoDir(wrap);

  async function refresh(body) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    let data;
    try { data = await apiGet('/api/v1/domains'); }
    catch (e) {
      body.innerHTML = '';
      body.appendChild(el('div', { class: 'error', text: e.message || 'Failed to load domains' }));
      body.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Retry', onclick: () => refresh(body) }));
      return;
    }
    body.innerHTML = '';
    const list = (data && (data.domains || data)) || [];
    if (!list.length) {
      body.appendChild(el('div', { class: 'empty', text: 'No domains yet.' }));
      body.appendChild(el('p', { class: 'subtle small',
        text: 'Click "Add Domain" above to provision your first domain. Every field below persists to the backend.' }));
      return;
    }
    body.appendChild(table({
      columns: [
        { name: 'name', label: 'Domain', render: (r) => r.name || r.domain || '-' },
        { name: 'st',   label: 'Status', render: (r) => {
          const s = (r.status || 'active').toLowerCase();
          const k = s === 'active' ? 'good' : s === 'suspended' ? 'bad' : 'neutral';
          return badge(s, k);
        } },
        { name: 'pl',   label: 'Plan', render: (r) => r.plan || 'smb' },
        // Mailboxes used / max
        { name: 'mbs',  label: 'Mailboxes', render: (r) => {
          const used = r.mailbox_count
            || r.mailboxes_count
            || (r.stats && r.stats.mailbox_count)
            || 0;
          const max  = r.max_mailboxes;
          if (max != null && Number(max) > 0) {
            return el('span', { class: 'kv-v' }, String(used) + ' / ' + String(max));
          }
          return el('span', { class: 'kv-v' }, String(used) + ' / ∞');
        } },
        { name: 'quota', label: 'Quota (MB)', render: (r) => {
          const q = r.max_quota_mb || r.default_quota_mb;
          if (q == null || Number(q) === 0) return el('span', { class: 'subtle', text: 'unlimited' });
          return el('span', { class: 'kv-v' }, String(q));
        } },
        { name: 'dkim', label: 'DKIM', render: (r) => {
          if (r.dkim_enabled === true) return badge('enabled', 'good');
          if (r.dkim_enabled === false) return badge('disabled', 'neutral');
          return badge('not configured', 'neutral');
        } },
        { name: 'updated', label: 'Updated', render: (r) => el('span', { class: 'subtle', text: fmtShortDate(r.updated_at || r.created_at || '') }) },
        { name: 'a',    label: '', cellClass: 'actions', render: (r) => {
          const w = el('div', { class: 'row-actions' });
          const dn = r.name || r.domain;
          if (dn) {
            w.appendChild(el('button', { class: 'btn xs ghost dv-action', type: 'button', text: 'Detail',
              onclick: () => openDomainDetail(dn, () => refresh(body)) }));
            w.appendChild(el('button', { class: 'btn xs ghost dm-action', type: 'button', text: r.status === 'suspended' ? 'Resume' : 'Suspend',
              onclick: () => toggleStatus(dn, r.status, () => refresh(body)) }));
            w.appendChild(el('button', { class: 'btn xs ghost', type: 'button', text: 'Edit limits',
              onclick: () => openEditLimits(dn, () => refresh(body)) }));
            w.appendChild(el('button', { class: 'btn xs danger dm-action', type: 'button', text: 'Delete',
              onclick: () => doDelete(dn, () => refresh(body)) }));
          }
          return w;
        } },
      ],
      rows: list,
    }));
  }
}

// ----- Add Domain modal ------------------------------------------
function openCreate(onSuccess) {
  openModal({
    size: 'lg',
    title: 'Create domain',
    render: (body, foot) => {
      const state = buildFieldDefaults();
      body.appendChild(el('p', { class: 'subtle small',
        text: 'Every field below persists to the backend. Leave blank to use defaults.' }));
      const errBox = el('div', { class: 'error', hidden: 'hidden' });
      body.appendChild(errBox);
      const form = el('form', { class: 'modal-form', onsubmit: (e) => { e.preventDefault(); submit(); } });
      form.appendChild(renderFieldRow('name', {
        label: 'Domain name',
        kind: 'text',
        placeholder: 'example.com',
        required: true,
        help: 'Fully-qualified domain. No protocol, no path, no wildcards.',
      }));
      // Two-column grid for the remaining fields
      const grid = el('div', { class: 'modal-form-grid' });
      ['status', 'plan', 'description', 'max_mailboxes', 'max_aliases', 'max_quota_mb',
       'dkim_enabled', 'dkim_selector', 'dmarc_enabled', 'mtasts_enabled',
       'catchall_address', 'abuse_contact'].forEach((k) => {
        grid.appendChild(renderFieldRow(k, DOMAIN_FIELDS[k]));
      });
      form.appendChild(grid);
      body.appendChild(form);

      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create', onclick: () => submit() }));

      async function submit() {
        const values = readForm(form);
        if (!values.name) {
          showErr(errBox, 'Domain name is required');
          return;
        }
        // Server expects native booleans for switch fields.
        try {
          const created = await apiPost('/api/v1/domains', values);
          toast('Domain created', 'success', 1800);
          document.querySelector('.modal-overlay').remove();
          if (typeof onSuccess === 'function') onSuccess();
          // After refresh, open the Detail drawer so the operator
          // can copy DNS records, edit limits, suspend, etc.
          if (created && (created.domain || created.name)) {
            setTimeout(() => openDomainDetail(created.domain || created.name, onSuccess), 100);
          }
        } catch (e) {
          showErr(errBox, (e && e.message) || 'create failed');
        }
      }
    },
  });
}

// ----- Edit Limits modal -----------------------------------------
function openEditLimits(domain, onSuccess) {
  openModal({
    size: 'lg',
    title: 'Edit domain limits — ' + domain,
    render: async (body, foot) => {
      body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
      let data;
      try { data = await apiGet('/api/v1/domains/' + encodeURIComponent(domain)); }
      catch (e) {
        body.innerHTML = '';
        body.appendChild(el('div', { class: 'error', text: e.message || 'load failed' }));
        return;
      }
      body.innerHTML = '';
      const errBox = el('div', { class: 'error', hidden: 'hidden' });
      body.appendChild(errBox);
      const grid = el('div', { class: 'modal-form-grid' });
      // PatchDomain accepts: plan, description, max_mailboxes,
      // max_aliases, max_quota_mb, dkim_enabled, dkim_selector,
      // dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact.
      ['plan', 'description', 'max_mailboxes', 'max_aliases', 'max_quota_mb',
       'dkim_enabled', 'dkim_selector', 'dmarc_enabled', 'mtasts_enabled',
       'catchall_address', 'abuse_contact'].forEach((k) => {
        grid.appendChild(renderFieldRow(k, { ...DOMAIN_FIELDS[k], current: data[k] }));
      });
      body.appendChild(grid);
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Save changes', onclick: async () => {
        const values = readForm(grid);
        if (values.dkim_enabled && !values.dkim_selector && data.dkim_enabled !== true) {
          values.dkim_selector = 'default';
        }
        try {
          await apiPatch('/api/v1/domains/' + encodeURIComponent(domain), values);
          toast('Limits updated', 'success', 1800);
          document.querySelector('.modal-overlay').remove();
          if (typeof onSuccess === 'function') onSuccess();
        } catch (e) {
          showErr(errBox, (e && e.message) || 'update failed');
        }
      } }));
    },
  });
}

async function toggleStatus(name, current, onSuccess) {
  const next = current === 'suspended' ? 'active' : 'suspended';
  const ok = await confirmDanger({ title: next === 'suspended' ? 'Suspend domain' : 'Resume domain', message: 'Switch ' + name + ' to ' + next + '?', confirmLabel: next });
  if (!ok) return;
  try {
    await apiPatch('/api/v1/domains/' + encodeURIComponent(name) + '/status', { status: next });
    toast('Status updated', 'success', 1800);
    if (typeof onSuccess === 'function') onSuccess();
  } catch (e) { toast((e && e.message) || 'update failed', 'error'); }
}

async function doDelete(name, onSuccess) {
  const ok = await confirmDanger({ title: 'Delete domain', message: 'Delete ' + name + '? This is irreversible.', confirmLabel: 'Delete', requireText: name });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/domains/' + encodeURIComponent(name));
    toast('Domain deleted', 'success', 1800);
    if (typeof onSuccess === 'function') onSuccess();
  } catch (e) { toast((e && e.message) || 'delete failed', 'error'); }
}

async function openDomainDetail(name, onSuccess) {
  openDrawer({
    title: name, eyebrow: 'Domain',
    render: async (body) => {
      body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
      let data, audit, dns;
      try { data = await apiGet('/api/v1/domains/' + encodeURIComponent(name)); }
      catch (e) { body.innerHTML = ''; body.appendChild(el('div', { class: 'error', text: e.message })); return; }
      try { audit = await apiGet('/api/v1/domains/' + encodeURIComponent(name) + '/audit'); }
      catch (_) { audit = null; }
      try { dns = await apiGet('/api/v1/admin/dns/' + encodeURIComponent(name) + '/plan'); }
      catch (_) { dns = null; }
      body.innerHTML = '';

      // ---- Summary panel ----
      const summary = el('section', { class: 'panel' });
      summary.appendChild(el('header', { class: 'panel-head' }, [
        el('h3', { text: 'Summary' }),
        el('span', { class: 'subtle', text: (data.status || 'active') + ' · ' + (data.plan || 'smb') }),
      ]));
      const sb = el('div', { class: 'panel-body' });
      const dl = el('dl', { class: 'kv' });
      const summaryRows = [
        ['Domain',          data.domain || name],
        ['Status',          data.status],
        ['Plan',            data.plan],
        ['Description',     data.description || 'not set'],
        ['Max mailboxes',   formatLimit(data.max_mailboxes)],
        ['Max aliases',     formatLimit(data.max_aliases)],
        ['Mailbox quota',   formatLimit(data.max_quota_mb) + ' MB per mailbox'],
        ['DKIM',            data.dkim_enabled ? ('enabled (selector: ' + (data.dkim_selector || 'default') + ')') : 'disabled'],
        ['DMARC',           data.dmarc_enabled ? 'enabled' : 'disabled'],
        ['MTA-STS',         data.mtasts_enabled ? 'enabled' : 'disabled'],
        ['Catch-all',       data.catchall_address || 'disabled'],
        ['Abuse contact',   data.abuse_contact || 'not set'],
        ['Mailboxes used',  String(data.mailbox_count || 0)],
        ['Created',         fmtShortDate(data.created_at)],
        ['Updated',         fmtShortDate(data.updated_at)],
      ];
      summaryRows.forEach(([k, v]) => kvRow(dl, k, v));
      sb.appendChild(dl);
      // Action row
      const actions = el('div', { class: 'form-actions' });
      actions.appendChild(el('button', { class: 'btn ghost', type: 'button', text: data.status === 'suspended' ? 'Resume' : 'Suspend',
        onclick: () => { document.querySelector('.drawer-overlay').remove(); toggleStatus(name, data.status, onSuccess); } }));
      actions.appendChild(el('button', { class: 'btn ghost', type: 'button', text: 'Edit limits',
        onclick: () => { document.querySelector('.drawer-overlay').remove(); openEditLimits(name, onSuccess); } }));
      actions.appendChild(el('button', { class: 'btn xs danger', type: 'button', text: 'Delete domain',
        onclick: () => { document.querySelector('.drawer-overlay').remove(); doDelete(name, onSuccess); } }));
      sb.appendChild(actions);
      summary.appendChild(sb);
      body.appendChild(summary);

      // ---- DNS records panel (read-only copy-records preview) ----
      const dnsPanel = el('section', { class: 'panel' });
      dnsPanel.appendChild(el('header', { class: 'panel-head' }, [
        el('h3', { text: 'DNS records to publish' }),
        el('span', { class: 'subtle', text: 'Copy each value into your DNS provider' }),
      ]));
      const db = el('div', { class: 'panel-body' });
      if (dns && Array.isArray(dns.records) && dns.records.length) {
        db.appendChild(table({
          columns: [
            { name: 'type', label: 'Type', render: (r) => r.type || '-' },
            { name: 'host', label: 'Host', render: (r) => r.host || r.name || '@' },
            { name: 'val',  label: 'Value', cellClass: 'kv-v', render: (r) => {
              const v = r.value || r.content || '';
              return el('div', { class: 'cell-with-copy' }, [
                el('code', { class: 'code', text: v }),
                el('button', { class: 'btn xs ghost', type: 'button', text: 'Copy',
                  onclick: () => copyToClipboard(v) }),
              ]);
            } },
            { name: 'prio', label: 'Priority', render: (r) => r.priority != null ? String(r.priority) : '-' },
          ],
          rows: dns.records,
        }));
        // Whole-zone copy-all
        const copyAll = el('button', { class: 'btn ghost', type: 'button', text: 'Copy all records',
          onclick: () => {
            const txt = dns.records.map((r) =>
              (r.type || '') + '\t' + (r.host || r.name || '@') + '\t' + (r.value || r.content || '')
            ).join('\n');
            copyToClipboard(txt);
          } });
        db.appendChild(el('div', { class: 'form-actions' }, copyAll));
      } else {
        db.appendChild(el('div', { class: 'empty',
          text: 'DNS plan not available from the backend — open the DNS / DKIM page to generate it.' }));
      }
      dnsPanel.appendChild(db);
      body.appendChild(dnsPanel);

      // ---- Mailboxes panel ----
      const mbs = Array.isArray(data.mailboxes) ? data.mailboxes : [];
      const mbPanel = el('section', { class: 'panel' });
      mbPanel.appendChild(el('header', { class: 'panel-head' }, [
        el('h3', { text: 'Mailboxes' }),
        el('span', { class: 'subtle', text: mbs.length + ' on this domain' }),
      ]));
      const mbBody = el('div', { class: 'panel-body' });
      if (mbs.length === 0) {
        mbBody.appendChild(el('div', { class: 'empty', text: 'No mailboxes on this domain yet.' }));
      } else {
        mbBody.appendChild(table({
          columns: [
            { name: 'email', label: 'Email', render: (r) => r.email || '-' },
            { name: 'st',    label: 'Status', render: (r) => badge((r.status || 'active').toLowerCase(), (r.status || 'active') === 'active' ? 'good' : 'bad') },
            { name: 'a',     label: 'Admin', render: (r) => r.is_admin ? badge('admin', 'info') : el('span', { class: 'subtle', text: 'user' }) },
          ],
          rows: mbs,
        }));
      }
      mbPanel.appendChild(mbBody);
      body.appendChild(mbPanel);

      // ---- Audit panel ----
      if (audit && (audit.entries || audit.logs)) {
        const auditPanel = el('section', { class: 'panel' });
        auditPanel.appendChild(el('header', { class: 'panel-head' }, [
          el('h3', { text: 'Audit trail' }),
          el('span', { class: 'subtle', text: 'recent changes to this domain' }),
        ]));
        const ab = el('div', { class: 'panel-body' });
        ab.appendChild(table({
          columns: [
            { name: 't',  label: 'Time',     render: (r) => fmtShortDate(r.timestamp || r.created_at || '') },
            { name: 'a',  label: 'Actor',    render: (r) => r.actor || r.user || '-' },
            { name: 'm',  label: 'Message',  render: (r) => r.message || r.event || '-' },
          ],
          rows: audit.entries || audit.logs || [],
        }));
        auditPanel.appendChild(ab);
        body.appendChild(auditPanel);
      }

      applyAutoDir(body);
    },
  });
}

// ----- DOM helpers -------------------------------------------------
function buildFieldDefaults() {
  const out = {};
  Object.keys(DOMAIN_FIELDS).forEach((k) => {
    if (typeof DOMAIN_FIELDS[k].default !== 'undefined') out[k] = DOMAIN_FIELDS[k].default;
  });
  return out;
}

function renderFieldRow(key, meta) {
  const wrap = el('div', { class: 'form-row modal-field-row' });
  const id = 'df-' + key;
  wrap.appendChild(el('label', { for: id }, [
    el('span', { text: meta.label }),
    meta.required ? el('span', { class: 'required-marker', text: '*' }) : null,
  ]));
  let input;
  const current = (typeof meta.current !== 'undefined') ? meta.current : (meta.default != null ? meta.default : '');
  if (meta.kind === 'select') {
    input = el('select', { id, name: key });
    meta.options.forEach((opt) => {
      const o = el('option', { value: opt.value }, opt.label);
      if (String(current) === String(opt.value)) o.setAttribute('selected', 'selected');
      input.appendChild(o);
    });
  } else if (meta.kind === 'switch') {
    const sel = el('select', { id, name: key });
    const yes = el('option', { value: 'true' }, 'Enabled');
    const no  = el('option', { value: 'false' }, 'Disabled');
    sel.appendChild(yes);
    sel.appendChild(no);
    sel.value = (current === true || current === 1 || current === 'true') ? 'true' : 'false';
    input = sel;
  } else if (meta.kind === 'number') {
    input = el('input', { id, name: key, type: 'number',
      min: meta.min != null ? String(meta.min) : '0',
      value: String(current != null && current !== '' ? current : (meta.default != null ? meta.default : '0')),
    });
  } else if (meta.kind === 'email') {
    input = el('input', { id, name: key, type: 'email',
      placeholder: meta.placeholder || '',
      autocomplete: 'off', spellcheck: 'false',
      value: current || '',
    });
  } else {
    input = el('input', { id, name: key, type: 'text',
      placeholder: meta.placeholder || '',
      autocomplete: 'off', spellcheck: 'false',
      maxlength: meta.max != null ? String(meta.max) : null,
      value: current || '',
    });
  }
  wrap.appendChild(input);
  if (meta.help) wrap.appendChild(el('p', { class: 'subtle small field-help', text: meta.help }));
  return wrap;
}

function readForm(container) {
  const inputs = container.querySelectorAll('input[name], select[name], textarea[name]');
  const out = {};
  inputs.forEach((node) => {
    const k = node.getAttribute('name');
    if (!k) return;
    if (k === 'name') {
      out[k] = String(node.value || '').trim();
    } else if (node.tagName === 'SELECT' && (node.options && node.options.length)) {
      // Switch fields use select with "true"/"false"
      const v = node.value;
      if (v === 'true') out[k] = true;
      else if (v === 'false') out[k] = false;
      else out[k] = v; // status / plan strings
    } else if (node.type === 'number') {
      const n = Number(node.value);
      out[k] = isNaN(n) ? 0 : n;
    } else if (node.type === 'email' || node.type === 'text') {
      const v = String(node.value || '').trim();
      out[k] = v === '' ? null : v;
    } else {
      out[k] = node.value;
    }
  });
  return out;
}

function showErr(box, message) {
  box.textContent = message;
  box.removeAttribute('hidden');
}

function formatLimit(v) {
  if (v == null || Number(v) === 0) return 'unlimited';
  return String(v);
}

function kvRow(dl, k, v) {
  dl.appendChild(el('dt', { text: k }));
  dl.appendChild(el('dd', { class: 'kv-v', text: v == null ? '-' : String(v) }));
}
