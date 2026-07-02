/* =====================================================================
   pages/clustering.js — Honest "needs backend" page for
   clustering / proxy support. The current Orvix build is a
   single-node deployment. Clustering / IMAP / POP3 / WebMail
   proxies are NOT implemented; the page renders an honest
   status so operators are not misled.
   ===================================================================== */

import { el } from '../components.js';
import { applyAutoDir } from '../rtl.js';

const FEATURES = [
  { title: 'Clustering setup', body: 'Multi-node consensus / quorum is not implemented in this build. The runtime is single-node.' },
  { title: 'IMAP proxy', body: 'IMAP proxy with sticky sessions is not implemented. Clients connect directly to the local IMAP listener.' },
  { title: 'POP3 proxy', body: 'POP3 proxy is not implemented. Same direct-connect behaviour as IMAP.' },
  { title: 'WebMail proxy', body: 'WebMail proxy is not implemented. Each node serves its own webmail host.' },
];

export function renderClusteringPage(root, opts) {
  root.innerHTML = '';
  opts = opts || {};
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: opts.title || 'Clustering' }),
      el('p', { class: 'page-subtitle subtle',
        text: 'Honest status of clustering / proxy support in this build.' }),
    ]),
  ]));

  const card = el('section', { class: 'panel panel-placeholder' });
  card.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Single-node deployment' }),
    el('span', { class: 'badge tag danger', text: 'Not implemented' }),
  ]));
  card.appendChild(el('div', { class: 'panel-body' }, [
    el('p', { text: 'Orvix Enterprise currently runs as a single-node deployment. Clustering, IMAP / POP3 / WebMail proxies, and automatic failover are not implemented in this build. The runtime telemetry shows the listeners you have configured locally; there is no replication layer.' }),
    el('p', { text: 'For high availability, deploy Orvix on a single hardened host and use a load balancer for HTTP / IMAP / POP3 / SMTP frontends; the storage layer is already designed for snapshot / restore via the backups module.' }),
  ]));
  wrap.appendChild(card);

  FEATURES.forEach((f) => {
    const p = el('section', { class: 'panel' });
    p.appendChild(el('header', { class: 'panel-head' }, [
      el('h3', { text: f.title }),
      el('span', { class: 'badge tag', text: 'planned' }),
    ]));
    p.appendChild(el('div', { class: 'panel-body', text: f.body }));
    wrap.appendChild(p);
  });

  root.appendChild(wrap);
  applyAutoDir(wrap);
}