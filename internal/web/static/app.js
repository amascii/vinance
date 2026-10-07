// vinance: small helpers on top of htmx.

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

// ---- the entry form ----

const entryForm = () => document.getElementById('qa-form');
const pad = (n) => String(n).padStart(2, '0');

// A click anywhere on the date field opens the calendar (no typing needed).
document.addEventListener('click', (evt) => {
  if (evt.target.id === 'qa-date' && typeof evt.target.showPicker === 'function') {
    try { evt.target.showPicker(); } catch (_) { /* needs a user gesture; the field still works by keyboard */ }
  }
});
// The amount's placeholder names the currency of the account being used.
function entryKind() {
  const select = $('select[name=kind]', entryForm() || document);
  return select ? select.value : 'expense';
}

function accountInfo(select) {
  const option = select && select.selectedOptions && select.selectedOptions[0];
  return option ? { name: option.dataset.name || option.textContent.trim(), currency: option.dataset.currency || '' } : null;
}

function refreshEntry() {
  const form = entryForm();
  if (!form) return;
  const kind = entryKind();
  const scoped = form.dataset.scopeName !== undefined;
  const main = scoped ? { name: form.dataset.scopeName, currency: form.dataset.scopeCurrency } : accountInfo(document.getElementById('qa-account'));
  const other = accountInfo(document.getElementById('qa-to'));
  const amount = document.getElementById('qa-amount');
  if (amount) amount.placeholder = main && main.currency ? `Amount ${main.currency}` : 'Amount';
}

document.addEventListener('change', (evt) => {
  if (!evt.target.closest || !evt.target.closest('#qa-form')) return;
  if (evt.target.id === 'qa-account' && evt.isTrusted) entryForm().dataset.accountTouched = '1'; // the person chose it
  refreshEntry();
});

// ---- past-transaction autocomplete ----
//
// After a few letters of a description, matching past transactions are listed. Tab (or a tap) fills
// the form from one and selects the amount so typing overwrites it. Up/Down choose, Enter accepts a
// chosen one (otherwise it saves), Escape dismisses.
let activeSuggestion = -1;

const suggestionButtons = () => $$('#qa-desc-suggestions .desc-suggestion');

function highlightSuggestion(index) {
  const buttons = suggestionButtons();
  activeSuggestion = buttons.length ? Math.max(-1, Math.min(index, buttons.length - 1)) : -1;
  buttons.forEach((b, i) => {
    const on = i === activeSuggestion;
    b.classList.toggle('active', on);
    b.setAttribute('aria-selected', on ? 'true' : 'false');
  });
}

function setSelectValue(select, value) {
  if (select && value && Array.from(select.options).some((o) => o.value === value)) select.value = value;
}

function setChoice(name, value) {
  setSelectValue($(`select[name=${name}]`, entryForm()), value);
}

function acceptSuggestion(btn) {
  const form = entryForm();
  if (!form || !btn) return;
  const d = btn.dataset;
  document.getElementById('qa-description').value = d.description;
  if (d.kind) setChoice('kind', d.kind);
  // The remembered account is replaced by the one this description used before, but an account the
  // person chose themselves is respected.
  if (d.account && !form.dataset.accountTouched) setSelectValue(document.getElementById('qa-account'), d.account);
  if (d.to) setSelectValue(document.getElementById('qa-to'), d.to);
  if (d.direction) setChoice('direction', d.direction);
  const tags = document.getElementById('qa-tags');
  if (d.tags && tags && !tags.value.trim()) tags.value = d.tags.split(' ').map((t) => '#' + t).join(' ');
  const amount = document.getElementById('qa-amount');
  if (d.amount && amount) amount.value = d.amount;
  document.getElementById('qa-desc-suggestions').innerHTML = '';
  activeSuggestion = -1;
  refreshEntry();
  if (amount) { amount.focus(); amount.select(); }
}

document.addEventListener('click', (evt) => {
  const btn = evt.target.closest('.desc-suggestion');
  if (btn) acceptSuggestion(btn);
});

document.addEventListener('keydown', (evt) => {
  if (!evt.target || evt.target.id !== 'qa-description') return;
  const buttons = suggestionButtons();
  if (!buttons.length) return;
  switch (evt.key) {
    case 'Tab':
      if (evt.shiftKey || evt.ctrlKey || evt.metaKey || evt.altKey) return;
      evt.preventDefault();
      acceptSuggestion(buttons[Math.max(activeSuggestion, 0)]);
      break;
    case 'ArrowDown':
      evt.preventDefault();
      highlightSuggestion(activeSuggestion + 1);
      break;
    case 'ArrowUp':
      evt.preventDefault();
      highlightSuggestion(activeSuggestion - 1);
      break;
    case 'Enter':
      if (activeSuggestion >= 0) {
        evt.preventDefault();
        acceptSuggestion(buttons[activeSuggestion]);
      }
      break;
    case 'Escape':
      document.getElementById('qa-desc-suggestions').innerHTML = '';
      activeSuggestion = -1;
      break;
  }
});

// Moving on to any other field without accepting a suggestion dismisses the list (it would otherwise
// sit between the amount and the tag buttons). A response still in flight is dropped too.
document.addEventListener('focusin', (evt) => {
  const t = evt.target;
  if (!t || t.id === 'qa-description' || t.closest('#qa-desc-suggestions')) return;
  if (!t.matches('input, select, textarea') || !t.closest('.entry-form')) return;
  document.getElementById('qa-desc-suggestions').innerHTML = '';
  activeSuggestion = -1;
});
document.addEventListener('htmx:beforeSwap', (evt) => {
  const target = evt.detail.target;
  if (target && target.id === 'qa-desc-suggestions' && document.activeElement && document.activeElement.id !== 'qa-description') {
    evt.detail.shouldSwap = false;
  }
});

// A fresh suggestion list resets the highlight.
document.addEventListener('htmx:afterSwap', (evt) => {
  if (evt.detail.target && evt.detail.target.id === 'qa-desc-suggestions') activeSuggestion = -1;
});

// ---- tag suggestions ----

// The server needs the caret position in the tags field to know which word is being typed.
document.addEventListener('htmx:configRequest', (evt) => {
  const el = evt.detail.elt;
  if (el && el.matches && el.matches('[data-tag-input]')) {
    evt.detail.parameters['tags'] = el.value; // the field's own name differs on editor lines (r-N-tags)
    evt.detail.parameters['pos'] = el.selectionStart ?? el.value.length;
  }
});

// Clicking a tag suggestion replaces the word under the caret in the field its list belongs to
// (the entry form's, or one editor line's).
function acceptTag(btn) {
  const owner = btn && btn.closest('[data-tags-for]');
  const input = owner && document.getElementById(owner.dataset.tagsFor);
  if (!btn || !input) return;
  const pos = input.selectionStart ?? input.value.length;
  const before = input.value.slice(0, pos);
  const after = input.value.slice(pos);
  const start = before.search(/[^\s,]+$/);
  const head = start < 0 ? before : before.slice(0, start);
  const tail = after.replace(/^[^\s,]*/, '').replace(/^[\s,]+/, '');
  input.value = head + '#' + btn.dataset.tag + ' ' + tail;
  const caret = (head + '#' + btn.dataset.tag + ' ').length;
  input.focus();
  input.setSelectionRange(caret, caret);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

document.addEventListener('click', (evt) => acceptTag(evt.target.closest('.tag-suggestion')));

// Tab completes the word under the caret when the list holds exactly one suggestion; otherwise Tab moves on.
document.addEventListener('keydown', (evt) => {
  if (evt.key !== 'Tab' || evt.shiftKey || evt.ctrlKey || evt.metaKey || evt.altKey) return;
  const input = evt.target;
  if (!input || !input.matches || !input.matches('[data-tag-input]') || !input.id) return;
  const only = $$(`[data-tags-for="${input.id}"] .tag-suggestion`);
  if (only.length !== 1) return;
  evt.preventDefault();
  acceptTag(only[0]);
});

// ---- focus and setup ----

// When the form appears (page load, or swapped in after an add) show what it will do and start in
// the description: the account and date are already filled in from the last entry.
function initEntry() {
  if (!entryForm()) return;
  refreshEntry();
  const description = document.getElementById('qa-description');
  if (description) description.focus();
}
document.addEventListener('htmx:load', (evt) => {
  const el = evt.detail && evt.detail.elt;
  if (el && (el.id === 'qa-form' || (el.querySelector && el.querySelector('#qa-form')))) initEntry();
});

// ---- bulk tag editing: tick rows, then act on the ticked ones ----
//
// The bar is part of the swapped results, so a new filter/search replaces it (and the rows) and the
// selection starts over. "Load more" only appends rows, so earlier ticks stay.
const bulkForm = () => document.getElementById('bulk');

function refreshBulk() {
  const form = bulkForm();
  if (!form) return;
  const picks = $$('.pick');
  const ticked = picks.filter((p) => p.checked).length;
  const all = form.elements.scope.value === 'all';
  const total = Number(form.dataset.count || 0);
  document.getElementById('bulk-count').textContent = all ? `All ${total.toLocaleString('en-US')} matching selected` : `${ticked} selected`;
  form.hidden = !(ticked > 0 || all);
  const allBtn = document.getElementById('bulk-all');
  if (allBtn) allBtn.hidden = all || total <= ticked;
}

document.addEventListener('change', (evt) => {
  if (!evt.target.classList || !evt.target.classList.contains('pick')) return;
  const form = bulkForm();
  if (form && !evt.target.checked) form.elements.scope.value = ''; // no longer "everything"
  refreshBulk();
});

document.addEventListener('click', (evt) => {
  if (!evt.target.closest('#bulk-all')) return;
  bulkForm().elements.scope.value = 'all';
  $$('.pick').forEach((p) => { p.checked = true; });
  refreshBulk();
});

document.addEventListener('htmx:afterSwap', () => {
  const form = bulkForm();
  if (form && form.elements.scope.value === 'all') $$('.pick').forEach((p) => { p.checked = true; });
  refreshBulk();
});
window.addEventListener('pageshow', refreshBulk); // Back restores ticked boxes

// Opening an account's edit form (the pencil) focuses its name with the text selected, so typing replaces it.
// A click handler rather than "toggle", so a form re-opened by a validation error does not steal focus.
document.addEventListener('click', (evt) => {
  const summary = evt.target.closest('ul.account-list > li > details > summary');
  if (!summary) return;
  const details = summary.parentElement;
  setTimeout(() => {
    if (!details.open) return;
    const name = details.querySelector('input[name=name]');
    if (name) { name.focus(); name.select(); }
  }, 0);
});
