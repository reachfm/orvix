/* =====================================================================
   modules/rtl.js — Per-element and global direction handling.

   Why this file exists: Arabic + English content appears together
   in many admin pages (queue sender domain, mailbox local-parts,
   email bodies in quarantine previews, license tier names, etc.).
   The naive approach of forcing 'dir="rtl"' on the whole document
   when the locale is Arabic makes mixed strings render wrong.
   Instead we use:

     * setDocDirection(dir):    sets the document direction once
                                for the whole page. Default is 'ltr'.
     * directionForText(s):     inspects a string and returns
                                'rtl' if it starts with (or is
                                dominated by) Arabic characters,
                                else 'ltr'. Mixed strings get 'auto'.
     * withAutoDir(el):         writes dir="auto" on an element
                                so the browser picks direction
                                per Unicode Bidi algorithm for the
                                inline content.

   The setDocDirection default of 'ltr' is intentional: the admin
   shell is English-first and we never want a misconfigured locale
   cookie to flip the layout. Pages that contain a known Arabic
   string may call setDocDirection('rtl') from a route handler.
   ===================================================================== */

const ARABIC_RANGE = /[\u0600-\u06FF\u0750-\u077F\u08A0-\u08FF\uFB50-\uFDFF\uFE70-\uFEFF]/;

export function directionForText(s) {
  if (s == null) return 'ltr';
  const txt = String(s);
  if (!txt.trim()) return 'ltr';
  // Strip the URL/email prefix (Latin), then look for Arabic chars.
  const stripped = txt.replace(/^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\s*[-—]?\s*/, '');
  const candidate = stripped.length > 0 ? stripped : txt;
  const arabicMatches = (candidate.match(/[\u0600-\u06FF\u0750-\u077F\u08A0-\u08FF\uFB50-\uFDFF\uFE70-\uFEFF]/g) || []).length;
  const latinMatches  = (candidate.match(/[A-Za-z]/g) || []).length;
  if (arabicMatches === 0) return 'ltr';
  if (latinMatches === 0)  return 'rtl';
  // Mixed: rely on Unicode Bidi auto.
  return 'auto';
}

/**
 * withAutoDir sets dir="auto" on every text-bearing child of the
 * given root so per-line direction is correct for mixed strings.
 */
export function withAutoDir(root) {
  if (!root) return;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT, null);
  const seen = new Set();
  let n = root;
  while (n) {
    seen.add(n);
    if (n.nodeType === 1 && n.childNodes.length && !n.matches('input, textarea, script, style')) {
      n.setAttribute('dir', n.getAttribute('dir') || 'auto');
    }
    n = walker.nextNode();
  }
  // Walk text nodes; for plain text nodes we set the parent's dir
  // explicitly to keep things simple.
  root.querySelectorAll('input, textarea').forEach((node) => {
    // Inputs and textareas: only set placeholder dir. We don't
    // change the input text direction — the operator types in
    // whichever language they intend.
    const ph = node.getAttribute('placeholder');
    if (ph) {
      const d = directionForText(ph);
      if (d === 'rtl') node.setAttribute('dir', 'rtl');
      else if (d === 'ltr') node.setAttribute('dir', 'ltr');
    }
  });
  // Tables: data cells with both Arabic + English get dir=auto
  // so column alignment follows the natural Bidi of each value.
  root.querySelectorAll('table.data-table').forEach((tbl) => {
    tbl.querySelectorAll('tbody tr').forEach((tr) => {
      tr.querySelectorAll('td, th').forEach((td) => {
        if (!td.getAttribute('dir')) td.setAttribute('dir', 'auto');
      });
    });
  });
}

/**
 * applyAutoDir walks the root and sets dir="auto" only on nodes
 * that already contain text. It is a lighter version of
 * withAutoDir — used at mount time when the layout is static.
 */
export function applyAutoDir(root) {
  if (!root) return;
  // Walks every element; we only set dir="auto" when the element
  // has DIRECT text children (no inner block). Otherwise, browser
  // per-line bidi resolution is fine.
  Array.from(root.querySelectorAll('*')).forEach((el) => {
    if (el.children.length === 0 && el.childNodes.length > 0) {
      el.setAttribute('dir', 'auto');
    }
  });
}

export function setDocDirection(dir) {
  if (dir !== 'rtl' && dir !== 'ltr') dir = 'ltr';
  document.documentElement.setAttribute('dir', dir);
  document.body && document.body.setAttribute('dir', dir);
}

/**
 * detectRtlFromURL honours a ?rtl=1 query param used by smoke
 * tests and language switches. It does NOT force the document
 * direction globally based on the active locale — pages opt in.
 */
export function detectRtlFromURL() {
  try {
    const params = new URLSearchParams(window.location.search);
    if (params.get('rtl') === '1') {
      setDocDirection('rtl');
      return true;
    }
  } catch (_) {}
  return false;
}
