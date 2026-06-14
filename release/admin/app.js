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

async function loadDomains() {
  state.domains = await apiGet("/api/v1/domains", []);
  renderTable("domains-table", state.domains, ["domain", "plan", "status"], "No domains have been provisioned yet.");
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
  renderTable("queue-table", state.queue, ["from", "to", "status"], "The delivery queue is empty.");
  renderTable("queue-preview", state.queue.slice(0, 5), ["from", "to", "status"], "The delivery queue is empty.");
}

async function loadLogs() {
  state.logs = await apiGet("/api/v1/audit/logs", []);
  renderTable("logs-table", state.logs, ["action", "actor", "result"], "No audit log entries are available from the API.");
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
  await Promise.allSettled([loadHealth(), loadDomains(), loadMailboxes(), loadQueue(), loadLogs()]);
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
