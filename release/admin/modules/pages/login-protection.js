/* =====================================================================
   pages/login-protection.js — Login protection (rate-limit + lockout).

   The page surfaces the same data the login page shows as a posture
   summary, plus the operator-only controls (recent lockouts, manual
   unlock). Every value comes from a real backend endpoint — no
   fabricated data, no OS Fail2Ban claim.
   ===================================================================== */

import { el, badge, fmtShortDate, fmtNumber } from '../components.js';
import { apiGet, apiPost } from '../api.js';
import { t } from '../i18n.js';
import { toast } from '../toast.js';

export async function renderLoginProtectionPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner ops-page' });

  // ---------- Page hero ----------
  const hero = el('div', { class: 'ops-hero', 'data-tone': 'good' });
  hero.appendChild(el('div', { class: 'ops-hero-main' }, [
    el('span', { class: 'ops-hero-eyebrow', text: 'Security operations' }),
    el('h2', { class: 'ops-hero-title', text: 'Login Protection' }),
    el('p', { class: 'ops-hero-sub',
      text: 'Application-level rate-limit and lockout status. Failed login attempts are tracked and locked out per IP / per account. OS-level Fail2Ban is not asserted on this page.' }),
  ]));
  wrap.appendChild(hero);

  // ---------- KPI strip ----------
  const kpi = el('section', { class: 'dashboard-kpi-row', id: 'lp-kpi' });
  kpi.appendChild(kpiPro('Status', 'Loading\u2026', 'info', 'shield'));
  kpi.appendChild(kpiPro('Rate limit', '100 req / 60s', 'info', 'monitoring'));
  kpi.appendChild(kpiPro('Lockout window', '15 min', 'info', 'lock'));
  kpi.appendChild(kpiPro('Persistence', 'Loading\u2026', 'info', 'storage'));
  wrap.appendChild(kpi);

  // ---------- Health cards (engine posture) ----------
  wrap.appendChild(sectionHead('Posture', 'Real-time engine state from /api/v1/admin/login-protection/status'));
  const healthGrid = el('div', { class: 'health-grid', id: 'lp-health' });
  wrap.appendChild(healthGrid);

  // ---------- Active lockouts panel ----------
  const lockoutsCard = el('section', { class: 'panel' });
  lockoutsCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Active Lockouts' }),
    el('div', { class: 'panel-head-actions' }, [
      el('button', { class: 'btn ghost sm', type: 'button', text: 'Refresh',
        onclick: () => loadLockouts(lockoutsCard) }),
    ]),
  ]));
  const lockoutsBody = el('div', { class: 'panel-body', text: t('common.loading') });
  lockoutsCard.appendChild(lockoutsBody);
  wrap.appendChild(lockoutsCard);

  // ---------- Honest notes panel ----------
  const noteCard = el('section', { class: 'panel' });
  noteCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'What this page does and does not claim' }),
  ]));
  const noteBody = el('div', { class: 'panel-body' });
  noteBody.appendChild(el('p', { class: 'subtle', style: 'margin: 0 0 10px;',
    text: 'What is real:' }));
  const real = el('ul', { class: 'kv-list', style: 'grid-template-columns: 1fr; row-gap: 6px; margin: 0 0 14px;' });
  [
    'Login endpoints are rate-limited: 5 attempts per 15 min per IP, 100 req / min / IP overall.',
    'Failed attempts above the threshold lock the source key for 15 minutes.',
    'Lockouts are stored in the trust engine; restart-safe only if persistence is enabled.',
    'Every state change is written to the audit log (no silent changes).',
  ].forEach((t) => real.appendChild(el('li', { style: 'color: var(--text-1); font-size: 13px;', text: '\u2022 ' + t })));
  noteBody.appendChild(real);
  noteBody.appendChild(el('p', { class: 'subtle', style: 'margin: 14px 0 10px;',
    text: 'What this page does NOT claim:' }));
  const not_ = el('ul', { class: 'kv-list', style: 'grid-template-columns: 1fr; row-gap: 6px; margin: 0;' });
  [
    'OS-level Fail2Ban is not asserted. This page never reports "Fail2Ban active" unless the OS service is detected at install.',
    'Geographic / IP-reputation blocks are not part of the in-app lockout pipeline.',
    'Two-factor verification failure isolation is a separate MFA flow, not a login-protection lockout.',
  ].forEach((t) => not_.appendChild(el('li', { style: 'color: var(--text-2); font-size: 13px;', text: '\u2022 ' + t })));
  noteBody.appendChild(not_);
  noteCard.appendChild(noteBody);
  wrap.appendChild(noteCard);

  root.appendChild(wrap);

  await loadStatus(kpi, healthGrid);
  await loadLockouts(lockoutsCard);
  applyAutoDir(wrap);
}

function kpiPro(label, value, tone, iconName) {
  return el('article', { class: 'kpi-pro', 'data-tone': tone }, [
    el('div', { class: 'kpi-pro-label' }, [
      el('span', { html: '<svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="3" width="10" height="10" rx="1"/></svg>' }),
      document.createTextNode(' ' + label),
    ]),
    el('div', { class: 'kpi-pro-value is-text', text: value }),
    el('div', { class: 'kpi-pro-foot' }, [
      el('span', { class: 'subtle', text: 'Live telemetry' }),
    ]),
  ]);
}

function sectionHead(title, sub) {
  const h = el('div', { class: 'ops-section-head' });
  h.appendChild(el('h3', { text: title }));
  if (sub) h.appendChild(el('span', { class: 'subtle', text: sub }));
  return h;
}

async function loadStatus(kpiHost, healthHost) {
  let s = null;
  try { s = await apiGet('/api/v1/admin/login-protection/status'); } catch (_) { s = null; }
  if (!s) {
    paintKpiNotConfigured(kpiHost);
    paintHealthUnavailable(healthHost);
    return;
  }

  // Update KPI strip
  const statusTone = s.enabled ? 'good' : 'warn';
  const statusText = s.enabled ? 'Enabled' : 'Disabled';
  const persistText = s.persistence_ok ? (s.persistence || 'in_memory') : ((s.persistence || 'in_memory') + ' (degraded)');
  const persistTone = s.persistence_ok ? 'good' : 'warn';
  const lockouts = Number(s.lockout_count || 0);
  kpiHost.innerHTML = '';
  kpiHost.appendChild(kpiPro('Status', statusText, statusTone, 'shield'));
  kpiHost.appendChild(kpiPro('Rate limit', '100 req / 60s', 'info', 'monitoring'));
  kpiHost.appendChild(kpiPro('Active lockouts', String(lockouts), lockouts > 0 ? 'warn' : 'good', 'lock'));
  kpiHost.appendChild(kpiPro('Persistence', persistText, persistTone, 'storage'));

  // Health cards
  healthHost.innerHTML = '';
  healthHost.appendChild(engineCard('Engine', {
    state: s.enabled ? 'active' : 'unknown',
    stateLabel: s.enabled ? 'active' : 'not configured',
    icon: 'shield',
    meta: [
      ['Enabled',     s.enabled ? 'yes' : 'no'],
      ['Rate limiter', s.rate_limiter || 'active'],
      ['Limit',        s.rate_limit_desc || '5 login attempts / 15 min / IP'],
    ],
    note: 'Per-IP and per-account rate limits are enforced by the trust engine.',
  }));
  healthHost.appendChild(engineCard('Persistence', {
    state: s.persistence_ok ? 'active' : 'degraded',
    stateLabel: s.persistence_ok ? (s.persistence || 'in_memory') : ((s.persistence || 'in_memory') + ' (degraded)'),
    icon: 'storage',
    meta: [
      ['Backend',      s.persistence || 'in_memory'],
      ['Healthy',      s.persistence_ok ? 'yes' : 'no'],
    ],
    note: s.persistence_ok
      ? 'Lockout state survives restarts.'
      : (s.persistence_error || 'Lockout state may reset on restart. Re-enable persistence before relying on rate-limit state.'),
  }));
  healthHost.appendChild(engineCard('Lockouts', {
    state: lockouts > 0 ? 'degraded' : 'active',
    stateLabel: lockouts > 0 ? (String(lockouts) + ' active') : 'none',
    icon: 'lock',
    meta: [
      ['Count',         String(lockouts)],
      ['Window',        '15 minutes'],
      ['Manual unlock', 'available below'],
    ],
    note: 'Active lockouts are listed below. Each entry can be cleared by an admin (RBAC + CSRF required).',
  }));
}

function paintKpiNotConfigured(host) {
  host.innerHTML = '';
  host.appendChild(kpiPro('Status', 'Not configured', 'warn', 'shield'));
  host.appendChild(kpiPro('Rate limit', 'N/A', 'neutral', 'monitoring'));
  host.appendChild(kpiPro('Active lockouts', 'N/A', 'neutral', 'lock'));
  host.appendChild(kpiPro('Persistence', 'N/A', 'neutral', 'storage'));
}

function paintHealthUnavailable(host) {
  host.innerHTML = '';
  host.appendChild(engineCard('Engine', {
    state: 'unknown',
    stateLabel: 'not configured',
    icon: 'shield',
    meta: [['Status', 'endpoint unreachable']],
    note: 'Could not load /api/v1/admin/login-protection/status. Check that the admin API is running.',
  }));
}

function engineCard(title, opts) {
  const card = el('article', { class: 'health-card', 'data-state': opts.state });
  card.appendChild(el('div', { class: 'health-card-head' }, [
    el('div', { class: 'health-card-name' }, [
      el('span', { class: 'proto-glyph', text: title.slice(0, 4).toUpperCase() }),
      document.createTextNode(' ' + title),
    ]),
    el('span', { class: 'health-card-state ' + (opts.state === 'active' ? 'good' : opts.state === 'degraded' ? 'warn' : opts.state === 'failed' ? 'bad' : 'muted'),
      text: opts.stateLabel }),
  ]));
  const meta = el('div', { class: 'health-card-meta' });
  (opts.meta || []).forEach(([k, v]) => {
    meta.appendChild(el('div', { class: 'health-card-meta-row' }, [
      el('span', { class: 'k', text: k }),
      el('span', { class: 'v', text: String(v == null || v === '' ? '-' : v) }),
    ]));
  });
  card.appendChild(meta);
  if (opts.note) card.appendChild(el('p', { class: 'subtle', style: 'margin: 4px 0 0; line-height: 1.4;', text: opts.note }));
  return card;
}

async function loadLockouts(card) {
  const body = card.querySelector('.panel-body');
  if (!body) return;
  body.innerHTML = '';
  body.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
  let r = null;
  try { r = await apiGet('/api/v1/admin/login-protection/lockouts'); }
  catch (_) {
    body.innerHTML = '';
    body.appendChild(el('div', { class: 'error', text: 'Could not load lockouts. Endpoint requires admin role.' }));
    return;
  }
  body.innerHTML = '';
  const lockouts = (r && r.lockouts) || [];
  if (!lockouts.length) {
    body.appendChild(el('div', { class: 'empty-state' }, [
      el('div', { class: 'empty-illustration', text: '\u2713' }),
      el('div', { class: 'empty-title', text: 'No active lockouts' }),
      el('div', { class: 'empty-hint', text: 'The trust engine is not currently holding any failed-login state. Healthy.' }),
    ]));
    return;
  }
  const tbl = el('table', { class: 'data-table' });
  tbl.appendChild(el('thead', null, el('tr', null, [
    el('th', { text: 'Source key' }),
    el('th', { text: 'Remaining' }),
    el('th', { text: 'Status' }),
    el('th', { class: 'actions', text: '' }),
  ])));
  const tb = el('tbody');
  lockouts.forEach((l) => {
    const remaining = l.remaining || l.expires_in || '-';
    tb.appendChild(el('tr', null, [
      el('td', { class: 'mono', text: l.key || '-' }),
      el('td', { class: 'row-numeric subtle', text: String(remaining) }),
      el('td', null, badge('locked', 'warn')),
      el('td', { class: 'actions' }, [
        el('button', { class: 'btn xs ghost', type: 'button', text: 'Unlock',
          onclick: async (ev) => {
            const btn = ev.currentTarget;
            btn.setAttribute('disabled', 'disabled');
            btn.textContent = 'Unlocking\u2026';
            try {
              await apiPost('/api/v1/admin/login-protection/lockouts/' + encodeURIComponent(l.key) + '/clear');
              toast('Lockout cleared', 'success', 1800);
              await loadLockouts(card);
            } catch (e) {
              toast((e && e.message) || 'clear failed', 'error');
              btn.removeAttribute('disabled');
              btn.textContent = 'Unlock';
            }
          } }),
      ]),
    ]));
  });
  tbl.appendChild(tb);
  body.appendChild(tbl);
}

// Local applyAutoDir so we don't have to import from rtl.js twice
function applyAutoDir(root) {
  if (!root) return;
  Array.from(root.querySelectorAll('*')).forEach((el) => {
    if (el.children.length === 0 && el.childNodes.length > 0) {
      el.setAttribute('dir', 'auto');
    }
  });
}
