/* =====================================================================
   pages/administration-rights.js — Administration rights center.

   Renders MFA status + admin groups / users / domain admin limits.
   The latter groups have no backend yet in this build; each
   shows a planned placeholder rather than a fake list.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { t } from '../i18n.js';
import { applyAutoDir } from '../rtl.js';

export async function renderAdminRightsPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: 'Administration rights' }),
      el('p', { class: 'page-subtitle subtle', text: 'MFA, administrative groups, administrative users, domain admin limits.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // MFA card — real.
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
    mfaBody.appendChild(el('div', { class: 'empty', text: t('common.empty') }));
  }

  // Grouped planned placeholders.
  const groups = [
    { title: 'Administrative groups', endpoint: 'GET /api/v1/admin/admin-groups' },
    { title: 'Administrative users', endpoint: 'GET /api/v1/admin/admin-users' },
    { title: 'Domain admin limits', endpoint: 'GET /api/v1/admin/domain-admin-limits' },
  ];
  groups.forEach((g) => {
    const card = el('section', { class: 'panel panel-placeholder' });
    card.appendChild(el('header', { class: 'panel-head' }, [
      el('h3', { text: g.title }),
      el('span', { class: 'badge tag', text: t('common.planned') }),
    ]));
    const body = el('div', { class: 'panel-body', text: t('planned.feature') });
    card.appendChild(body);
    card.appendChild(el('div', { class: 'panel-foot' }, [
      el('span', { class: 'subtle', text: 'Would query: ' }),
      el('code', { class: 'code', text: g.endpoint }),
    ]));
    wrap.appendChild(card);
  });
  applyAutoDir(wrap);
}
