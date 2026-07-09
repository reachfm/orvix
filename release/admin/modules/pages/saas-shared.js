/* =====================================================================
   modules/pages/saas-shared.js — common render helpers for the two
   Customer / Internal pages.
   ===================================================================== */

import { apiGet, apiPost, apiPatch, apiDelete } from '../api.js';
import { el, table, badge, statusKind, esc, fmtNumber, fmtBytes, fmtShortDate, fmtDate, $ } from '../components.js';
import { t } from '../i18n.js';

export function pageHeader(root, opts) {
  const head = el('div', { class: 'page-hero' });
  const main = el('div', { class: 'page-hero-main' });
  if (opts.eyebrow) main.appendChild(el('div', { class: 'page-eyebrow', text: opts.eyebrow }));
  main.appendChild(el('h1', { class: 'page-title', text: opts.title }));
  if (opts.subtitle) main.appendChild(el('p', { class: 'page-subtitle', text: opts.subtitle }));
  head.appendChild(main);
  if (opts.actions) {
    const a = el('div', { class: 'page-actions' });
    (Array.isArray(opts.actions) ? opts.actions : [opts.actions]).forEach((b) => a.appendChild(b));
    head.appendChild(a);
  }
  root.appendChild(head);
}

export function emptyState(root, opts) {
  const box = el('div', { class: 'empty-state' });
  if (opts.icon) {
    box.appendChild(el('span', { class: 'empty-illustration', text: opts.icon }));
  }
  if (opts.title) box.appendChild(el('div', { class: 'empty-title', text: opts.title }));
  if (opts.body) box.appendChild(el('div', { class: 'empty-hint', text: opts.body }));
  if (opts.actions) {
    const wrap = el('div', { class: 'empty-actions' });
    (Array.isArray(opts.actions) ? opts.actions : [opts.actions]).forEach((a) => wrap.appendChild(a));
    box.appendChild(wrap);
  }
  root.appendChild(box);
}

export function panel(root, opts) {
  const wrap = el('section', { class: 'panel' + (opts.wide ? ' panel-wide' : '') });
  const head = el('div', { class: 'panel-head' });
  if (opts.title) {
    const h3 = el('h3', { text: '' });
    h3.appendChild(el('span', { class: 'panel-icon', text: (opts.title || '?').charAt(0).toUpperCase() }));
    h3.appendChild(document.createTextNode(opts.title));
    head.appendChild(h3);
  }
  if (opts.meta) head.appendChild(el('div', { class: 'panel-head-meta', text: opts.meta }));
  if (opts.actions) {
    const act = el('div', { class: 'panel-head-actions' });
    (Array.isArray(opts.actions) ? opts.actions : [opts.actions]).forEach((b) => act.appendChild(b));
    head.appendChild(act);
  }
  wrap.appendChild(head);
  const body = el('div', { class: 'panel-body' });
  if (opts.body) {
    if (typeof opts.body === 'string') body.appendChild(document.createTextNode(opts.body));
    else body.appendChild(opts.body);
  }
  wrap.appendChild(body);
  root.appendChild(wrap);
  return body;
}

export function kpi(root, label, value, tone) {
  const card = el('div', { class: 'kpi-pro', 'data-tone': tone || 'info' });
  card.appendChild(el('div', { class: 'kpi-pro-label', text: label }));
  card.appendChild(el('div', { class: 'kpi-pro-value', text: value }));
  root.appendChild(card);
}

export function kpiRow(root, items) {
  const row = el('div', { class: 'dashboard-kpi-row' });
  items.forEach((it) => kpi(row, it.label, it.value, it.tone));
  root.appendChild(row);
}

export async function loadAll() {
  return await Promise.allSettled([]);
}

export { apiGet, apiPost, apiPatch, apiDelete, table, badge, statusKind, esc, fmtNumber, fmtBytes, fmtShortDate, fmtDate, $, t, el };
