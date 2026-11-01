/*
 * osb language support: syntax highlighting (YAML, shell, Dockerfile, JSON)
 * and context-aware completion and documentation for osb configurations.
 *
 * Shared by the website (window.osbLang) and the VS Code extension
 * (require('./osb-lang')). No dependencies.
 *
 * The catalog is the output of `osb actions list -o json` or osb.catalog() in
 * the browser: {actions: [...], flavors: [...], targets: [...], schema: "..."}.
 */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.osbLang = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // ---------------------------------------------------------------------------
  // Highlighting. Tokens are spans with the classes:
  //   tc comment, tk key/keyword, ts string, tn number/bool, te ${{ }} expression,
  //   tv variable, ti Dockerfile instruction, tp punctuation (list dash, |)

  function esc(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }
  function span(cls, s) {
    return s ? '<span class="' + cls + '">' + esc(s) + '</span>' : '';
  }

  // Highlights ${{ }} expressions inside an already selected text segment.
  function withExpr(s, cls) {
    var out = '';
    var re = /\$\{\{[\s\S]*?\}\}/g;
    var last = 0;
    var m;
    while ((m = re.exec(s))) {
      out += cls ? span(cls, s.slice(last, m.index)) : esc(s.slice(last, m.index));
      out += span('te', m[0]);
      last = m.index + m[0].length;
    }
    out += cls ? span(cls, s.slice(last)) : esc(s.slice(last));
    return out;
  }

  // Splits a YAML value into value and trailing comment.
  function splitComment(v) {
    var q = null;
    for (var i = 0; i < v.length; i++) {
      var c = v[i];
      if (q) {
        if (c === q && v[i - 1] !== '\\') q = null;
      } else if (c === '"' || c === "'") {
        if (i === 0 || /[\s[{,:]/.test(v[i - 1])) q = c;
      } else if (c === '#' && (i === 0 || /\s/.test(v[i - 1]))) {
        return [v.slice(0, i), v.slice(i)];
      }
    }
    return [v, ''];
  }

  function yamlValue(v) {
    var parts = splitComment(v);
    var val = parts[0];
    var out = '';
    var lead = val.match(/^\s*/)[0];
    var core = val.slice(lead.length);
    var trail = core.match(/\s*$/)[0];
    core = core.slice(0, core.length - trail.length);
    out += esc(lead);
    if (/^(["']).*\1$/.test(core)) out += withExpr(core, 'ts');
    else if (/^(true|false|null|~|yes|no|-?\d+(\.\d+)?|0[0-7]+|0x[0-9a-f]+)$/i.test(core)) out += span('tn', core);
    else if (/^[|>][-+0-9]*$/.test(core)) out += span('tp', core);
    else if (/^[[{]/.test(core)) out += flowValue(core);
    else out += withExpr(core, '');
    out += esc(trail) + span('tc', parts[1]);
    return out;
  }

  // Flow collections: [a, b] and {k: v}
  function flowValue(s) {
    var out = '';
    var re = /("(?:[^"\\]|\\.)*"|'[^']*'|\$\{\{[\s\S]*?\}\}|[A-Za-z0-9_.\/@-]+(?=\s*:\s)|[[\]{},:]|[^[\]{},:"'$]+|\$)/g;
    var m;
    while ((m = re.exec(s))) {
      var t = m[0];
      if (/^["']/.test(t)) out += withExpr(t, 'ts');
      else if (/^\$\{\{/.test(t)) out += span('te', t);
      else if (/^[[\]{},:]$/.test(t)) out += span('tp', t);
      else if (re.lastIndex < s.length && /^\s*:\s/.test(s.slice(re.lastIndex)) && /^[A-Za-z0-9_.\/@-]+$/.test(t)) out += span('tk', t);
      else if (/^\s*(true|false|null|-?\d+(\.\d+)?)\s*$/.test(t)) out += span('tn', t);
      else out += withExpr(t, '');
    }
    return out;
  }

  function highlightYAML(text) {
    var lines = text.split('\n');
    var block = -1; // indent of the key that started a block scalar
    return lines.map(function (line) {
      var indent = line.match(/^\s*/)[0].length;
      if (block >= 0) {
        if (line.trim() === '' || indent > block) return withExpr(line, '');
        block = -1;
      }
      if (/^\s*#/.test(line)) return span('tc', line);
      if (/^(---|\.\.\.)\s*$/.test(line)) return span('tp', line);
      var m = line.match(/^(\s*)((?:-\s+)*)([^\s#'"][^:#]*?|"[^"]*"|'[^']*')(\s*:)(?=\s|$)(.*)$/);
      if (m) {
        var keyIndent = m[1].length + m[2].length;
        if (/^\s*[|>][-+0-9]*\s*(#.*)?$/.test(m[5])) block = keyIndent;
        return esc(m[1]) + span('tp', m[2]) + span('tk', m[3]) + span('tp', m[4]) + yamlValue(m[5]);
      }
      m = line.match(/^(\s*)((?:-\s+)+|-$)(.*)$/);
      if (m) return esc(m[1]) + span('tp', m[2]) + yamlValue(m[3]);
      return yamlValue(line);
    }).join('\n');
  }

  var SH_KEYWORDS = /^(if|then|else|elif|fi|for|in|do|done|while|until|case|esac|function|return|exit|local|export|set|unset|shift|break|continue)$/;

  function highlightShellLine(line, state) {
    // state.q: open quote (multi-line strings); state.heredoc: end delimiter
    if (state.heredoc) {
      if (line.replace(/^\t+/, '') === state.heredoc) {
        state.heredoc = null;
        return span('tp', line);
      }
      return span('ts', line);
    }
    var out = '';
    var i = 0;
    var word = '';
    function flushWord() {
      if (!word) return;
      if (SH_KEYWORDS.test(word)) out += span('tk', word);
      else if (/^-?\d+$/.test(word)) out += span('tn', word);
      else out += esc(word);
      word = '';
    }
    if (state.q) {
      var end = line.indexOf(state.q);
      if (end < 0) return span('ts', line);
      out += span('ts', line.slice(0, end + 1));
      i = end + 1;
      state.q = null;
    }
    while (i < line.length) {
      var c = line[i];
      var rest = line.slice(i);
      var m;
      if (c === '#' && (i === 0 || /[\s;]/.test(line[i - 1]))) {
        flushWord();
        out += span('tc', rest);
        break;
      }
      if (c === "'" || c === '"') {
        flushWord();
        var j = c === "'" ? line.indexOf("'", i + 1) : findDQ(line, i + 1);
        if (j < 0) {
          out += span('ts', rest);
          state.q = c;
          break;
        }
        var str = line.slice(i, j + 1);
        out += c === '"' ? '<span class="ts">' + str.split(/(\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*)/).map(function (p, k) {
          return k % 2 ? span('tv', p) : esc(p);
        }).join('') + '</span>' : span('ts', str);
        i = j + 1;
        continue;
      }
      if ((m = rest.match(/^\$\{\{[\s\S]*?\}\}/))) {
        flushWord();
        out += span('te', m[0]);
        i += m[0].length;
        continue;
      }
      if ((m = rest.match(/^\$(\{[^}]*\}|[A-Za-z_][A-Za-z0-9_]*|[0-9@#?*$!-]|\()/))) {
        flushWord();
        out += span('tv', m[0]);
        i += m[0].length;
        continue;
      }
      if ((m = rest.match(/^<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)\1/))) {
        flushWord();
        out += span('tp', m[0]);
        state.heredoc = m[2];
        i += m[0].length;
        continue;
      }
      if (/[\s;&|()<>]/.test(c)) {
        flushWord();
        out += /[;&|]/.test(c) ? span('tp', c) : esc(c);
        i++;
        continue;
      }
      word += c;
      i++;
    }
    flushWord();
    return out;
  }

  function findDQ(line, from) {
    for (var i = from; i < line.length; i++) {
      if (line[i] === '\\') i++;
      else if (line[i] === '"') return i;
    }
    return -1;
  }

  function highlightShell(text) {
    var state = {};
    return text.split('\n').map(function (l) { return highlightShellLine(l, state); }).join('\n');
  }

  function highlightDockerfile(text) {
    var state = {};
    var cont = false; // continuation of a RUN line
    var heredoc = null; // {end, shell}
    return text.split('\n').map(function (line) {
      if (heredoc) {
        if (line === heredoc.end) {
          heredoc = null;
          return span('tp', line);
        }
        return heredoc.shell ? highlightShellLine(line, state) : span('ts', line);
      }
      if (/^\s*#/.test(line)) return span('tc', line);
      var m = line.match(/^(\s*)([A-Za-z]+)(\s+.*|$)/);
      if (!cont && m && /^(FROM|RUN|CMD|LABEL|EXPOSE|ENV|ADD|COPY|ENTRYPOINT|VOLUME|USER|WORKDIR|ARG|ONBUILD|STOPSIGNAL|HEALTHCHECK|SHELL|MAINTAINER)$/i.test(m[2])) {
        var ins = m[2].toUpperCase();
        var rest = m[3];
        var h = rest.match(/<<-?(['"]?)([A-Za-z_][A-Za-z0-9_]*)\1/);
        if (h) heredoc = { end: h[2], shell: ins === 'RUN' };
        cont = /\\\s*$/.test(line) && !h;
        var flags = rest.match(/^(\s+(?:--[a-z-]+(?:=\S+)?\s*)*)(.*)$/);
        var body = flags[2];
        var hl = ins === 'RUN' || ins === 'CMD' || ins === 'ENTRYPOINT' ? highlightShellLine(body, {}) : withExpr(body, ins === 'FROM' ? 'ts' : '');
        if (h && ins === 'RUN') state = {};
        return esc(m[1]) + span('ti', m[2]) + span('tv', flags[1]) + hl;
      }
      cont = /\\\s*$/.test(line);
      return highlightShellLine(line, {});
    }).join('\n');
  }

  function highlightJSON(text) {
    return esc(text).replace(/("(?:[^"\\]|\\.)*")(\s*:)?|\b(true|false|null)\b|-?\b\d+(\.\d+)?([eE][+-]?\d+)?\b/g, function (m, str, colon) {
      if (str) return colon ? '<span class="tk">' + str + '</span>' + colon : '<span class="ts">' + str + '</span>';
      return '<span class="tn">' + m + '</span>';
    });
  }

  /** Returns HTML for text in the given language. */
  function highlight(text, lang) {
    lang = (lang || '').toLowerCase();
    if (lang === 'yaml' || lang === 'yml') return highlightYAML(text);
    if (lang === 'dockerfile' || lang === 'docker') return highlightDockerfile(text);
    if (lang === 'json') return highlightJSON(text);
    if (/^(sh|bash|shell|zsh|console|powershell|ps1)$/.test(lang)) return highlightShell(text);
    return esc(text);
  }

  /** Guesses the language from a file name. */
  function languageOf(name) {
    if (/(^|\/)Dockerfile$|\.dockerfile$/i.test(name)) return 'dockerfile';
    if (/\.ya?ml$|(^|\/)(user-data|meta-data)$/.test(name)) return 'yaml';
    if (/\.json$/.test(name)) return 'json';
    if (/\.(sh|bash)$|(^|\/)run\.sh/.test(name)) return 'sh';
    return 'text';
  }

  // ---------------------------------------------------------------------------
  // YAML structure around a position (indentation based, tolerant of errors)

  function parseLine(line) {
    var m = line.match(/^(\s*)((?:-\s+)*)(-?)/);
    var indent = m[1].length;
    var dashes = (m[2].match(/-/g) || []).length + (m[3] ? 1 : 0);
    var keyIndent = indent + m[2].length + (m[3] ? 1 : 0);
    var rest = line.slice(keyIndent);
    var km = rest.match(/^([^\s#'"{}[\],][^:#]*?|"[^"]*"|'[^']*')\s*:(\s|$)/);
    return {
      indent: indent, dashes: dashes, keyIndent: keyIndent,
      key: km ? km[1].replace(/^["']|["']$/g, '') : null,
      value: km ? rest.slice(km[0].length) : rest,
      blank: line.trim() === '' || /^\s*#/.test(line),
    };
  }

  /**
   * Analyzes the position: returns
   *   path:   keys from the root to the mapping that contains the position,
   *           with '[]' for list items (e.g. ['steps', '[]', 'args'])
   *   key:    the key on the current line (if any), keyStart/keyEnd offsets
   *   inValue: whether the cursor is in the value part of the line
   *   prefix: the word before the cursor, from/to: its range
   *   item:   {start, end, indent} line range of the innermost list item
   */
  function analyze(text, offset) {
    var lines = text.split('\n');
    var pos = 0;
    var row = 0;
    while (row < lines.length - 1 && pos + lines[row].length < offset) {
      pos += lines[row].length + 1;
      row++;
    }
    var col = offset - pos;
    var line = lines[row];
    var p = parseLine(line);
    var before = line.slice(0, col);
    var colonAt = p.key !== null ? line.indexOf(':', p.keyIndent + p.key.length) : -1;
    var inValue = p.key !== null && col > colonAt;
    var wm = before.match(/[A-Za-z0-9_.@-]*$/);
    var prefix = wm[0];
    var from = offset - prefix.length;
    var am = line.slice(col).match(/^[A-Za-z0-9_.@-]*/);
    var to = offset + am[0].length;

    // Indentation of the current position (for blank lines: the cursor column)
    var curIndent = p.blank ? col : p.keyIndent;
    if (!p.blank && p.key === null && p.dashes === 0) curIndent = p.indent;
    // A "- " on the current line opens an item at p.indent
    var path = [];
    var itemRow = -1;
    var itemIndent = -1;
    if (!p.blank && p.dashes > 0) {
      itemRow = row;
      itemIndent = p.indent;
      path.unshift('[]');
      curIndent = p.indent;
    }
    for (var r = row - 1; r >= 0; r--) {
      var q = parseLine(lines[r]);
      if (q.blank) continue;
      if (q.dashes > 0 && q.indent < curIndent && q.keyIndent <= curIndent) {
        // we are inside this list item
        if (q.key !== null && q.keyIndent < curIndent && !(q.keyIndent === curIndent)) {
          // nested under a key of the item's first line ("- name:\n    x")
          if (q.keyIndent < curIndent && !(curIndent === q.keyIndent)) {
            if (curIndent > q.keyIndent) path.unshift(q.key);
          }
        }
        if (itemRow < 0) {
          itemRow = r;
          itemIndent = q.indent;
        }
        path.unshift('[]');
        curIndent = q.indent;
        continue;
      }
      if (q.key !== null && q.keyIndent < curIndent) {
        path.unshift(q.key);
        curIndent = q.keyIndent;
        if (curIndent === 0) break;
      }
    }
    // The innermost list item: lines that belong to it
    var item = null;
    if (itemRow >= 0) {
      var end = itemRow + 1;
      while (end < lines.length) {
        var e = parseLine(lines[end]);
        if (!e.blank && e.indent <= itemIndent) break;
        end++;
      }
      item = { start: itemRow, end: end, indent: itemIndent };
    }
    return {
      lines: lines, row: row, col: col, line: line, parsed: p, path: path,
      key: p.key, keyStart: pos + p.keyIndent, keyEnd: p.key !== null ? pos + p.keyIndent + p.key.length : -1,
      inValue: inValue, prefix: prefix, from: from, to: to, item: item,
      onKey: p.key === null ? !p.blank || true : !inValue,
    };
  }

  /** The keys (and their values) of a list item's mapping. */
  function itemKeys(a) {
    var out = {};
    if (!a.item) return out;
    var keyIndent = -1;
    for (var r = a.item.start; r < a.item.end; r++) {
      var q = parseLine(a.lines[r]);
      if (q.blank || q.key === null) continue;
      if (keyIndent < 0) keyIndent = q.keyIndent;
      if (q.keyIndent === keyIndent) out[q.key] = q.value.replace(/\s+#.*$/, '').trim().replace(/^["']|["']$/g, '');
    }
    return out;
  }

  /** Keys already present in the mapping at the position (to avoid duplicates). */
  function siblingKeys(a) {
    var out = {};
    var indent = a.parsed.blank ? a.col : a.parsed.keyIndent;
    for (var dir = -1; dir <= 1; dir += 2) {
      for (var r = a.row + dir; r >= 0 && r < a.lines.length; r += dir) {
        var q = parseLine(a.lines[r]);
        if (q.blank) continue;
        if (q.keyIndent < indent && !(q.dashes > 0 && q.keyIndent === indent)) {
          if (dir < 0 && q.dashes > 0 && q.keyIndent === indent && q.key) out[q.key] = true;
          break;
        }
        if (q.keyIndent === indent && q.key) out[q.key] = true;
        if (dir < 0 && q.dashes > 0 && q.keyIndent === indent) break;
        if (dir > 0 && q.dashes > 0 && q.indent < indent) break;
      }
    }
    return out;
  }

  // ---------------------------------------------------------------------------
  // Schema navigation

  function Lang(catalog) {
    this.catalog = catalog || { actions: [] };
    this.schema = {};
    try {
      this.schema = typeof catalog.schema === 'string' ? JSON.parse(catalog.schema) : (catalog.schema || {});
    } catch (e) { /* ignore */ }
    this.actions = {};
    var self = this;
    (this.catalog.actions || []).forEach(function (a) { self.actions[a.name] = a; });
  }

  Lang.prototype.resolve = function (s) {
    var guard = 0;
    while (s && s.$ref && guard++ < 10) {
      var ref = s.$ref.replace(/^#\//, '').split('/');
      var t = this.schema;
      for (var i = 0; i < ref.length && t; i++) t = t[ref[i]];
      s = t;
    }
    return s || {};
  };

  /** Schema of the value at path (list items as '[]'). */
  Lang.prototype.schemaAt = function (path) {
    var s = this.resolve(this.schema);
    for (var i = 0; i < path.length; i++) {
      var k = path[i];
      s = this.resolve(s);
      if (s.anyOf) s = this.resolve(pickObject(s.anyOf, this));
      if (s.oneOf) s = this.resolve(pickObject(s.oneOf, this));
      if (k === '[]') {
        s = this.resolve(s.items || {});
      } else if (s.properties && s.properties[k]) {
        s = s.properties[k];
      } else if (s.additionalProperties && typeof s.additionalProperties === 'object') {
        s = s.additionalProperties;
      } else if (s.patternProperties) {
        s = s.patternProperties[Object.keys(s.patternProperties)[0]];
      } else {
        return null;
      }
    }
    s = this.resolve(s);
    if (s.anyOf) s = this.resolve(pickObject(s.anyOf, this));
    if (s.oneOf) s = this.resolve(pickObject(s.oneOf, this));
    return s;
  };

  function pickObject(list, lang) {
    for (var i = 0; i < list.length; i++) {
      var r = lang.resolve(list[i]);
      if (r.properties || r.additionalProperties || r.type === 'object' || (r.items && r.type === 'array')) return list[i];
    }
    return list[0];
  }

  /** Custom actions defined in the document (name -> {description, args}). */
  Lang.prototype.localActions = function (text) {
    var out = {};
    var lines = text.split('\n');
    var inActions = false;
    var cur = null;
    var inArgs = false;
    for (var r = 0; r < lines.length; r++) {
      var q = parseLine(lines[r]);
      if (q.blank) continue;
      if (q.indent === 0) {
        inActions = q.key === 'actions';
        cur = null;
        continue;
      }
      if (!inActions) continue;
      if (q.indent === 2 && q.key) {
        cur = out[q.key] = { name: q.key, category: 'this file', description: '', args: [], local: true };
        inArgs = false;
      } else if (cur && q.indent === 4 && q.key) {
        inArgs = q.key === 'arguments';
        if (q.key === 'description') cur.description = q.value.trim().replace(/^["']|["']$/g, '');
      } else if (cur && inArgs && q.indent === 6 && q.key) {
        var desc = (lines[r].match(/description:\s*("[^"]*"|'[^']*'|[^,}]+)/) || [])[1] || '';
        cur.args.push({ name: q.key, type: (lines[r].match(/type:\s*(\w+)/) || [])[1] || 'string', description: desc.replace(/^["']|["']$/g, '').trim(), required: /required:\s*true/.test(lines[r]) });
      }
    }
    return out;
  };

  Lang.prototype.action = function (name, text) {
    if (this.actions[name]) return this.actions[name];
    return text ? this.localActions(text)[name] : undefined;
  };

  // Where step lists occur (the items have action/args)
  function isStepPath(path) {
    if (path.length < 2 || path[path.length - 1] !== '[]') return false;
    var k = path[path.length - 2];
    return k === 'steps';
  }

  // ---------------------------------------------------------------------------
  // Completion

  /**
   * Returns {from, to, items: [{label, kind, detail, doc, insert}]} or null.
   * kind: 'key' | 'action' | 'value' | 'arg'
   */
  Lang.prototype.complete = function (text, offset, opts) {
    opts = opts || {};
    var a = analyze(text, offset);
    var self = this;
    var items = [];
    var path = a.path;
    var p = a.parsed;
    var lineBefore = a.line.slice(0, a.col);

    // Values
    if (a.inValue) {
      var key = a.key;
      var valueText = lineBefore.slice(lineBefore.indexOf(':') + 1);
      if (/^\s*["']?[A-Za-z0-9_.@-]*$/.test(valueText) || /^\s*\[[^\]]*$/.test(valueText)) {
        if (key === 'action' && isStepPath(path)) {
          var locals = this.localActions(text);
          Object.keys(locals).forEach(function (n) { items.push(actionItem(locals[n])); });
          (this.catalog.actions || []).forEach(function (act) { items.push(actionItem(act)); });
        } else if (key === 'flavors' || (path[path.length - 1] === 'flavors' && key === 'based-on')) {
          (this.catalog.flavors || []).forEach(function (f) { items.push({ label: f.name, kind: 'value', detail: 'flavor', doc: f.description }); });
          ['debian', 'arch'].forEach(function (f) { items.push({ label: f, kind: 'value', detail: 'family' }); });
        } else if (key === 'targets') {
          (this.catalog.targets || []).forEach(function (t) { items.push({ label: t.name, kind: 'value', detail: t.kind, doc: t.description }); });
        } else {
          // enum / bool values from the schema or the action's arguments
          var vals = [];
          var argDoc = null;
          if (path[path.length - 1] === 'args' && path.length >= 2) {
            var act = this.action(itemKeys(a).action, text);
            argDoc = act && (act.args || []).filter(function (x) { return x.name === key; })[0];
            if (argDoc) {
              if (argDoc.type === 'bool') vals = ['true', 'false'];
              if (argDoc.options) vals = argDoc.options.map(String);
              var em = (argDoc.description || '').match(/One of: ([^.]+)\./);
              if (em) vals = em[1].split(/,\s*/);
            }
          } else {
            var s = this.schemaAt(path.concat([key]));
            if (s) {
              if (s.enum) vals = s.enum.map(String);
              else if (s.type === 'boolean' || (Array.isArray(s.type) && s.type.indexOf('boolean') >= 0 && s.type.length <= 2)) vals = ['true', 'false'];
            }
          }
          vals.forEach(function (v) { items.push({ label: v, kind: 'value' }); });
        }
      }
    } else if (!/["'#]/.test(lineBefore.trim().replace(/^-\s*/, '').replace(/[A-Za-z0-9_.@-]*$/, '')) || lineBefore.trim() === '') {
      // Keys
      var present = siblingKeys(a);
      if (path[path.length - 1] === 'args' && path.length >= 3 && path[path.length - 2] === '[]') {
        var actName = itemKeys(a).action;
        var act2 = this.action(actName, text);
        if (act2) {
          (act2.args || []).forEach(function (x) {
            if (!present[x.name] || x.name === a.key) items.push(argItem(x, act2));
          });
        }
      } else {
        var sch = this.schemaAt(path);
        if (sch && sch.properties) {
          Object.keys(sch.properties).forEach(function (k) {
            if (present[k] && k !== a.key) return;
            var ps = self.resolve(sch.properties[k]);
            items.push({ label: k, kind: 'key', detail: typeLabel(ps), doc: ps.description || '', insert: keyInsert(k, ps, p, lineBefore) });
          });
        }
      }
    }
    if (!items.length) return null;
    var prefix = a.prefix.toLowerCase();
    if (prefix || !opts.all) {
      items = items.filter(function (it) { return it.label.toLowerCase().indexOf(prefix) >= 0; });
      items.sort(function (x, y) {
        var xs = x.label.toLowerCase().indexOf(prefix) === 0 ? 0 : 1;
        var ys = y.label.toLowerCase().indexOf(prefix) === 0 ? 0 : 1;
        return xs - ys;
      });
    }
    if (!items.length) return null;
    return { from: a.from, to: a.to, items: items };
  };

  function typeLabel(s) {
    if (!s) return '';
    if (s.enum) return s.enum.join(' | ');
    if (Array.isArray(s.type)) return s.type.join(' | ');
    return s.type || (s.properties ? 'object' : '');
  }

  function keyInsert(k, s, p, lineBefore) {
    var nested = s && (s.type === 'object' || (s.properties && !s.type) || s.type === 'array') && !(Array.isArray(s.type));
    if (k === 'steps' || k === 'args' || (nested && k !== 'args')) return k + ':';
    return k + ': ';
  }

  function actionItem(act) {
    return {
      label: act.name, kind: 'action',
      detail: (act.extra ? 'extra · ' : '') + (act.category || ''),
      doc: actionMarkdown(act), insert: act.name,
    };
  }

  function argItem(x, act) {
    return {
      label: x.name, kind: 'arg',
      detail: (x.type || 'string') + (x.required ? ', required' : ''),
      doc: argMarkdown(x), insert: x.name + ': ',
    };
  }

  // ---------------------------------------------------------------------------
  // Documentation (Markdown)

  function fmtDefault(v) {
    if (v === undefined || v === null || v === '') return '';
    if (Array.isArray(v)) return '[' + v.join(', ') + ']';
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  }

  function argMarkdown(x) {
    var md = '**' + x.name + '** `' + (x.type || 'string') + '`' + (x.required ? ' (required)' : '');
    var d = fmtDefault(x.default);
    if (d) md += ' · default `' + d + '`';
    if (x.description) md += '\n\n' + x.description;
    if (x.options && x.options.length) md += '\n\nOne of: ' + x.options.map(function (o) { return '`' + o + '`'; }).join(', ');
    return md;
  }

  function actionMarkdown(act) {
    var md = '**' + act.name + '**' + (act.extra ? ' · extra action (' + act.category + ')' : act.local ? ' · custom action' : ' · ' + (act.category || 'action'));
    if (act.description) md += '\n\n' + act.description;
    if (act.flavors && act.flavors.length) md += '\n\nFlavors: ' + act.flavors.join(', ');
    if (act.args && act.args.length) {
      md += '\n\n' + act.args.map(function (x) {
        var d = fmtDefault(x.default);
        return '- `' + x.name + '` ' + (x.type || 'string') + (x.required ? ', **required**' : '') + (d ? ', default `' + d + '`' : '') + (x.description ? ' — ' + x.description : '');
      }).join('\n');
    }
    if (act.example) md += '\n\n```yaml\n- ' + act.example.replace(/\n$/, '').split('\n').join('\n  ') + '\n```';
    return md;
  }

  /**
   * Documentation for the position: {from, to, markdown} or null. Covers
   * action names, action arguments and configuration keys.
   */
  Lang.prototype.hover = function (text, offset) {
    var a = analyze(text, offset);
    var lineStart = offset - a.col;
    var word = wordAt(a.line, a.col);
    if (!word) return null;
    var from = lineStart + word.start;
    var to = lineStart + word.end;
    var path = a.path;
    // action name
    if (a.key === 'action' && a.inValue && isStepPath(path)) {
      var act = this.action(word.text, text);
      if (act) return { from: from, to: to, markdown: actionMarkdown(act) };
      return { from: from, to: to, markdown: '**' + word.text + '**: unknown action' };
    }
    if (a.key && !a.inValue && from >= a.keyStart && to <= a.keyEnd) {
      // action argument
      if (path[path.length - 1] === 'args' && path.length >= 3 && path[path.length - 2] === '[]') {
        var act2 = this.action(itemKeys(a).action, text);
        var x = act2 && (act2.args || []).filter(function (y) { return y.name === a.key; })[0];
        if (x) return { from: a.keyStart, to: a.keyEnd, markdown: argMarkdown(x) + '\n\n_argument of_ `' + act2.name + '`' };
        if (act2) return { from: a.keyStart, to: a.keyEnd, markdown: '`' + a.key + '` is not an argument of `' + act2.name + '`' };
        return null;
      }
      var s = this.schemaAt(path.concat([a.key]));
      if (s && s.description) return { from: a.keyStart, to: a.keyEnd, markdown: '**' + a.key + '** ' + (typeLabel(s) ? '`' + typeLabel(s) + '`' : '') + '\n\n' + s.description };
    }
    return null;
  };

  /** The action of the step at the position (for context help), or null. */
  Lang.prototype.stepAction = function (text, offset) {
    var a = analyze(text, offset);
    var path = a.path.slice();
    // climb to the innermost step item
    for (var i = path.length; i >= 2; i--) {
      if (isStepPath(path.slice(0, i))) {
        var name = itemKeys(a).action;
        if (name && path.length === i) return this.action(name, text) || null;
        if (name && path[i] === 'args') return this.action(name, text) || null;
        return name ? this.action(name, text) || null : null;
      }
    }
    return null;
  };

  function wordAt(line, col) {
    var re = /[A-Za-z0-9_.@-]+/g;
    var m;
    while ((m = re.exec(line))) {
      if (m.index <= col && col <= m.index + m[0].length) return { text: m[0], start: m.index, end: m.index + m[0].length };
    }
    return null;
  }

  /** Minimal Markdown to HTML for documentation popups. */
  function markdownToHTML(md) {
    var blocks = md.split(/\n```(\w*)\n([\s\S]*?)\n```/);
    var html = '';
    for (var i = 0; i < blocks.length; i++) {
      if (i % 3 === 0) {
        html += blocks[i].split(/\n{2,}/).map(function (para) {
          if (!para.trim()) return '';
          if (/^- /.test(para)) {
            return '<ul>' + para.split(/\n(?=- )/).map(function (li) { return '<li>' + inline(li.replace(/^- /, '')) + '</li>'; }).join('') + '</ul>';
          }
          return '<p>' + inline(para) + '</p>';
        }).join('');
      } else if (i % 3 === 2) {
        html += '<pre><code>' + highlight(blocks[i], blocks[i - 1] || 'yaml') + '</code></pre>';
      }
    }
    return html;
  }

  function inline(s) {
    return esc(s)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/_([^_]+)_/g, '<em>$1</em>')
      .replace(/\n/g, '<br>');
  }

  return {
    highlight: highlight,
    languageOf: languageOf,
    analyze: analyze,
    Lang: Lang,
    actionMarkdown: actionMarkdown,
    argMarkdown: argMarkdown,
    markdownToHTML: markdownToHTML,
  };
});
