/* =====================================================================
   modules/icons.js — Inline SVG icon set for the admin console.

   Each icon is a 16x16 SVG. The shape is filled with currentColor so
   the icon inherits text color from its container. Use the `i(name)`
   helper to render an icon element.

   No external dependencies. Zero network calls. Zero fonts.
   ===================================================================== */

const _PATHS = {
  // navigation
  dashboard:  '<rect x="2" y="2" width="5" height="9" rx="1.5"/><rect x="9" y="2" width="5" height="5" rx="1.5"/><rect x="9" y="9" width="5" height="5" rx="1.5"/><rect x="2" y="13" width="12" height="3" rx="1.5"/>',
  settings:   '<circle cx="8" cy="8" r="2.5"/><path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.3 3.3l1.4 1.4M11.3 11.3l1.4 1.4M3.3 12.7l1.4-1.4M11.3 4.7l1.4-1.4"/>',
  services:   '<rect x="2" y="3" width="12" height="3" rx="1"/><rect x="2" y="10" width="12" height="3" rx="1"/><circle cx="5" cy="4.5" r="0.6" fill="currentColor"/><circle cx="5" cy="11.5" r="0.6" fill="currentColor"/>',
  domain:     '<circle cx="8" cy="8" r="6"/><path d="M2 8h12M8 2c2 2 2 10 0 12M8 2c-2 2-2 10 0 12"/>',
  mailbox:    '<rect x="2" y="4" width="12" height="9" rx="1.5"/><path d="M2 5l6 4 6-4"/>',
  group:      '<circle cx="6" cy="6" r="2.5"/><circle cx="12" cy="7" r="2"/><path d="M2 13c0-2.2 1.8-4 4-4s4 1.8 4 4M10 13c0-1.7 1.3-3 3-3s3 1.3 3 3"/>',
  list:       '<path d="M5 3h9M5 8h9M5 13h9"/><circle cx="2.5" cy="3" r="0.7" fill="currentColor"/><circle cx="2.5" cy="8" r="0.7" fill="currentColor"/><circle cx="2.5" cy="13" r="0.7" fill="currentColor"/>',
  folder:     '<path d="M2 5a1 1 0 011-1h3.5l1.5 1.5h5A1 1 0 0114 6.5V12a1 1 0 01-1 1H3a1 1 0 01-1-1V5z"/>',
  shield:     '<path d="M8 1.5L2.5 4v5c0 3 2.5 5 5.5 5.5 3-0.5 5.5-2.5 5.5-5.5V4L8 1.5z"/>',
  rules:      '<path d="M3 3h10M5 3v3M5 6c0 1.5-2 2-2 4 0 1.5 1 2 2 2M11 3v3M11 6c0 1.5 2 2 2 4 0 1.5-1 2-2 2"/>',
  firewall:   '<path d="M8 1.5L2.5 4v4.5c0 3 2.5 5 5.5 6 3-1 5.5-3 5.5-6V4L8 1.5z"/><path d="M5.5 7.5l1.5 1.5L11 5"/>',
  lock:       '<rect x="3" y="7" width="10" height="7" rx="1.5"/><path d="M5 7V5a3 3 0 016 0v2"/>',
  certificate:'<rect x="2" y="3" width="12" height="9" rx="1.5"/><circle cx="6" cy="7.5" r="1.5"/><path d="M9 6h2M9 9h2M4 11l1 4 1-1 1 1 1-4"/>',
  queue:      '<rect x="2" y="3" width="12" height="3" rx="1"/><rect x="2" y="10" width="12" height="3" rx="1"/><rect x="2" y="6.5" width="12" height="3" rx="1" fill="none" stroke="currentColor" stroke-dasharray="2 2"/>',
  monitoring: '<path d="M2 12L5 8L8 10L11 5L14 7"/><circle cx="5" cy="8" r="0.8" fill="currentColor"/><circle cx="8" cy="10" r="0.8" fill="currentColor"/><circle cx="11" cy="5" r="0.8" fill="currentColor"/>',
  log:        '<rect x="3" y="2" width="10" height="12" rx="1"/><path d="M5.5 6h5M5.5 9h5M5.5 12h3"/>',
  update:     '<path d="M3 8a5 5 0 019-3M13 8a5 5 0 01-9 3"/><path d="M11 1v4h-4M5 15v-4h4"/>',
  backup:     '<rect x="3" y="5" width="10" height="8" rx="1.5"/><path d="M6 5V3.5A1.5 1.5 0 017.5 2h1A1.5 1.5 0 0110 3.5V5"/><circle cx="8" cy="9" r="1.5"/>',
  migration:  '<path d="M2 8h12M10 5l4 3-4 3M6 5L2 8l4 3"/>',
  license:    '<rect x="3" y="3" width="10" height="10" rx="1.5"/><path d="M5.5 7l1.5 1.5L10 5.5"/>',
  admin:      '<path d="M8 1.5L2 4v4.5C2 12 4.5 14 8 14.5 11.5 14 14 12 14 8.5V4L8 1.5z"/><circle cx="8" cy="8" r="1.5" fill="currentColor"/>',
  user:       '<circle cx="8" cy="6" r="2.5"/><path d="M3 14c0-2.5 2.2-4.5 5-4.5s5 2 5 4.5"/>',
  audit:      '<circle cx="6" cy="6" r="3"/><path d="M2 14c0-2.2 1.8-4 4-4s4 1.8 4 4M10 7l2 2 4-4"/>',
  search:     '<circle cx="7" cy="7" r="4.5"/><path d="M10.5 10.5L14 14"/>',
  refresh:    '<path d="M3 8a5 5 0 019-3M13 8a5 5 0 01-9 3"/><path d="M11 1v4h-4M5 15v-4h4"/>',
  plus:       '<path d="M8 3v10M3 8h10"/>',
  close:      '<path d="M3.5 3.5l9 9M12.5 3.5l-9 9"/>',
  check:      '<path d="M3 8.5l3.5 3.5L13 5"/>',
  warn:       '<path d="M8 2L1.5 13.5h13L8 2z"/><path d="M8 6v4M8 12v0.5" stroke="currentColor" stroke-width="1.5" fill="currentColor"/>',
  bad:        '<circle cx="8" cy="8" r="6.5"/><path d="M5 5l6 6M11 5l-6 6"/>',
  info:       '<circle cx="8" cy="8" r="6.5"/><circle cx="8" cy="5" r="0.8" fill="currentColor"/><path d="M8 7.5v4.5"/>',
  download:   '<path d="M8 2v8M5 7l3 3 3-3M2 13h12"/>',
  upload:     '<path d="M8 14V6M5 9l3-3 3 3M2 13h12"/>',
  copy:       '<rect x="5" y="2" width="9" height="11" rx="1.5"/><path d="M3 5v9a1 1 0 001 1h7"/>',
  edit:       '<path d="M11 2.5l2.5 2.5L6 12.5 3 13l0.5-3L11 2.5z"/>',
  trash:      '<path d="M3 4h10M6 4V2.5h4V4M5 4l0.5 9a1 1 0 001 1h3a1 1 0 001-1L11 4"/>',
  more:       '<circle cx="3" cy="8" r="1.2" fill="currentColor"/><circle cx="8" cy="8" r="1.2" fill="currentColor"/><circle cx="13" cy="8" r="1.2" fill="currentColor"/>',
  filter:     '<path d="M2 3h12L9.5 8v5L6.5 12V8L2 3z"/>',
  dns:        '<circle cx="8" cy="8" r="6.5"/><path d="M1.5 8h13M8 1.5c2 2 3 4 3 6.5s-1 4.5-3 6.5M8 1.5c-2 2-3 4-3 6.5s1 4.5 3 6.5"/>',
  storage:    '<ellipse cx="8" cy="4" rx="6" ry="2"/><path d="M2 4v8c0 1.1 2.7 2 6 2s6-0.9 6-2V4M2 8c0 1.1 2.7 2 6 2s6-0.9 6-2"/>',
  charts:     '<path d="M3 13L7 8L10 11L14 4"/>',
  alert:      '<path d="M8 1.5L1.5 13.5h13L8 1.5z"/><path d="M8 6v3.5"/>',
  cpu:        '<rect x="4" y="4" width="8" height="8" rx="1"/><rect x="6" y="6" width="4" height="4" rx="0.5"/><path d="M7 1.5v2M9 1.5v2M7 12.5v2M9 12.5v2M1.5 7h2M1.5 9h2M12.5 7h2M12.5 9h2"/>',
  mail:       '<rect x="2" y="4" width="12" height="9" rx="1.5"/><path d="M2 5l6 4 6-4"/>',
  virus:      '<circle cx="8" cy="8" r="2.5"/><path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.3 3.3l1.4 1.4M11.3 11.3l1.4 1.4M3.3 12.7l1.4-1.4M11.3 4.7l1.4-1.4"/>',
  spam:       '<circle cx="8" cy="8" r="6.5"/><path d="M4.5 4.5l7 7"/>',
  routing:    '<circle cx="4" cy="4" r="2"/><circle cx="12" cy="12" r="2"/><path d="M5 6c2 0 4 2 4 4M11 10c-1 0-2-1-2-2"/>',
  globe:      '<circle cx="8" cy="8" r="6.5"/><path d="M1.5 8h13M8 1.5c2 2 3 4 3 6.5s-1 4.5-3 6.5M8 1.5c-2 2-3 4-3 6.5s1 4.5 3 6.5"/>',
  link:       '<path d="M6 10l4-4M5 8.5L3 10.5a2 2 0 003 3l2-2M11 7.5L13 5.5a2 2 0 00-3-3l-2 2"/>',
  key:        '<circle cx="6" cy="10" r="3"/><path d="M8.5 7.5L14 2M11 5l1.5 1.5"/>',
  power:      '<path d="M8 2v6M5 5a5 5 0 1010 0 5 5 0 00-3.5-4.8"/>',
  cluster:    '<circle cx="4" cy="4" r="1.5"/><circle cx="12" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="8" cy="8" r="1.5"/><path d="M4 5.5L8 6.5M12 5.5L8 6.5M4 10.5L8 9.5M12 10.5L8 9.5"/>',
  imap:       '<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M2 5l6 4 6-4"/>',
  pop3:       '<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M2 5l6 4 6-4M5 12h6"/>',
  smtp:       '<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M2 5l6 4 6-4M2 13l4-4M14 13l-4-4"/>',
};

// Renders an inline <svg> icon by name. Pass size, className, title.
export function i(name, opts = {}) {
  const path = _PATHS[name];
  if (!path) {
    return `<svg viewBox="0 0 16 16" width="${opts.size || 16}" height="${opts.size || 16}" aria-hidden="true" focusable="false"><circle cx="8" cy="8" r="3" fill="currentColor"/></svg>`;
  }
  const size = opts.size || 16;
  const cls = opts.className ? ` class="${opts.className}"` : '';
  const title = opts.title ? `<title>${opts.title}</title>` : '';
  return `<svg viewBox="0 0 16 16" width="${size}" height="${size}" aria-hidden="true" focusable="false"${cls}>${title}<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${path}</g></svg>`;
}

// Returns just the inner SVG markup (no <svg> wrapper) for use inside
// another SVG. Same path set as i().
export function iInner(name) {
  return _PATHS[name] || '<circle cx="8" cy="8" r="3" fill="currentColor"/>';
}

export function hasIcon(name) { return Boolean(_PATHS[name]); }

// Friendly labels for the icon names; used by shortcut overlay.
export const ICON_NAMES = Object.keys(_PATHS);
