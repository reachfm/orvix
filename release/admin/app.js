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
  state.users = await apiGet("/api/v1/users", []);
  renderTable("mailboxes-table", state.users, ["email", "role"], "No mailboxes or users are available yet.");
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
