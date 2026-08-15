/* =====================================================================
   Orvix Webmail — Web Push (RFC 8030) front-end.
   Vanilla JS, no external dependencies. Loaded after webmail.js by
   release/webmail/index.html.

   Responsibilities:
     1. Register the service worker at /webmail/sw.js (scope = /webmail/).
        The SW works on both webmail.<domain> and admin.<domain>/webmail.
     2. Render a "Settings → Notifications" panel that lets the user:
        - See whether push is enabled server-side.
        - See the VAPID public key fingerprint + active subscription count.
        - Click "Enable notifications" to prompt the browser for
          permission and create a PushSubscription server-side.
        - Click "Disable notifications" to revoke any active subscription
          for this device.
        - Click "Send test notification" to fire a test push to the
          active subscription.
     3. Re-register when the browser fires `pushsubscriptionchange`
        (Chrome auto-rotates the subscription periodically).
     4. Surface permission + subscription state in a small banner
        under the topbar so the user always knows what is happening.

   No token storage in localStorage. The PushManager's subscription
   object is the only place a PushSubscription lives on the client.
   ===================================================================== */

(function () {
  'use strict';

  var SW_URL = '/webmail/sw.js';
  var SW_SCOPE = '/webmail/';
  var VAPID_PUBLIC_KEY = null;
  var ACTIVE_SUBSCRIPTION = null;
  var LAST_PERMISSION = 'default';
  var INIT_DONE = false;

  // ────────────────────────────────────────────────────────────
  // Tiny DOM helpers. We deliberately do NOT import the parent
  // webmail.js's `el()` because the push module is loaded into
  // a different <script> tag and lives in its own IIFE. Keeping
  // its helpers local also avoids accidental coupling to the
  // parent's internal state.
  // ────────────────────────────────────────────────────────────

  function $(id) { return document.getElementById(id); }

  function el(tag, attrs, text) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        if (attrs[k] === undefined || attrs[k] === null) return;
        if (k === 'class') node.className = attrs[k];
        else if (k === 'text') node.textContent = attrs[k];
        else if (k === 'html') node.innerHTML = attrs[k];
        else node.setAttribute(k, attrs[k]);
      });
    }
    if (text != null) node.textContent = text;
    return node;
  }

  // dirAuto mirrors webmail.js's dirAuto but only handles the
  // unicode direction ranges we actually render in the panel
  // (Arabic / Hebrew). Settings text is mostly English, but
  // operators who localise the panel keep RTL correctness.
  function dirAuto(s) {
    if (!s) return 'auto';
    for (var i = 0; i < s.length; i++) {
      var c = s.charCodeAt(i);
      if (c >= 0x0590 && c <= 0x05ff) return 'rtl'; // Hebrew
      if (c >= 0x0600 && c <= 0x06ff) return 'rtl'; // Arabic
      if (c >= 0x0750 && c <= 0x077f) return 'rtl';
      if (c >= 0x08a0 && c <= 0x08ff) return 'rtl';
      if (c >= 0xfb1d && c <= 0xfb4f) return 'rtl';
      if (c >= 0xfb50 && c <= 0xfdff) return 'rtl';
      if (c >= 0xfe70 && c <= 0xfeff) return 'rtl';
      if (c === 0x20 || c === 0x09 || c === 0x0a || c === 0x0d) continue;
      if (
        (c >= 0x21 && c <= 0x2f) || (c >= 0x3a && c <= 0x40) ||
        (c >= 0x5b && c <= 0x60) || (c >= 0x7b && c <= 0x7e)
      ) continue;
      return 'ltr';
    }
    return 'auto';
  }

  // ────────────────────────────────────────────────────────────
  // Status / API helpers. These talk to the webmail API with
  // credentials:'include' so the HttpOnly cookies drive auth.
  // ────────────────────────────────────────────────────────────

  function statusAPI() {
    return fetch('/api/v1/webmail/push/status', {
      method: 'GET',
      credentials: 'include',
      headers: { Accept: 'application/json' },
    }).then(function (res) {
      return res.text().then(function (body) {
        var data = null;
        try { data = body ? JSON.parse(body) : null; } catch (e) { data = null; }
        if (!res.ok) {
          var err = new Error((data && data.error) || ('HTTP ' + res.status));
          err.status = res.status;
          err.body = data;
          throw err;
        }
        return data;
      });
    });
  }

  function subscribeAPI(subJSON) {
    return fetch('/api/v1/webmail/push/subscribe', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(subJSON),
    }).then(parseJSON);
  }

  function unsubscribeAPI(payload) {
    return fetch('/api/v1/webmail/push/unsubscribe', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(payload || {}),
    }).then(parseJSON);
  }

  function testAPI(endpoint) {
    return fetch('/api/v1/webmail/push/test', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ endpoint: endpoint }),
    }).then(parseJSON);
  }

  function parseJSON(res) {
    return res.text().then(function (body) {
      var data = null;
      try { data = body ? JSON.parse(body) : null; } catch (e) { data = null; }
      if (!res.ok) {
        var err = new Error((data && data.error) || ('HTTP ' + res.status));
        err.status = res.status;
        err.body = data;
        throw err;
      }
      return data;
    });
  }

  // ────────────────────────────────────────────────────────────
  // Service worker registration + lifecycle.
  // ────────────────────────────────────────────────────────────

  function isSupported() {
    return (
      typeof navigator !== 'undefined' &&
      'serviceWorker' in navigator &&
      'PushManager' in window &&
      'Notification' in window
    );
  }

  function registerServiceWorker() {
    if (!isSupported()) return Promise.resolve(null);
    return navigator.serviceWorker.register(SW_URL, { scope: SW_SCOPE })
      .then(function (reg) {
        // Chrome rotates the subscription periodically. The browser
        // fires `pushsubscriptionchange` on the SW when that happens
        // and the SW relays it to clients via postMessage. We listen
        // here so the user's webmail session re-subscribes with the
        // new endpoint transparently.
        if (navigator.serviceWorker.addEventListener) {
          navigator.serviceWorker.addEventListener('message', function (ev) {
            if (!ev || !ev.data) return;
            if (ev.data.type === 'pushsubscriptionchange') {
              resubscribeFromCurrentState().catch(function () {
                /* silent — UI will show the disabled state */
              });
            }
          });
        }
        return reg;
      })
      .catch(function (err) {
        // Service worker registration can fail in dev (mixed content,
        // CSP, no TLS). Don't block the SPA — just leave push disabled.
        // The Status panel surfaces this as "Push unsupported".
        console.warn('service worker registration failed', err);
        return null;
      });
  }

  function currentSubscription() {
    if (!isSupported()) return Promise.resolve(null);
    return navigator.serviceWorker.ready.then(function (reg) {
      return reg.pushManager.getSubscription();
    });
  }

  function urlBase64ToUint8Array(base64String) {
    var padding = '='.repeat((4 - base64String.length % 4) % 4);
    var base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    var raw = atob(base64);
    var out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }

  function resubscribeFromCurrentState() {
    if (!VAPID_PUBLIC_KEY) return Promise.resolve(null);
    return subscribe(VAPID_PUBLIC_KEY, /*silent*/ true);
  }

  function subscribe(vapidKey, silent) {
    if (!isSupported()) return Promise.reject(new Error('unsupported'));
    return Notification.requestPermission().then(function (perm) {
      LAST_PERMISSION = perm;
      if (perm !== 'granted') {
        if (!silent) emitToast('Permission ' + perm, 'warn');
        renderBanner();
        return null;
      }
      return navigator.serviceWorker.ready.then(function (reg) {
        return reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(vapidKey),
        });
      }).then(function (sub) {
        ACTIVE_SUBSCRIPTION = sub;
        var json = sub.toJSON();
        return subscribeAPI({
          endpoint: json.endpoint,
          keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
        }).then(function () {
          if (!silent) emitToast('Notifications enabled', 'success');
          renderBanner();
          return sub;
        });
      });
    });
  }

  function unsubscribe(silent) {
    if (!ACTIVE_SUBSCRIPTION) return Promise.resolve();
    var endpoint = ACTIVE_SUBSCRIPTION.endpoint;
    return unsubscribeAPI({ endpoint: endpoint }).then(function () {
      return ACTIVE_SUBSCRIPTION.unsubscribe().then(function () {
        ACTIVE_SUBSCRIPTION = null;
        if (!silent) emitToast('Notifications disabled', 'success');
        renderBanner();
      });
    }).catch(function (err) {
      // Best effort: if the server-side disable fails, still
      // unsubscribe locally so the user is not stuck.
      ACTIVE_SUBSCRIPTION = null;
      if (!silent) emitToast('Disable failed: ' + err.message, 'error');
      renderBanner();
    });
  }

  function sendTest() {
    if (!ACTIVE_SUBSCRIPTION) {
      emitToast('Enable notifications first', 'warn');
      return Promise.resolve();
    }
    return testAPI(ACTIVE_SUBSCRIPTION.endpoint)
      .then(function () {
        emitToast('Test notification sent', 'success');
      })
      .catch(function (err) {
        emitToast('Test failed: ' + err.message, 'error');
      });
  }

  // ────────────────────────────────────────────────────────────
  // Toast helper. The parent webmail exposes window.toast but
  // we re-emit a custom event so the parent's toast container
  // picks it up. If the parent toast container is not available
  // we fall back to the browser console (dev mode).
  // ────────────────────────────────────────────────────────────

  function emitToast(message, type) {
    var evt;
    try {
      evt = new CustomEvent('orvix:toast', {
        detail: { message: message, type: type || 'info' },
      });
    } catch (e) {
      console.log('[orvix-push]', type || 'info', message);
      return;
    }
    document.dispatchEvent(evt);
  }

  // ────────────────────────────────────────────────────────────
  // Banner — a small persistent indicator below the topbar that
  // mirrors push status. Hidden when push is fully active or
  // when the browser does not support Push at all.
  // ────────────────────────────────────────────────────────────

  function renderBanner() {
    var banner = $('orvix-push-banner');
    if (!banner) return;
    if (!isSupported()) {
      banner.hidden = true;
      return;
    }
    if (LAST_PERMISSION === 'denied') {
      banner.hidden = false;
      banner.className = 'orvix-push-banner orvix-push-banner-warn';
      banner.textContent = 'Notifications are blocked in this browser. Enable them in site settings to receive mail alerts.';
      return;
    }
    if (!ACTIVE_SUBSCRIPTION) {
      banner.hidden = false;
      banner.className = 'orvix-push-banner';
      banner.textContent = 'Notifications are not enabled on this device.';
      return;
    }
    banner.hidden = true;
  }

  // ────────────────────────────────────────────────────────────
  // Settings panel. Rendered as a modal overlay on top of the
  // shell so the user can open it from anywhere. The modal is
  // pure DOM — no innerHTML — and uses dirAuto() on every text
  // element so Arabic / Hebrew strings render correctly.
  // ────────────────────────────────────────────────────────────

  function openSettings() {
    closeSettings();
    var backdrop = el('div', { class: 'modal-backdrop', id: 'orvix-push-modal' });
    var card = el('div', {
      class: 'modal-card',
      role: 'dialog',
      'aria-modal': 'true',
      'aria-label': 'Notification settings',
    });
    card.setAttribute('dir', dirAuto(document.documentElement.lang || 'en'));

    var header = el('div', { class: 'modal-header' });
    header.appendChild(el('h2', { class: 'modal-title', dir: 'auto' }, 'Notifications'));
    var closeBtn = el('button', {
      class: 'icon-btn',
      type: 'button',
      'aria-label': 'Close',
      title: 'Close',
    }, '×');
    closeBtn.addEventListener('click', closeSettings);
    header.appendChild(closeBtn);
    card.appendChild(header);

    var body = el('div', { class: 'modal-body push-settings-body', id: 'orvix-push-body' });
    body.appendChild(buildStatusBlock());
    card.appendChild(body);

    backdrop.appendChild(card);
    backdrop.addEventListener('click', function (ev) {
      if (ev.target === backdrop) closeSettings();
    });
    document.body.appendChild(backdrop);

    // Refresh live data so the panel reflects the current server
    // state. We re-render the status block when the API responds.
    statusAPI().then(function (data) {
      VAPID_PUBLIC_KEY = data && data.vapid_public_key ? data.vapid_public_key : null;
      var fresh = buildStatusBlock();
      body.replaceChild(fresh, body.firstChild);
    }).catch(function (err) {
      var note = el('p', { class: 'push-settings-note push-settings-error', dir: 'auto' },
        'Could not load push status: ' + (err && err.message ? err.message : 'unknown error'));
      body.appendChild(note);
    });
  }

  function closeSettings() {
    var existing = $('orvix-push-modal');
    if (existing && existing.parentNode) existing.parentNode.removeChild(existing);
  }

  function buildStatusBlock() {
    var wrap = el('div', { class: 'push-settings' });

    wrap.appendChild(el('p', { class: 'push-settings-lede', dir: 'auto' },
      'Receive a browser notification when a new message lands in your INBOX. ' +
      'The server only ever sees an opaque push endpoint; the message subject ' +
      'and sender are encrypted end-to-end per Web Push (RFC 8291) before they ' +
      'leave Orvix.'));

    if (!isSupported()) {
      wrap.appendChild(el('p', { class: 'push-settings-note push-settings-warn', dir: 'auto' },
        'This browser does not support Web Push. Try a recent version of Chrome, ' +
        'Firefox, Edge, or Safari 16.4+.'));
      return wrap;
    }

    // Permission state line.
    var permLine = el('p', { class: 'push-settings-row', dir: 'auto' });
    permLine.appendChild(el('strong', { dir: 'auto' }, 'Browser permission: '));
    permLine.appendChild(document.createTextNode(LAST_PERMISSION || Notification.permission || 'default'));
    wrap.appendChild(permLine);

    // Active subscriptions count — populated by the API refresh.
    wrap.appendChild(el('p', { class: 'push-settings-row push-settings-row-count', dir: 'auto', id: 'orvix-push-count' },
      'Active subscriptions on this mailbox: …'));

    // Action row.
    var actions = el('div', { class: 'push-settings-actions' });

    var enableBtn = el('button', {
      class: 'btn primary',
      type: 'button',
      id: 'orvix-push-enable',
    }, 'Enable notifications');
    enableBtn.addEventListener('click', function () {
      if (!VAPID_PUBLIC_KEY) {
        emitToast('Push is disabled by the server operator', 'warn');
        return;
      }
      subscribe(VAPID_PUBLIC_KEY, false).then(function () {
        closeSettings();
      });
    });
    actions.appendChild(enableBtn);

    var disableBtn = el('button', {
      class: 'btn',
      type: 'button',
      id: 'orvix-push-disable',
    }, 'Disable notifications');
    disableBtn.addEventListener('click', function () {
      unsubscribe(false).then(function () {
        closeSettings();
      });
    });
    actions.appendChild(disableBtn);

    var testBtn = el('button', {
      class: 'btn',
      type: 'button',
      id: 'orvix-push-test',
    }, 'Send test notification');
    testBtn.addEventListener('click', sendTest);
    actions.appendChild(testBtn);

    wrap.appendChild(actions);

    // Note: VAPID public key is exposed (it's not a secret — it
    // identifies the application server) so the user can confirm
    // the value matches what their operator documented.
    var note = el('p', { class: 'push-settings-note', dir: 'auto', id: 'orvix-push-vapid' },
      'VAPID public key: (loading…)');
    wrap.appendChild(note);

    return wrap;
  }

  // Render the subscription count + VAPID key after statusAPI
  // returns. Called by openSettings() once the API resolves.
  function refreshStatusInline(data) {
    var countEl = $('orvix-push-count');
    if (countEl) {
      var n = (data && typeof data.active_count === 'number') ? data.active_count : 0;
      var subs = (data && data.subscriptions) || [];
      var label = n === 1 ? '1 active subscription' : (n + ' active subscriptions');
      countEl.textContent = label;
      // Show per-subscription fingerprints when there are any.
      if (subs.length > 0) {
        var list = el('ul', { class: 'push-settings-list' });
        subs.forEach(function (s) {
          var li = el('li', { dir: 'auto' });
          li.appendChild(el('strong', { dir: 'auto' }, s.endpoint_kind || 'other'));
          li.appendChild(document.createTextNode(' — ' + (s.endpoint_fingerprint || '')));
          list.appendChild(li);
        });
        countEl.parentNode.insertBefore(list, countEl.nextSibling);
      }
    }
    var vapidEl = $('orvix-push-vapid');
    if (vapidEl) {
      var pk = data && data.vapid_public_key ? data.vapid_public_key : '(disabled)';
      vapidEl.textContent = 'VAPID public key: ' + pk;
    }
  }

  // ────────────────────────────────────────────────────────────
  // init() — called by webmail.js init() (via the onInit hook)
  // once the auth gate has confirmed the session.
  // ────────────────────────────────────────────────────────────

  function init() {
    if (INIT_DONE) return;
    INIT_DONE = true;

    if (!isSupported()) return;

    LAST_PERMISSION = (typeof Notification !== 'undefined' && Notification.permission) || 'default';

    // 1. Banner.
    if (!$('orvix-push-banner')) {
      var banner = el('div', {
        id: 'orvix-push-banner',
        class: 'orvix-push-banner',
        hidden: true,
        role: 'status',
        'aria-live': 'polite',
      });
      // Insert immediately after the topbar.
      var topbar = document.querySelector('.topbar');
      if (topbar && topbar.parentNode) {
        topbar.parentNode.insertBefore(banner, topbar.nextSibling);
      } else {
        document.body.appendChild(banner);
      }
    }

    // 2. Settings menu entry — append to the sidebar footer so it
    //    is reachable from every folder / search view.
    var sidebarFooter = document.querySelector('.sidebar .footer');
    if (sidebarFooter && !$('orvix-push-settings-link')) {
      var settingsBtn = el('button', {
        class: 'sidebar-link',
        type: 'button',
        id: 'orvix-push-settings-link',
        dir: 'auto',
      }, '⚙  Notification settings');
      settingsBtn.addEventListener('click', openSettings);
      sidebarFooter.parentNode.insertBefore(settingsBtn, sidebarFooter);
    }

    // 3. Register SW + load server-side status + active subscription.
    registerServiceWorker()
      .then(function () { return currentSubscription(); })
      .then(function (sub) {
        ACTIVE_SUBSCRIPTION = sub;
        renderBanner();
      })
      .catch(function () { /* leave banner / state as default */ });

    statusAPI()
      .then(function (data) {
        VAPID_PUBLIC_KEY = data && data.vapid_public_key ? data.vapid_public_key : null;
        refreshStatusInline(data);
      })
      .catch(function (err) {
        // Server is missing VAPID or push is disabled — that's fine.
        VAPID_PUBLIC_KEY = null;
        renderBanner();
      });
  }

  // Public exports. The parent webmail.js calls onInit() once the
  // auth gate has confirmed the session; openSettings() is invoked
  // when the user clicks the sidebar link.
  window.OrvixWebmailPush = {
    init: init,
    onInit: init,
    openSettings: openSettings,
    closeSettings: closeSettings,
  };

  // webmail.js can finish auth-gated boot before this deferred module
  // loads. If the SPA shell already exists, attach the push UI now;
  // otherwise the parent onInit hook above will call init() later.
  if (document.querySelector('.sidebar .footer')) {
    try { init(); } catch (e) { /* push must never block webmail */ }
  }
})();
