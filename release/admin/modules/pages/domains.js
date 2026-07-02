/* =====================================================================
   pages/domains.js — Domain management UI.

   Wires:
     GET    /api/v1/domains
     GET    /api/v1/domains/:name
     GET    /api/v1/domains/:name/audit
     POST   /api/v1/domains             (CSRF)
     PATCH  /api/v1/domains/:name/status (CSRF)
     DELETE /api/v1/domains/:name        (CSRF)
   ===================================================================== */

import { el, table, badge, fmtShortDate, openModal, openDrawer, confirmDanger, copyToClipboard } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderDomainsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Domains' }),
      el('p', { class: 'page-subtitle subtle', text: 'Provision, suspend, and audit mail domains.' }),
    ]),
  ]));
  wrap.appendChild(wrap_head());

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Domains' })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  await refresh();
  applyAutoDir(wrap);

  async function refresh() {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
    let data;
    try { data = await apiGet('/api/v1/domains'); }
    catch (e) { body.innerHTML = ''; body.appendChild(el('div', { class: 'error', text: e.message || 'load failed' })); return; }
    body.innerHTML = '';
    const list = (data && (data.domains || data)) || [];
    if (!list.length) { body.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
    body.appendChild(table({
      columns: [
        { name: 'name', label: 'Domain', render: (r) => r.name || r.domain || '-' },
        { name: 'st',   label: 'Status', render: (r) => {
          const s = (r.status || 'active').toLowerCase();
          const k = s === 'active' ? 'good' : s === 'suspended' ? 'bad' : 'neutral';
          return badge(s, k);
        } },
        { name: 'pl',   label: 'Plan', render: (r) => r.plan || '-' },
        { name: 'mb',   label: 'Max mailboxes', render: (r) => r.max_mailboxes != null ? String(r.max_mailboxes) : '-' },
        // mailbox_count shows the number of mailboxes already
        // provisioned for this domain. Some backend responses
        // include it directly; we fall back to mailboxes_count,
        // mailbox_count, mailboxCount, or mailboxCount (camel)
        // to be lenient across backend revisions.
        { name: 'mbc',  label: 'Mailboxes', render: (r) => {
          const n = (r && (r.mailbox_count
            || r.mailboxes_count
            || r.mailboxCount
            || r.mailbox_count_total
            || (r.stats && r.stats.mailbox_count))) ;
          return n != null ? String(n) : '-';
        } },
        { name: 'a',    label: '', cellClass: 'actions', render: (r) => {
          const w = el('div', { class: 'row-actions' });
          if (r.name) {
            // dv-action / dm-action: legacy row-action classes
            // asserted by the static-analysis test. We keep them
            // as literal class names so the contracts remain
            // discoverable.
            w.appendChild(el('button', { class: 'btn xs ghost dv-action', type: 'button', text: 'Detail',
              onclick: () => openDomainDetail(r.name) }));
            w.appendChild(el('button', { class: 'btn xs ghost dm-action', type: 'button', text: r.status === 'suspended' ? 'Resume' : 'Suspend',
              onclick: () => toggleStatus(r.name, r.status) }));
            w.appendChild(el('button', { class: 'btn xs danger dm-action', type: 'button', text: 'Delete',
              onclick: () => doDelete(r.name) }));
          }
          return w;
        } },
      ],
      rows: list,
    }));
  }
}

function wrap_head() {
  const actions = el('div', { class: 'form-actions' });
  // add-domain-btn is the class contract asserted by the
  // legacy static-analysis test. Keeping it as the literal
  // class name on this button so downstream test greps still
  // match without rewriting the assertion surface.
  actions.appendChild(el('button', { class: 'btn primary add-domain-btn', type: 'button', text: 'New domain',
    onclick: () => openCreate() }));
  return actions;
}

function openCreate() {
  openModal({
    title: 'Create domain',
    render: (body, foot) => {
      const name = el('input', { type: 'text', placeholder: 'example.com', required: 'required', autocomplete: 'off', spellcheck: 'false' });
      body.appendChild(el('div', { class: 'form-row' }, [el('label', null, 'Domain'), name]));
      foot.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('common.cancel'),
        onclick: () => document.querySelector('.modal-overlay').remove() }));
      foot.appendChild(el('button', { class: 'btn primary', type: 'button', text: 'Create', onclick: async () => {
        if (!name.value) { toast('Domain required', 'error'); return; }
        try {
          await apiPost('/api/v1/domains', { name: name.value });
          toast('Domain created', 'success', 1800);
          location.reload();
        } catch (e) { toast((e && e.message) || 'create failed', 'error'); }
      } }));
    },
  });
}

async function toggleStatus(name, current) {
  const next = current === 'suspended' ? 'active' : 'suspended';
  const ok = await confirmDanger({ title: next === 'suspended' ? 'Suspend domain' : 'Resume domain', message: 'Switch ' + name + ' to ' + next + '?', confirmLabel: next });
  if (!ok) return;
  try {
    await apiPatch('/api/v1/domains/' + encodeURIComponent(name) + '/status', { status: next });
    toast('Status updated', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (e) { toast((e && e.message) || 'update failed', 'error'); }
}

async function doDelete(name) {
  const ok = await confirmDanger({ title: 'Delete domain', message: 'Delete ' + name + '? This is irreversible.', confirmLabel: 'Delete', requireText: name });
  if (!ok) return;
  try {
    await apiDelete('/api/v1/domains/' + encodeURIComponent(name));
    toast('Domain deleted', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (e) { toast((e && e.message) || 'delete failed', 'error'); }
}

async function openDomainDetail(name) {
  openDrawer({
    title: name, eyebrow: 'Domain',
    render: async (body) => {
      body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
      let data, audit;
      try { data = await apiGet('/api/v1/domains/' + encodeURIComponent(name)); }
      catch (e) { body.innerHTML = ''; body.appendChild(el('div', { class: 'error', text: e.message })); return; }
      try { audit = await apiGet('/api/v1/domains/' + encodeURIComponent(name) + '/audit'); }
      catch (_) { audit = null; }
      body.innerHTML = '';
      const dl = el('dl', { class: 'kv' });
      ['name','status','plan','max_mailboxes','max_aliases','max_quota_mb','created_at'].forEach((k) => {
        if (data && data[k] != null) { dl.appendChild(el('dt', { text: k })); dl.appendChild(el('dd', { class: 'kv-v', text: String(data[k]) })); }
      });
      body.appendChild(dl);
      if (audit && (audit.entries || audit.logs)) {
        const list = audit.entries || audit.logs;
        body.appendChild(el('h4', { text: 'Audit' }));
        body.appendChild(table({
          columns: [
            { name: 't',  label: 'Time',     render: (r) => fmtShortDate(r.timestamp || r.created_at || '') },
            { name: 'a',  label: 'Actor',    render: (r) => r.actor || r.user || '-' },
            { name: 'm',  label: 'Message',  render: (r) => r.message || r.event || '-' },
          ],
          rows: list,
        }));
      }
    },
  });
}
