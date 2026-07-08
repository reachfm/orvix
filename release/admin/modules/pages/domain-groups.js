/* =====================================================================
   pages/domain-groups.js — Domain groups for batch operations.

   CRUD against /api/v1/admin/domain-groups and
   /api/v1/admin/domain-groups/:id/members. Used to group domains
   for bulk mailbox / DNS / backup operations.
   ===================================================================== */

import { el, modal } from '../components.js';
import { apiGet, apiPost, apiPut, apiDelete } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';

export async function renderDomainGroupsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Domain groups' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Group domains together for batch operations.' }),
    ]),
    el('div', { class: 'page-actions' }, [
      el('button', { class: 'btn primary', 'data-dg-action': 'create',
        text: 'Create group' }),
    ]),
  ]));
  const grid = el('div', { class: 'card-grid two' });
  wrap.appendChild(grid);
  root.appendChild(wrap);

  let list = [];
  let domains = [];
  try {
    const [gResp, dResp] = await Promise.all([
      apiGet('/api/v1/admin/domain-groups'),
      apiGet('/api/v1/domains'),
    ]);
    list = (gResp && gResp.groups) || [];
    domains = (dResp && Array.isArray(dResp) ? dResp : []) || [];
  } catch (e) {
    toast('Failed to load: ' + (e.message || e), 'error');
  }
  paint(grid, list, domains);

  wrap.addEventListener('click', async (ev) => {
    const tEl = ev.target.closest('[data-dg-action]');
    if (!tEl) return;
    const action = tEl.getAttribute('data-dg-action');
    if (action === 'create') return openCreate(grid, list, domains);
    if (action === 'edit-members') return openEditMembers(grid, list, domains, Number(tEl.getAttribute('data-id')));
    if (action === 'delete') return doDelete(grid, list, Number(tEl.getAttribute('data-id')));
  });

  applyAutoDir(wrap);
}

function paint(grid, list, domains) {
  grid.innerHTML = '';
  if (!list.length) {
    grid.appendChild(el('div', { class: 'empty panel',
      text: 'No domain groups defined. Create one to start grouping domains.' }));
    return;
  }
  list.forEach((g) => {
    const card = el('section', { class: 'panel' });
    card.appendChild(el('header', { class: 'panel-head' }, [
      el('span', { class: 'dot', style: 'background:' + (g.color || '#1f6feb') }),
      el('h3', { text: g.name }),
    ]));
    if (g.description) {
      card.appendChild(el('div', { class: 'panel-body subtle',
        text: g.description }));
    }
    const ul = el('ul', { class: 'kv-list' });
    if (g.members && g.members.length) {
      g.members.forEach((m) => {
        ul.appendChild(el('li', { class: 'kv-item', text: m.name }));
      });
    } else {
      ul.appendChild(el('li', { class: 'kv-item subtle', text: 'No members yet.' }));
    }
    card.appendChild(el('div', { class: 'panel-body' }, ul));
    card.appendChild(el('div', { class: 'panel-foot' }, [
      el('button', { class: 'btn ghost', 'data-dg-action': 'edit-members',
        'data-id': String(g.id), text: 'Edit members' }),
      el('button', { class: 'btn ghost danger', 'data-dg-action': 'delete',
        'data-id': String(g.id), text: 'Delete' }),
    ]));
    grid.appendChild(card);
  });
}

function buildGroupForm(g, domains) {
  const root = el('div', { class: 'form-stack' });
  root.appendChild(inputField('Name', 'name', g.name || ''));
  root.appendChild(inputField('Description', 'description', g.description || ''));
  root.appendChild(inputField('Color', 'color', g.color || '#1f6feb'));
  // domain picker
  const ids = (g.members || []).map((m) => m.domain_id);
  const picker = el('select', { class: 'input', name: 'domain_ids', multiple: 'multiple', size: '8' });
  domains.forEach((d) => {
    const opt = el('option', { value: String(d.id || d.domain_id || 0), text: d.domain || d.name || '' });
    if (ids.indexOf(Number(opt.value)) >= 0) opt.selected = true;
    picker.appendChild(opt);
  });
  root.appendChild(el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: 'Domains (ctrl/cmd-click to multi-select)' }),
    picker,
  ]));
  return root;
}

function inputField(label, name, val) {
  return el('label', { class: 'field' }, [
    el('span', { class: 'field-label', text: label }),
    el('input', { class: 'input', name, type: 'text', value: val }),
  ]);
}

function readForm(root) {
  const out = {};
  root.querySelectorAll('input').forEach((inp) => {
    out[inp.name] = inp.value;
  });
  const sel = root.querySelector('select[name="domain_ids"]');
  if (sel) {
    out.domain_ids = Array.from(sel.selectedOptions).map((o) => Number(o.value));
  }
  return out;
}

async function openCreate(grid, list, domains) {
  const form = buildGroupForm({}, domains);
  modal({
    title: 'Create domain group',
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Create', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      if (!payload.name) { toast('Name is required', 'warn'); return false; }
      try {
        await apiPost('/api/v1/admin/domain-groups', payload);
        toast('Domain group created', 'success');
        const resp = await apiGet('/api/v1/admin/domain-groups');
        list = (resp && resp.groups) || [];
        paint(grid, list, domains);
        return true;
      } catch (e) {
        toast('Create failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function openEditMembers(grid, list, domains, id) {
  const g = list.find((x) => x.id === id);
  if (!g) return;
  const form = buildGroupForm(g, domains);
  modal({
    title: 'Edit members: ' + g.name,
    body: form,
    actions: [
      { label: 'Cancel', kind: 'ghost', value: 'cancel' },
      { label: 'Save', kind: 'primary', value: 'ok' },
    ],
    onAction: async (val) => {
      if (val !== 'ok') return;
      const payload = readForm(form);
      try {
        await apiPut('/api/v1/admin/domain-groups/' + id + '/members',
          { domain_ids: payload.domain_ids || [] });
        toast('Members updated', 'success');
        const resp = await apiGet('/api/v1/admin/domain-groups');
        list = (resp && resp.groups) || [];
        paint(grid, list, domains);
        return true;
      } catch (e) {
        toast('Update failed: ' + (e.message || e), 'error');
        return false;
      }
    },
  });
}

async function doDelete(grid, list, id) {
  const g = list.find((x) => x.id === id);
  if (!g) return;
  if (!confirm('Delete domain group "' + g.name + '"?')) return;
  try {
    await apiDelete('/api/v1/admin/domain-groups/' + id);
    toast('Domain group deleted', 'success');
    const resp = await apiGet('/api/v1/admin/domain-groups');
    list = (resp && resp.groups) || [];
    paint(grid, list, []);
    paint(grid, list, await loadDomains());
  } catch (e) {
    toast('Delete failed: ' + (e.message || e), 'error');
  }
}

async function loadDomains() {
  try {
    const r = await apiGet('/api/v1/domains');
    return Array.isArray(r) ? r : [];
  } catch (_) {
    return [];
  }
}