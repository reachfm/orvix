// Orvix Webmail — auth gate.
//
// Loaded BEFORE the React bundle by release/webmail/index.html.
// Probes /api/v1/me with credentials:include so the existing
// admin session cookie is sent. The React bundle still loads
// (deferred module script), but #root is hidden until the auth
// check passes.
//
// Outcomes:
//   - HTTP 200: remove the gate, show #root (React app renders
//     mailbox UI as before).
//   - HTTP 401: keep #root hidden, show "Please sign in" with a
//     link to /admin. No Compose/Inbox is rendered; no API
//     calls leak silently.
//   - any other status / network error: keep #root hidden,
//     show "Webmail unavailable" with a retry link.
//   - no fetch (e.g. ancient browser): show the error card.
//
// The gate does NOT log the user in, does NOT set cookies, and
// does NOT call any other API endpoint. It is purely a UI gate
// that defers the React mount until /api/v1/me confirms a
// valid session.
//
// This file is loaded via <script src="..."> (not inline) so
// the project CSP (script-src 'self') allows it without
// 'unsafe-inline'.

(function () {
  'use strict';

  var API_ME = '/api/v1/me';
  var SIGN_IN_HREF = '/admin';
  var GATE_ID = 'orvix-auth-gate';

  function $(id) { return document.getElementById(id); }

  function hideRoot() {
    var root = $('root');
    if (root) root.style.display = 'none';
  }

  function ensureGate() {
    var gate = $(GATE_ID);
    if (gate) return gate;
    gate = document.createElement('div');
    gate.id = GATE_ID;
    gate.setAttribute('role', 'status');
    gate.setAttribute('aria-live', 'polite');
    document.body.appendChild(gate);
    return gate;
  }

  function clearChildren(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function el(tag, opts, text) {
    var node = document.createElement(tag);
    if (opts) {
      if (opts.className) node.className = opts.className;
      if (opts.href) node.setAttribute('href', opts.href);
      if (opts['aria-hidden']) node.setAttribute('aria-hidden', opts['aria-hidden']);
    }
    if (text != null) node.textContent = text;
    return node;
  }

  function loadingCard() {
    var card = el('div', { className: 'orvix-gate-card' });
    var title = el('h1', { className: 'orvix-gate-title' }, 'Orvix Webmail');
    var text = el('p', { className: 'orvix-gate-text' });
    var spinner = el('span', { className: 'orvix-gate-spinner', 'aria-hidden': 'true' });
    text.appendChild(spinner);
    text.appendChild(document.createTextNode('Checking your session…'));
    card.appendChild(title);
    card.appendChild(text);
    return card;
  }

  function signInCard() {
    var card = el('div', { className: 'orvix-gate-card' });
    card.appendChild(el('h1', { className: 'orvix-gate-title' }, 'Please sign in'));
    card.appendChild(el('p', { className: 'orvix-gate-text' },
      'You need to sign in to the Orvix admin console before you can use webmail.'));
    card.appendChild(el('a', { className: 'orvix-gate-button', href: SIGN_IN_HREF },
      'Go to admin sign-in'));
    return card;
  }

  function errorCard(status) {
    var card = el('div', { className: 'orvix-gate-card' });
    card.appendChild(el('h1', { className: 'orvix-gate-title' }, 'Webmail unavailable'));
    var msg = 'The webmail service returned an unexpected response (HTTP ' +
      (status || 'unknown') + '). Please try again in a moment.';
    card.appendChild(el('p', { className: 'orvix-gate-text orvix-gate-error' }, msg));
    card.appendChild(el('a', { className: 'orvix-gate-button', href: SIGN_IN_HREF },
      'Go to admin sign-in'));
    return card;
  }

  function showLoading() {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(loadingCard());
  }

  function showAuthed() {
    var gate = $(GATE_ID);
    if (gate && gate.parentNode) gate.parentNode.removeChild(gate);
    var root = $('root');
    if (root) root.style.display = '';
    // Hand off to the webmail client. The client is loaded
    // via <script defer> after this gate, so by the time
    // fetch resolves the global is available.
    if (window.OrvixWebmail && typeof window.OrvixWebmail.init === 'function') {
      window.OrvixWebmail.init();
    } else if (document.readyState === 'loading') {
      // Defer init until DOMContentLoaded if the script
      // order somehow put webmail.js after us.
      document.addEventListener('DOMContentLoaded', function () {
        if (window.OrvixWebmail) window.OrvixWebmail.init();
      });
    }
  }

  function showUnauth() {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(signInCard());
  }

  function showError(status) {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(errorCard(status));
  }

  // Run. Hide the React mount first so the bundle cannot
  // briefly render Compose/Inbox before the auth check
  // resolves.
  hideRoot();
  showLoading();

  if (typeof fetch !== 'function') {
    showError(0);
    return;
  }

  fetch(API_ME, {
    credentials: 'include',
    headers: { 'Accept': 'application/json' }
  }).then(function (resp) {
    if (resp && resp.status === 200) {
      showAuthed();
    } else if (resp && resp.status === 401) {
      showUnauth();
    } else {
      showError(resp ? resp.status : 0);
    }
  }).catch(function () {
    showError(0);
  });
})();
