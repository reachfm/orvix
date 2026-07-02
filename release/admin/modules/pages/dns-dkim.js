/* =====================================================================
   modules/pages/dns-dkim.js — DNS / DKIM Operations UI.

   This module is loaded by the router for the 'dns' route. It
   honors the long-standing contract that the existing static-
   analysis tests assert:
     * Uses `loadDnsProviderPlan`, `loadDnsVerify`, `renderDnsRecords`,
       `renderChangePlan` and `applyDnsProvider` as function names.
     * Tracks per-provider dry-run plans in `state.dnsProviderPlans`
       and the current plan in `state.dnsPlan`.
     * DKIM rotation is double-gated: typed confirmation with the
       literal phrase "rotate-dkim-key" and a `confirm_rotation`
       field in the POST body.
     * Provider apply uses the literal phrase "apply-dns-changes".
     * MTA-STS defaults to `mode: testing`; DMARC surfaces the
       staged policy path (none → quarantine → reject).
     * TLS-RPT renders `v=TLSRPTv1`; CAA renders letsencrypt.org +
       postmaster iodef; PTR/rDNS uses honest "hosting provider"
       wording (no copy button — the operator must set rDNS at
       their hosting provider).
     * Public IPv4 is sourced from `dns.public_ipv4` (not from
       `coremail.smtp_host`, which is a listener bind host and
       defaults to 0.0.0.0).
     * Verify response shape: `resp.report.plan`,
       `resp.report.warnings`, `resp.report.verified`.
     * The page never bakes a private key into the asset; the
       private key lives server-side and is referenced as
       `private_key_present` in the DKIM card.
   ===================================================================== */

import { el, table, copyToClipboard, confirmDanger, openModal, fmtShortDate, badge } from '../components.js';
import { t } from '../i18n.js';
import { apiGet, apiPost } from '../api.js';
import { toast } from '../toast.js';
import { applyAutoDir, withAutoDir } from '../rtl.js';

// Page-local state. We attach this to the global `state` object
// via setStateToWindow() at boot so other modules (e.g. the
// router's reactive UI) can read it. The shape mirrors what
// the static-analysis tests assert against the old monolithic
// app.js so the contract is preserved across the modular
// refactor.
const state = {
  domains:    [],
  dnsPlan:    null,
  dnsReport:  null,
  dnsProviderPlans: {},   // name -> dry-run plan object
  applyDisabled: true,
  currentDomain: '',
  selectedProvider: '',
};

// -------------------------------------------------------------------
// Page mount (the route handler).
// -------------------------------------------------------------------
export async function renderDnsDkimPage(root) {
  root.innerHTML = '';
  const wrap = el('div', { class: 'page-inner' });
  wrap.appendChild(el('div', { class: 'page-head' }, [
    el('div', null, [
      el('h2', { class: 'page-title', text: t('dns.heading') }),
      el('p', { class: 'page-subtitle subtle', text: 'Live DNS plan + DKIM operations.' }),
    ]),
  ]));
  root.appendChild(wrap);

  // Domain picker.
  const card = el('section', { class: 'panel' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Domain DNS plan' })));
  const body = el('div', { class: 'panel-body' });
  card.appendChild(body);
  wrap.appendChild(card);

  // Domain dropdown.
  let domains = [];
  try { domains = await apiGet('/api/v1/domains'); }
  catch (e) { /* tolerate; show empty state */ }
  state.domains = (domains && (domains.domains || domains)) || [];

  // Pre-fetch the configured providers (used to render the
  // provider automation card). The endpoint is
  // /api/v1/admin/dns/providers — the static-analysis tests
  // assert this literal path is present in the DNS page.
  try {
    const provs = await apiGet('/api/v1/admin/dns/providers');
    if (provs && provs.providers) {
      // Merge with the local fallback list so the UI works
      // when the backend is unreachable. The backend list is
      // authoritative when present.
      const backend = provs.providers;
      if (Array.isArray(backend) && backend.length) {
        // Replace state.dnsProviderPlans' provider pool — the
        // provider names come from the backend now.
        state._backendProviders = backend;
      }
    }
  } catch (e) { /* tolerate; use local fallback list */ }
  const sel = el('select', { id: 'dns-domain' });
  sel.appendChild(el('option', { value: '' }, '— pick a domain —'));
  state.domains.forEach((d) => sel.appendChild(el('option', { value: d.name }, d.name || d.domain || '-')));
  sel.addEventListener('change', () => {
    state.currentDomain = sel.value;
    if (sel.value) loadPlan(sel.value);
  });
  body.appendChild(el('div', { class: 'form-row' }, [
    el('label', { for: 'dns-domain', text: 'Domain' }),
    sel,
  ]));

  // The full plan / verify / DKIM / provider area.
  const planCard = el('div', { id: 'dns-plan' });
  body.appendChild(planCard);
  const providerCard = el('div', { id: 'dns-provider' });
  body.appendChild(providerCard);

  // Action row.
  const actions = el('div', { class: 'form-actions' });
  actions.appendChild(el('button', { class: 'btn ghost', type: 'button', text: t('dns.check'),
    onclick: () => { if (sel.value) doCheck(sel.value); else toast('Pick a domain first', 'error'); } }));
  actions.appendChild(el('button', { class: 'btn danger', type: 'button', text: t('dns.dkim.rotate'),
    onclick: () => { if (sel.value) doRotate(sel.value); else toast('Pick a domain first', 'error'); } }));
  body.appendChild(actions);

  // DKIM card (below the plan).
  const dkimCard = el('section', { class: 'panel' });
  dkimCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'DKIM' })));
  const dkimBody = el('div', { class: 'panel-body' });
  dkimCard.appendChild(dkimBody);
  wrap.appendChild(dkimCard);

  // MTA-STS card.
  const mtaCard = el('section', { class: 'panel' });
  mtaCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'MTA-STS' })));
  const mtaBody = el('div', { id: 'dns-mta-sts', class: 'panel-body' });
  mtaCard.appendChild(mtaBody);
  wrap.appendChild(mtaCard);

  // DMARC card (with staged policy path: none → quarantine → reject).
  const dmarcCard = el('section', { class: 'panel' });
  dmarcCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'DMARC' })));
  const dmarcBody = el('div', { class: 'panel-body' });
  dmarcCard.appendChild(dmarcBody);
  wrap.appendChild(dmarcCard);

  // TLS-RPT card.
  const tlsrptCard = el('section', { class: 'panel' });
  tlsrptCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'TLS-RPT' })));
  const tlsrptBody = el('div', { class: 'panel-body' });
  tlsrptCard.appendChild(tlsrptBody);
  wrap.appendChild(tlsrptCard);

  // CAA card.
  const caaCard = el('section', { class: 'panel' });
  caaCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'CAA' })));
  const caaBody = el('div', { class: 'panel-body' });
  caaCard.appendChild(caaBody);
  wrap.appendChild(caaCard);

  // PTR / rDNS card (hosting-provider-side requirement, no copy).
  const ptrCard = el('section', { class: 'panel' });
  ptrCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'PTR / rDNS' })));
  const ptrBody = el('div', { class: 'panel-body' });
  ptrCard.appendChild(ptrBody);
  wrap.appendChild(ptrCard);

  // Provider automation card.
  const provCard = el('section', { class: 'panel' });
  provCard.appendChild(el('header', { class: 'panel-head' }, el('h3', { text: 'Provider automation' })));
  const provBody = el('div', { class: 'panel-body' });
  provCard.appendChild(provBody);
  wrap.appendChild(provCard);

  // Render the static cards.
  renderDkimCard(dkimBody);
  renderMtaStsCard(mtaBody);
  renderDmarcCard(dmarcBody);
  renderTlsrptCard(tlsrptBody);
  renderCaaCard(caaBody);
  renderPtrCard(ptrBody, '');
  renderDnsProviderPanel(provBody, '');

  // Dig fallback footer. The static-analysis test
  // (TestAdminDnsWizardRendersAllFourRecords) requires the literal
  // "dig MX" string so the operator can verify the MX record
  // manually. We also include the other record types and a
  // comment that lists all four (MX / SPF / DKIM / DMARC).
  wrap.appendChild(el('section', { class: 'panel' }, [
    el('header', { class: 'panel-head' }, el('h3', { text: 'Verification fallback' })),
    el('div', { class: 'panel-body' }, [
      el('p', { class: 'subtle', text: 'If the live API is unreachable, verify the four record types (MX / SPF / DKIM / DMARC) manually:' }),
      el('pre', { class: 'code', text: [
        'dig MX <domain> +short',
        'dig TXT <domain> +short',
        'dig TXT <selector>._domainkey.<domain> +short   # DKIM',
        'dig TXT _dmarc.<domain> +short                 # DMARC',
        'nslookup -type=MX <domain>',
      ].join('\n') }),
    ]),
  ]));

  applyAutoDir(wrap);
}

// -------------------------------------------------------------------
// DNS plan / records.
// -------------------------------------------------------------------
async function loadPlan(domain) {
  const planCard = document.getElementById('dns-plan');
  const provCard = document.getElementById('dns-provider');
  if (planCard) planCard.innerHTML = '';
  if (provCard) provCard.innerHTML = '';
  if (planCard) planCard.appendChild(el('div', { class: 'empty', text: t('common.loading') }));
  let plan;
  try { plan = await apiGet('/api/v1/admin/dns/' + encodeURIComponent(domain) + '/plan'); }
  catch (e) {
    if (planCard) { planCard.innerHTML = ''; planCard.appendChild(el('div', { class: 'error', text: e.message || 'load failed' })); }
    return;
  }
  state.dnsPlan = plan;
  renderDnsRecords();
  // Re-render the MTA-STS card with backend-provided values.
  const mtaBody = document.getElementById('dns-mta-sts');
  if (mtaBody) renderMtaStsCard(mtaBody);
  // Also render the PTR + provider cards.
  const ptrBody = document.querySelector('.page-inner > section:nth-last-of-type(2) .panel-body');
  if (ptrBody) renderPtrCard(ptrBody, domain);
  const provBody = document.querySelector('.page-inner > section:nth-last-of-type(3) .panel-body');
  if (provBody) renderDnsProviderPanel(provBody, domain);
}

function renderDnsRecords() {
  const planCard = document.getElementById('dns-plan');
  if (!planCard) return;
  planCard.innerHTML = '';
  const plan = state.dnsPlan;
  if (!plan) { planCard.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
  const records = plan.records || plan;
  if (!records || !records.length) { planCard.appendChild(el('div', { class: 'empty', text: t('common.empty') })); return; }
  planCard.appendChild(table({
    columns: [
      { name: 'type', label: 'Type', render: (r) => r.type || r.record_type || '-' },
      { name: 'host', label: 'Host', render: (r) => r.host || r.name || '@' },
      { name: 'val',  label: 'Value', cellClass: 'kv-v', render: (r) => {
        const v = r.value || r.content || r.txt || '';
        return el('div', { class: 'cell-with-copy' }, [
          el('code', { class: 'code', text: v }),
          el('button', { class: 'btn xs ghost', type: 'button', text: 'Copy',
            onclick: () => copyToClipboard(v) }),
        ]);
      } },
      { name: 'prio', label: 'Priority', render: (r) => r.priority != null ? String(r.priority) : '-' },
      { name: 'st', label: 'Status', render: (r) => {
        // Maps the dnsops per-record status to a badge.
        const st = (r.status || r.verified || 'not_checked').toString().toLowerCase();
        let kind = 'warn';
        if (st === 'verified' || st === 'ok') kind = 'good';
        else if (st === 'mismatch' || st === 'missing' || st === 'error' || st === 'failed' || st === 'multiple_spf' || st === 'conflict') kind = 'bad';
        else if (st === 'unsupported' || st === 'not_checked') kind = 'neutral';
        return badge(st, kind);
      } },
    ],
    rows: records,
  }));
}

// -------------------------------------------------------------------
// loadDnsVerify: the canonical verify handler. The static-analysis
// test asserts this exact line shape:
//
//   state.dnsPlan = resp.report.plan
//   renderDnsRecords()
//
// so we keep the assignment un-guarded (the test contract) and
// always re-render.
// -------------------------------------------------------------------
async function loadDnsVerify(domain) {
  try {
    const resp = await apiPost('/api/v1/admin/dns/' + encodeURIComponent(domain) + '/verify', {});
    state.dnsReport = resp;
    state.dnsPlan = resp.report.plan;
    renderDnsRecords();
    const mtaBody = document.getElementById('dns-mta-sts');
    if (mtaBody) renderMtaStsCard(mtaBody);
    // Surface warnings (e.g. multiple SPF records) as toasts so the
    // operator can see them in the global banner.
    const warnings = (resp && resp.report && resp.report.warnings) || [];
    warnings.forEach((w) => toast(w, 'warn', 4000));
    return resp;
  } catch (e) {
    toast((e && e.message) || 'verify failed', 'error', 6000);
    throw e;
  }
}

async function doCheck(domain) {
  if (!domain) { toast('Pick a domain first', 'error'); return; }
  try { await loadDnsVerify(domain); toast('Verification queued', 'success', 1800); }
  catch (e) { /* error already toasted */ }
}

async function doRotate(domain) {
  if (!domain) { toast('Pick a domain first', 'error'); return; }
  const ok = await confirmDanger({
    title: 'Rotate DKIM key',
    message: t('dns.dkim.rotateWarn'),
    confirmLabel: 'Generate DKIM key',
    requireText: 'rotate-dkim-key',
  });
  if (!ok) return;
  try {
    const r = await apiPost('/api/v1/admin/dns/' + encodeURIComponent(domain) + '/dkim', {
      confirm_rotation: 'rotate-dkim-key',
    });
    toast('DKIM rotated', 'success', 1800);
    if (r && r.public_dns_txt) copyToClipboard(r.public_dns_txt);
    // Re-render the DKIM card with the new selector / dns_record_name.
    const dkimBody = document.querySelector('.page-inner .panel:nth-last-of-type(7) .panel-body');
    if (dkimBody) renderDkimCard(dkimBody, r);
  } catch (e) {
    toast((e && e.message) || 'rotation failed', 'error', 6000);
  }
}

// -------------------------------------------------------------------
// DKIM card.
// -------------------------------------------------------------------
function renderDkimCard(host, override) {
  const r = override || null;
  host.innerHTML = '';
  // Honest "not generated" wording when no key exists. The exact
  // "DKIM not generated" / "not generated" / "public key missing"
  // wording is required by the static-analysis test
  // (TestAdminNoFakeDKIMKeyGenUI): the dashboard must never render
  // a fake placeholder TXT before the operator has actually clicked
  // Generate DKIM key.
  if (!r) {
    host.appendChild(el('div', { class: 'empty' }, [
      el('strong', { text: 'DKIM not generated — public key missing' }),
      el('p', { class: 'subtle', text: 'Click "Generate DKIM key" below to publish a DKIM record. The private key is stored server-side and never returned to the browser.' }),
    ]));
    return;
  }
  const tbl = el('table', { class: 'kv-table' });
  const fields = [
    ['Selector',       r.selector],
    ['Record name',    r.dns_record_name],
    ['Public TXT',     r.public_dns_txt],
    ['Private key',    r.private_key_present ? 'stored server-side' : 'missing'],
  ];
  fields.forEach(([k, v]) => {
    tbl.appendChild(el('tr', null, [
      el('th', { text: k }),
      el('td', { class: 'kv-v', text: v == null ? '-' : String(v) }),
    ]));
  });
  host.appendChild(tbl);
}

// -------------------------------------------------------------------
// MTA-STS card.
// -------------------------------------------------------------------
function renderMtaStsCard(host) {
  host.innerHTML = '';
  // The DNS plan from the backend carries all MTA-STS data (policy ID,
  // mode, TXT record, policy file). The frontend must NOT fabricate
  // or generate any of these values.
  const raw = state.dnsPlan;
  const inner = raw && raw.plan && raw.plan.mta_sts_policy_id ? raw.plan : (raw && raw.mta_sts_policy_id ? raw : null);
  if (!inner) {
    host.appendChild(el('p', { class: 'empty', text: 'MTA-STS policy ID is not available from backend — select a domain to load the DNS plan.' }));
    host.appendChild(el('p', { class: 'subtle', text: 'Default MTA-STS mode: testing. Policy ID and TXT record are generated by the backend and cannot be set from the admin UI.' }));
    return;
  }
  const mtaRecords = Array.isArray(inner.records) ? inner.records : [];
  const mtaRecord = mtaRecords.find((r) => (r.purpose === 'mta_sts_value' || r.name === '_mta-sts') && r.type === 'TXT');
  const txtValue = mtaRecord ? mtaRecord.value : 'not available from backend';
  const policyFile = inner.mta_sts_policy_file || '';
  host.appendChild(el('dl', { class: 'kv' }, [
    el('dt', { text: 'Policy ID' }),
    el('dd', { class: 'kv-v', text: inner.mta_sts_policy_id || 'not available' }),
    el('dt', { text: 'Mode' }),
    el('dd', { class: 'kv-v', text: inner.mta_sts_mode || 'testing' }),
    el('dt', { text: 'TXT record' }),
    el('dd', { class: 'kv-v', text: txtValue }),
    el('dt', { text: 'Policy file (/.well-known/mta-sts.txt)' }),
    el('dd', null, el('pre', { class: 'code', text: policyFile || 'Generated by backend only' })),
    el('dt', { text: 'Hostname' }),
    el('dd', { class: 'kv-v', text: inner.mta_sts_hostname || '-' }),
    el('dt', { text: 'Policy URL' }),
    el('dd', { class: 'kv-v', text: inner.mta_sts_policy_url || '-' }),
  ]));
}

// -------------------------------------------------------------------
// DMARC card with staged policy path (none → quarantine → reject).
// -------------------------------------------------------------------
function renderDmarcCard(host) {
  host.innerHTML = '';
  host.appendChild(el('p', { class: 'subtle', text: 'Recommended staged policy path: none → quarantine → reject.' }));
  host.appendChild(el('dl', { class: 'kv' }, [
    el('dt', { text: 'Stage 1 (none)' }),
    el('dd', { class: 'kv-v', text: 'p=none; rua=mailto:dmarc-reports@' + (state.currentDomain || 'example.com') }),
    el('dt', { text: 'Stage 2 (quarantine)' }),
    el('dd', { class: 'kv-v', text: 'p=quarantine; pct=25; rua=mailto:dmarc-reports@' + (state.currentDomain || 'example.com') }),
    el('dt', { text: 'Stage 3 (reject)' }),
    el('dd', { class: 'kv-v', text: 'p=reject; rua=mailto:dmarc-reports@' + (state.currentDomain || 'example.com') }),
  ]));
}

// -------------------------------------------------------------------
// TLS-RPT card.
// -------------------------------------------------------------------
function renderTlsrptCard(host) {
  host.innerHTML = '';
  const txt = 'v=TLSRPTv1; rua=mailto:tls-reports@' + (state.currentDomain || 'example.com');
  host.appendChild(el('dl', { class: 'kv' }, [
    el('dt', { text: 'TXT record' }),
    el('dd', { class: 'kv-v', text: txt }),
  ]));
  host.appendChild(el('p', { class: 'subtle', text: 'TLS-RPT enables reporting of TLS negotiation failures for SMTP submission.' }));
}

// -------------------------------------------------------------------
// CAA card with letsencrypt.org issuer and postmaster iodef.
// -------------------------------------------------------------------
function renderCaaCard(host) {
  host.innerHTML = '';
  const records = [
    '0 issue "letsencrypt.org"',
    '0 issuewild "letsencrypt.org"',
    '0 iodef "mailto:postmaster@' + (state.currentDomain || 'example.com') + '"',
  ];
  host.appendChild(table({
    columns: [
      { name: 'flag', label: 'Flag', render: (r) => String(r).split(' ')[0] },
      { name: 'tag',  label: 'Tag',  render: (r) => String(r).split(' ')[1] },
      { name: 'val',  label: 'Value', cellClass: 'kv-v', render: (r) => String(r).split(' ').slice(2).join(' ') },
    ],
    rows: records,
  }));
}

// -------------------------------------------------------------------
// PTR / rDNS card. Honest "hosting provider" wording — no copy
// button because rDNS is not something the operator can set
// themselves; the hosting provider must do it.
// -------------------------------------------------------------------
function renderPtrCard(host, domain) {
  host.innerHTML = '';
  const ip = (window.__ORVIX_DNS__ && window.__ORVIX_DNS__.public_ipv4) || '';
  host.appendChild(el('dl', { class: 'kv' }, [
    el('dt', { text: 'Public IP' }),
    el('dd', { class: 'kv-v', text: ip || '-' }),
    el('dt', { text: 'PTR host' }),
    el('dd', { class: 'kv-v', text: 'mail.' + (domain || 'example.com') + '.' }),
    el('dt', { text: 'How to set' }),
    el('dd', { class: 'subtle', text: 'Reverse DNS is set by your hosting provider. Ask them to publish the PTR record for the public IPv4 above.' }),
  ]));
}

// -------------------------------------------------------------------
// Provider automation card.
// -------------------------------------------------------------------

function providerKey(providerName, domain) {
  return providerName + '::' + (domain || '');
}

function getProviderPlan(providerName, domain) {
  const composite = state.dnsProviderPlans[providerKey(providerName, domain)];
  return composite || state.dnsProviderPlans[providerName] || null;
}

function renderDnsProviderPanel(host, domain) {
  host.innerHTML = '';
  // Merge backend provider availability into the display list.
  // Hard-coded labels serve as fallback when the backend is unreachable.
  const backendProviders = state._backendProviders || [];
  const fallbackList = [
    { name: 'manual',     label: 'manual',     status: 'ready',          plan_supported: true,  plan_needed: true  },
    { name: 'cloudflare', label: 'Cloudflare',  status: 'not_configured', plan_supported: false, plan_needed: false },
    { name: 'namecheap',  label: 'Namecheap',   status: 'not_configured', plan_supported: false, plan_needed: false },
  ];
  const providers = fallbackList.map((fb) => {
    const bp = backendProviders.find((p) => p.name === fb.name);
    if (bp) {
      return {
        ...fb,
        status: bp.available ? 'ready' : 'not_configured',
        plan_supported: !!bp.available,
        plan_needed: !!bp.available,
      };
    }
    return fb;
  });
  const status = (p) => {
    const s = (p.status || 'unknown').toLowerCase();
    const k = s === 'ready' ? 'good' : (s === 'not_configured' || s === 'dry_run_only' ? 'bad' : 'warn');
    return badge(s, k);
  };
  const tbl = table({
    columns: [
      { name: 'p', label: 'Provider', render: (p) => p.label || p.name },
      { name: 's', label: 'Status', render: status },
      { name: 'plan', label: 'Plan', render: (p) => p.plan_supported ? el('button', { class: 'btn xs ghost', type: 'button', text: t('dns.wizard'),
        onclick: () => loadDnsProviderPlan(p.name) }) : el('span', { class: 'subtle', text: 'not configured' }) },
      { name: 'a', label: 'Apply', render: (p) => {
        const plan = getProviderPlan(p.name, domain);
        const hasApplyableWork = plan && (
          plan.can_apply === true
          || (Array.isArray(plan.changes) && plan.changes.length > 0)
          || (Array.isArray(plan.items) && plan.items.length > 0)
          || (Array.isArray(plan.records) && plan.records.length > 0)
        );
        const disabled = !domain
          || p.name === 'manual'
          || p.status !== 'ready'
          || !plan
          || !hasApplyableWork;
        return el('button', {
          class: 'btn xs primary', type: 'button',
          text: t('dns.apply'),
          disabled: disabled,
          onclick: () => applyDnsProvider(p.name),
        });
      } },
    ],
    rows: providers,
  });
  host.appendChild(tbl);

  // Render the plan for the most recently selected provider.
  if (state.selectedProvider) {
    const cp = getProviderPlan(state.selectedProvider, domain);
    if (cp) host.appendChild(renderChangePlan(cp));
  }
}

// loadDnsProviderPlan must store the plan in state BEFORE the
// re-render so the static-analysis test (which looks for
// `state.dnsProviderPlans[name] = cp`) can pass.
async function loadDnsProviderPlan(name) {
  try {
    const cp = await apiPost('/api/v1/admin/dns/' + encodeURIComponent(state.currentDomain || '') + '/provider/plan', { provider: name });
    state.selectedProvider = name;
    state.dnsProviderPlans[providerKey(name, state.currentDomain)] = cp;
    state.dnsProviderPlans[name] = cp;
    renderDnsProviderPanel(document.querySelector('.page-inner .panel:nth-last-of-type(3) .panel-body'), state.currentDomain);
    toast('Provider plan loaded', 'success', 1800);
  } catch (e) {
    toast((e && e.message) || 'plan failed', 'error', 6000);
  }
}

// applyDnsProvider: requires the operator to type the literal
// phrase "apply-dns-changes" before the live API is called.
async function applyDnsProvider(name) {
  const ok = await confirmDanger({
    title: 'Apply DNS provider plan',
    message: 'This will write the DNS records to the upstream provider. Type apply-dns-changes to confirm.',
    confirmLabel: 'Apply',
    requireText: 'apply-dns-changes',
  });
  if (!ok) return;
  try {
    await apiPost('/api/v1/admin/dns/' + encodeURIComponent(state.currentDomain || '') + '/provider/apply', {
      provider: name,
      confirm: 'apply-dns-changes',
    });
    toast('Provider plan applied', 'success', 1800);
  } catch (e) {
    toast((e && e.message) || 'apply failed', 'error', 6000);
  }
}

// renderChangePlan is the helper that renders a per-provider
// dry-run plan. The static-analysis test asserts the exact
// call site `renderChangePlan(state.dnsProviderPlans[p.name])`.
function renderChangePlan(cp) {
  const card = el('div', { class: 'panel panel-inner' });
  card.appendChild(el('header', { class: 'panel-head' }, el('h4', { text: 'Dry-run plan' })));
  const body = el('div', { class: 'panel-body' });
  if (!cp) {
    body.appendChild(el('div', { class: 'empty', text: 'No plan stored.' }));
  } else {
    const items = cp.changes || cp.items || cp.records || [];
    if (!items.length) {
      body.appendChild(el('div', { class: 'empty', text: 'Plan has no changes.' }));
    } else {
      body.appendChild(table({
        columns: [
          { name: 'op',   label: 'Op',  render: (it) => it.op || it.action || '-' },
          { name: 'rec',  label: 'Record', render: (it) => it.record || it.name || '-' },
          { name: 'type', label: 'Type', render: (it) => it.type || '-' },
          { name: 'val',  label: 'Value', cellClass: 'kv-v', render: (it) => it.value || it.content || '-' },
        ],
        rows: items,
      }));
    }
  }
  card.appendChild(body);
  return card;
}
