import { el, badge, fmtShortDate } from '../components.js';
import { apiGet, apiPost } from '../api.js';
import { t } from '../i18n.js';
import { toast } from '../toast.js';

export async function renderLoginProtectionPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Login Protection' }),
      el('p', { class: 'page-subtitle subtle', text: 'Failed login tracking, lockout status, and rate limiting.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const grid = el('div', { class: 'dash-grid' });
  wrap.appendChild(grid);

  // Status card
  const stCard = el('section', { class: 'panel' });
  stCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Login Protection Status' })));
  stCard.appendChild(el('div', { class: 'panel-body', text: t('common.loading') }));
  grid.appendChild(stCard);

  // Lockouts card
  const loCard = el('section', { class: 'panel' });
  loCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Active Lockouts' }),
    el('button', { class: 'btn xs ghost', type: 'button', text: 'Refresh', onclick: () => loadLockouts(loCard) }),
  ]));
  loCard.appendChild(el('div', { class: 'panel-body', text: t('common.loading') }));
  grid.appendChild(loCard);

  // Load status
  try {
    const st = await apiGet('/api/v1/admin/login-protection/status');
    stCard.querySelector('.panel-body').innerHTML = '';
    const items = [];
    items.push(['Protection', st.enabled ? 'Enabled' : 'Disabled']);
    items.push(['Rate limiter', st.rate_limiter || 'active']);
    items.push(['Lockouts', String(st.lockout_count || 0)]);
    items.push(['Backend', st.persistence || 'in_memory']);
    if (!st.persistence_ok) {
      stCard.querySelector('.panel-body').appendChild(el('div', { class: 'banner banner-warn' },
        el('span', { class: 'banner-text', text: st.persistence_error || 'Persistence degraded — lockouts may reset on restart.' })));
    }
    if (st.persistence) {
      items.push(['Backend', st.persistence]);
    }
    const dl = el('dl', { class: 'kv' });
    items.forEach(([k, v]) => {
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { text: String(v) }));
    });
    stCard.querySelector('.panel-body').appendChild(dl);
  } catch (_) {
    stCard.querySelector('.panel-body').innerHTML = '';
    stCard.querySelector('.panel-body').appendChild(el('div', { class: 'error', text: 'Could not load status.' }));
  }

  await loadLockouts(loCard);
}

async function loadLockouts(card) {
  const body = card.querySelector('.panel-body');
  if (!body) return;
  body.innerHTML = '';
  body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
  try {
    const r = await apiGet('/api/v1/admin/login-protection/lockouts');
    body.innerHTML = '';
    const lockouts = (r && r.lockouts) || [];
    if (!lockouts.length) {
      body.appendChild(el('div', { class: 'empty', text: 'No active lockouts.' }));
      return;
    }
    const list = el('ul', { style: 'list-style:none;margin:0;padding:0;' });
    lockouts.forEach((l) => {
      const remaining = l.remaining || '-';
      const item = el('li', { style: 'display:flex;align-items:center;gap:12px;padding:8px 0;border-bottom:1px solid var(--border-1);' }, [
        el('code', { text: l.key || '-' }),
        el('span', { class: 'subtle', text: remaining }),
        el('button', { class: 'btn xs ghost', type: 'button', text: 'Unlock', onclick: async () => {
          try {
            await apiPost('/api/v1/admin/login-protection/lockouts/' + encodeURIComponent(l.key) + '/clear');
            toast('Lockout cleared', 'success', 1800);
            loadLockouts(card);
          } catch (e) { toast((e && e.message) || 'clear failed', 'error'); }
        } }),
      ]);
      list.appendChild(item);
    });
    body.appendChild(list);
  } catch (e) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: (e && e.message) || 'load failed' }));
  }
}
