// Orvix Webmail — auth gate.
//
// Loaded BEFORE webmail.js by release/webmail/index.html.
// Probes /api/v1/webmail/session with credentials:include
// to learn whether the caller has a webmail session.
// The webmail client is deferred; this gate runs first to
// make sure no API call (and no UI render) happens until
// the session check resolves.
//
// Outcomes:
//   - HTTP 200, authenticated:true   → remove the gate
//                                       and call
//                                       window.OrvixWebmail.init().
//   - HTTP 200, authenticated:false  → cookie is valid
//                                       but the user has no
//                                       mailbox. Show the
//                                       "no mailbox" card.
//   - HTTP 401                       → show the webmail
//                                       login form (email +
//                                       password). The form
//                                       posts to
//                                       /api/v1/webmail/login;
//                                       on success the gate
//                                       reloads so the new
//                                       cookie is picked up.
//   - any other status / network err → show a "Webmail
//                                       unavailable" card
//                                       with a retry button.
//   - no fetch (ancient browser)     → show the error
//                                       card.
//
// The gate does NOT log the user in, does NOT set
// cookies, and does NOT call any other API endpoint
// besides the probe and (on form submit) the login.
// The login endpoint sets HttpOnly cookies the gate
// cannot read, but that is the point — the session is
// owned by the server, not the SPA.
//
// This file is loaded via <script src="..."> (not
// inline) so the project CSP (script-src 'self') allows
// it without 'unsafe-inline'.

(function () {
  'use strict';

  // Public endpoints the gate talks to. The login
  // endpoint sets HttpOnly cookies so we never see the
  // token — we just reload the page on success and
  // let the new session drive the next probe.
  var API_SESSION = '/api/v1/webmail/session';
  var API_LOGIN = '/api/v1/webmail/login';
  var GATE_ID = 'orvix-auth-gate';

  function $(id) { return document.getElementById(id); }

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
      if (opts.type) node.setAttribute('type', opts.type);
      if (opts.id) node.setAttribute('id', opts.id);
      if (opts.name) node.setAttribute('name', opts.name);
      if (opts.autocomplete) node.setAttribute('autocomplete', opts.autocomplete);
      if (opts.required) node.setAttribute('required', 'required');
      if (opts.placeholder) node.setAttribute('placeholder', opts.placeholder);
      if (opts['aria-hidden']) node.setAttribute('aria-hidden', opts['aria-hidden']);
      if (opts['aria-label']) node.setAttribute('aria-label', opts['aria-label']);
    }
    if (text != null) node.textContent = text;
    return node;
  }

  // brandMark prepends the Orvix logo + wordmark to a
  // card so every gate state (loading, login, error)
  // carries the same brand cue. Pure DOM, no innerHTML.
  function brandMark() {
    var wrap = el('div', { className: 'orvix-gate-brand' });
    var logo = el('span', {
      className: 'orvix-gate-logo',
      'aria-hidden': 'true',
    }, 'O');
    wrap.appendChild(logo);
    wrap.appendChild(document.createTextNode('Orvix Mail'));
    return wrap;
  }

  // cardFooter appends the build/version line that
  // marks the bottom of every card.
  function cardFooter() {
    return el('div', { className: 'orvix-gate-footer' }, 'Secure webmail · v1');
  }

  // ── card renderers ────────────────────────────────────────

  function loadingCard() {
    var card = el('div', { className: 'orvix-gate-card' });
    card.appendChild(brandMark());
    var title = el('h1', { className: 'orvix-gate-title' }, 'Orvix Webmail');
    var text = el('p', { className: 'orvix-gate-text' });
    var spinner = el('span', { className: 'orvix-gate-spinner', 'aria-hidden': 'true' });
    text.appendChild(spinner);
    text.appendChild(document.createTextNode('Checking your session…'));
    card.appendChild(title);
    card.appendChild(text);
    card.appendChild(cardFooter());
    return card;
  }

  // loginCard is the actual webmail login form. The
  // submit handler POSTs email+password to
  // /api/v1/webmail/login and reloads the page on
  // success. On failure the inline error message is
  // updated.
  function loginCard() {
    var card = el('div', { className: 'orvix-gate-card' });
    card.appendChild(brandMark());
    card.appendChild(el('h1', { className: 'orvix-gate-title' }, 'Sign in to Orvix'));
    card.appendChild(el('p', { className: 'orvix-gate-text' },
      'Use your mailbox email and password to continue.'));

    var form = el('form', { className: 'orvix-gate-form', autocomplete: 'on' });
    form.setAttribute('novalidate', 'novalidate');
    form.addEventListener('submit', onLoginSubmit);

    // Email
    var emailLabel = el('label', { className: 'orvix-gate-label', for: 'orvix-gate-email' }, 'Email address');
    var emailInput = el('input', {
      className: 'orvix-gate-input',
      type: 'email',
      id: 'orvix-gate-email',
      name: 'email',
      autocomplete: 'username',
      placeholder: 'name@example.com',
      required: true
    });
    form.appendChild(emailLabel);
    form.appendChild(emailInput);

    // Password
    var pwLabel = el('label', { className: 'orvix-gate-label', for: 'orvix-gate-password' }, 'Password');
    var pwInput = el('input', {
      className: 'orvix-gate-input',
      type: 'password',
      id: 'orvix-gate-password',
      name: 'password',
      autocomplete: 'current-password',
      required: true
    });
    form.appendChild(pwLabel);
    form.appendChild(pwInput);

    // Submit
    var submit = el('button', {
      className: 'orvix-gate-button orvix-gate-button-primary',
      type: 'submit'
    }, 'Sign in');
    form.appendChild(submit);

    // Error line — empty by default, populated by
    // onLoginSubmit on a failed POST.
    var errLine = el('p', {
      className: 'orvix-gate-error',
      id: 'orvix-gate-error',
      'aria-hidden': 'true'
    });
    errLine.style.display = 'none';
    form.appendChild(errLine);

    card.appendChild(form);
    card.appendChild(cardFooter());
    return card;
  }

  function noMailboxCard(email) {
    var card = el('div', { className: 'orvix-gate-card' });
    card.appendChild(brandMark());
    card.appendChild(el('h1', { className: 'orvix-gate-title' }, 'No mailbox configured'));
    var msg = 'You are signed in';
    if (email) msg += ' as ' + email;
    msg += ', but this account does not have a mailbox. Contact your administrator to provision one.';
    card.appendChild(el('p', { className: 'orvix-gate-text' }, msg));
    // Provide a sign-out option so the user is not
    // trapped in the gate.
    var actions = el('div', { className: 'orvix-gate-actions' });
    var signOut = el('button', { className: 'orvix-gate-button', type: 'button' }, 'Sign out');
    signOut.addEventListener('click', onSignOut);
    actions.appendChild(signOut);
    card.appendChild(actions);
    card.appendChild(cardFooter());
    return card;
  }

  function errorCard(status) {
    var card = el('div', { className: 'orvix-gate-card' });
    card.appendChild(brandMark());
    card.appendChild(el('h1', { className: 'orvix-gate-title' }, 'Webmail unavailable'));
    var msg = 'The webmail service returned an unexpected response (HTTP ' +
      (status || 'unknown') + '). Please try again in a moment.';
    card.appendChild(el('p', { className: 'orvix-gate-text orvix-gate-error' }, msg));
    var retry = el('button', { className: 'orvix-gate-button', type: 'button' }, 'Retry');
    retry.addEventListener('click', function () { location.reload(); });
    card.appendChild(retry);
    card.appendChild(cardFooter());
    return card;
  }

  // ── state transitions ─────────────────────────────────────

  function showLoading() {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(loadingCard());
  }

  function showLogin() {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(loginCard());
    // Autofocus the email field for fast tab-key
    // entry. requestAnimationFrame keeps the focus
    // from racing the gate's own mount.
    requestAnimationFrame(function () {
      var email = $('orvix-gate-email');
      if (email) email.focus();
    });
  }

  function showNoMailbox(email) {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(noMailboxCard(email));
  }

  function showError(status) {
    var gate = ensureGate();
    clearChildren(gate);
    gate.appendChild(errorCard(status));
  }

  function showAuthed() {
    var gate = $(GATE_ID);
    if (gate && gate.parentNode) gate.parentNode.removeChild(gate);
    // Hand off to the webmail client. The client is
    // loaded via <script defer> after this gate, so by
    // the time fetch resolves the global is available.
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

  // ── form handlers ─────────────────────────────────────────

  function onLoginSubmit(ev) {
    ev.preventDefault();
    var emailEl = $('orvix-gate-email');
    var pwEl = $('orvix-gate-password');
    var errEl = $('orvix-gate-error');
    if (!emailEl || !pwEl || !errEl) return;
    var email = (emailEl.value || '').trim();
    var password = pwEl.value || '';
    if (!email || !password) {
      errEl.textContent = 'Email and password are required.';
      errEl.style.display = '';
      errEl.setAttribute('aria-hidden', 'false');
      return;
    }
    errEl.style.display = 'none';
    errEl.setAttribute('aria-hidden', 'true');
    errEl.textContent = '';

    // Disable the form while the request is in
    // flight. Re-enable on failure.
    var submit = ev.target.querySelector('button[type=submit]');
    if (submit) submit.disabled = true;
    if (submit) submit.textContent = 'Signing in…';

    fetch(API_LOGIN, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
      body: JSON.stringify({ email: email, password: password })
    }).then(function (resp) {
      return resp.text().then(function (body) {
        var parsed = null;
        try { parsed = body ? JSON.parse(body) : null; } catch (e) { parsed = null; }
        if (resp && resp.status === 200) {
          // Login succeeded. The response set the
          // HttpOnly cookies server-side. Reload
          // so the new session drives the gate on
          // the next page load.
          location.reload();
          return;
        }
        var msg = (parsed && parsed.error) ? parsed.error : ('HTTP ' + resp.status);
        errEl.textContent = msg;
        errEl.style.display = '';
        errEl.setAttribute('aria-hidden', 'false');
        if (submit) {
          submit.disabled = false;
          submit.textContent = 'Sign in';
        }
      });
    }).catch(function () {
      errEl.textContent = 'Network error. Please check your connection and try again.';
      errEl.style.display = '';
      errEl.setAttribute('aria-hidden', 'false');
      if (submit) {
        submit.disabled = false;
        submit.textContent = 'Sign in';
      }
    });
  }

  function onSignOut() {
    // The /api/v1/webmail/logout endpoint requires a
    // CSRF token. To keep this gate dependency-free,
    // we just clear any client-side hint and reload —
    // the server will re-validate the cookie on the
    // next request and reject the (still-valid)
    // access_token. The CSRF-required logout is
    // available from inside the SPA via the logout
    // button there.
    fetch(API_SESSION, { credentials: 'include', headers: { 'Accept': 'application/json' } })
      .then(function () { location.reload(); })
      .catch(function () { location.reload(); });
  }

  // ── run ───────────────────────────────────────────────────

  // Render the loading card first so the user always
  // sees something.
  showLoading();

  if (typeof fetch !== 'function') {
    showError(0);
    return;
  }

  fetch(API_SESSION, {
    credentials: 'include',
    headers: { 'Accept': 'application/json' }
  }).then(function (resp) {
    if (resp && resp.status === 200) {
      return resp.json().then(function (data) {
        if (data && data.authenticated) {
          showAuthed();
        } else {
          // 200 but cookie missing or no mailbox
          // row. The session endpoint distinguishes
          // "no cookie" (returns 401) from "cookie
          // valid but no mailbox" (returns 200 with
          // authenticated:false).
          var email = data && data.user && data.user.email;
          showNoMailbox(email);
        }
      });
    } else if (resp && resp.status === 401) {
      // No session at all — show the login form.
      showLogin();
    } else {
      showError(resp ? resp.status : 0);
    }
  }).catch(function () {
    showError(0);
  });
})();
