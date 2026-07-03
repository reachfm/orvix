/* =====================================================================
   pages/clustering.js — Honest clustering + proxy status page.

   Wires:
     GET /api/v1/admin/cluster/status  → node list + proxy slots

   The current build is a single-node deployment. The
   page renders the truthful status: deployment_mode,
   peer nodes, every proxy slot, and an honest note
   that clustering + proxy replication are not
   implemented in this build.

   The page uses real runtime telemetry where it
   exists (listener ports, mail hosts, TLS). Operators
   can read this to confirm there is no fabricated
   "active" state on top of an absent engine.
   ===================================================================== */

import { el } from '../components.js';
import { apiGet } from '../api.js';
import { applyAutoDir } from '../rtl.js';

export async function renderClusteringPage(root, opts) {
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
  root.appendChild(wrap);

  const stateCard = el('section', { class: 'panel panel-placeholder' });
  stateCard.appendChild(el('header', { class: 'panel-head' }, [
    el('h3', { text: 'Deployment mode' }),
    el('span', { class: 'badge tag', text: 'single-node' }),
  ]));
  const stateBody = el('div', { class: 'panel-body', text: 'Loading...' });
  stateCard.appendChild(stateBody);
  wrap.appendChild(stateCard);

  const proxyCard = el('section', { class: 'panel' });
  proxyCard.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Proxy slots' })));
  const proxyBody = el('div', { class: 'panel-body', text: 'Loading...' });
  proxyCard.appendChild(proxyBody);
  wrap.appendChild(proxyCard);

  const nodesCard = el('section', { class: 'panel' });
  nodesCard.appendChild(el('header', { class: 'panel-head' },
    el('h3', { text: 'Peer nodes' })));
  const nodesBody = el('div', { class: 'panel-body' });
  nodesCard.appendChild(nodesBody);
  wrap.appendChild(nodesCard);

  try {
    const r = await apiGet('/api/v1/admin/cluster/status');
    paint(stateCard, proxyCard, nodesCard, r);
  } catch (e) {
    stateBody.innerHTML = '';
    stateBody.appendChild(el('div', { class: 'error', text: 'Failed to load: ' + (e.message || e) }));
  }
  applyAutoDir(wrap);
}

function paint(stateCard, proxyCard, nodesCard, r) {
  const stateBody = stateCard.querySelector('.panel-body');
  stateBody.innerHTML = '';
  stateBody.appendChild(kvTable([
    ['Deployment mode', r.deployment_mode],
    ['Current nodes', r.current_nodes],
    ['Max nodes', r.max_nodes],
    ['Consensus', r.consensus],
  ]));
  stateBody.appendChild(el('p', { class: 'subtle',
    text: r.honest_note || 'clustering / proxy replication is not implemented in this build.' }));

  const proxyBody = proxyCard.querySelector('.panel-body');
  proxyBody.innerHTML = '';
  const proxies = r.proxies || [];
  if (!proxies.length) {
    proxyBody.appendChild(el('div', { class: 'empty', text: 'No proxy slots configured.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    tbl.appendChild(el('thead', null, el('tr', null, [
      el('th', { text: 'Slot' }),
      el('th', { text: 'Kind' }),
      el('th', { text: 'Configured' }),
      el('th', { text: 'Runtime state' }),
      el('th', { text: 'Detail' }),
    ])));
    const tb = el('tbody');
    proxies.forEach((p) => {
      tb.appendChild(el('tr', null, [
        el('td', { text: p.name }),
        el('td', { text: p.kind }),
        el('td', { text: p.configured ? 'yes' : 'no' }),
        el('td', { class: 'kv-v', text: p.runtime_state }),
        el('td', { class: 'kv-v', text: p.detail }),
      ]));
    });
    tbl.appendChild(tb);
    proxyBody.appendChild(tbl);
  }

  const nodesBody = nodesCard.querySelector('.panel-body');
  nodesBody.innerHTML = '';
  const peers = r.peer_nodes || [];
  if (!peers.length) {
    nodesBody.appendChild(el('div', { class: 'empty', text: 'Single-node deployment — no peer nodes.' }));
  } else {
    const tbl = el('table', { class: 'data-table' });
    peers.forEach((p) => {
      tbl.appendChild(el('tr', null, [
        el('td', { text: p.id || p.name || '-' }),
        el('td', { class: 'kv-v', text: p.state || '-' }),
      ]));
    });
    nodesBody.appendChild(tbl);
  }
}

function kvTable(rows) {
  const tbl = el('table', { class: 'kv-table' });
  rows.forEach(([k, v]) => {
    tbl.appendChild(el('tr', null, [
      el('th', { text: k }),
      el('td', { class: 'kv-v', text: String(v == null ? '-' : v) }),
    ]));
  });
  return tbl;
}
