/* GoLab @mention autocomplete for Quill 2.
 *
 * Sprint 14. No external dependencies. Usage:
 *
 *   var mention = new GolabQuillMention(quill, {
 *     fetchUrl: '/api/users/autocomplete',
 *     onSelect: function (user) { ... }
 *   });
 *
 * Lifecycle is tied to the Quill instance the caller passes in.
 * Call mention.destroy() when the editor unmounts (composeEditor
 * does this on Alpine teardown).
 *
 * Design notes:
 *
 * - The inserted text is plain "@username<space>". We do NOT use a
 *   Quill custom blot because the server-side renderer is the
 *   single source of truth for mention link styling: plain text
 *   in the editor becomes an <a class="mention"> after
 *   PostHandler.Create runs LinkMentions.
 * - Arrow up/down/enter/tab/escape all handled in capture phase so
 *   the event never reaches Quill's default bindings.
 * - The dropdown is a sibling of document.body, absolutely
 *   positioned below the caret. It layers above modals via
 *   z-index 10600 (set in CSS).
 * - Fetches are debounced 150ms; every new keystroke cancels the
 *   in-flight request before issuing the next.
 */
(function (global) {
  'use strict';

  var MENTION_RE = /@([a-zA-Z0-9_]{0,32})$/;
  var DEBOUNCE_MS = 150;
  var MAX_RESULTS = 8;

  function GolabQuillMention(quill, opts) {
    if (!quill) throw new Error('GolabQuillMention requires a Quill instance');
    this.quill = quill;
    this.opts = opts || {};
    this.fetchUrl = this.opts.fetchUrl || '/api/users/autocomplete';
    this.onSelect = this.opts.onSelect || function () {};

    this.active = false;        // mention mode on/off
    this.anchorIndex = -1;      // editor index of the leading '@'
    this.queryLen = 0;          // chars typed after the '@'
    this.results = [];
    this.selectedIdx = 0;
    this.dropdown = null;
    this.abortCtrl = null;
    this.debounceTimer = null;

    this._onText = this._onText.bind(this);
    this._onSelection = this._onSelection.bind(this);
    this._onKeyDown = this._onKeyDown.bind(this);
    this._onDocClick = this._onDocClick.bind(this);

    this._paused = false;
    this.quill.on('text-change', this._onText);
    this.quill.on('selection-change', this._onSelection);
    // Capture phase for keyboard: we must beat Quill's keyboard module.
    this.quill.root.addEventListener('keydown', this._onKeyDown, true);
    document.addEventListener('mousedown', this._onDocClick, true);
  }

  GolabQuillMention.prototype.destroy = function () {
    this.quill.off('text-change', this._onText);
    this.quill.off('selection-change', this._onSelection);
    this.quill.root.removeEventListener('keydown', this._onKeyDown, true);
    document.removeEventListener('mousedown', this._onDocClick, true);
    this._closeDropdown();
    if (this.abortCtrl) {
      try { this.abortCtrl.abort(); } catch (e) {}
    }
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
  };

  // Sprint 15a.2: pause / resume decouple the Quill-level listeners
  // (text-change, selection-change) from the host editor without
  // tearing down the whole module. The use case is the P1 / P2 fix:
  // the image-upload and emoji-picker paths need to call
  // quill.insertEmbed / quill.insertText while the mention module's
  // _onSelection is NOT subscribed, because _onSelection itself
  // calls quill.getSelection(), and during Quill 2.0.3's internal
  // post-insert update that inner getSelection crashes on a null
  // DOM selection when the editor just regained focus from a file
  // dialog or popup.
  //
  // Only the two Quill-event subscriptions are paused. The DOM
  // keydown listener on quill.root and the document-level mousedown
  // listener stay active; both are benign during programmatic
  // inserts (keydown only fires on actual keystrokes, mousedown
  // on actual mouse events). Keeping them attached means an
  // in-progress mention dropdown continues to close on outside
  // clicks even while paused.
  //
  // Both methods are idempotent via the _paused flag so nested
  // pause / resume pairs from defensive callers cannot double-
  // attach or double-detach.
  GolabQuillMention.prototype.pause = function () {
    if (this._paused) return;
    this._paused = true;
    try { this.quill.off('text-change', this._onText); } catch (e) { /* ignore */ }
    try { this.quill.off('selection-change', this._onSelection); } catch (e) { /* ignore */ }
  };

  GolabQuillMention.prototype.resume = function () {
    if (!this._paused) return;
    this._paused = false;
    try { this.quill.on('text-change', this._onText); } catch (e) { /* ignore */ }
    try { this.quill.on('selection-change', this._onSelection); } catch (e) { /* ignore */ }
  };

  // ---- Quill event hooks ----------------------------------------

  GolabQuillMention.prototype._onText = function () {
    var sel = this.quill.getSelection();
    if (!sel) { this._close(); return; }
    this._scanAt(sel.index);
  };

  GolabQuillMention.prototype._onSelection = function (range) {
    if (!range) { this._close(); return; }
    // If the caret moved BEFORE the anchor @ we cancel - mention
    // mode only tracks forward typing.
    if (this.active && range.index <= this.anchorIndex) {
      this._close();
      return;
    }
    this._scanAt(range.index);
  };

  GolabQuillMention.prototype._scanAt = function (caret) {
    var text = this.quill.getText(0, caret);
    var m = text.match(MENTION_RE);
    if (!m) { this._close(); return; }

    // Require the '@' to be at start-of-text or after whitespace /
    // punctuation so emails and inline @s in URLs don't trigger.
    var atIdx = text.length - m[0].length;
    if (atIdx > 0) {
      var prev = text.charAt(atIdx - 1);
      if (!/[\s(\[{,.;!?>]/.test(prev)) { this._close(); return; }
    }

    this.active = true;
    this.anchorIndex = atIdx;
    this.queryLen = m[1].length;
    this._fetch(m[1]);
  };

  GolabQuillMention.prototype._onKeyDown = function (ev) {
    if (!this.active) return;
    // Cancel mention mode on Space / Enter inside the query: the
    // user has committed whatever they typed as plain text.
    if (ev.key === 'Escape') {
      ev.preventDefault(); ev.stopPropagation();
      this._close();
      return;
    }
    if (!this.results.length) return;

    if (ev.key === 'ArrowDown') {
      ev.preventDefault(); ev.stopPropagation();
      this.selectedIdx = (this.selectedIdx + 1) % this.results.length;
      this._renderDropdown();
    } else if (ev.key === 'ArrowUp') {
      ev.preventDefault(); ev.stopPropagation();
      this.selectedIdx = (this.selectedIdx - 1 + this.results.length) % this.results.length;
      this._renderDropdown();
    } else if (ev.key === 'Enter' || ev.key === 'Tab') {
      ev.preventDefault(); ev.stopPropagation();
      this._select(this.results[this.selectedIdx]);
    }
  };

  GolabQuillMention.prototype._onDocClick = function (ev) {
    if (!this.dropdown) return;
    if (this.dropdown.contains(ev.target)) return;
    this._close();
  };

  // ---- Fetch -----------------------------------------------------

  GolabQuillMention.prototype._fetch = function (query) {
    var self = this;
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
    this.debounceTimer = setTimeout(function () {
      if (self.abortCtrl) { try { self.abortCtrl.abort(); } catch (e) {} }
      if (typeof AbortController !== 'undefined') {
        self.abortCtrl = new AbortController();
      }
      var url = self.fetchUrl + '?q=' + encodeURIComponent(query);
      fetch(url, {
        credentials: 'same-origin',
        signal: self.abortCtrl ? self.abortCtrl.signal : undefined
      }).then(function (r) { return r.ok ? r.json() : []; })
        .then(function (data) {
          if (!self.active) return;
          var list = Array.isArray(data) ? data : [];
          self.results = list.slice(0, MAX_RESULTS);
          self.selectedIdx = 0;
          if (self.results.length === 0) {
            self._closeDropdown();
          } else {
            self._renderDropdown();
          }
        })
        .catch(function (err) {
          if (err && err.name === 'AbortError') return;
          // Silent network failure - typeahead should never toast.
        });
    }, DEBOUNCE_MS);
  };

  // ---- Dropdown render ------------------------------------------

  GolabQuillMention.prototype._renderDropdown = function () {
    if (!this.dropdown) {
      this.dropdown = document.createElement('div');
      this.dropdown.className = 'mention-dropdown';
      document.body.appendChild(this.dropdown);
    }
    var self = this;
    this.dropdown.innerHTML = '';
    this.results.forEach(function (u, i) {
      var row = document.createElement('div');
      row.className = 'mention-item' + (i === self.selectedIdx ? ' is-selected' : '');

      var avatar = document.createElement('span');
      avatar.className = 'mention-avatar';
      if (u.avatar) {
        var img = document.createElement('img');
        img.src = u.avatar;
        img.alt = '';
        avatar.appendChild(img);
      } else {
        avatar.textContent = (u.username || '?').charAt(0).toUpperCase();
      }
      row.appendChild(avatar);

      var text = document.createElement('span');
      text.className = 'mention-text';
      var name = document.createElement('span');
      name.className = 'mention-name';
      name.textContent = u.display_name || u.username;
      var handle = document.createElement('span');
      handle.className = 'mention-handle';
      handle.textContent = '@' + u.username;
      text.appendChild(name);
      text.appendChild(handle);
      row.appendChild(text);

      row.addEventListener('mousedown', function (ev) {
        // mousedown instead of click so the selection happens
        // before the editor loses focus to the document click.
        ev.preventDefault();
        self._select(u);
      });
      row.addEventListener('mouseenter', function () {
        self.selectedIdx = i;
        self._paintSelection();
      });

      self.dropdown.appendChild(row);
    });
    this._position();
  };

  GolabQuillMention.prototype._paintSelection = function () {
    if (!this.dropdown) return;
    var rows = this.dropdown.querySelectorAll('.mention-item');
    var self = this;
    rows.forEach(function (r, i) {
      r.classList.toggle('is-selected', i === self.selectedIdx);
    });
  };

  GolabQuillMention.prototype._position = function () {
    if (!this.dropdown) return;
    var bounds = this.quill.getBounds(this.anchorIndex);
    if (!bounds) return;
    var host = this.quill.root.getBoundingClientRect();
    var top = host.top + window.scrollY + bounds.top + bounds.height + 4;
    var left = host.left + window.scrollX + bounds.left;
    // Keep the dropdown inside the viewport horizontally.
    var dropWidth = this.dropdown.offsetWidth || 240;
    var vpWidth = document.documentElement.clientWidth;
    if (left + dropWidth > vpWidth - 8) {
      left = Math.max(8, vpWidth - dropWidth - 8);
    }
    this.dropdown.style.top = top + 'px';
    this.dropdown.style.left = left + 'px';
  };

  // ---- Selection -> insert --------------------------------------

  GolabQuillMention.prototype._select = function (user) {
    if (!user || !user.username) { this._close(); return; }
    var start = this.anchorIndex;
    var end = this.anchorIndex + 1 + this.queryLen; // +1 for '@'
    var insertion = '@' + user.username + ' ';
    this.quill.deleteText(start, end - start, 'user');
    this.quill.insertText(start, insertion, 'user');
    this.quill.setSelection(start + insertion.length, 0, 'user');
    try { this.onSelect(user); } catch (e) {}
    this._close();
  };

  GolabQuillMention.prototype._close = function () {
    this.active = false;
    this.anchorIndex = -1;
    this.queryLen = 0;
    this.results = [];
    this.selectedIdx = 0;
    this._closeDropdown();
  };

  GolabQuillMention.prototype._closeDropdown = function () {
    if (this.dropdown) {
      this.dropdown.remove();
      this.dropdown = null;
    }
  };

  global.GolabQuillMention = GolabQuillMention;
})(window);
