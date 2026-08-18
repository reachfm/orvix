// test-webmail-reading-pane.mjs — regression test for the webmail
// reading-pane raw-MIME bug.
//
// The reading pane used to render msg.rfc822 (raw RFC822/MIME wire
// format) directly as the visible message body. For a realistic
// external multipart message (Outlook/Mimecast shape) that meant the
// user saw MIME boundaries, Content-Type/Content-Transfer-Encoding
// headers, and base64 attachment payloads instead of the actual
// email. The fix makes the reading pane render the server's
// already-parsed html_body/text_body fields instead.
//
// This script loads the CANONICAL source
// (web/webmail-release/assets/webmail.js) in a bare Node context —
// no DOM/browser needed, since window.OrvixWebmail.utils.renderBody
// is a pure function of (msg, loadRemoteImages) that returns an HTML
// string. `window`/`document` are stubbed only so the file's
// top-level IIFE loads without throwing; nothing in the functions
// under test touches them.
//
// All message content here is synthetic — no real customer email
// content is used anywhere in this repository.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '..', '..', '..');
const WEBMAIL_JS = path.join(REPO_ROOT, 'web', 'webmail-release', 'assets', 'webmail.js');

let PASSED = 0;
let FAILED = 0;
function pass(msg) { console.log('  PASS ' + msg); PASSED++; }
function fail(msg) { console.log('  FAIL ' + msg); FAILED++; }
function assert(cond, msg) { if (cond) pass(msg); else fail(msg); }

// Load webmail.js in a minimal global stub. It's a plain IIFE that
// only reads document/window inside function bodies (never at
// top-level load time) except for the final `window.OrvixWebmail =
// {...}` assignment.
global.window = {};
global.document = {};
const src = readFileSync(WEBMAIL_JS, 'utf8');
// eslint-disable-next-line no-eval
(0, eval)(src);
const utils = global.window.OrvixWebmail && global.window.OrvixWebmail.utils;
if (!utils || typeof utils.renderBody !== 'function') {
  console.error('FATAL: window.OrvixWebmail.utils.renderBody not found after loading webmail.js');
  process.exit(1);
}

// ── Synthetic Outlook/Mimecast-style parsed message ──────────────
// Mirrors the shape internal/coremail/mime.ExtractBodies would
// produce for a real external multipart message with a remote
// tracking pixel and a PDF attachment — NOT real customer content.
const outlookMimecastMsg = {
  subject: 'Q3 Report',
  from: 'John Smith <john.smith@acme-example.com>',
  has_html: true,
  html_body:
    '<p>Hi Salma,</p>' +
    '<p>Please find attached our Q3 report as requested.</p>' +
    '<p>Kind regards,<br>John Smith<br>Acme Corp</p>' +
    '<img src="http://tracker.example.com/open.gif" width="1" height="1">',
  text_body:
    'Hi Salma,\n\nPlease find attached our Q3 report as requested.\n\n' +
    'Kind regards,\nJohn Smith\nAcme Corp',
  has_remote_images: true,
  attachments: [
    { id: 1, filename: 'Q3-Report.pdf', content_type: 'application/pdf', size_bytes: 48831 },
  ],
  // rfc822 is present in the real API response (kept for other
  // callers) but MUST NOT influence renderBody's output at all.
  rfc822:
    'From: John Smith <john.smith@acme-example.com>\r\n' +
    'Content-Type: multipart/mixed; boundary="OUTLOOK_OUTER_BOUNDARY_7f3a"\r\n' +
    '\r\n--OUTLOOK_OUTER_BOUNDARY_7f3a\r\n' +
    'Content-Type: multipart/alternative; boundary="OUTLOOK_ALT_BOUNDARY_9c21"\r\n' +
    '\r\n--OUTLOOK_ALT_BOUNDARY_9c21\r\n' +
    'Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n' +
    'Hi Salma,=0D=0A=0D=0APlease find attached...\r\n' +
    '--OUTLOOK_ALT_BOUNDARY_9c21--\r\n' +
    '--OUTLOOK_OUTER_BOUNDARY_7f3a\r\n' +
    'Content-Type: application/pdf; name="Q3-Report.pdf"\r\nContent-Transfer-Encoding: base64\r\n\r\n' +
    'JVBERi0xLjQKJSUlU1lOVEhFVElDLVBERi1QQVlMT0FELU5PVC1BLVJFQUwtUERGCg==\r\n' +
    '--OUTLOOK_OUTER_BOUNDARY_7f3a--\r\n',
};

console.log('=== Webmail Reading Pane MIME Rendering Regression ===\n');

console.log('--- renderBody uses parsed bodies, never rfc822 ---');
{
  const html = utils.renderBody(outlookMimecastMsg, false);
  assert(html.includes('Please find attached our Q3 report'),
    'renderBody output contains the human-readable message text');
  assert(!html.includes('Content-Type:'), 'renderBody output does not contain "Content-Type:"');
  assert(!html.includes('Content-Transfer-Encoding:'), 'renderBody output does not contain "Content-Transfer-Encoding:"');
  assert(!html.includes('OUTLOOK_OUTER_BOUNDARY') && !html.includes('OUTLOOK_ALT_BOUNDARY'),
    'renderBody output does not contain raw MIME boundary markers');
  assert(!html.includes('JVBERi0xLjQK'), 'renderBody output does not contain the raw base64 attachment payload');
  assert(!html.includes('quoted-printable'), 'renderBody output does not contain MIME encoding artifacts');
}

console.log('\n--- remote image protection ---');
{
  const blocked = utils.renderBody(outlookMimecastMsg, false);
  // Check the actual `src=` attribute value specifically (not just
  // "contains the URL anywhere") — the URL legitimately still
  // appears in a `data-blocked-src="..."` attribute so a later
  // "Load remote images" click can restore it without a re-fetch.
  const srcMatch = blocked.match(/<img\b[^>]*?\ssrc\s*=\s*"([^"]*)"/i);
  assert(!!srcMatch && !srcMatch[1].startsWith('http'),
    'remote <img> src attribute itself is blocked by default (loadRemoteImages=false), not left pointing at the remote URL');
  const loaded = utils.renderBody(outlookMimecastMsg, true);
  assert(loaded.includes('http://tracker.example.com/open.gif'),
    'remote <img> src is restored once loadRemoteImages=true (explicit user opt-in)');
}

console.log('\n--- text/plain-only message still works ---');
{
  const textOnly = { has_html: false, text_body: 'Just a plain text message, no HTML at all.' };
  const html = utils.renderBody(textOnly, false);
  assert(html.includes('Just a plain text message'), 'plain-text-only message renders its text_body');
  assert(html.indexOf('<pre') === 0, 'plain-text-only message is wrapped in <pre> (escaped, not raw HTML)');
}

console.log('\n--- HTML-only message (multipart/alternative not present) still works ---');
{
  const htmlOnly = { has_html: true, html_body: '<p>An HTML-only message body.</p>', text_body: '' };
  const html = utils.renderBody(htmlOnly, false);
  assert(html.includes('An HTML-only message body'), 'HTML-only message renders its html_body');
}

console.log('\n--- multipart/alternative preference (html_body wins when has_html) ---');
{
  const alt = {
    has_html: true,
    html_body: '<p>HTML alternative wins</p>',
    text_body: 'Plain-text alternative should not be shown when HTML is available',
  };
  const html = utils.renderBody(alt, false);
  assert(html.includes('HTML alternative wins'), 'multipart/alternative renders the HTML part when has_html=true');
}

console.log('\n--- empty message does not crash ---');
{
  const empty = { has_html: false, text_body: '' };
  const html = utils.renderBody(empty, false);
  assert(html.includes('empty-body') || html.includes('empty message'),
    'empty message renders a graceful empty-body placeholder, not a crash');
}

console.log('\n--- reply/forward quoting uses parsed text, never raw MIME ---');
{
  if (typeof utils.plainTextForQuoting !== 'function') {
    fail('window.OrvixWebmail.utils.plainTextForQuoting is exported');
  } else {
    pass('window.OrvixWebmail.utils.plainTextForQuoting is exported');
    const quoted = utils.plainTextForQuoting(outlookMimecastMsg);
    assert(quoted.includes('Please find attached our Q3 report'),
      'quote text contains the human-readable message text');
    assert(!quoted.includes('Content-Type:') && !quoted.includes('Content-Transfer-Encoding:'),
      'quote text does not contain raw MIME headers');
    assert(!quoted.includes('OUTLOOK_OUTER_BOUNDARY') && !quoted.includes('OUTLOOK_ALT_BOUNDARY'),
      'quote text does not contain raw MIME boundary markers');
    assert(!quoted.includes('JVBERi0xLjQK'), 'quote text does not contain the raw base64 attachment payload');

    // HTML-only source (no text_body) — plainTextForQuoting must
    // still produce readable text via its tag-strip fallback, not
    // raw markup or empty text.
    const htmlOnlyMsg = { text_body: '', html_body: '<p>Hi there.</p><p>Best,<br>Jane</p>' };
    const htmlQuoted = utils.plainTextForQuoting(htmlOnlyMsg);
    assert(htmlQuoted.includes('Hi there.') && htmlQuoted.includes('Best,'),
      'quote text for an HTML-only message falls back to stripped html_body text');
    assert(!htmlQuoted.includes('<p>') && !htmlQuoted.includes('<br>'),
      'quote text fallback strips HTML tags rather than showing raw markup');
  }
}

console.log('\n=== ' + PASSED + ' passed, ' + FAILED + ' failed ===');
if (FAILED > 0) process.exit(1);
