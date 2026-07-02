/* =====================================================================
   pages/_planned.js — Shared "planned / not yet wired" placeholder.

   Many sidebar items point to backend features that are not yet
   implemented. Instead of faking empty data, this page renders an
   honest placeholder: route name, planned copy, and (when known)
   the backend endpoint that would expose the feature. The operator
   never sees a fake "success" screen.
   ===================================================================== */

import { el } from '../components.js';
import { t } from '../i18n.js';

export function renderPlannedPage(root, opts) {
  root.innerHTML = '';
  const feature = opts.feature || t('sidebar.dashboard');
  const detail  = opts.detail  || t('planned.feature');
  const endpoint = opts.endpoint || '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: feature }),
      el('p', { class: 'page-subtitle subtle', text: t('common.planned') }),
    ]),
  ]));
  const card = el('section', { class: 'panel panel-placeholder' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('span', { class: 'badge tag', text: t('common.planned') }),
    el('h3', { text: feature }),
  ]));
  card.appendChild(el('div', { class: 'panel-body', text: detail }));
  if (endpoint) {
    card.appendChild(el('div', { class: 'panel-foot' }, [
      el('span', { class: 'subtle', text: 'Would query: ' }),
      el('code', { class: 'code', text: endpoint }),
    ]));
  }
  wrap.appendChild(card);
  root.appendChild(wrap);
}
