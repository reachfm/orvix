/* =====================================================================
   modules/form.js — Shared form builder for the admin console.

   Goal: every admin modal / drawer / inline form uses the same
   primitives so the visual + interactive language stays consistent:

     * field types: text, email, number, url, password, textarea,
       select, switches, multi-checkbox, code, kv-list, members-list
     * field groups (collapsible / non, with title + subtitle)
     * inline help text per field
     * required-field marker + per-field error display
     * form-level error banner
     * submit button sticky state: idle / submitting / disabled
     * cancel + secondary buttons
     * keyboard: Enter submits (textarea excepted), Esc closes
     * success toast + form close on 2xx, error display on failure

   Pages call:

     import { openFormModal } from '../form.js';

     openFormModal({
       title: 'Create admin group',
       size: 'lg',
       groups: [
         { title: 'Identity', fields: [
           { name: 'name', label: 'Name', kind: 'text', required: true,
             placeholder: 'group.something',
             help: 'Lowercase identifier; built-in groups are prefixed builtin.' },
         ]},
         { title: 'Grants', fields: [
           { name: 'grants', kind: 'checkbox-grid', options: GRANTS },
         ]},
       ],
       submitLabel: 'Create',
       onSubmit: async (values) => {
         await apiPost('/api/v1/admin/admin-groups', values);
         return { ok: true, reload: () => location.reload() };
       },
     });

   `onSubmit` may return:
     * { ok: true, reload?: () => void }  — close modal, optional reload
     * { ok: false, error: 'msg' }        — keep modal open, show error
     * void                                — assume success
   Thrown errors are caught and reported as form-level errors.
   ===================================================================== */

import { el } from './components.js';
import { toast } from './toast.js';

// ---------- field row builders ----------------------------------
// Each returns a DOM node that has the standard `.ff-row` class
// (form row) plus a `.field-help` div (when help is supplied) and a
// `.ff-error` placeholder that the form runtime can populate.

function labelledField({ id, label, required, help }) {
  const wrap = el('div', { class: 'ff-row' });
  const labelEl = el('label', { for: id, class: 'ff-label' }, [
    el('span', { class: 'ff-label-text', text: label }),
    required ? el('span', { class: 'ff-required', text: '*' }) : null,
  ]);
  wrap.appendChild(labelEl);
  if (help) wrap.appendChild(el('p', { class: 'ff-help subtle', text: help }));
  const err = el('p', { class: 'ff-error', hidden: 'hidden' });
  wrap.appendChild(err);
  return { wrap, err };
}

function buildText({ id, value, kind, placeholder, autocomplete, maxLength, pattern, spellcheck, type }) {
  return el('input', {
    id, name: id,
    type: type || 'text',
    class: 'ff-input',
    value: value == null ? '' : String(value),
    placeholder: placeholder || '',
    autocomplete: autocomplete || 'off',
    spellcheck: spellcheck === false ? 'false' : null,
    maxlength: maxLength || null,
    pattern: pattern || null,
  });
}

function buildTextarea({ id, value, placeholder, rows, maxLength }) {
  const node = el('textarea', {
    id, name: id,
    class: 'ff-input ff-textarea',
    placeholder: placeholder || '',
    rows: rows || 3,
    maxlength: maxLength || null,
  });
  node.value = value == null ? '' : String(value);
  return node;
}

function buildSelect({ id, value, options }) {
  const sel = el('select', { id, name: id, class: 'ff-input ff-select' });
  (options || []).forEach((o) => {
    const opt = el('option', { value: String(o.value) }, o.label || String(o.value));
    if (String(o.value) === String(value)) opt.setAttribute('selected', 'selected');
    sel.appendChild(opt);
  });
  return sel;
}

function buildSwitch({ id, value }) {
  // A two-state select that renders as a horizontal switch control
  // (visually upgraded via CSS). Native boolean on submit.
  const wrap = el('div', { class: 'ff-switch', 'data-on': value ? 'true' : 'false' });
  const sel = el('select', { id, name: id, class: 'ff-switch-native', 'aria-label': id });
  sel.appendChild(el('option', { value: 'true' }, 'Enabled'));
  sel.appendChild(el('option', { value: 'false' }, 'Disabled'));
  sel.value = value ? 'true' : 'false';
  wrap.appendChild(sel);
  const track = el('span', { class: 'ff-switch-track' });
  wrap.appendChild(track);
  wrap.addEventListener('click', () => {
    sel.value = sel.value === 'true' ? 'false' : 'true';
    wrap.setAttribute('data-on', sel.value);
  });
  return wrap;
}

// Select-multi / chip input. Each chosen value becomes a chip with
// an × button; typing into the input filters the options, Enter /
// comma commit. Best for member-management style fields.
function buildMembers({ id, value, options, placeholder }) {
  const initial = Array.isArray(value) ? value.slice() : [];
  const wrap = el('div', { class: 'ff-members' });
  const chips = el('div', { class: 'ff-chips' });
  const filtered = el('datalist', { id: id + '-list' });
  (options || []).forEach((o) => {
    filtered.appendChild(el('option', { value: String(o.value || o) }));
  });
  const input = el('input', {
    type: 'text', class: 'ff-input ff-members-input', id, name: id,
    placeholder: placeholder || 'Type to add', autocomplete: 'off', spellcheck: 'false',
    list: id + '-list',
  });

  function sync() {
    chips.querySelectorAll('.ff-chip').forEach((c) => c.remove());
    initial.forEach((v, idx) => {
      const chip = el('span', { class: 'ff-chip', 'data-idx': String(idx) }, [
        v,
        el('button', {
          type: 'button', class: 'ff-chip-x', 'aria-label': 'Remove ' + v,
          onclick: () => { initial.splice(idx, 1); sync(); },
        }, '\u00d7'),
      ]);
      chips.appendChild(chip);
    });
    // stash the canonical list on the input so readForm picks it up.
    input.value = JSON.stringify(initial);
  }
  function commit(raw) {
    raw = String(raw || '').trim();
    if (!raw) return;
    if (initial.indexOf(raw) >= 0) return;
    initial.push(raw);
    sync();
    input.value = '';
  }
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      commit(input.value);
    } else if (e.key === 'Backspace' && !input.value && initial.length) {
      initial.pop();
      sync();
    }
  });
  input.addEventListener('blur', () => commit(input.value));

  wrap.appendChild(chips);
  wrap.appendChild(input);
  wrap.appendChild(filtered);
  sync();
  // expose the live array via a getter on the input
  input._getMembers = () => initial.slice();
  return wrap;
}

// Multi-checkbox grid (RBAC grants / permissions editor).
function buildCheckboxGrid({ id, value, options }) {
  const set = new Set(Array.isArray(value) ? value : []);
  const wrap = el('div', { class: 'ff-checkbox-grid', id, role: 'group', 'aria-label': id });
  (options || []).forEach((o) => {
    const checked = set.has(o.value || o);
    const cb = el('input', {
      type: 'checkbox', name: id + '[]',
      value: String(o.value || o),
      class: 'ff-cb',
      checked: checked ? 'checked' : null,
    });
    const tile = el('label', { class: 'ff-cb-tile' }, [
      cb,
      el('span', { class: 'ff-cb-label', text: o.label || String(o.value || o) }),
      o.desc ? el('span', { class: 'ff-cb-desc subtle', text: o.desc }) : null,
    ]);
    wrap.appendChild(tile);
  });
  wrap._getChecked = () => Array.from(wrap.querySelectorAll('input[type=checkbox]:checked')).map((n) => n.value);
  return wrap;
}

function buildKeyValueList({ id, value, keyLabel, valLabel, addLabel }) {
  let rows = Array.isArray(value) ? value.slice() : [];
  const wrap = el('div', { class: 'ff-kv-list', id });
  const tb = el('div', { class: 'ff-kv-body' });
  function paint() {
    tb.innerHTML = '';
    if (!rows.length) tb.appendChild(el('p', { class: 'subtle small', text: 'No entries yet.' }));
    rows.forEach((r, idx) => {
      const row = el('div', { class: 'ff-kv-row' }, [
        el('input', {
          type: 'text', class: 'ff-input ff-kv-key',
          placeholder: keyLabel || 'Key', value: r.k || '',
          oninput: (ev) => { rows[idx].k = ev.target.value; },
        }),
        el('input', {
          type: 'text', class: 'ff-input ff-kv-val',
          placeholder: valLabel || 'Value', value: r.v || '',
          oninput: (ev) => { rows[idx].v = ev.target.value; },
        }),
        el('button', {
          type: 'button', class: 'btn xs ghost danger', text: 'Remove',
          onclick: () => { rows.splice(idx, 1); paint(); },
        }),
      ]);
      tb.appendChild(row);
    });
  }
  paint();
  const addBtn = el('button', {
    type: 'button', class: 'btn xs ghost',
    text: addLabel || '+ Add', onclick: () => { rows.push({ k: '', v: '' }); paint(); },
  });
  wrap.appendChild(tb);
  wrap.appendChild(addBtn);
  wrap._getRows = () => rows.slice();
  return wrap;
}

// Dispatch a field spec to its renderer.
function buildField(field, values) {
  const id = 'ff-' + field.name;
  const value = (values && Object.prototype.hasOwnProperty.call(values, field.name)) ? values[field.name] : field.default;
  const { wrap, err } = labelledField({
    id, label: field.label, required: field.required, help: field.help,
  });
  let control;
  switch (field.kind) {
    case 'email':
      control = buildText({ id, value, type: 'email', placeholder: field.placeholder, autocomplete: 'email' });
      break;
    case 'url':
      control = buildText({ id, value, type: 'url', placeholder: field.placeholder });
      break;
    case 'number':
      control = buildText({ id, value, type: 'number', placeholder: field.placeholder });
      if (field.min != null) control.min = field.min;
      if (field.max != null) control.max = field.max;
      if (field.step != null) control.step = field.step;
      break;
    case 'password':
      control = buildText({ id, value, type: 'password', placeholder: field.placeholder, autocomplete: 'new-password' });
      break;
    case 'textarea':
      control = buildTextarea({ id, value, placeholder: field.placeholder, rows: field.rows, maxLength: field.maxLength });
      break;
    case 'select':
      control = buildSelect({ id, value, options: field.options || [] });
      break;
    case 'switch':
      control = buildSwitch({ id, value: !!value });
      break;
    case 'members':
      control = buildMembers({ id, value, options: field.options, placeholder: field.placeholder });
      break;
    case 'checkbox-grid':
      control = buildCheckboxGrid({ id, value, options: field.options || [] });
      break;
    case 'kv-list':
      control = buildKeyValueList({
        id, value, keyLabel: field.keyLabel, valLabel: field.valLabel, addLabel: field.addLabel,
      });
      break;
    case 'code':
      control = buildTextarea({ id, value, placeholder: field.placeholder, rows: field.rows || 4 });
      control.classList.add('ff-code');
      break;
    case 'static':
      control = el('div', { class: 'ff-static', text: field.value || '-' });
      break;
    default:
      control = buildText({ id, value, type: field.type, placeholder: field.placeholder, maxLength: field.maxLength, pattern: field.pattern });
  }
  // readFrom hook overrides default value extraction.
  control._fieldName = field.name;
  control._fieldKind = field.kind;
  control._readValue = field.readValue;
  wrap.appendChild(control);
  wrap._err = err;
  return wrap;
}

// ---------- form runtime ---------------------------------------
function readForm(body) {
  const fields = body.querySelectorAll('[data-ff-field]');
  const out = {};
  fields.forEach((row) => {
    const ctrl = row.querySelector('.ff-input, .ff-switch-native, .ff-checkbox-grid, .ff-members, .ff-kv-list');
    if (!ctrl) return;
    const name = row.getAttribute('data-ff-field');
    const kind = row.getAttribute('data-ff-kind');
    let v;
    if (typeof ctrl._readValue === 'function') {
      v = ctrl._readValue(ctrl);
    } else if (kind === 'switch') {
      v = ctrl.value === 'true';
    } else if (kind === 'checkbox-grid') {
      v = ctrl._getChecked ? ctrl._getChecked() : [];
    } else if (kind === 'members') {
      v = ctrl._getMembers ? ctrl._getMembers() : [];
    } else if (kind === 'kv-list') {
      v = ctrl._getRows ? ctrl._getRows() : [];
    } else if (kind === 'static') {
      return;
    } else if (ctrl.tagName === 'SELECT') {
      v = ctrl.value;
    } else if (ctrl.type === 'number') {
      const n = ctrl.value === '' ? null : Number(ctrl.value);
      v = isNaN(n) ? null : n;
    } else if (ctrl.tagName === 'TEXTAREA') {
      v = ctrl.value;
    } else {
      v = ctrl.value;
    }
    out[name] = v;
  });
  return out;
}

function showFieldError(row, msg) {
  const err = row._err;
  if (!err) return;
  err.textContent = msg || 'Invalid';
  err.removeAttribute('hidden');
  row.classList.add('ff-has-error');
}

function clearFieldError(row) {
  const err = row._err;
  if (!err) return;
  err.textContent = '';
  err.setAttribute('hidden', 'hidden');
  row.classList.remove('ff-has-error');
}

function setFormError(banner, msg) {
  if (!banner) return;
  if (!msg) { banner.setAttribute('hidden', 'hidden'); banner.textContent = ''; return; }
  banner.textContent = msg;
  banner.removeAttribute('hidden');
}

// Client-side validation. Each field can declare required + validate(value).
function validate(groups, values) {
  const errors = [];
  for (const g of groups) {
    for (const f of g.fields) {
      const v = values[f.name];
      if (f.required) {
        const empty = (v == null) || (Array.isArray(v) && !v.length) || (typeof v === 'string' && !v.trim());
        if (empty) errors.push({ field: f.name, msg: f.label + ' is required' });
      }
      if (f.kind === 'email' && v) {
        if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(String(v))) errors.push({ field: f.name, msg: 'Enter a valid email address' });
      }
      if (f.kind === 'url' && v) {
        try { new URL(String(v)); } catch (_) { errors.push({ field: f.name, msg: 'Enter a valid URL' }); }
      }
      if (f.kind === 'number' && v != null && f.min != null && Number(v) < Number(f.min)) {
        errors.push({ field: f.name, msg: 'Must be at least ' + f.min });
      }
      if (typeof f.validate === 'function' && v != null) {
        const r = f.validate(v, values);
        if (r && r.error) errors.push({ field: f.name, msg: r.error });
      }
    }
  }
  return errors;
}

function buildGroups(groups) {
  const root = el('div', { class: 'ff-groups' });
  (groups || []).forEach((g) => {
    const sec = el('section', { class: 'ff-group' });
    if (g.title) {
      const head = el('header', { class: 'ff-group-head' }, [
        el('h4', { text: g.title }),
        g.subtitle ? el('p', { class: 'ff-group-sub subtle', text: g.subtitle }) : null,
      ]);
      sec.appendChild(head);
    }
    const body = el('div', { class: 'ff-group-body' });
    if (g.fields && g.fields.length) {
      const grid = el('div', { class: 'ff-grid ' + (g.layout || 'cols-2') });
      g.fields.forEach((f) => {
        const row = buildField(f);
        row.setAttribute('data-ff-field', f.name);
        row.setAttribute('data-ff-kind', f.kind || 'text');
        if (f.span === 'full') row.classList.add('ff-row-full');
        if (f.span === 2) row.classList.add('ff-row-span-2');
        grid.appendChild(row);
      });
      body.appendChild(grid);
    }
    if (g.note) body.appendChild(el('p', { class: 'subtle small', text: g.note }));
    sec.appendChild(body);
    root.appendChild(sec);
  });
  return root;
}

// ---------- modal entry points ----------------------------------
// openFormModal: open a modal with the given groups + submit handler.
export async function openFormModal({
  title, size = 'lg', groups, values = {},
  submitLabel = 'Save', cancelLabel = 'Cancel',
  secondaryLabel, secondaryValue,
  onSubmit, onClose,
  successMessage, errorMessage,
}) {
  // Lazy import to avoid circular import with components.js.
  const compModule = await import('./components.js');
  const openModal = compModule.openModal;
  const closeOverlay = compModule.closeOverlay;

  const form = buildGroups(groups);
  const banner = el('div', { class: 'ff-form-error', hidden: 'hidden' });
  const grouped = el('div', null, [banner, form]);
  let submitting = false;
  let secondaryResolve;
  const secondaryPromise = secondaryValue
    ? new Promise((r) => { secondaryResolve = r; }) : null;

  openModal({
    title,
    size,
    dismissable: true,
    render: (body, foot) => {
      body.appendChild(grouped);
      const cancel = el('button', { type: 'button', class: 'btn ghost', text: cancelLabel,
        onclick: () => { closeOverlay(); if (onClose) onClose('cancel'); },
      });
      foot.appendChild(cancel);
      if (secondaryLabel && secondaryResolve) {
        const sec = el('button', { type: 'button', class: 'btn ghost', text: secondaryLabel,
          onclick: () => { secondaryResolve(secondaryValue); },
        });
        foot.appendChild(sec);
      }
      const submit = el('button', { type: 'button', class: 'btn primary', text: submitLabel });
      foot.appendChild(submit);
      submit.addEventListener('click', () => doSubmit());

      body.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && e.target && e.target.tagName !== 'TEXTAREA') {
          e.preventDefault();
          doSubmit();
        }
      });

      function doSubmit() {
        if (submitting) return;
        submitting = true;
        submit.setAttribute('disabled', 'disabled');
        submit.classList.add('ff-submitting');
        submit.innerHTML = '';
        submit.appendChild(el('span', { class: 'ff-spinner', 'aria-hidden': 'true' }));
        submit.appendChild(document.createTextNode(' ' + submitLabel + '…'));
        // Clear any previous state
        form.querySelectorAll('.ff-row').forEach(clearFieldError);
        setFormError(banner, null);
        const values = readForm(body);
        const fieldErrors = validate(groups, values);
        if (fieldErrors.length) {
          let firstRow = null;
          fieldErrors.forEach((e) => {
            const row = form.querySelector('[data-ff-field="' + e.field + '"]');
            if (row) {
              showFieldError(row, e.msg);
              if (!firstRow) firstRow = row;
            }
          });
          setFormError(banner, 'Please fix the highlighted fields and try again.');
          if (firstRow) firstRow.scrollIntoView({ block: 'center', behavior: 'smooth' });
          reset();
          return;
        }
        Promise.resolve()
          .then(() => onSubmit ? onSubmit(values) : null)
          .then((res) => {
            submitting = false;
            submit.removeAttribute('disabled');
            submit.classList.remove('ff-submitting');
            submit.textContent = submitLabel;
            if (res && res.ok === false) {
              setFormError(banner, res.error || errorMessage || 'Save failed.');
              return;
            }
            const msg = (res && res.successMessage) || successMessage || 'Saved.';
            toast(msg, 'success', 1800);
            closeOverlay();
            if (onClose) onClose('save', res);
            if (res && typeof res.reload === 'function') res.reload();
          })
          .catch((err) => {
            submitting = false;
            submit.removeAttribute('disabled');
            submit.classList.remove('ff-submitting');
            submit.textContent = submitLabel;
            const msg = (err && (err.detail || err.message)) || (errorMessage || 'Save failed.');
            setFormError(banner, msg);
          });
      }
      function reset() {
        submitting = false;
        submit.removeAttribute('disabled');
        submit.classList.remove('ff-submitting');
        submit.textContent = submitLabel;
      }
    },
  });
  if (secondaryPromise) {
    secondaryPromise.then((v) => { closeOverlay(); if (onClose) onClose('secondary', v); });
  }
}

// openFormDrawer: same shape, but renders inside a right-side drawer
// suited to wide forms (e.g. acceptance rule builder).
export async function openFormDrawer(opts) {
  const compModule = await import('./components.js');
  const openDrawer = compModule.openDrawer;
  const closeOverlay = compModule.closeOverlay;
  const { title, eyebrow, groups, values, submitLabel, onSubmit, onClose, successMessage } = opts;
  const form = buildGroups(groups);
  const banner = el('div', { class: 'ff-form-error', hidden: 'hidden' });
  let submitting = false;
  openDrawer({
    title, eyebrow,
    render: (body) => {
      const head = el('header', { class: 'drawer-foot-head' }, [
        banner,
      ]);
      body.appendChild(head);
      body.appendChild(form);
      const foot = el('footer', { class: 'drawer-foot' });
      body.appendChild(foot);
      const cancel = el('button', { type: 'button', class: 'btn ghost', text: opts.cancelLabel || 'Cancel',
        onclick: () => { closeOverlay(); if (onClose) onClose('cancel'); } });
      foot.appendChild(cancel);
      const submit = el('button', { type: 'button', class: 'btn primary', text: submitLabel || 'Save' });
      foot.appendChild(submit);
      submit.addEventListener('click', () => doSubmit());

      body.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && e.target && e.target.tagName !== 'TEXTAREA') {
          e.preventDefault();
          doSubmit();
        }
      });

      function doSubmit() {
        if (submitting) return;
        submitting = true;
        submit.setAttribute('disabled', 'disabled');
        submit.classList.add('ff-submitting');
        submit.textContent = (submitLabel || 'Save') + '…';
        form.querySelectorAll('.ff-row').forEach(clearFieldError);
        setFormError(banner, null);
        const vals = readForm(body);
        const fieldErrors = validate(groups, vals);
        if (fieldErrors.length) {
          let firstRow = null;
          fieldErrors.forEach((e) => {
            const row = form.querySelector('[data-ff-field="' + e.field + '"]');
            if (row) showFieldError(row, e.msg);
            if (!firstRow) firstRow = row;
          });
          setFormError(banner, 'Please fix the highlighted fields and try again.');
          if (firstRow) firstRow.scrollIntoView({ block: 'center', behavior: 'smooth' });
          submitting = false; submit.removeAttribute('disabled'); submit.classList.remove('ff-submitting'); submit.textContent = submitLabel || 'Save';
          return;
        }
        Promise.resolve()
          .then(() => onSubmit ? onSubmit(vals) : null)
          .then((res) => {
            submitting = false; submit.removeAttribute('disabled'); submit.classList.remove('ff-submitting'); submit.textContent = submitLabel || 'Save';
            if (res && res.ok === false) { setFormError(banner, res.error || 'Save failed.'); return; }
            const msg = (res && res.successMessage) || successMessage || 'Saved.';
            toast(msg, 'success', 1800);
            closeOverlay();
            if (onClose) onClose('save', res);
            if (res && typeof res.reload === 'function') res.reload();
          })
          .catch((err) => {
            submitting = false; submit.removeAttribute('disabled'); submit.classList.remove('ff-submitting'); submit.textContent = submitLabel || 'Save';
            setFormError(banner, (err && (err.detail || err.message)) || 'Save failed.');
          });
      }
    },
  });
}

// Tiny inline form helper for filter rows / toolbars (no modal).
export function buildInlineForm({ groups }) {
  const form = buildGroups(groups);
  return form;
}

export const _formInternals = {
  buildField, buildGroups, validate, readForm,
  showFieldError, clearFieldError, setFormError,
};
