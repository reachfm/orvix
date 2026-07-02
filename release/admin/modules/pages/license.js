/* =====================================================================
   pages/license.js — License posture panel.

   Wires:
     GET  /api/v1/license
     POST /api/v1/license/validate (CSRF)

   The page renders the license state with badges and never
   shows secrets. "Offline" is rendered as a warning state, not
   as success.
   ===================================================================== */

import { el, badge, fmtShortDate, confirmDanger } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost } from '../api.js';
import { setLicense, getLicense, getRuntime } from '../state.js';
import { toast } from '../toast.js';
import { applyAutoDir } from '../rtl.js';
import { isZeroDate, safeNote } from './dashboard.js';

function stateKind(license) {
  if (!license) return 'neutral';
  if (license.offline) return 'warn';
  if (!license.public_key_present) return 'bad';
  if (!license.license_present) return 'bad';
  if (license.expired) return 'bad';
  if (license.valid) return 'good';
  return 'neutral';
}
function stateLabel(license) {
  if (!license) return 'unknown';
  if (license.offline) return t('license.offline');
  if (!license.public_key_present) return t('license.publicKeyMissing');
  if (!license.license_present) return t('license.licenseMissing');
  if (license.expired) return t('license.expired');
  if (license.valid) return t('license.valid');
  return t('license.invalid');
}

export async function renderLicensePage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('license.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'License posture. No secret material is shown.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Status' }),
    el('span', null, badge('—', 'neutral')),
  ]));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  let data, err;
  try { data = await apiGet('/api/v1/license'); }
  catch (e) { err = e; }
  body.innerHTML = '';
  if (err) { body.appendChild(el('div', { class: 'error', text: (err && err.message) || 'Could not load license.' })); applyAutoDir(wrap); return; }
  setLicense(data || {});
  // Prefer the runtime-telemetry license snapshot when present
  // because the runtime /admin/runtime endpoint is the operator's
  // primary source of truth for license status (the /license
  // endpoint validates against a possibly-stale local file).
  // The dashboard's license card surfaces rt.license values when
  // available and falls back to the dedicated endpoint.
  const rt = (typeof getRuntime === 'function' && getRuntime()) || null;
  const l = (rt && rt.license) || state.license || data || {};
  card.querySelector('.panel-head span').replaceWith(badge(stateLabel(l), stateKind(l)));

  const dl = el('dl', { class: 'kv' });
  const fields = [
    ['Tier',     l.tier || l.kind || '-'],
    ['Seats',    l.seats != null ? String(l.seats) : '-'],
    ['Expires',  l.expires || l.expires_at ? safeNote(l.expires || l.expires_at) : '-'],
    ['Offline',  l.offline ? 'yes' : 'no'],
    ['Public key', l.public_key_present ? 'present' : (l.public_key_present === false ? 'missing' : '-')],
    ['License file', l.license_present ? 'present' : (l.license_present === false ? 'missing' : '-')],
    ['Valid', l.valid ? 'yes' : (l.valid === false ? 'no' : '-')],
    ['Expired', l.expired ? 'yes' : (l.expired === false ? 'no' : '-')],
  ];
  fields.forEach(([k, v]) => {
    dl.appendChild(el('dt', { text: k }));
    dl.appendChild(el('dd', { class: 'kv-v', text: String(v) }));
  });
  body.appendChild(dl);

  if (l.offline) {
    body.appendChild(el('p', { class: 'subtle warn', text: 'License validation is in offline mode. Operator should not consider the platform as fully licensed.' }));
  }

  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn primary', type: 'button', text: t('license.validate'),
    onclick: () => doValidate() }));
  body.appendChild(actions);
  applyAutoDir(wrap);
}

async function doValidate() {
  const ok = await confirmDanger({ title: 'Validate license', message: 'Re-check license against backend?', confirmLabel: 'Validate' });
  if (!ok) return;
  try {
    const r = await apiPost('/api/v1/license/validate', {});
    setLicense(r || {});
    toast('License validated', 'success', 1800);
    setTimeout(() => location.reload(), 400);
  } catch (e) {
    toast((e && e.message) || 'validate failed', 'error', 6000);
  }
}
