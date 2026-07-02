/* =====================================================================
   pages/security.js — Security center landing.

   Renders the same backend telemetry as the dashboard, but
   focused on the security & filtering group: SSL posture, MFA,
   password policy, anti-spam. Where the backend does not expose
   a given section the page renders the planned placeholder.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { t } from '../i18n.js';
import { applyAutoDir } from '../rtl.js';
import { renderPlannedPage } from './_planned.js';

export async function renderSecurityPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Security & filtering' }),
      el('p', { class: 'page-subtitle subtle', text: 'SSL posture, MFA, anti-spam.' }),
    ]),
  ]));
  root.appendChild(wrap);

  const mfaCard = el('section', { class: 'panel' });
  mfaCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'MFA status' })));
  const mfaBody = el('div', { class: 'panel-body' });
  mfaCard.appendChild(mfaBody);
  wrap.appendChild(mfaCard);

  let mfa;
  try { mfa = await apiGet('/api/v1/admin/mfa/status'); }
  catch (e) { mfa = null; }
  mfaBody.innerHTML = '';
  if (mfa) {
    const dl = el('dl', { class: 'kv' });
    Object.keys(mfa).forEach((k) => {
      dl.appendChild(el('dt', { text: k }));
      dl.appendChild(el('dd', { class: 'kv-v', text: String(mfa[k]) }));
    });
    mfaBody.appendChild(dl);
  } else {
    mfaBody.appendChild(el('div', { class: 'empty', text: 'No MFA telemetry reported.' }));
  }

  // The remaining security sections (anti-spam, spam control,
  // routing, rules, quarantine) are not yet wired to a backend
  // endpoint. We render an honest "planned" placeholder for each.
  const subs = [
    { route: 'security/ssl',      label: 'SSL certificates' },
    { route: 'security/antispam', label: 'Antivirus / anti-spam' },
    { route: 'security/spam',     label: 'Global spam control' },
    { route: 'security/routing',  label: 'Acceptance & routing' },
    { route: 'security/rules',    label: 'Incoming message rules' },
    { route: 'security/quarantine', label: 'View quarantine' },
  ];
  const grid = el('div', { class: 'kv-cards' });
  subs.forEach((s) => {
    grid.appendChild(el('div', { class: 'kv-cell' }, [
      el('dt', { text: s.label }),
      el('dd', { class: 'kv-v' }, el('span', { class: 'badge tag', text: t('common.planned') })),
    ]));
  });
  wrap.appendChild(grid);

  applyAutoDir(wrap);
}
