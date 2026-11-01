// A small code editor: a textarea over a highlighted <pre>, with line numbers,
// completion, hover documentation and context help for osb configurations.
// Uses window.osbLang (osb-lang.js).

const L = () => window.osbLang;

export class Editor {
  /**
   * host: element to fill. opts: {value, language, lang (osbLang.Lang), help (element for
   * context help), onChange(value), minHeight}
   */
  constructor(host, opts = {}) {
    this.opts = opts;
    this.language = opts.language || 'yaml';
    this.lang = opts.lang || null;
    this.errorLine = 0;
    host.classList.add('ed');
    this.view = document.createElement('div');
    this.view.className = 'ed-view';
    if (opts.minHeight) this.view.style.height = opts.minHeight + 'px';
    this.gutter = document.createElement('pre');
    this.gutter.className = 'ed-gutter';
    this.gutter.setAttribute('aria-hidden', 'true');
    this.hl = document.createElement('pre');
    this.hl.className = 'ed-hl';
    this.hl.setAttribute('aria-hidden', 'true');
    this.ta = document.createElement('textarea');
    this.ta.className = 'ed-input';
    this.ta.spellcheck = false;
    this.ta.setAttribute('autocapitalize', 'off');
    this.ta.setAttribute('autocomplete', 'off');
    this.ta.setAttribute('aria-label', opts.label || 'Editor');
    this.ta.setAttribute('aria-autocomplete', 'list');
    this.view.append(this.gutter, this.hl, this.ta);
    this.popup = document.createElement('div');
    this.popup.className = 'ed-complete';
    this.popup.hidden = true;
    this.popup.setAttribute('role', 'listbox');
    this.tip = document.createElement('div');
    this.tip.className = 'ed-tip doc';
    this.tip.hidden = true;
    host.append(this.view, this.popup, this.tip);
    this.measure = document.createElement('span');
    this.measure.className = 'ed-measure';
    this.measure.textContent = 'x'.repeat(100);
    host.append(this.measure);

    this.ta.value = opts.value || '';
    this.ta.addEventListener('input', (e) => this.onInput(e));
    this.ta.addEventListener('scroll', () => this.sync());
    this.ta.addEventListener('keydown', (e) => this.onKey(e));
    this.ta.addEventListener('blur', () => setTimeout(() => this.close(), 150));
    this.ta.addEventListener('click', () => { this.close(); this.updateHelp(); });
    this.ta.addEventListener('keyup', (e) => { if (/Arrow|Home|End|Page/.test(e.key)) this.updateHelp(); });
    this.ta.addEventListener('mousemove', (e) => this.onHover(e));
    this.ta.addEventListener('mouseleave', () => { clearTimeout(this.hoverTimer); this.tip.hidden = true; });
    new ResizeObserver(() => this.sync()).observe(this.view);
    this.render();
  }

  get value() { return this.ta.value; }

  set value(v) {
    this.ta.value = v;
    this.ta.scrollTop = 0;
    this.render();
    this.updateHelp();
  }

  setLanguage(language) {
    this.language = language;
    this.render();
  }

  setLang(lang) {
    this.lang = lang;
    this.updateHelp();
  }

  focus() { this.ta.focus(); }

  markError(line) {
    this.errorLine = line || 0;
    this.renderGutter();
  }

  gotoLine(line) {
    const lines = this.ta.value.split('\n');
    let start = 0;
    for (let i = 0; i < line - 1 && i < lines.length; i++) start += lines[i].length + 1;
    this.ta.focus();
    this.ta.setSelectionRange(start, start + (lines[line - 1] || '').length);
    this.ta.scrollTop = Math.max(0, (line - 5) * this.lineHeight());
    this.updateHelp();
  }

  // --- rendering -------------------------------------------------------------

  render() {
    const html = L() ? L().highlight(this.ta.value, this.language) : escapeHTML(this.ta.value);
    // A trailing newline keeps the heights of <pre> and <textarea> equal.
    this.hl.innerHTML = html + '\n ';
    this.renderGutter();
    this.sync();
  }

  renderGutter() {
    const n = this.ta.value.split('\n').length;
    let s = '';
    for (let i = 1; i <= n; i++) s += i === this.errorLine ? `<span class="err">${i}</span>\n` : i + '\n';
    this.gutter.innerHTML = s + ' ';
  }

  sync() {
    this.hl.scrollTop = this.ta.scrollTop;
    this.hl.scrollLeft = this.ta.scrollLeft;
    this.gutter.scrollTop = this.ta.scrollTop;
  }

  lineHeight() {
    return parseFloat(getComputedStyle(this.ta).lineHeight) || 20;
  }

  charWidth() {
    return this.measure.getBoundingClientRect().width / 100 || 8;
  }

  /** Pixel position of an offset relative to the editor host. */
  coords(offset) {
    const before = this.ta.value.slice(0, offset);
    const row = before.split('\n').length - 1;
    const col = before.length - before.lastIndexOf('\n') - 1;
    const cs = getComputedStyle(this.ta);
    const x = this.ta.offsetLeft + parseFloat(cs.paddingLeft) + col * this.charWidth() - this.ta.scrollLeft;
    const y = this.ta.offsetTop + parseFloat(cs.paddingTop) + row * this.lineHeight() - this.ta.scrollTop;
    return { x, y };
  }

  /** Offset at a mouse position, or -1. */
  offsetAt(e) {
    const r = this.ta.getBoundingClientRect();
    const cs = getComputedStyle(this.ta);
    const x = e.clientX - r.left - parseFloat(cs.paddingLeft) + this.ta.scrollLeft;
    const y = e.clientY - r.top - parseFloat(cs.paddingTop) + this.ta.scrollTop;
    const row = Math.floor(y / this.lineHeight());
    const col = Math.floor(x / this.charWidth());
    const lines = this.ta.value.split('\n');
    if (row < 0 || row >= lines.length || col < 0 || col > lines[row].length) return -1;
    let off = 0;
    for (let i = 0; i < row; i++) off += lines[i].length + 1;
    return off + col;
  }

  // --- editing ---------------------------------------------------------------

  /** Scrolls the textarea so that the caret is visible. */
  revealCaret() {
    const pos = this.ta.selectionStart;
    const before = this.ta.value.slice(0, pos);
    const row = before.split('\n').length - 1;
    const col = before.length - before.lastIndexOf('\n') - 1;
    const lh = this.lineHeight();
    const cs = getComputedStyle(this.ta);
    const top = row * lh;
    const visible = this.ta.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
    if (top < this.ta.scrollTop) this.ta.scrollTop = top;
    else if (top + lh > this.ta.scrollTop + visible) this.ta.scrollTop = top + lh - visible;
    const x = col * this.charWidth();
    const width = this.ta.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    if (x < this.ta.scrollLeft) this.ta.scrollLeft = Math.max(0, x - 20);
    else if (x > this.ta.scrollLeft + width) this.ta.scrollLeft = x - width + 20;
    this.sync();
  }

  onInput(e) {
    this.render();
    this.revealCaret();
    this.opts.onChange?.(this.ta.value);
    if (this.language !== 'yaml' || !this.lang || this.accepting) return;
    const pos = this.ta.selectionStart;
    const ch = this.ta.value[pos - 1] || '';
    if (e.inputType === 'insertText' && /[A-Za-z0-9_-]/.test(ch)) this.suggest(false);
    else if (e.inputType === 'insertText' && (ch === ' ' || ch === '[') && /(:|- |\[|, )\s*$/.test(this.ta.value.slice(Math.max(0, pos - 3), pos))) this.suggest(false, true);
    else if (!this.popup.hidden) this.suggest(false);
  }

  replace(from, to, text) {
    this.ta.focus();
    this.ta.setSelectionRange(from, to);
    // execCommand keeps the native undo history where it is supported.
    if (!document.execCommand || !document.execCommand('insertText', false, text)) {
      this.ta.setRangeText(text, from, to, 'end');
      this.ta.dispatchEvent(new Event('input'));
    }
  }

  onKey(e) {
    if (!this.popup.hidden) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        this.select(this.sel + (e.key === 'ArrowDown' ? 1 : -1));
        return;
      }
      const it = this.res.items[this.sel];
      const typed = this.ta.value.slice(this.res.from, this.ta.selectionStart);
      if (e.key === 'Enter' && it && typed === it.label) {
        // already complete: Enter is a newline
        this.close();
      } else if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        this.accept(this.sel);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        this.close();
        return;
      }
    }
    if ((e.ctrlKey || e.metaKey) && e.key === ' ') {
      e.preventDefault();
      this.suggest(true);
      return;
    }
    if (e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      const { selectionStart: s, selectionEnd: t, value } = this.ta;
      if (s === t && !e.shiftKey) {
        this.replace(s, t, '  ');
        return;
      }
      // indent / outdent the selected lines
      const start = value.lastIndexOf('\n', s - 1) + 1;
      const block = value.slice(start, t);
      const out = e.shiftKey ? block.replace(/^ {1,2}/gm, '') : block.replace(/^/gm, '  ');
      this.replace(start, t, out);
      this.ta.setSelectionRange(start, start + out.length);
      return;
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      // keep the indentation; indent after "key:" and continue list items
      const { selectionStart: s, selectionEnd: t, value } = this.ta;
      const lineStart = value.lastIndexOf('\n', s - 1) + 1;
      const line = value.slice(lineStart, s);
      let indent = line.match(/^\s*/)[0];
      const dash = line.match(/^(\s*)- /);
      if (dash) indent = dash[1] + '  ';
      if (/:\s*(\|[-+]?|>[-+]?)?\s*$/.test(line) && !/^\s*#/.test(line)) indent += '  ';
      e.preventDefault();
      this.replace(s, t, '\n' + indent);
    }
  }

  // --- completion ------------------------------------------------------------

  suggest(manual, all = false) {
    if (!this.lang || this.language !== 'yaml') return;
    const pos = this.ta.selectionStart;
    const res = this.lang.complete(this.ta.value, pos, { all: manual || all });
    if (!res || (!manual && !all && pos - res.from < 1)) {
      this.close();
      return;
    }
    // Do not offer what is already typed completely
    const typed = this.ta.value.slice(res.from, res.to);
    if (!manual && res.items.length === 1 && res.items[0].label === typed) {
      this.close();
      return;
    }
    this.res = res;
    this.popup.replaceChildren();
    this.list = document.createElement('ul');
    this.doc = document.createElement('div');
    this.doc.className = 'ed-complete-doc doc';
    res.items.slice(0, 80).forEach((it, i) => {
      const li = document.createElement('li');
      li.setAttribute('role', 'option');
      li.innerHTML = `<span class="ed-k ed-k-${it.kind}"></span><b>${escapeHTML(it.label)}</b><small>${escapeHTML(it.detail || '')}</small>`;
      li.addEventListener('mousedown', (e) => { e.preventDefault(); this.accept(i); });
      this.list.append(li);
    });
    this.popup.append(this.list, this.doc);
    const c = this.coords(res.from);
    this.popup.hidden = false;
    const hostW = this.popup.parentElement.clientWidth;
    this.popup.style.left = Math.max(0, Math.min(c.x, hostW - this.popup.offsetWidth)) + 'px';
    this.popup.style.top = c.y + this.lineHeight() + 2 + 'px';
    this.select(0);
  }

  select(i) {
    const n = this.list.children.length;
    if (!n) return;
    this.sel = (i + n) % n;
    [...this.list.children].forEach((li, k) => li.setAttribute('aria-selected', String(k === this.sel)));
    this.list.children[this.sel].scrollIntoView({ block: 'nearest' });
    const it = this.res.items[this.sel];
    this.doc.innerHTML = it.doc ? L().markdownToHTML(it.doc) : '';
    this.doc.hidden = !it.doc;
  }

  accept(i) {
    const it = this.res.items[i];
    if (!it) return;
    this.close();
    let text = it.insert || it.label;
    if (text.endsWith(':')) {
      // a key that opens a nested block: newline with deeper indentation
      const lineStart = this.ta.value.lastIndexOf('\n', this.res.from - 1) + 1;
      const indent = this.ta.value.slice(lineStart, this.res.from).replace(/\S/g, ' ');
      text += '\n' + indent + '  ';
    }
    this.accepting = true;
    this.replace(this.res.from, this.res.to, text);
    this.accepting = false;
    this.updateHelp();
    if (/: $/.test(text)) setTimeout(() => this.suggest(false, true), 0);
  }

  close() {
    this.popup.hidden = true;
  }

  // --- documentation ---------------------------------------------------------

  onHover(e) {
    clearTimeout(this.hoverTimer);
    if (!this.lang || this.language !== 'yaml') return;
    this.hoverTimer = setTimeout(() => {
      const off = this.offsetAt(e);
      const h = off >= 0 ? this.lang.hover(this.ta.value, off) : null;
      if (!h) {
        this.tip.hidden = true;
        return;
      }
      this.tip.innerHTML = L().markdownToHTML(h.markdown);
      this.tip.hidden = false;
      const c = this.coords(h.from);
      const hostW = this.tip.parentElement.clientWidth;
      this.tip.style.left = Math.max(0, Math.min(c.x, hostW - this.tip.offsetWidth - 4)) + 'px';
      this.tip.style.top = c.y + this.lineHeight() + 4 + 'px';
    }, 350);
  }

  updateHelp() {
    const help = this.opts.help;
    if (!help) return;
    if (!this.lang || this.language !== 'yaml') {
      help.hidden = true;
      return;
    }
    const act = this.lang.stepAction(this.ta.value, this.ta.selectionStart);
    help.hidden = !act;
    if (act && help.dataset.action !== act.name) {
      help.dataset.action = act.name;
      help.innerHTML = L().markdownToHTML(L().actionMarkdown(act));
    }
  }
}

function escapeHTML(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
