const state = {
  token: sessionStorage.getItem("orvix_access_token") || "",
  profile: null,
  health: null,
  domains: [],
  users: [],
  queue: [],
  logs: []
};

const el = (id) => document.getElementById(id);
const loginView = el("login-view");
const appView = el("app-view");
const loginForm = el("login-form");
const loginButton = el("login-button");
const loginMessage = el("login-message");
const globalAlert = el("global-alert");

function setText(id, value) {
  const node = el(id);
  if (node) node.textContent = value;
}

function showAlert(message) {
  globalAlert.textContent = message;
  globalAlert.classList.toggle("hidden", !message);
}

function authHeaders() {
  return state.token ? { "Authorization": `Bearer ${state.token}` } : {};
}

async function apiGet(path, fallback) {
  const res = await fetch(path, { headers: authHeaders() });
  if (res.status === 401) {
    signOut();
    throw new Error("Session expired. Sign in again.");
  }
  if (!res.ok) {
    if (fallback !== undefined) return fallback;
    throw new Error(`${path} returned ${res.status}`);
  }
  return await res.json();
}

async function getCSRFToken() {
  const res = await fetch("/api/v1/csrf-token", { headers: authHeaders() });
  if (!res.ok) throw new Error("Failed to get CSRF token");
  const data = await res.json();
  return data.csrf_token;
}

async function loadProfile() {
  state.profile = await apiGet("/api/v1/me");
  setText("admin-email", state.profile.email || "Signed in");
  setText("admin-role", state.profile.role || "admin");
}

async function loadHealth() {
  try {
    state.health = await fetch("/api/v1/health").then((res) => res.ok ? res.json() : Promise.reject(new Error("health failed")));
    setText("api-health", "OK");
    setText("api-health-note", `${state.health.version || "Orvix Enterprise Mail"} checked ${new Date().toLocaleTimeString()}`);
    setText("coremail-runtime", "Online");
    setText("smtp-status", "Online");
    setText("imap-status", "Online");
    setText("pop3-status", "Online");
    setText("overall-status", "System healthy");
    el("overall-dot").className = "dot good";
  } catch (err) {
    setText("api-health", "Error");
    setText("api-health-note", "Health endpoint is unavailable.");
    setText("coremail-runtime", "Unknown");
    setText("smtp-status", "Unknown");
    setText("imap-status", "Unknown");
    setText("pop3-status", "Unknown");
    setText("overall-status", "Needs attention");
    el("overall-dot").className = "dot bad";
  }
}

async function loadSummary() {
  var data;
  try {
    data = await apiGet("/api/v1/admin/summary", null);
  } catch (_) { return; }
  if (!data || !data.domains) return;
  el("summary-domains").innerHTML = '<div class="setting-row"><div><strong>Total</strong><span>' + (data.domains.total || 0) + '</span></div></div><div class="setting-row"><div><strong>Active</strong><span>' + (data.domains.active || 0) + '</span></div></div><div class="setting-row"><div><strong>Suspended</strong><span>' + (data.domains.suspended || 0) + '</span></div></div>';
  el("summary-mailboxes").innerHTML = '<div class="setting-row"><div><strong>Total</strong><span>' + (data.mailboxes.total || 0) + '</span></div></div><div class="setting-row"><div><strong>Active</strong><span>' + (data.mailboxes.active || 0) + '</span></div></div><div class="setting-row"><div><strong>Suspended</strong><span>' + (data.mailboxes.suspended || 0) + '</span></div></div><div class="setting-row"><div><strong>Admin</strong><span>' + (data.mailboxes.admin || 0) + '</span></div></div>';
  el("summary-queue").innerHTML = '<div class="setting-row"><div><strong>Total</strong><span>' + (data.queue.total || 0) + '</span></div></div><div class="setting-row"><div><strong>Pending</strong><span>' + (data.queue.pending || 0) + '</span></div></div><div class="setting-row"><div><strong>Deferred</strong><span>' + (data.queue.deferred || 0) + '</span></div></div><div class="setting-row"><div><strong>Failed</strong><span>' + (data.queue.failed || 0) + '</span></div></div>';
  el("summary-audit").innerHTML = '<div class="setting-row"><div><strong>Recent (24h)</strong><span>' + (data.audit.recent || 0) + '</span></div></div>';
  el("summary-runtime").innerHTML = '<div class="setting-row"><div><strong>Status</strong><span>' + escapeHTML(data.runtime.status || "unknown") + '</span></div></div><div class="setting-row"><div><strong>Version</strong><span>' + escapeHTML(data.version || "-") + '</span></div></div>';
}

async function loadDomains() {
  const domains = await apiGet("/api/v1/domains", []);
  state.domains = domains;
  const node = el("domains-table");
  if (!node) return;
  if (!Array.isArray(domains) || domains.length === 0) {
    node.innerHTML = '<div class="empty-state">No domains have been provisioned yet.</div>';
    return;
  }
  const rows = domains.map(function(d) {
    const isActive = d.status === "active";
    const statusBtn = isActive
      ? '<button class="ghost-btn dm-action" data-action="disable" data-domain="' + escapeHTML(d.domain) + '" data-domain-id="' + d.id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">Disable</button>'
      : '<button class="ghost-btn dm-action" data-action="enable" data-domain="' + escapeHTML(d.domain) + '" data-domain-id="' + d.id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">Enable</button>';
    const deleteBtn = '<button class="ghost-btn dm-action" data-action="delete" data-domain="' + escapeHTML(d.domain) + '" data-domain-id="' + d.id + '" data-mailbox-count="' + (d.mailbox_count || 0) + '" style="font-size:12px;padding:4px 8px;min-height:auto;">Delete</button>';
    const viewBtn = '<button class="ghost-btn dv-action" data-action="view" data-domain="' + escapeHTML(d.domain) + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">View</button>';
    return '<tr>' +
      '<td>' + escapeHTML(d.domain || "-") + '</td>' +
      '<td>' + escapeHTML(d.plan || "-") + '</td>' +
      '<td>' + escapeHTML(d.status || "active") + '</td>' +
      '<td>' + (d.mailbox_count != null ? d.mailbox_count : "-") + '</td>' +
      '<td>' + viewBtn + statusBtn + deleteBtn + '</td></tr>';
  }).join("");
  node.innerHTML = '<table class="table"><thead><tr><th>Domain</th><th>Plan</th><th>Status</th><th>Mailboxes</th><th>Actions</th></tr></thead><tbody>' + rows + '</tbody></table>';
}

async function loadMailboxes() {
  const users = await apiGet("/api/v1/users", []);
  state.users = users;
  const node = el("mailboxes-table");
  if (!node) return;
  if (!Array.isArray(users) || users.length === 0) {
    node.innerHTML = '<div class="empty-state">No mailboxes or users are available yet.</div>';
    return;
  }
  const rows = users.map(function(u) {
    const hasMailbox = u.mailbox_id != null;
    const isAdmin = u.is_admin === true;
    const isSuspended = u.status === "suspended";
    const canModify = hasMailbox && !isAdmin;
    var actionsHtml;
    if (!hasMailbox) {
      actionsHtml = '<span style="color:var(--muted);font-size:12px;">No mailbox record</span>';
    } else {
      actionsHtml = '' +
        '<button class="ghost-btn mv-action" data-action="view" data-mailbox-id="' + u.mailbox_id + '" data-email="' + escapeHTML(u.email) + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">View</button>' +
        '<button class="ghost-btn mb-action" data-action="password" data-email="' + escapeHTML(u.email) + '" data-mailbox-id="' + u.mailbox_id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;"' + (canModify ? '' : ' disabled') + '>Password</button>' +
        '<button class="ghost-btn mb-action" data-action="' + (isSuspended ? 'enable' : 'disable') + '" data-email="' + escapeHTML(u.email) + '" data-mailbox-id="' + u.mailbox_id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;"' + (canModify ? '' : ' disabled') + '>' + (isSuspended ? 'Enable' : 'Disable') + '</button>' +
        '<button class="ghost-btn mb-action" data-action="delete" data-email="' + escapeHTML(u.email) + '" data-mailbox-id="' + u.mailbox_id + '" style="font-size:12px;padding:4px 8px;min-height:auto;"' + (canModify ? '' : ' disabled') + '>Delete</button>' +
        '';
    }
    return '<tr>' +
      '<td>' + escapeHTML(u.email || "-") + '</td>' +
      '<td>' + escapeHTML(u.role || "-") + '</td>' +
      '<td>' + (isAdmin ? "Yes" : "No") + '</td>' +
      '<td>' + escapeHTML(u.status || "active") + '</td>' +
      '<td>' + actionsHtml + '</td></tr>';
  }).join("");
  node.innerHTML = '<table class="table"><thead><tr><th>Email</th><th>Role</th><th>Admin</th><th>Status</th><th>Actions</th></tr></thead><tbody>' + rows + '</tbody></table>';
}

async function loadQueue() {
  state.queue = await apiGet("/api/v1/queue", []);
  setText("queue-status", state.queue.length === 0 ? "Clear" : `${state.queue.length} queued`);
  setText("queue-note", state.queue.length === 0 ? "No queued messages reported." : "Queue entries require attention.");
  renderQueueTable("queue-table", state.queue);
  renderTable("queue-preview", state.queue.slice(0, 5), ["id", "from", "to", "status", "attempts"], "No queued messages.");
}

function renderQueueTable(id, rows) {
  var node = el(id);
  if (!node) return;
  if (!Array.isArray(rows) || rows.length === 0) {
    node.innerHTML = '<div class="empty-state">No queued messages.</div>';
    return;
  }
  var head = '<table class="table"><thead><tr><th>ID</th><th>From</th><th>To</th><th>Status</th><th>Attempts</th><th>Next Attempt</th><th>Created</th><th>Actions</th></tr></thead><tbody>';
  var body = rows.map(function(r) {
    return '<tr>' +
      '<td>' + escapeHTML(String(r.id || "")) + '</td>' +
      '<td>' + escapeHTML(r.from || "-") + '</td>' +
      '<td>' + escapeHTML(r.to || "-") + '</td>' +
      '<td>' + escapeHTML(r.status || "-") + '</td>' +
      '<td>' + (r.attempts != null ? r.attempts : "-") + '</td>' +
      '<td>' + escapeHTML(r.next_attempt_at || "-") + '</td>' +
      '<td>' + escapeHTML(r.created_at || "-") + '</td>' +
      '<td>' +
        '<button class="ghost-btn q-action" data-action="retry" data-queue-id="' + r.id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">Retry</button>' +
        '<button class="ghost-btn q-action" data-action="delete" data-queue-id="' + r.id + '" style="font-size:12px;padding:4px 8px;min-height:auto;">Delete</button>' +
      '</td></tr>';
  }).join("");
  node.innerHTML = head + body + '</tbody></table>';
}

async function loadLogs() {
  state.logs = await apiGet("/api/v1/audit/logs", []);
  renderTable("logs-table", state.logs, ["action", "actor", "target", "result", "timestamp"], "No audit log entries.");
}

function renderTable(id, rows, columns, emptyText) {
  const node = el(id);
  if (!node) return;
  if (!Array.isArray(rows) || rows.length === 0) {
    node.innerHTML = `<div class="empty-state">${escapeHTML(emptyText)}</div>`;
    return;
  }
  const head = columns.map((col) => `<th>${escapeHTML(titleCase(col))}</th>`).join("");
  const body = rows.map((row) => `<tr>${columns.map((col) => `<td>${escapeHTML(valueFor(row, col))}</td>`).join("")}</tr>`).join("");
  node.innerHTML = `<table class="table"><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table>`;
}

function valueFor(row, key) {
  const value = row[key] ?? row[key.replace("_", "")] ?? "";
  if (value === null || value === undefined || value === "") return "-";
  return String(value);
}

function titleCase(value) {
  return value.replace(/_/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    "\"": "&quot;",
    "'": "&#39;"
  }[ch]));
}

async function refreshAll() {
  showAlert("");
  await Promise.allSettled([loadHealth(), loadSummary(), loadDomains(), loadMailboxes(), loadQueue(), loadLogs()]);
}

function showApp() {
  loginView.classList.add("hidden");
  appView.classList.remove("hidden");
}

function showLogin() {
  appView.classList.add("hidden");
  loginView.classList.remove("hidden");
}

function signOut() {
  state.token = "";
  state.profile = null;
  sessionStorage.removeItem("orvix_access_token");
  showLogin();
}

loginForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  loginMessage.textContent = "";
  loginButton.disabled = true;
  try {
    const payload = {
      email: el("email").value.trim(),
      username: el("email").value.trim(),
      password: el("password").value
    };
    const res = await fetch("/api/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
    if (!res.ok) throw new Error("Invalid administrator credentials.");
    const data = await res.json();
    if (!data.access_token) throw new Error("Login response did not include an access token.");
    state.token = data.access_token;
    sessionStorage.setItem("orvix_access_token", state.token);
    await loadProfile();
    showApp();
    await refreshAll();
  } catch (err) {
    loginMessage.textContent = err.message || "Sign in failed.";
  } finally {
    loginButton.disabled = false;
  }
});

el("logout-button").addEventListener("click", signOut);

el("show-add-mailbox").addEventListener("click", () => {
  el("add-mailbox-panel").classList.toggle("hidden");
  el("add-mailbox-message").textContent = "";
  el("add-mailbox-message").className = "message";
});

el("add-mailbox-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const btn = el("add-mailbox-btn");
  const msg = el("add-mailbox-message");
  msg.textContent = "";
  msg.className = "message";
  btn.disabled = true;
  try {
    const csrfToken = await getCSRFToken();
    const email = el("mb-email").value.trim();
    const password = el("mb-password").value;
    const name = el("mb-name").value.trim();
    const res = await fetch("/api/v1/mailboxes", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Authorization": `Bearer ${state.token}`,
        "X-CSRF-Token": csrfToken
      },
      body: JSON.stringify({ email, password, name: name || undefined })
    });
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}));
      throw new Error(errBody.error || `HTTP ${res.status}`);
    }
    msg.textContent = `Mailbox ${email} created successfully.`;
    msg.className = "message success";
    el("mb-email").value = "";
    el("mb-password").value = "";
    el("mb-name").value = "";
    await loadMailboxes();
  } catch (err) {
    msg.textContent = err.message || "Failed to create mailbox.";
    msg.className = "message error";
  } finally {
    btn.disabled = false;
  }
});

el("mailboxes-table").addEventListener("click", async function(event) {
  var btn = event.target.closest("button.mb-action");
  if (!btn) return;
  var action = btn.dataset.action;
  var mailboxId = btn.dataset.mailboxId;
  var email = btn.dataset.email;
  if (!mailboxId) return;
  btn.disabled = true;
  try {
    var csrfToken = await getCSRFToken();
    if (action === "password") {
      var newPassword = prompt("Reset password for " + email + "\n\nEnter new password (min 8 characters):");
      if (!newPassword) return;
      if (newPassword.length < 8) { showAlert("Password must be at least 8 characters."); btn.disabled = false; return; }
      var res = await fetch("/api/v1/mailboxes/" + mailboxId + "/password", {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken },
        body: JSON.stringify({ password: newPassword })
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Password reset for " + email + ".");
    } else if (action === "disable") {
      if (!confirm("Disable mailbox " + email + "? The user will not be able to access their mailbox.")) return;
      var res = await fetch("/api/v1/mailboxes/" + mailboxId + "/status", {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken },
        body: JSON.stringify({ status: "suspended" })
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Mailbox " + email + " disabled.");
    } else if (action === "enable") {
      var res = await fetch("/api/v1/mailboxes/" + mailboxId + "/status", {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken },
        body: JSON.stringify({ status: "active" })
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Mailbox " + email + " enabled.");
    } else if (action === "delete") {
      if (!confirm("Delete mailbox " + email + "? This action cannot be undone.")) return;
      var res = await fetch("/api/v1/mailboxes/" + mailboxId, {
        method: "DELETE",
        headers: { "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken }
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Mailbox " + email + " deleted.");
    }
    await loadMailboxes();
  } catch (err) {
    showAlert(err.message || "Operation failed.");
  } finally {
    btn.disabled = false;
  }
});

el("show-add-domain").addEventListener("click", () => {
  el("add-domain-panel").classList.toggle("hidden");
  el("add-domain-message").textContent = "";
  el("add-domain-message").className = "message";
});

el("add-domain-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const btn = el("add-domain-btn");
  const msg = el("add-domain-message");
  msg.textContent = "";
  msg.className = "message";
  btn.disabled = true;
  try {
    const csrfToken = await getCSRFToken();
    const name = el("dm-name").value.trim();
    const res = await fetch("/api/v1/domains", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Authorization": `Bearer ${state.token}`,
        "X-CSRF-Token": csrfToken
      },
      body: JSON.stringify({ name })
    });
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}));
      throw new Error(errBody.error || `HTTP ${res.status}`);
    }
    msg.textContent = `Domain ${name} created successfully.`;
    msg.className = "message success";
    el("dm-name").value = "";
    await loadDomains();
  } catch (err) {
    msg.textContent = err.message || "Failed to create domain.";
    msg.className = "message error";
  } finally {
    btn.disabled = false;
  }
});

el("domains-table").addEventListener("click", async function(event) {
  var btn = event.target.closest("button.dm-action");
  if (!btn) return;
  var action = btn.dataset.action;
  var domain = btn.dataset.domain;
  var domainId = btn.dataset.domainId;
  btn.disabled = true;
  try {
    var csrfToken = await getCSRFToken();
    if (action === "enable") {
      var res = await fetch("/api/v1/domains/" + encodeURIComponent(domain) + "/status", {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken },
        body: JSON.stringify({ status: "active" })
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Domain " + domain + " enabled.");
    } else if (action === "disable") {
      if (!confirm("Disable domain " + domain + "? New mailbox creation will be blocked.")) return;
      var res = await fetch("/api/v1/domains/" + encodeURIComponent(domain) + "/status", {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken },
        body: JSON.stringify({ status: "suspended" })
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Domain " + domain + " disabled.");
    } else if (action === "delete") {
      var mbCount = parseInt(btn.dataset.mailboxCount || "0", 10);
      if (mbCount > 0) {
        showAlert("Cannot delete domain " + domain + ": it contains " + mbCount + " mailbox(es). Remove all mailboxes first.");
        btn.disabled = false;
        return;
      }
      if (!confirm("Delete domain " + domain + "? This action cannot be undone.")) return;
      var res = await fetch("/api/v1/domains/" + encodeURIComponent(domain), {
        method: "DELETE",
        headers: { "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken }
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Domain " + domain + " deleted.");
    }
    await loadDomains();
  } catch (err) {
    showAlert(err.message || "Operation failed.");
  } finally {
    btn.disabled = false;
  }
});

// Queue actions
el("queue-table").addEventListener("click", async function(event) {
  var btn = event.target.closest("button.q-action");
  if (!btn) return;
  var action = btn.dataset.action;
  var queueId = btn.dataset.queueId;
  if (!queueId) return;
  btn.disabled = true;
  try {
    var csrfToken = await getCSRFToken();
    if (action === "retry") {
      var res = await fetch("/api/v1/queue/" + queueId + "/retry", {
        method: "POST",
        headers: { "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken }
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Queue item " + queueId + " queued for retry.");
    } else if (action === "delete") {
      if (!confirm("Delete queue item " + queueId + "? This action cannot be undone.")) { btn.disabled = false; return; }
      var res = await fetch("/api/v1/queue/" + queueId, {
        method: "DELETE",
        headers: { "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken }
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Queue item " + queueId + " deleted.");
    }
    await loadQueue();
  } catch (err) {
    showAlert(err.message || "Operation failed.");
  } finally {
    btn.disabled = false;
  }
});

function showDetail(name) {
  document.querySelectorAll("[data-page-view]").forEach(function(v) { v.classList.add("hidden"); });
  document.querySelectorAll("[data-detail-view]").forEach(function(v) { v.classList.add("hidden"); });
  var detail = document.querySelector("[data-detail-view=\"" + name + "\"]");
  if (detail) detail.classList.remove("hidden");
}

document.querySelectorAll("[data-detail-back]").forEach(function(btn) {
  btn.addEventListener("click", function() {
    document.querySelectorAll("[data-detail-view]").forEach(function(v) { v.classList.add("hidden"); });
    document.querySelectorAll("[data-page-view]").forEach(function(v) { v.classList.remove("hidden"); });
  });
});

async function showMailboxDetail(mailboxId, email) {
  showDetail("mailbox-detail");
  el("detail-mb-title").textContent = "Mailbox: " + email;
  el("detail-mb-content").innerHTML = '<div class="loading">Loading...</div>';
  el("detail-mb-audit").innerHTML = '<div class="loading">Loading...</div>';

  try {
    var detail = await apiGet("/api/v1/mailboxes/" + mailboxId, null);
    if (detail) {
      el("detail-mb-content").innerHTML =
        '<div class="setting-row"><div><strong>Email</strong></div><span>' + escapeHTML(detail.email || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Domain</strong></div><span>' + escapeHTML(detail.domain || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Status</strong></div><span>' + escapeHTML(detail.status || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Admin</strong></div><span>' + (detail.is_admin ? "Yes" : "No") + '</span></div>' +
        '<div class="setting-row"><div><strong>Created</strong></div><span>' + escapeHTML(detail.created_at || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Updated</strong></div><span>' + escapeHTML(detail.updated_at || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Messages</strong></div><span>' + (detail.stats ? (detail.stats.messages || 0) : "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Queue Items</strong></div><span>' + (detail.stats ? (detail.stats.queue_items || 0) : "-") + '</span></div>';
    }
  } catch (_) {
    el("detail-mb-content").innerHTML = '<div class="empty-state">Failed to load mailbox details.</div>';
  }

  try {
    var audit = await apiGet("/api/v1/mailboxes/" + mailboxId + "/audit", []);
    renderTable("detail-mb-audit", audit, ["action", "actor", "target", "result", "timestamp"], "No audit entries.");
  } catch (_) {
    el("detail-mb-audit").innerHTML = '<div class="empty-state">Audit unavailable.</div>';
  }
}

async function showDomainDetail(domain) {
  showDetail("domain-detail");
  el("detail-dm-title").textContent = "Domain: " + domain;
  el("detail-dm-content").innerHTML = '<div class="loading">Loading...</div>';
  el("detail-dm-mailboxes").innerHTML = '<div class="loading">Loading...</div>';
  el("detail-dm-audit").innerHTML = '<div class="loading">Loading...</div>';

  try {
    var detail = await apiGet("/api/v1/domains/" + encodeURIComponent(domain), null);
    if (detail) {
      el("detail-dm-content").innerHTML =
        '<div class="setting-row"><div><strong>Domain</strong></div><span>' + escapeHTML(detail.domain || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Plan</strong></div><span>' + escapeHTML(detail.plan || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Status</strong></div><span>' + escapeHTML(detail.status || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Mailbox Count</strong></div><span>' + (detail.mailbox_count != null ? detail.mailbox_count : "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Created</strong></div><span>' + escapeHTML(detail.created_at || "-") + '</span></div>' +
        '<div class="setting-row"><div><strong>Updated</strong></div><span>' + escapeHTML(detail.updated_at || "-") + '</span></div>';
    }
    if (detail && Array.isArray(detail.mailboxes)) {
      renderTable("detail-dm-mailboxes", detail.mailboxes, ["mailbox_id", "email", "status", "is_admin"], "No mailboxes.");
    }
  } catch (_) {
    el("detail-dm-content").innerHTML = '<div class="empty-state">Failed to load domain details.</div>';
  }

  try {
    var audit = await apiGet("/api/v1/domains/" + encodeURIComponent(domain) + "/audit", []);
    renderTable("detail-dm-audit", audit, ["action", "actor", "target", "result", "timestamp"], "No audit entries.");
  } catch (_) {
    el("detail-dm-audit").innerHTML = '<div class="empty-state">Audit unavailable.</div>';
  }
}

document.addEventListener("click", function(event) {
  var mbViewBtn = event.target.closest("button.mv-action");
  if (mbViewBtn) {
    var mailboxId = mbViewBtn.dataset.mailboxId;
    var email = mbViewBtn.dataset.email;
    if (mailboxId) showMailboxDetail(mailboxId, email);
    return;
  }
  var dmViewBtn = event.target.closest("button.dv-action");
  if (dmViewBtn) {
    var domain = dmViewBtn.dataset.domain;
    if (domain) showDomainDetail(domain);
    return;
  }
});

document.querySelectorAll("[data-page]").forEach((button) => {
  button.addEventListener("click", () => {
    const page = button.dataset.page;
    document.querySelectorAll("[data-page]").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelectorAll("[data-page-view]").forEach((view) => view.classList.toggle("hidden", view.dataset.pageView !== page));
    setText("page-subtitle", `${button.textContent.trim()} workspace`);
  });
});

document.querySelectorAll("[data-refresh]").forEach((button) => {
  button.addEventListener("click", async () => {
    showAlert("");
    try {
      if (button.dataset.refresh === "domains") await loadDomains();
      if (button.dataset.refresh === "mailboxes") await loadMailboxes();
      if (button.dataset.refresh === "queue") await loadQueue();
      if (button.dataset.refresh === "logs") await loadLogs();
    } catch (err) {
      showAlert(err.message || "Refresh failed.");
    }
  });
});

if (state.token) {
  loadProfile().then(() => {
    showApp();
    refreshAll();
  }).catch(() => signOut());
}
