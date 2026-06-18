const state = {
  token: sessionStorage.getItem("orvix_access_token") || "",
  profile: null,
  health: null,
  domains: [],
  users: [],
  queue: [],
  backups: [],
  logs: [],
  monitoringHealth: null,
  monitoringAlerts: [],
  updateStatus: null,
  updateHistory: [],
  updatePreflight: null,
  selectedDomains: new Set(),
  selectedMailboxes: new Set(),
  webmailAccounts: [],
  currentWebmailAccount: null
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

async function apiPost(path, payload) {
  const csrf = await getCSRFToken();
  const res = await fetch(path, {
    method: "POST",
    headers: Object.assign({}, authHeaders(), {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrf
    }),
    body: JSON.stringify(payload || {})
  });
  if (res.status === 401) {
    signOut();
    throw new Error("Session expired. Sign in again.");
  }
  const text = await res.text();
  var data = {};
  if (text) {
    try { data = JSON.parse(text); } catch (_) { data = { error: text }; }
  }
  if (!res.ok) throw new Error(data.error || `${path} returned ${res.status}`);
  return data;
}

async function downloadCSV(path, filename) {
  const res = await fetch(path, { headers: authHeaders() });
  if (res.status === 401) {
    signOut();
    throw new Error("Session expired. Sign in again.");
  }
  if (!res.ok) throw new Error(`${path} returned ${res.status}`);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

async function getCSRFToken() {
  // credentials: 'include' is required so the
  // Set-Cookie header on the response is stored by
  // the browser. Without it, the cookie is dropped
  // silently and the next state-changing request
  // fails with "CSRF token missing in cookie" even
  // though the header matches. The browser's default
  // same-origin behaviour is to send cookies on
  // same-origin requests, but cross-subdomain
  // requests (admin.<parent> -> api.<parent>) need
  // explicit credentials: 'include' to attach the
  // csrf_token cookie that was set on
  // Domain=.orvix.email.
  const res = await fetch("/api/v1/csrf-token", {
    headers: authHeaders(),
    credentials: "include"
  });
  if (!res.ok) throw new Error("Failed to get CSRF token");
  const data = await res.json();
  return data.csrf_token;
}

// csrfFetch issues a state-changing request with a
// fresh CSRF token and credentials: 'include'. If
// the server returns 403 with a CSRF-specific error,
// the helper fetches a fresh token and retries
// exactly once. This guards against the race where
// the operator opened two tabs and the second tab's
// token was rotated by a different action — the first
// 403 should not leave the operator's action
// unfulfilled.
//
// The retry guard is single-shot on purpose: a
// second 403 means there is a real configuration
// problem (CSRF middleware broken, cookie not sent,
// wrong header) and the operator should see the
// error rather than loop forever.
async function csrfFetch(url, init) {
  const csrfToken = await getCSRFToken();
  const doFetch = (token) => fetch(url, Object.assign({}, init, {
    credentials: "include",
    headers: Object.assign({}, init && init.headers, {
      "X-CSRF-Token": token
    })
  }));
  let res = await doFetch(csrfToken);
  if (res.status === 403) {
    // Inspect the body. CSRF middleware returns
    // {"error": "CSRF token missing in cookie"} or
    // {"error": "CSRF token mismatch"} or similar.
    // On any of those, fetch a fresh token and
    // retry once. Other 403s (e.g. admin role
    // missing) are surfaced unchanged.
    let body = null;
    try { body = await res.json(); } catch (e) { body = null; }
    const msg = (body && body.error) ? String(body.error) : "";
    if (msg.indexOf("CSRF") >= 0) {
      const fresh = await getCSRFToken();
      res = await doFetch(fresh);
    }
  }
  return res;
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
  renderTable("summary-recent-activity", data.recent_activity || [], ["action", "actor", "target", "result", "timestamp"], "No recent activity.");
  renderTable("summary-top-domains", data.top_domains || [], ["domain", "mailbox_count"], "No domain activity.");
}

async function loadDomains() {
  const domains = await apiGet("/api/v1/domains" + queryString({
    q: el("domain-search") ? el("domain-search").value.trim() : "",
    status: el("domain-status-filter") ? el("domain-status-filter").value : ""
  }), []);
  state.domains = domains;
  state.selectedDomains = new Set(Array.from(state.selectedDomains).filter(function(name) {
    return domains.some(function(d) { return d.domain === name; });
  }));
  const node = el("domains-table");
  if (!node) return;
  if (!Array.isArray(domains) || domains.length === 0) {
    node.innerHTML = '<div class="empty-state">No domains have been provisioned yet.</div>';
    return;
  }
  const rows = domains.map(function(d) {
    const isActive = d.status === "active";
    const checked = state.selectedDomains.has(d.domain) ? " checked" : "";
    const statusBtn = isActive
      ? '<button class="ghost-btn dm-action" data-action="disable" data-domain="' + escapeHTML(d.domain) + '" data-domain-id="' + d.id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">Disable</button>'
      : '<button class="ghost-btn dm-action" data-action="enable" data-domain="' + escapeHTML(d.domain) + '" data-domain-id="' + d.id + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">Enable</button>';
    const deleteBtn = '<button class="ghost-btn dm-action" data-action="delete" data-domain="' + escapeHTML(d.domain) + '" data-domain-id="' + d.id + '" data-mailbox-count="' + (d.mailbox_count || 0) + '" style="font-size:12px;padding:4px 8px;min-height:auto;">Delete</button>';
    const viewBtn = '<button class="ghost-btn dv-action" data-action="view" data-domain="' + escapeHTML(d.domain) + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">View</button>';
    return '<tr>' +
      '<td><input type="checkbox" class="domain-select" data-domain="' + escapeHTML(d.domain) + '"' + checked + '></td>' +
      '<td>' + escapeHTML(d.domain || "-") + '</td>' +
      '<td>' + escapeHTML(d.plan || "-") + '</td>' +
      '<td>' + escapeHTML(d.status || "active") + '</td>' +
      '<td>' + (d.mailbox_count != null ? d.mailbox_count : "-") + '</td>' +
      '<td>' + viewBtn + statusBtn + deleteBtn + '</td></tr>';
  }).join("");
  node.innerHTML = '<table class="table"><thead><tr><th>Select</th><th>Domain</th><th>Plan</th><th>Status</th><th>Mailboxes</th><th>Actions</th></tr></thead><tbody>' + rows + '</tbody></table>';
  updateBulkLabels();
}

async function loadMailboxes() {
  const users = await apiGet("/api/v1/users" + queryString({
    q: el("mailbox-search") ? el("mailbox-search").value.trim() : "",
    status: el("mailbox-status-filter") ? el("mailbox-status-filter").value : "",
    admin: el("mailbox-admin-filter") ? el("mailbox-admin-filter").value : ""
  }), []);
  state.users = users;
  state.selectedMailboxes = new Set(Array.from(state.selectedMailboxes).filter(function(id) {
    return users.some(function(u) { return String(u.mailbox_id) === String(id) && u.is_admin !== true; });
  }));
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
    const checked = canModify && state.selectedMailboxes.has(String(u.mailbox_id)) ? " checked" : "";
    const selectHtml = canModify
      ? '<input type="checkbox" class="mailbox-select" data-mailbox-id="' + u.mailbox_id + '"' + checked + '>'
      : '<span style="color:var(--muted);font-size:12px;">-</span>';
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
      '<td>' + selectHtml + '</td>' +
      '<td>' + escapeHTML(u.email || "-") + '</td>' +
      '<td>' + escapeHTML(u.role || "-") + '</td>' +
      '<td>' + (isAdmin ? "Yes" : "No") + '</td>' +
      '<td>' + escapeHTML(u.status || "active") + '</td>' +
      '<td>' + actionsHtml + '</td></tr>';
  }).join("");
  node.innerHTML = '<table class="table"><thead><tr><th>Select</th><th>Email</th><th>Role</th><th>Admin</th><th>Status</th><th>Actions</th></tr></thead><tbody>' + rows + '</tbody></table>';
  updateBulkLabels();
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

async function loadMonitoringHealth() {
  var health = await apiGet("/api/v1/monitoring/health", null);
  state.monitoringHealth = health;
  renderMonitoringHealth(health);
}

async function loadMonitoringAlerts() {
  var data = await apiGet("/api/v1/monitoring/alerts", { alerts: [] });
  var alerts = Array.isArray(data) ? data : (data && Array.isArray(data.alerts) ? data.alerts : []);
  state.monitoringAlerts = alerts;
  setText("monitoring-open-alerts", String(alerts.length));
  renderMonitoringAlerts("monitoring-alerts-table", alerts);
}

async function loadMonitoring() {
  try {
    await Promise.all([loadMonitoringHealth(), loadMonitoringAlerts()]);
  } catch (err) {
    setText("monitoring-status", "Unavailable");
    setText("monitoring-status-note", err.message || "Monitoring API is unavailable.");
    setText("monitoring-open-alerts", "-");
    setText("monitoring-queue", "-");
    var components = el("monitoring-components");
    var disk = el("monitoring-disk");
    var alerts = el("monitoring-alerts-table");
    if (components) components.innerHTML = '<div class="empty-state">Monitoring health unavailable.</div>';
    if (disk) disk.innerHTML = '<div class="empty-state">Disk usage unavailable.</div>';
    if (alerts) alerts.innerHTML = '<div class="empty-state">Monitoring alerts unavailable.</div>';
  }
}

function renderMonitoringHealth(health) {
  if (!health) {
    setText("monitoring-status", "Unavailable");
    setText("monitoring-status-note", "Monitoring health unavailable.");
    return;
  }
  setText("monitoring-status", titleCase(health.status || "unknown"));
  setText("monitoring-status-note", "Generated " + formatTimestamp(health.generatedAt || health.generated_at || ""));
  setText("monitoring-open-alerts", String(health.openAlerts != null ? health.openAlerts : (health.open_alerts != null ? health.open_alerts : "-")));
  var capacity = health.capacity || {};
  var queueCount = capacity.queueCount != null ? capacity.queueCount : (capacity.queue_count != null ? capacity.queue_count : 0);
  var deadLetters = capacity.queueDeadLetter != null ? capacity.queueDeadLetter : (capacity.queue_dead_letter != null ? capacity.queue_dead_letter : 0);
  setText("monitoring-queue", queueCount + " / " + deadLetters);

  var components = [
    ["Database", health.db],
    ["Queue", health.queue],
    ["Backup", health.backup],
    ["Admin API", health.api]
  ].map(function(pair) {
    var component = pair[1] || {};
    return {
      component: pair[0],
      status: component.status || "unknown",
      message: component.message || "-"
    };
  });
  renderTable("monitoring-components", components, ["component", "status", "message"], "No component health data.");

  var diskRows = Array.isArray(health.disk) ? health.disk.map(function(row) {
    var usedPct = row.usedPct != null ? row.usedPct : row.used_pct;
    return {
      label: row.label || "-",
      used: formatBytes(row.usedBytes != null ? row.usedBytes : row.used_bytes),
      free: formatBytes(row.freeBytes != null ? row.freeBytes : row.free_bytes),
      used_pct: usedPct != null ? usedPct + "%" : "-"
    };
  }) : [];
  renderTable("monitoring-disk", diskRows, ["label", "used", "free", "used_pct"], "No disk usage data.");
}

function renderMonitoringAlerts(id, alerts) {
  var node = el(id);
  if (!node) return;
  if (!Array.isArray(alerts) || alerts.length === 0) {
    node.innerHTML = '<div class="empty-state">No active monitoring alerts.</div>';
    return;
  }
  var head = '<table class="table"><thead><tr><th>ID</th><th>Severity</th><th>Category</th><th>Title</th><th>Message</th><th>Created</th><th>Actions</th></tr></thead><tbody>';
  var body = alerts.map(function(alert) {
    var idValue = alert.id || "";
    return '<tr>' +
      '<td>' + escapeHTML(String(idValue)) + '</td>' +
      '<td>' + escapeHTML(alert.severity || "-") + '</td>' +
      '<td>' + escapeHTML(alert.category || "-") + '</td>' +
      '<td>' + escapeHTML(alert.title || "-") + '</td>' +
      '<td>' + escapeHTML(alert.message || "-") + '</td>' +
      '<td>' + escapeHTML(formatTimestamp(alert.createdAt || alert.created_at || "")) + '</td>' +
      '<td><button class="ghost-btn monitoring-resolve" data-alert-id="' + escapeHTML(String(idValue)) + '" style="font-size:12px;padding:4px 8px;min-height:auto;">Resolve</button></td>' +
      '</tr>';
  }).join("");
  node.innerHTML = head + body + '</tbody></table>';
}

async function loadUpdateStatus() {
  var status = await apiGet("/api/v1/update/status", null);
  state.updateStatus = status;
  renderUpdateStatus(status);
}

async function loadUpdateHistory() {
  var data = await apiGet("/api/v1/update/history?limit=50", { history: [] });
  var rows = data && Array.isArray(data.history) ? data.history : [];
  state.updateHistory = rows;
  renderUpdateHistory("update-history-table", rows);
}

async function loadUpdatePreflight() {
  var preflight = await apiGet("/api/v1/update/preflight", null);
  state.updatePreflight = preflight;
  renderUpdatePreflight(preflight);
}

async function loadUpdate() {
  try {
    await Promise.all([loadUpdateStatus(), loadUpdateHistory(), loadUpdatePreflight()]);
  } catch (err) {
    setText("update-current-version", "Unavailable");
    setText("update-current-sha", "-");
    setText("update-build-time", err.message || "Update API is unavailable.");
    setText("update-latest-version", "-");
    setText("update-latest-sha", "Latest SHA unavailable.");
    setText("update-status", "Unavailable");
    setText("update-status-note", "Update status unavailable.");
    setText("update-channel", "Channel unavailable.");
    var preflight = el("update-preflight");
    var history = el("update-history-table");
    var notes = el("update-release-notes");
    if (preflight) preflight.innerHTML = '<div class="empty-state">Preflight checks unavailable.</div>';
    if (history) history.innerHTML = '<div class="empty-state">Update history unavailable.</div>';
    if (notes) notes.innerHTML = '<div class="empty-state">Release notes unavailable.</div>';
  }
}

function renderUpdateStatus(status) {
  if (!status) {
    setText("update-current-version", "Unavailable");
    setText("update-current-sha", "-");
    setText("update-latest-version", "-");
    setText("update-latest-sha", "Latest SHA unavailable.");
    setText("update-status", "Unavailable");
    return;
  }
  var currentVersion = status.currentVersion || status.current_version || "unknown";
  var currentSha = status.currentSha || status.current_sha || "";
  var latestVersion = status.latest_version || status.availableVersion || "";
  var latestSha = status.latest_sha || status.availableSha || "";
  var updateAvailable = Boolean(status.update_available != null ? status.update_available : status.updateAvailable);
  var releaseNotes = Array.isArray(status.release_notes) ? status.release_notes : (status.releaseNotes ? [status.releaseNotes] : []);
  var message = status.message || status.updateError || "";
  setText("update-current-version", currentVersion);
  setText("update-current-sha", shortSHA(currentSha));
  setText("update-build-time", "Build: " + (status.buildTime || "-"));
  setText("update-channel", "Channel: " + (status.channel || "stable"));
  var jobStatus = status.jobStatus || "idle";
  var updateText = updateAvailable ? "Update Available" : (message === "update check not configured" ? "Not Configured" : titleCase(jobStatus || "idle"));
  setText("update-status", updateText);
  var checked = status.checkedAt ? "Checked " + formatTimestamp(status.checkedAt) : "Not checked yet";
  var available = latestVersion ? " | Latest " + latestVersion : "";
  setText("update-status-note", checked + available);
  setText("update-latest-version", latestVersion || "-");
  setText("update-latest-sha", latestSha ? "Latest SHA: " + shortSHA(latestSha) : "Latest SHA unavailable.");
  var notes = el("update-release-notes");
  if (notes) {
    if (releaseNotes.length) {
      notes.innerHTML = '<ul class="plain-list">' + releaseNotes.map(function(note) {
        return '<li>' + escapeHTML(note) + '</li>';
      }).join("") + '</ul>';
    } else if (message) {
      notes.innerHTML = '<div class="empty-state">' + escapeHTML(message) + '</div>';
    } else {
      notes.innerHTML = '<div class="empty-state">No release notes available.</div>';
    }
  }
}

function renderUpdatePreflight(preflight) {
  var node = el("update-preflight");
  if (!node) return;
  if (!preflight || !Array.isArray(preflight.checks)) {
    node.innerHTML = '<div class="empty-state">No preflight data available.</div>';
    return;
  }
  var summary = '<div class="setting-row"><div><strong>Result</strong><span>' + escapeHTML(preflight.message || "-") + '</span></div><span>' + (preflight.pass ? "PASS" : "FAIL") + '</span></div>';
  var rows = preflight.checks.map(function(check) {
    return {
      check: check.name || "-",
      status: check.status || "-",
      detail: check.detail || "-"
    };
  });
  node.innerHTML = summary + tableHTML(rows, ["check", "status", "detail"], "No preflight checks.");
}

function renderUpdateHistory(id, rows) {
  var safeRows = Array.isArray(rows) ? rows.map(function(row) {
    return {
      started: formatTimestamp(row.startedAt || row.started_at || ""),
      completed: formatTimestamp(row.completedAt || row.completed_at || ""),
      duration: (row.durationSeconds != null ? row.durationSeconds : row.duration_seconds || 0) + "s",
      previous_sha: shortSHA(row.previousSha || row.previous_sha || ""),
      new_sha: shortSHA(row.newSha || row.new_sha || ""),
      status: row.status || "-",
      actor: row.actor || "-",
      notes: row.notes || "-"
    };
  }) : [];
  var node = el(id);
  if (!node) return;
  node.innerHTML = tableHTML(safeRows, ["started", "completed", "duration", "previous_sha", "new_sha", "status", "actor", "notes"], "No update history.");
}

async function loadBackups() {
  state.backups = await apiGet("/api/v1/backups", []);
  renderBackupsTable("backups-table", state.backups);
}

async function loadBackupStats() {
  var node = el("backup-stats");
  var healthNode = el("backup-health");
  if (!node) return;
  try {
    var [metrics, health] = await Promise.all([
      apiGet("/api/v1/backups/metrics", null),
      apiGet("/api/v1/backups/health", null)
    ]);
    if (!metrics) metrics = {totalBackups:0,totalSizeBytes:0};
    if (!health) health = {schedulerEnabled:false,retentionEnabled:true,directoryExists:false,writable:false,availableDiskBytes:0};
    var diskSpace = "";
    if (health.availableDiskBytes > 0) {
      diskSpace = " | " + formatBytes(health.availableDiskBytes) + " free";
    }
    node.innerHTML =
      '<div class="setting-row"><div><strong>Total Backups</strong></div><span>' + (metrics.totalBackups || 0) + '</span></div>' +
      '<div class="setting-row"><div><strong>Total Size</strong></div><span>' + formatBytes(metrics.totalSizeBytes) + '</span></div>' +
      '<div class="setting-row"><div><strong>Newest Backup</strong></div><span>' + escapeHTML(metrics.newestBackupAt || "-") + '</span></div>' +
      '<div class="setting-row"><div><strong>Oldest Backup</strong></div><span>' + escapeHTML(metrics.oldestBackupAt || "-") + '</span></div>' +
      '<div class="setting-row"><div><strong>Last Successful</strong></div><span>' + escapeHTML(metrics.lastSuccessfulAt || "-") + '</span></div>' +
      '<div class="setting-row"><div><strong>Next Scheduled</strong></div><span>' + escapeHTML(metrics.nextScheduledAt || "-") + '</span></div>';
    if (healthNode) {
      healthNode.innerHTML =
        '<div class="setting-row"><div><strong>Directory</strong></div><span>' + (health.directoryExists ? "Exists" : "MISSING") + '</span></div>' +
        '<div class="setting-row"><div><strong>Writable</strong></div><span>' + (health.writable ? "Yes" : "No") + '</span></div>' +
        '<div class="setting-row"><div><strong>Disk Free</strong></div><span>' + formatBytes(health.availableDiskBytes) + '</span></div>' +
        '<div class="setting-row"><div><strong>Scheduler</strong></div><span>' + (health.schedulerEnabled ? "Enabled" : "Disabled") + '</span></div>' +
        '<div class="setting-row"><div><strong>Retention</strong></div><span>' + (health.retentionEnabled ? "Enabled" : "Disabled") + '</span></div>';
    }
  } catch (_) {
    node.innerHTML = '<div class="empty-state">Backup stats unavailable.</div>';
    if (healthNode) healthNode.innerHTML = '<div class="empty-state">Backup health unavailable.</div>';
  }
}

async function loadBackupSchedule() {
  var enabledBox = el("bs-enabled");
  var freqSelect = el("bs-frequency");
  var retentionInput = el("bs-retention");
  if (!enabledBox || !freqSelect || !retentionInput) return;
  try {
    var cfg = await apiGet("/api/v1/backups/schedule", null);
    if (!cfg) return;
    enabledBox.checked = cfg.enabled === true;
    freqSelect.value = cfg.frequency || "manual";
    retentionInput.value = cfg.retentionCount || 7;
  } catch (_) {
    // Use defaults
  }
}

var backupScheduleForm = el("backup-schedule-form");
if (backupScheduleForm) {
  backupScheduleForm.addEventListener("submit", async function(event) {
    event.preventDefault();
    var btn = el("bs-save-btn");
    var msg = el("bs-message");
    if (!btn || !msg) return;
    msg.textContent = "";
    msg.className = "message";
    btn.disabled = true;
    try {
      var enabled = el("bs-enabled").checked;
      var frequency = el("bs-frequency").value;
      var retentionCount = parseInt(el("bs-retention").value, 10);
      if (!retentionCount || retentionCount < 1) {
        msg.textContent = "Retention count must be at least 1.";
        msg.className = "message error";
        btn.disabled = false;
        return;
      }
      var cfg = await apiPost("/api/v1/backups/schedule", {
        enabled: enabled,
        frequency: frequency,
        retentionCount: retentionCount
      });
      msg.textContent = "Schedule saved (" + (cfg.frequency || frequency) + ").";
      msg.className = "message success";
    } catch (err) {
      msg.textContent = err.message || "Failed to save schedule.";
      msg.className = "message error";
    } finally {
      btn.disabled = false;
    }
  });
}

var backupRetentionBtn = el("backup-run-retention");
if (backupRetentionBtn) {
  backupRetentionBtn.addEventListener("click", async function() {
    if (!confirm("Run retention cleanup? Oldest backups will be deleted to stay within the retention count.")) return;
    backupRetentionBtn.disabled = true;
    try {
      var result = await apiPost("/api/v1/backups/retention", {});
      showAlert("Retention cleanup: " + (result.deleted || 0) + " backup(s) deleted.");
      await loadBackups();
      await loadBackupStats();
    } catch (err) {
      showAlert(err.message || "Retention cleanup failed.");
    } finally {
      backupRetentionBtn.disabled = false;
    }
  });
}

// ── Webmail Accounts ───────────────────────────────────────

async function loadWebmailAccounts() {
  var node = el("webmail-accounts-table");
  if (!node) return;
  var search = el("webmail-search").value.trim();
  var domain = el("webmail-domain-filter").value.trim();
  var status = el("webmail-status-filter").value;
  var admin = el("webmail-admin-filter").value;
  try {
    var data = await apiGet("/api/v1/webmail/accounts" + queryString({ search: search, domain: domain, status: status, admin: admin }), []);
    state.webmailAccounts = data && Array.isArray(data) ? data : (data && Array.isArray(data.accounts) ? data.accounts : []);
    renderWebmailAccountsTable("webmail-accounts-table", state.webmailAccounts);
  } catch (_) {
    node.innerHTML = '<div class="empty-state">Webmail accounts unavailable.</div>';
  }
}

function renderWebmailAccountsTable(id, accounts) {
  var node = el(id);
  if (!node) return;
  if (!Array.isArray(accounts) || accounts.length === 0) {
    node.innerHTML = '<div class="empty-state">No webmail accounts found.</div>';
    return;
  }
  var head = '<table class="table"><thead><tr><th>Mailbox ID</th><th>Email</th><th>Status</th><th>Domain</th><th>Admin</th><th>Last Login</th><th>Created</th><th></th></tr></thead><tbody>';
  var body = accounts.map(function(a) {
    var statusStr = escapeHTML(a.status || "unknown");
    var lastLogin = a.last_login_at ? escapeHTML(a.last_login_at) : "-";
    var created = a.created_at ? escapeHTML(a.created_at) : "-";
    var isAdmin = a.is_admin ? "Yes" : "No";
    return '<tr><td>' + a.mailbox_id + '</td><td>' + escapeHTML(a.email) + '</td><td>' + statusStr + '</td><td>' + escapeHTML(a.domain) + '</td><td>' + isAdmin + '</td><td>' + lastLogin + '</td><td>' + created + '</td>' +
      '<td><button class="ghost-btn wm-view" data-mailbox-id="' + a.mailbox_id + '" data-email="' + escapeHTML(a.email) + '">View</button></td></tr>';
  }).join("");
  node.innerHTML = head + body + '</tbody></table>';
}

async function loadWebmailDetail(mailboxId, email) {
  state.currentWebmailAccount = mailboxId;
  el("wmd-title").textContent = "Webmail Account — " + escapeHTML(email);
  el("wmd-content").innerHTML = '<div class="empty-state">Mailbox ID: ' + mailboxId + ' | Email: ' + escapeHTML(email) + '</div>';
  showDetail("webmail-detail");

  try {
    var [sessions, activity, storage] = await Promise.all([
      apiGet("/api/v1/webmail/sessions?mailboxId=" + mailboxId, []),
      apiGet("/api/v1/webmail/activity/" + mailboxId, null),
      apiGet("/api/v1/webmail/storage/" + mailboxId, null)
    ]);
    var sessionsArr = Array.isArray(sessions) ? sessions : (sessions && Array.isArray(sessions.sessions) ? sessions.sessions : []);
    renderWebmailSessions(sessionsArr);
    renderWebmailActivity(activity);
    renderWebmailStorage(storage);
  } catch (_) {
    el("wmd-sessions").innerHTML = '<div class="empty-state">Sessions unavailable.</div>';
    el("wmd-activity").innerHTML = '<div class="empty-state">Activity unavailable.</div>';
    el("wmd-storage").innerHTML = '<div class="empty-state">Storage unavailable.</div>';
  }
}

function renderWebmailSessions(sessions) {
  var node = el("wmd-sessions");
  if (!node) return;
  if (!Array.isArray(sessions) || sessions.length === 0) {
    node.innerHTML = '<div class="empty-state">No active sessions.</div>';
    return;
  }
  var head = '<table class="table"><thead><tr><th>Session ID</th><th>IP</th><th>User Agent</th><th>Created</th><th>Last Seen</th><th></th></tr></thead><tbody>';
  var body = sessions.map(function(s) {
    var ua = escapeHTML((s.user_agent || "").substring(0, 80));
    return '<tr><td>' + s.id + '</td><td>' + escapeHTML(s.ip) + '</td><td>' + ua + '</td><td>' + escapeHTML(s.created_at) + '</td><td>' + escapeHTML(s.last_seen_at) + '</td>' +
      '<td><button class="ghost-btn wm-revoke-session" data-session-id="' + s.id + '">Revoke</button></td></tr>';
  }).join("");
  node.innerHTML = head + body + '</tbody></table>';
}

function renderWebmailActivity(activity) {
  var node = el("wmd-activity");
  if (!node) return;
  if (!activity) {
    node.innerHTML = '<div class="empty-state">No login activity data.</div>';
    return;
  }
  node.innerHTML =
    '<div class="setting-row"><div><strong>Successful Logins</strong></div><span>' + (activity.successful_logins || 0) + '</span></div>' +
    '<div class="setting-row"><div><strong>Failed Logins</strong></div><span>' + (activity.failed_logins || 0) + '</span></div>' +
    '<div class="setting-row"><div><strong>Last Login</strong></div><span>' + escapeHTML(activity.last_login_at || "-") + '</span></div>' +
    '<div class="setting-row"><div><strong>Last Failed Login</strong></div><span>' + escapeHTML(activity.last_failed_login_at || "-") + '</span></div>';
}

function renderWebmailStorage(storage) {
  var node = el("wmd-storage");
  if (!node) return;
  if (!storage) {
    node.innerHTML = '<div class="empty-state">No storage metrics available.</div>';
    return;
  }
  node.innerHTML =
    '<div class="setting-row"><div><strong>Message Count</strong></div><span>' + (storage.message_count || 0) + '</span></div>' +
    '<div class="setting-row"><div><strong>Mailbox Size</strong></div><span>' + formatBytes(storage.mailbox_size) + '</span></div>' +
    '<div class="setting-row"><div><strong>Sent Count</strong></div><span>' + (storage.sent_count || 0) + '</span></div>' +
    '<div class="setting-row"><div><strong>Received Count</strong></div><span>' + (storage.received_count || 0) + '</span></div>';
}

function formatBytes(bytes) {
  var value = Number(bytes || 0);
  if (value < 1024) return value + " B";
  if (value < 1024 * 1024) return (value / 1024).toFixed(1) + " KB";
  if (value < 1024 * 1024 * 1024) return (value / (1024 * 1024)).toFixed(1) + " MB";
  return (value / (1024 * 1024 * 1024)).toFixed(2) + " GB";
}

function formatTimestamp(value) {
  if (!value) return "-";
  var date = new Date(value);
  if (isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

function shortSHA(value) {
  if (!value) return "-";
  return String(value).slice(0, 12);
}

function renderBackupsTable(id, rows) {
  var node = el(id);
  if (!node) return;
  if (!Array.isArray(rows) || rows.length === 0) {
    node.innerHTML = '<div class="empty-state">No backups have been created yet.</div>';
    return;
  }
  var head = '<table class="table"><thead><tr><th>ID</th><th>Name</th><th>Status</th><th>Size</th><th>Created</th><th>Actions</th></tr></thead><tbody>';
  var body = rows.map(function(b) {
    var idValue = b.id || "";
    return '<tr>' +
      '<td>' + escapeHTML(idValue) + '</td>' +
      '<td>' + escapeHTML(b.name || "-") + '</td>' +
      '<td>' + escapeHTML(b.status || "-") + '</td>' +
      '<td>' + escapeHTML(formatBytes(b.size_bytes)) + '</td>' +
      '<td>' + escapeHTML(b.created_at || "-") + '</td>' +
      '<td>' +
        '<button class="ghost-btn backup-action" data-action="download" data-backup-id="' + escapeHTML(idValue) + '" style="font-size:12px;padding:4px 8px;min-height:auto;margin-right:4px;">Download</button>' +
        '<button class="ghost-btn backup-action" data-action="delete" data-backup-id="' + escapeHTML(idValue) + '" style="font-size:12px;padding:4px 8px;min-height:auto;">Delete</button>' +
      '</td></tr>';
  }).join("");
  node.innerHTML = head + body + '</tbody></table>';
}

function renderTable(id, rows, columns, emptyText) {
  const node = el(id);
  if (!node) return;
  node.innerHTML = tableHTML(rows, columns, emptyText);
}

function tableHTML(rows, columns, emptyText) {
  if (!Array.isArray(rows) || rows.length === 0) {
    return `<div class="empty-state">${escapeHTML(emptyText)}</div>`;
  }
  const head = columns.map((col) => `<th>${escapeHTML(titleCase(col))}</th>`).join("");
  const body = rows.map((row) => `<tr>${columns.map((col) => `<td>${escapeHTML(valueFor(row, col))}</td>`).join("")}</tr>`).join("");
  return `<table class="table"><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table>`;
}

function valueFor(row, key) {
  const value = row[key] ?? row[key.replace("_", "")] ?? "";
  if (value === null || value === undefined || value === "") return "-";
  return String(value);
}

function titleCase(value) {
  return value.replace(/_/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function queryString(params) {
  const q = new URLSearchParams();
  Object.keys(params).forEach(function(key) {
    if (params[key]) q.set(key, params[key]);
  });
  const value = q.toString();
  return value ? "?" + value : "";
}

function updateBulkLabels() {
  setButtonText("mailbox-bulk-enable", "Enable Selected (" + state.selectedMailboxes.size + ")");
  setButtonText("mailbox-bulk-suspend", "Suspend Selected (" + state.selectedMailboxes.size + ")");
  setButtonText("domain-bulk-enable", "Enable Selected (" + state.selectedDomains.size + ")");
  setButtonText("domain-bulk-suspend", "Suspend Selected (" + state.selectedDomains.size + ")");
}

function setButtonText(id, value) {
  const node = el(id);
  if (node) node.textContent = value;
}

async function bulkMailboxStatus(status) {
  const ids = Array.from(state.selectedMailboxes).map(function(id) { return Number(id); }).filter(Boolean);
  if (ids.length === 0) { setText("mailbox-bulk-result", "Select at least one non-admin mailbox."); return; }
  const result = await apiPost("/api/v1/mailboxes/bulk/status", { mailbox_ids: ids, status: status });
  state.selectedMailboxes.clear();
  setText("mailbox-bulk-result", "Updated " + (result.updated || 0) + ", skipped " + (result.skipped || 0) + ".");
  await loadMailboxes();
  await loadSummary();
}

async function bulkDomainStatus(status) {
  const domains = Array.from(state.selectedDomains);
  if (domains.length === 0) { setText("domain-bulk-result", "Select at least one domain."); return; }
  const result = await apiPost("/api/v1/domains/bulk/status", { domains: domains, status: status });
  state.selectedDomains.clear();
  setText("domain-bulk-result", "Updated " + (result.updated || 0) + ", skipped " + (result.skipped || 0) + ".");
  await loadDomains();
  await loadSummary();
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
  await Promise.allSettled([loadHealth(), loadSummary(), loadDomains(), loadMailboxes(), loadQueue(), loadBackups(), loadBackupStats(), loadBackupSchedule(), loadMonitoring(), loadUpdate(), loadLogs()]);
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
    if (action === "retry") {
      // Use csrfFetch (credentials: 'include' +
      // fresh CSRF token + single retry on 403) so
      // the Set-Cookie on the response is stored
      // AND the cookie is sent on the next request.
      // Without credentials: 'include' the browser
      // drops the Set-Cookie header (the response
      // is for a cross-site fetch in some operator
      // topologies) and the next request 403s with
      // "CSRF token missing in cookie".
      var res = await csrfFetch("/api/v1/queue/" + queueId + "/retry", {
        method: "POST",
        headers: { "Authorization": "Bearer " + state.token }
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Queue item " + queueId + " queued for retry.");
    } else if (action === "delete") {
      if (!confirm("Delete queue item " + queueId + "? This action cannot be undone.")) { btn.disabled = false; return; }
      var res = await csrfFetch("/api/v1/queue/" + queueId, {
        method: "DELETE",
        headers: { "Authorization": "Bearer " + state.token }
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

el("backups-table").addEventListener("click", async function(event) {
  var btn = event.target.closest("button.backup-action");
  if (!btn) return;
  var action = btn.dataset.action;
  var backupId = btn.dataset.backupId;
  if (!backupId) return;
  btn.disabled = true;
  try {
    if (action === "download") {
      window.location.href = "/api/v1/backups/" + encodeURIComponent(backupId) + "/download";
    } else if (action === "delete") {
      if (!confirm("Delete backup " + backupId + "? This action cannot be undone.")) { btn.disabled = false; return; }
      var csrfToken = await getCSRFToken();
      var res = await fetch("/api/v1/backups/" + encodeURIComponent(backupId), {
        method: "DELETE",
        headers: { "Authorization": "Bearer " + state.token, "X-CSRF-Token": csrfToken }
      });
      if (!res.ok) {
        var errBody = await res.json().catch(function() { return {}; });
        throw new Error(errBody.error || "HTTP " + res.status);
      }
      showAlert("Backup " + backupId + " deleted.");
      await loadBackups();
    }
  } catch (err) {
    showAlert(err.message || "Backup operation failed.");
  } finally {
    btn.disabled = false;
  }
});

el("monitoring-alerts-table").addEventListener("click", async function(event) {
  var btn = event.target.closest("button.monitoring-resolve");
  if (!btn) return;
  var alertId = btn.dataset.alertId;
  if (!alertId) return;
  btn.disabled = true;
  try {
    await apiPost("/api/v1/monitoring/alerts/" + encodeURIComponent(alertId) + "/resolve", {});
    showAlert("Monitoring alert " + alertId + " resolved.");
    await loadMonitoring();
  } catch (err) {
    showAlert(err.message || "Alert resolve failed.");
  } finally {
    btn.disabled = false;
  }
});

var backupCreate = el("backup-create");
if (backupCreate) {
  backupCreate.addEventListener("click", async function() {
    showAlert("");
    backupCreate.disabled = true;
    try {
      var created = await apiPost("/api/v1/backups", {});
      showAlert("Backup " + (created.id || created.name || "created") + " created.");
      await loadBackups();
      await loadBackupStats();
    } catch (err) {
      showAlert(err.message || "Backup creation failed.");
    } finally {
      backupCreate.disabled = false;
    }
  });
}

var updateCheck = el("update-check");
if (updateCheck) {
  updateCheck.addEventListener("click", async function() {
    showAlert("");
    updateCheck.disabled = true;
    try {
      var status = await apiPost("/api/v1/update/check", {});
      state.updateStatus = status;
      renderUpdateStatus(status);
      await Promise.allSettled([loadUpdateHistory(), loadUpdatePreflight()]);
      if (status && status.message === "update check not configured") {
        showAlert("Update check not configured.");
      } else {
        showAlert("Update check completed.");
      }
    } catch (err) {
      showAlert(err.message || "Update check failed.");
    } finally {
      updateCheck.disabled = false;
    }
  });
}

var updateRun = el("update-run");
if (updateRun) {
  updateRun.addEventListener("click", async function() {
    if (!confirm("Run runtime update? The Orvix service may restart.")) return;
    showAlert("");
    updateRun.disabled = true;
    try {
      var result = await apiPost("/api/v1/update/run", {});
      await loadUpdate();
      if (result && result.status === "completed") {
        showAlert("Runtime update completed.");
      } else {
        showAlert("Runtime update finished with status: " + escapeHTML((result && result.status) || "failed") + ".");
      }
    } catch (err) {
      showAlert(err.message || "Runtime update failed.");
    } finally {
      updateRun.disabled = false;
    }
  });
}

["mailbox-search", "mailbox-status-filter", "mailbox-admin-filter"].forEach(function(id) {
  var node = el(id);
  if (!node) return;
  var eventName = node.tagName === "SELECT" ? "change" : "input";
  node.addEventListener(eventName, function() {
    state.selectedMailboxes.clear();
    setText("mailbox-bulk-result", "");
    loadMailboxes().catch(function(err) { showAlert(err.message || "Mailbox filter failed."); });
  });
});

["domain-search", "domain-status-filter"].forEach(function(id) {
  var node = el(id);
  if (!node) return;
  var eventName = node.tagName === "SELECT" ? "change" : "input";
  node.addEventListener(eventName, function() {
    state.selectedDomains.clear();
    setText("domain-bulk-result", "");
    loadDomains().catch(function(err) { showAlert(err.message || "Domain filter failed."); });
  });
});

["webmail-search", "webmail-domain-filter", "webmail-status-filter", "webmail-admin-filter"].forEach(function(id) {
  var node = el(id);
  if (!node) return;
  var eventName = node.tagName === "SELECT" ? "change" : "input";
  node.addEventListener(eventName, function() {
    loadWebmailAccounts().catch(function(err) { showAlert(err.message || "Webmail filter failed."); });
  });
});

el("mailboxes-table").addEventListener("change", function(event) {
  var box = event.target.closest("input.mailbox-select");
  if (!box) return;
  if (box.checked) state.selectedMailboxes.add(String(box.dataset.mailboxId));
  else state.selectedMailboxes.delete(String(box.dataset.mailboxId));
  updateBulkLabels();
});

el("domains-table").addEventListener("change", function(event) {
  var box = event.target.closest("input.domain-select");
  if (!box) return;
  if (box.checked) state.selectedDomains.add(box.dataset.domain);
  else state.selectedDomains.delete(box.dataset.domain);
  updateBulkLabels();
});

[
  ["mailbox-bulk-enable", function() { return bulkMailboxStatus("active"); }],
  ["mailbox-bulk-suspend", function() { return bulkMailboxStatus("suspended"); }],
  ["domain-bulk-enable", function() { return bulkDomainStatus("active"); }],
  ["domain-bulk-suspend", function() { return bulkDomainStatus("suspended"); }]
].forEach(function(pair) {
  var node = el(pair[0]);
  if (!node) return;
  node.addEventListener("click", async function() {
    showAlert("");
    node.disabled = true;
    try {
      await pair[1]();
    } catch (err) {
      showAlert(err.message || "Bulk operation failed.");
    } finally {
      node.disabled = false;
      updateBulkLabels();
    }
  });
});

var mailboxExport = el("mailbox-export");
if (mailboxExport) {
  mailboxExport.addEventListener("click", async function() {
    try {
      await downloadCSV("/api/v1/mailboxes/export", "mailboxes.csv");
    } catch (err) {
      showAlert(err.message || "Mailbox export failed.");
    }
  });
}

var domainExport = el("domain-export");
if (domainExport) {
  domainExport.addEventListener("click", async function() {
    try {
      await downloadCSV("/api/v1/domains/export", "domains.csv");
    } catch (err) {
      showAlert(err.message || "Domain export failed.");
    }
  });
}

async function webmailControl(action, endpoint) {
  var mbId = state.currentWebmailAccount;
  if (!mbId) return;
  try {
    var result = await apiPost("/api/v1/webmail/controls/" + endpoint + "/" + mbId, {});
    el("wmd-ctrl-message").textContent = action + " completed.";
    el("wmd-ctrl-message").className = "message success";
  } catch (err) {
    el("wmd-ctrl-message").textContent = err.message || action + " failed.";
    el("wmd-ctrl-message").className = "message error";
  }
}

var wmdForceLogout = el("wmd-force-logout");
if (wmdForceLogout) wmdForceLogout.addEventListener("click", function() { webmailControl("Force logout", "force-logout"); });
var wmdUnlock = el("wmd-unlock");
if (wmdUnlock) wmdUnlock.addEventListener("click", function() { webmailControl("Unlock", "unlock"); });
var wmdResetPrefs = el("wmd-reset-preferences");
if (wmdResetPrefs) wmdResetPrefs.addEventListener("click", function() { webmailControl("Reset preferences", "reset-preferences"); });
var wmdClearCounters = el("wmd-clear-counters");
if (wmdClearCounters) wmdClearCounters.addEventListener("click", function() { webmailControl("Clear counters", "clear-counters"); });

function showDetail(name) {
  document.querySelectorAll("[data-page-view]").forEach(function(v) { v.classList.add("hidden"); });
  document.querySelectorAll("[data-detail-view]").forEach(function(v) { v.classList.add("hidden"); });
  var detail = document.querySelector("[data-detail-view=\"" + name + "\"]");
  if (detail) detail.classList.remove("hidden");
}

document.querySelectorAll("[data-detail-back]").forEach(function(btn) {
  btn.addEventListener("click", function() {
    var detailName = btn.dataset.detailBack;
    var pageName = detailName === "webmail-detail" ? "webmail" : "";
    if (detailName === "mailbox-detail") pageName = "mailboxes";
    if (detailName === "domain-detail") pageName = "domains";
    document.querySelectorAll("[data-detail-view]").forEach(function(v) { v.classList.add("hidden"); });
    document.querySelectorAll("[data-page-view]").forEach(function(v) {
      v.classList.toggle("hidden", pageName ? v.dataset.pageView !== pageName : false);
    });
    if (pageName) {
      document.querySelectorAll("[data-page]").forEach(function(item) {
        item.classList.toggle("active", item.dataset.page === pageName);
      });
    }
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
  var wmViewBtn = event.target.closest("button.wm-view");
  if (wmViewBtn) {
    var mailboxId = wmViewBtn.dataset.mailboxId;
    var email = wmViewBtn.dataset.email;
    if (mailboxId) loadWebmailDetail(Number(mailboxId), email);
    return;
  }
  var revokeBtn = event.target.closest("button.wm-revoke-session");
  if (revokeBtn) {
    var sessionId = revokeBtn.dataset.sessionId;
    if (!sessionId) return;
    revokeBtn.disabled = true;
    (async function() {
      try {
        await apiPost("/api/v1/webmail/sessions/" + sessionId + "/revoke", {});
        showAlert("Session " + sessionId + " revoked.");
        var mbId = state.currentWebmailAccount;
        var email = el("wmd-title").textContent.replace("Webmail Account — ", "");
        if (mbId) loadWebmailDetail(mbId, email);
      } catch (err) {
        showAlert(err.message || "Revoke failed.");
      } finally {
        revokeBtn.disabled = false;
      }
    })();
    return;
  }
});

document.querySelectorAll("[data-page]").forEach((button) => {
  button.addEventListener("click", () => {
    const page = button.dataset.page;
    document.querySelectorAll("[data-page]").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelectorAll("[data-page-view]").forEach((view) => view.classList.toggle("hidden", view.dataset.pageView !== page));
    setText("page-subtitle", `${button.textContent.trim()} workspace`);
    if (page === "webmail") loadWebmailAccounts().catch(function(err) { showAlert(err.message || "Failed to load webmail."); });
    if (page === "monitoring") loadMonitoring().catch(function(err) { showAlert(err.message || "Failed to load monitoring."); });
    if (page === "update") loadUpdate().catch(function(err) { showAlert(err.message || "Failed to load update status."); });
  });
});

document.querySelectorAll("[data-refresh]").forEach((button) => {
  button.addEventListener("click", async () => {
    showAlert("");
    try {
      if (button.dataset.refresh === "domains") await loadDomains();
      if (button.dataset.refresh === "mailboxes") await loadMailboxes();
      if (button.dataset.refresh === "queue") await loadQueue();
      if (button.dataset.refresh === "backups") { await loadBackups(); await loadBackupStats(); await loadBackupSchedule(); }
      if (button.dataset.refresh === "webmail") await loadWebmailAccounts();
      if (button.dataset.refresh === "monitoring") await loadMonitoring();
      if (button.dataset.refresh === "update") await loadUpdate();
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
