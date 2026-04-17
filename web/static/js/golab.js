/* GoLab frontend glue.
   - Alpine components (toasts) registered on alpine:init
   - Form handlers for register / login / settings / create-channel
   - Click handlers for data-action buttons (react, repost, delete,
     follow, channel-toggle, logout)
   - WebSocket client with exponential backoff reconnect
   - HTMX event hooks (re-binds handlers after swaps)
*/
(function () {
  'use strict';

  // ---------- helpers ----------

  function apiJSON(url, method, body) {
    return fetch(url, {
      method: method,
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : null,
      credentials: 'same-origin'
    }).then(function (r) {
      return r.json().then(function (data) {
        return { ok: r.ok, status: r.status, data: data };
      }).catch(function () {
        return { ok: r.ok, status: r.status, data: {} };
      });
    });
  }

  function toast(type, message) {
    window.dispatchEvent(new CustomEvent('notify', {
      detail: { type: type, message: message }
    }));
  }

  function setError(formEl, message) {
    if (!formEl) return;
    var err = formEl.querySelector('.form-error');
    if (err) err.textContent = message || '';
  }

  // ---------- Alpine components ----------

  document.addEventListener('alpine:init', function () {
    // Toast host
    window.Alpine.data('toastHost', function () {
      return {
        items: [],
        nextId: 1,
        push: function (detail) {
          var self = this;
          var id = this.nextId++;
          var t = {
            id: id,
            type: detail.type || 'info',
            message: detail.message || '',
            visible: true
          };
          this.items.push(t);
          setTimeout(function () {
            t.visible = false;
            setTimeout(function () {
              self.items = self.items.filter(function (x) { return x.id !== id; });
            }, 250);
          }, detail.duration || 3800);
        }
      };
    });

    // Avatar uploader (settings page)
    //
    // Triggering is handled by the parent <label> wrapping the hidden input.
    // Clicking the zone -> browser fires the native file picker exactly once
    // -> change event fires -> handleFile -> upload. No manual .click() calls.
    window.Alpine.data('avatarUploader', function (initialUrl, letter) {
      return {
        preview: initialUrl || '',
        dragging: false,
        initial: letter || '',
        uploading: false,
        handleDrop: function (ev) {
          this.dragging = false;
          var file = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
          if (file) this.upload(file);
        },
        handleFile: function (ev) {
          var input = ev.target;
          var file = input.files && input.files[0];
          if (file) this.upload(file);
          // Clear the input value so selecting the same file again still fires change.
          input.value = '';
        },
        upload: function (file) {
          var self = this;
          if (!file || !file.type || !file.type.startsWith('image/')) {
            toast('error', 'Please select an image');
            return;
          }
          if (self.uploading) return; // prevent double submissions
          self.uploading = true;

          // Race guard: both FileReader.onload (local preview) and fetch
          // (real server URL) race to set `preview`. Without this flag,
          // FileReader's local data URL could arrive AFTER the fetch
          // response and silently overwrite the real avatar URL, leaving
          // the settings page showing the pre-upload image while the
          // server already has the new one. Once the server answers (ok
          // or error), FileReader must not touch preview anymore.
          var serverResolved = false;

          var reader = new FileReader();
          reader.onload = function (e) {
            if (serverResolved) return;
            self.preview = e.target.result;
          };
          reader.readAsDataURL(file);

          var form = new FormData();
          form.append('avatar', file);
          fetch('/api/users/me/avatar', {
            method: 'POST',
            body: form,
            credentials: 'same-origin'
          }).then(function (r) {
            return r.json().then(function (d) { return { ok: r.ok, data: d }; })
              .catch(function () { return { ok: r.ok, data: {} }; });
          }).then(function (res) {
            serverResolved = true;
            self.uploading = false;
            if (res.ok && res.data && res.data.avatar_url) {
              self.preview = res.data.avatar_url;
              var hidden = document.getElementById('st-avatar-url');
              if (hidden) hidden.value = res.data.avatar_url;
              // Live-update the nav avatar without a full reload. Mirror
              // the markup the avatar partial produces so the circle
              // styling (via .avatar.avatar-sm) stays consistent.
              var navAvatar = document.querySelector('.nav-avatar');
              if (navAvatar) {
                var img = document.createElement('img');
                img.src = res.data.avatar_url;
                img.alt = '';
                img.className = 'avatar avatar-sm img-reveal loaded';
                navAvatar.replaceChildren(img);
              }
              toast('success', 'Avatar uploaded');
            } else {
              toast('error', (res.data && res.data.error) || 'Upload failed');
            }
          }).catch(function () {
            serverResolved = true;
            self.uploading = false;
            toast('error', 'Upload failed');
          });
        },
        remove: function () {
          var self = this;
          apiJSON('/api/users/me/avatar', 'DELETE', null).then(function (res) {
            if (res.ok) {
              self.preview = '';
              var hidden = document.getElementById('st-avatar-url');
              if (hidden) hidden.value = '';
              toast('info', 'Avatar removed');
            }
          });
        }
      };
    });

    // Notifications dropdown
    window.Alpine.data('notificationsPanel', function () {
      return {
        open: false,
        items: [],
        unread: 0,
        loading: false,
        loaded: false,
        async toggle() {
          this.open = !this.open;
          if (this.open && !this.loaded) await this.load();
        },
        async load() {
          this.loading = true;
          try {
            var res = await fetch('/api/notifications', { credentials: 'same-origin' });
            var data = await res.json();
            this.items = (data.notifications || []).map(function (n) { return format(n); });
            this.unread = data.unread || 0;
            this.loaded = true;
          } catch (e) { /* ignore */ }
          this.loading = false;
        },
        async refreshCount() {
          try {
            var res = await fetch('/api/notifications/count', { credentials: 'same-origin' });
            var data = await res.json();
            var before = this.unread;
            this.unread = data.count || 0;
            if (this.unread > before) this.loaded = false; // reload next open
          } catch (e) { /* ignore */ }
        },
        async markAllRead() {
          await apiJSON('/api/notifications/read-all', 'POST', null);
          this.unread = 0;
          this.items.forEach(function (n) { n.is_read = true; });
        }
      };

      function format(n) {
        var action = '';
        switch (n.notif_type) {
          case 'reaction': action = 'reacted to your post'; break;
          case 'reply':    action = 'replied to your post'; break;
          case 'follow':   action = 'started following you'; break;
          case 'mention':  action = 'mentioned you'; break;
          default:         action = 'sent you a notification'; break;
        }
        var link = n.post_id ? '/p/' + n.post_id : '/u/' + n.actor_username;
        return {
          id: n.id,
          action: action,
          link: link,
          is_read: n.is_read,
          actor_name: n.actor_display_name || n.actor_username,
          actor_username: n.actor_username,
          actor_initial: (n.actor_username || '?').charAt(0).toUpperCase(),
          actor_avatar: n.actor_avatar_url || '',
          excerpt: n.post_excerpt || '',
          time_ago: timeAgo(n.created_at)
        };
      }

      function timeAgo(iso) {
        if (!iso) return '';
        var d = (Date.now() - new Date(iso).getTime()) / 1000;
        if (d < 60) return 'just now';
        if (d < 3600) return Math.floor(d / 60) + 'm ago';
        if (d < 86400) return Math.floor(d / 3600) + 'h ago';
        return Math.floor(d / 86400) + 'd ago';
      }
    });

    // Rich compose editor (Quill 2). Mounted at `x-ref="editor"`.
    //
    // Responsibilities:
    //  - Initialize Quill with our toolbar + image upload handler
    //  - Track character count (text-length, not HTML-length)
    //  - Upload images to /api/upload/image and embed them
    //  - Submit post as HTML via /api/posts, then reset editor state
    //
    // Quill is a global provided by web/static/js/quill.min.js. If it's
    // missing (e.g. slow network) we degrade to a plain contenteditable
    // so the user can at least type.
    window.Alpine.data('composeEditor', function (cfg) {
      cfg = cfg || {};
      return {
        quill: null,
        charCount: 0,
        max: 5000,
        submitting: false,
        // Sprint 10.5: track the currently selected space slug so we
        // can gate posting in "announcements" behind power_level >= 75
        // client-side. The server still enforces this.
        spaceSlug: '',
        powerLevel: cfg.powerLevel || 0,

        onSpaceChange: function (ev) {
          var option = ev.target.options[ev.target.selectedIndex];
          this.spaceSlug = option ? (option.dataset.slug || '') : '';
        },

        canPostToAnnouncements: function () {
          return this.powerLevel >= 75;
        },

        canSubmit: function () {
          if (this.submitting) return false;
          if (this.charCount < 1 || this.charCount > this.max) return false;
          if (this.spaceSlug === 'announcements' && !this.canPostToAnnouncements()) return false;
          return true;
        },

        init: function () {
          var self = this;
          var container = this.$refs.editor;

          // Guard against double-mount. Two ways this can happen:
          //   1. A template accidentally has both x-data="composeEditor()"
          //      AND x-init="init()" - Alpine auto-calls init() once and
          //      the explicit x-init calls it again.
          //   2. HTMX hx-boost page swaps leave a stale Quill instance on
          //      a container that Alpine then tries to re-mount.
          // In both cases Quill adds a second toolbar and editor. We check
          // container state and the stashed Quill reference, and bail out
          // early if either indicates an existing mount.
          if (container.__quillMounted || container.classList.contains('ql-container')) {
            return;
          }
          // Also: if Alpine re-runs init on the same component data, this.quill
          // is already set from the first pass. Skip.
          if (this.quill) {
            return;
          }

          if (!window.Quill) {
            // Graceful fallback: plain editable div
            container.setAttribute('contenteditable', 'true');
            container.style.minHeight = '100px';
            container.addEventListener('input', function () {
              self.charCount = (container.innerText || '').trim().length;
            });
            container.__quillMounted = true;
            return;
          }
          this.quill = new window.Quill(container, {
            theme: 'snow',
            placeholder: "What's on your mind?",
            modules: {
              toolbar: {
                container: [
                  [{ header: [1, 2, 3, false] }],
                  ['bold', 'italic', 'underline', 'strike'],
                  ['link', 'blockquote', 'code-block'],
                  [{ list: 'ordered' }, { list: 'bullet' }],
                  ['image'],
                  ['clean']
                ],
                handlers: {
                  image: function () { self.uploadImage(); }
                }
              }
            }
          });
          container.__quillMounted = true;
          this.quill.on('text-change', function () {
            self.charCount = self.textLength();
          });

          // Paste a .gif URL -> convert to an <img> embed automatically.
          // Quill's matchers fire during clipboard processing so the URL
          // never lands as plain text first.
          try {
            this.quill.clipboard.addMatcher(Node.TEXT_NODE, function (node, delta) {
              var text = node.data || '';
              var gifRe = /\bhttps?:\/\/[^\s<>"']+\.gif(\?[^\s<>"']*)?\b/i;
              var m = text.match(gifRe);
              if (m) {
                return new (window.Quill.import('delta'))()
                  .insert({ image: m[0] });
              }
              return delta;
            });
          } catch (e) { /* Quill version mismatch, ignore */ }

          // Append an emoji button to Quill's toolbar. We don't touch
          // the toolbar config (stays declarative) so the button sits
          // at the end as a detached custom tool.
          this.attachEmojiButton();
        },

        attachEmojiButton: function () {
          var self = this;
          var form = this.$el;
          var toolbar = form.querySelector('.ql-toolbar');
          if (!toolbar || toolbar.querySelector('.ql-emoji-btn')) return;

          var btn = document.createElement('button');
          btn.type = 'button';
          btn.className = 'ql-emoji-btn';
          btn.title = 'Emoji';
          btn.setAttribute('aria-label', 'Insert emoji');
          btn.innerHTML =
            '<svg viewBox="0 0 24 24" width="18" height="18" fill="none"' +
            ' stroke="currentColor" stroke-width="2" stroke-linecap="round"' +
            ' stroke-linejoin="round">' +
            '<circle cx="12" cy="12" r="10"/>' +
            '<path d="M8 14s1.5 2 4 2 4-2 4-2"/>' +
            '<line x1="9" y1="9" x2="9.01" y2="9"/>' +
            '<line x1="15" y1="9" x2="15.01" y2="9"/></svg>';

          btn.addEventListener('click', function (e) {
            e.preventDefault();
            e.stopPropagation();
            self.toggleEmojiPicker(btn);
          });

          toolbar.appendChild(btn);
        },

        // Compact grid picker with 40 of the most-used emoji for
        // a community context. No library, no network, instant open.
        // We ship the full LC-Emoji-Picker files alongside but the
        // library isn't on the critical path (we can fall back to
        // it lazily if the user ever asks for the long tail).
        toggleEmojiPicker: function (anchor) {
          var existing = document.querySelector('.emoji-quickpicker');
          if (existing) { existing.remove(); return; }

          var self = this;
          var panel = document.createElement('div');
          panel.className = 'emoji-quickpicker';
          var common = [
            '\u{1F600}', '\u{1F602}', '\u{1F60A}', '\u{1F60D}', '\u{1F914}',
            '\u{1F44D}', '\u{1F44E}', '\u{1F525}', '\u{1F389}', '\u{1F64C}',
            '\u{1F64F}', '\u{1F4AF}', '\u{2764}\u{FE0F}', '\u{1F499}', '\u{1F4A1}',
            '\u{1F527}', '\u{1F4BB}', '\u{1F512}', '\u{1F510}', '\u{1F6E1}\u{FE0F}',
            '\u{1F9E0}', '\u{1F440}', '\u{1F44B}', '\u{1F60E}', '\u{1F605}',
            '\u{1F62D}', '\u{1F631}', '\u{1F44F}', '\u{1F680}', '\u{1F41B}',
            '\u{1F41E}', '\u{2705}', '\u{274C}', '\u{26A0}\u{FE0F}', '\u{1F4A5}',
            '\u{1F4AC}', '\u{1F4DD}', '\u{1F4CC}', '\u{1F517}', '\u{2728}'
          ];
          common.forEach(function (e) {
            var b = document.createElement('button');
            b.type = 'button';
            b.textContent = e;
            b.className = 'emoji-quickpicker-item';
            b.addEventListener('click', function (ev) {
              ev.stopPropagation();
              if (!self.quill) return;
              var range = self.quill.getSelection(true);
              self.quill.insertText(range.index, e, 'user');
              self.quill.setSelection(range.index + e.length);
              panel.remove();
            });
            panel.appendChild(b);
          });

          var rect = anchor.getBoundingClientRect();
          panel.style.top = (rect.bottom + window.scrollY + 6) + 'px';
          panel.style.left = (rect.left + window.scrollX) + 'px';
          document.body.appendChild(panel);

          // Close on outside click or Escape.
          setTimeout(function () {
            var closer = function (e) {
              if (!panel.contains(e.target) && e.target !== anchor) {
                panel.remove();
                document.removeEventListener('click', closer);
                document.removeEventListener('keydown', keyCloser);
              }
            };
            var keyCloser = function (e) {
              if (e.key === 'Escape') {
                panel.remove();
                document.removeEventListener('click', closer);
                document.removeEventListener('keydown', keyCloser);
              }
            };
            document.addEventListener('click', closer);
            document.addEventListener('keydown', keyCloser);
          }, 10);
        },

        // Alpine calls destroy() when the component is torn down (element
        // removed from DOM, HTMX swap, etc). Clearing the mount flag lets
        // the next incarnation mount a fresh Quill.
        destroy: function () {
          var container = this.$refs && this.$refs.editor;
          if (container) {
            delete container.__quillMounted;
          }
          this.quill = null;
        },

        textLength: function () {
          if (!this.quill) return 0;
          // Quill's getText() includes trailing newline; trim it.
          return (this.quill.getText() || '').replace(/\n$/, '').length;
        },

        htmlContent: function () {
          if (!this.quill) return this.$refs.editor.innerHTML || '';
          var html = this.quill.root.innerHTML;
          // Empty Quill document is '<p><br></p>'. Treat that as empty.
          if (html === '<p><br></p>') return '';
          return html;
        },

        uploadImage: function () {
          var self = this;
          var input = document.createElement('input');
          input.type = 'file';
          input.accept = 'image/*';
          input.onchange = function () {
            var file = input.files && input.files[0];
            if (!file) return;
            var form = new FormData();
            form.append('image', file);
            toast('info', 'Uploading image...');

            fetch('/api/upload/image', {
              method: 'POST',
              body: form,
              credentials: 'same-origin'
            }).then(function (r) {
              // Keep the original ok/status around even if JSON decode fails
              // so the diagnostic below can differentiate "server returned
              // non-JSON" from "actual network failure".
              return r.text().then(function (text) {
                var data = {};
                try { data = JSON.parse(text); } catch (e) { /* non-JSON body */ }
                return { ok: r.ok, status: r.status, data: data, raw: text };
              });
            }).then(function (res) {
              if (!res.ok) {
                console.error('Image upload failed', res.status, res.raw);
                toast('error', (res.data && res.data.error) || 'Image upload failed (' + res.status + ')');
                return;
              }
              // Success toast first so the user sees confirmation even if
              // the embed into Quill has trouble (e.g. stale selection).
              toast('success', 'Image added');
              if (!self.quill) return;
              try {
                var range = self.quill.getSelection(true);
                if (!range) {
                  // Focus dropped while the native file dialog was open -
                  // insert at the end of the document as fallback.
                  var end = self.quill.getLength();
                  self.quill.setSelection(end, 0);
                  range = self.quill.getSelection(true) || { index: end };
                }
                self.quill.insertEmbed(range.index, 'image', res.data.url, 'user');
                self.quill.setSelection(range.index + 1, 0);
              } catch (e) {
                console.error('Quill insertEmbed failed', e);
                toast('info', 'Image uploaded but could not be inserted - paste the URL: ' + res.data.url);
              }
            }).catch(function (err) {
              console.error('Image upload network error', err);
              toast('error', 'Image upload failed (network)');
            });
          };
          input.click();
        },

        submit: function () {
          if (this.submitting) return;
          if (this.charCount < 1 || this.charCount > this.max) return;
          this.submitting = true;
          var self = this;
          var form = this.$el;
          var content = this.htmlContent();
          var body = { content: content };

          // Legacy channel dropdown (optional; only present when the
          // compose partial is rendered with a channel list).
          var chanEl = form.querySelector('[name=channel_id]');
          if (chanEl && chanEl.value) body.channel_id = parseInt(chanEl.value, 10);

          // Parent id for reply forms.
          var parentEl = form.querySelector('[name=parent_id]');
          if (parentEl && parentEl.value) body.parent_id = parseInt(parentEl.value, 10);

          // Sprint 10: space, post type, tags.
          var spaceEl = form.querySelector('[name=space_id]');
          if (spaceEl && spaceEl.value) body.space_id = parseInt(spaceEl.value, 10);

          var typeEl = form.querySelector('[name=post_type]:checked') ||
                       form.querySelector('[name=post_type]');
          if (typeEl && typeEl.value) body.post_type = typeEl.value;

          // Tags come from the tagInput() Alpine component's hidden input
          // which holds comma-joined slugs. Empty string -> no tags.
          var tagsEl = form.querySelector('[name=tags]');
          if (tagsEl && tagsEl.value) {
            body.tags = tagsEl.value.split(',')
              .map(function (s) { return s.trim(); })
              .filter(function (s) { return s.length > 0; });
          }

          apiJSON('/api/posts', 'POST', body).then(function (res) {
            self.submitting = false;
            if (res.ok) {
              if (self.quill) self.quill.setContents([]);
              else self.$refs.editor.innerHTML = '';
              self.charCount = 0;
              toast('success', 'Posted');
              setTimeout(function () { window.location.reload(); }, 250);
            } else {
              toast('error', res.data.error || 'Could not post');
              form.classList.add('shake');
              setTimeout(function () { form.classList.remove('shake'); }, 500);
            }
          });
        }
      };
    });

    // Tag autocomplete input. Users type a tag name, the component
    // queries /api/tags/search and shows up to 10 suggestions. Pressing
    // Enter or comma commits the typed text (slugified client-side) as
    // a tag chip. Max 5 tags per post per Sprint 10 rules.
    //
    // Selected tag slugs are written to a hidden <input name="tags"> as a
    // comma-joined string; the composeEditor submit() reads that value.
    window.Alpine.data('tagInput', function (initial) {
      return {
        selectedTags: Array.isArray(initial) ? initial.slice(0, 5) : [],
        query: '',
        suggestions: [],
        maxTags: 5,
        loading: false,

        // Keep the hidden input in sync on every change so the outer
        // form picks up the current selection at submit time.
        hiddenValue: function () { return this.selectedTags.join(','); },

        slugify: function (name) {
          return (name || '')
            .toLowerCase()
            .trim()
            .replace(/\s+/g, '-')
            .replace(/[^a-z0-9-]/g, '')
            .replace(/-+/g, '-')
            .replace(/^-|-$/g, '')
            .slice(0, 32);
        },

        searchTags: function () {
          var q = this.query.trim();
          if (q.length < 1) { this.suggestions = []; return; }
          this.loading = true;
          var self = this;
          fetch('/api/tags/search?q=' + encodeURIComponent(q), {
            credentials: 'same-origin'
          }).then(function (r) { return r.ok ? r.json() : []; })
            .then(function (data) {
              self.loading = false;
              var already = new Set(self.selectedTags);
              self.suggestions = (data || []).filter(function (t) {
                return !already.has(t.slug);
              });
            }).catch(function () { self.loading = false; });
        },

        addTag: function (name) {
          var slug = this.slugify(name);
          if (!slug || slug.length < 2) return;
          if (this.selectedTags.length >= this.maxTags) {
            toast('info', 'Maximum ' + this.maxTags + ' tags');
            return;
          }
          if (this.selectedTags.includes(slug)) return;
          this.selectedTags.push(slug);
          this.query = '';
          this.suggestions = [];
        },

        removeTag: function (slug) {
          this.selectedTags = this.selectedTags.filter(function (t) { return t !== slug; });
        },

        // Enter and comma both commit the typed tag.
        handleKey: function (ev) {
          if (ev.key === 'Enter' || ev.key === ',') {
            ev.preventDefault();
            if (this.query.trim()) this.addTag(this.query);
          } else if (ev.key === 'Backspace' && !this.query && this.selectedTags.length > 0) {
            // Backspace on empty input pops the last chip, like Slack / GitHub.
            this.selectedTags.pop();
          }
        }
      };
    });

    // Global search
    window.Alpine.data('globalSearch', function () {
      return {
        query: '',
        active: false,
        loading: false,
        results: { posts: [], channels: [], users: [] },
        close() {
          this.active = false;
          this.query = '';
        },
        async doSearch() {
          var q = this.query.trim();
          if (q.length < 2) {
            this.results = { posts: [], channels: [], users: [] };
            return;
          }
          this.loading = true;
          try {
            var res = await fetch('/api/search?q=' + encodeURIComponent(q), { credentials: 'same-origin' });
            var data = await res.json();
            this.results = {
              posts:    data.posts || [],
              channels: data.channels || [],
              users:    data.users || []
            };
          } catch (e) { /* ignore */ }
          this.loading = false;
        },
        hasResults() {
          return this.results.posts.length + this.results.channels.length + this.results.users.length > 0;
        }
      };
    });
  });

  // ---------- header height sync ----------
  //
  // The util-bar and navbar are both `position: fixed`. Page content needs
  // top padding equal to their combined height so nothing hides behind
  // them. We measure on load, on resize, and via ResizeObserver so mobile
  // nav wraps, font loads, and zoom all stay correct without magic numbers.

  function syncHeaderHeights() {
    var util = document.querySelector('.util-bar');
    var nav = document.getElementById('navbar');
    var utilH = util ? util.offsetHeight : 38;
    var navH = nav ? nav.offsetHeight : 64;
    var root = document.documentElement;
    root.style.setProperty('--util-bar-height', utilH + 'px');
    root.style.setProperty('--navbar-height', navH + 'px');
    root.style.setProperty('--header-total', (utilH + navH) + 'px');
  }

  function initHeaderSync() {
    syncHeaderHeights();
    // Re-measure after layout shifts (font load, image reflow, etc.)
    window.addEventListener('load', syncHeaderHeights);
    window.addEventListener('resize', syncHeaderHeights);
    if ('ResizeObserver' in window) {
      var ro = new ResizeObserver(syncHeaderHeights);
      var util = document.querySelector('.util-bar');
      var nav = document.getElementById('navbar');
      if (util) ro.observe(util);
      if (nav) ro.observe(nav);
    }
    // Fonts API (Chrome/Firefox) - measure once fonts are swapped in.
    if (document.fonts && document.fonts.ready) {
      document.fonts.ready.then(syncHeaderHeights);
    }
  }

  // ---------- navbar scroll + mobile ----------

  function initNavbar() {
    var nav = document.getElementById('navbar');
    if (nav) {
      var onScroll = function () {
        nav.classList.toggle('scrolled', window.scrollY > 60);
      };
      window.addEventListener('scroll', onScroll, { passive: true });
      onScroll();
    }
    var toggle = document.getElementById('nav-toggle');
    var navLinks = document.getElementById('nav-links');
    if (toggle && navLinks) {
      toggle.addEventListener('click', function () {
        navLinks.classList.toggle('open');
        toggle.classList.toggle('active');
        document.body.style.overflow = navLinks.classList.contains('open') ? 'hidden' : '';
      });
      navLinks.querySelectorAll('a').forEach(function (a) {
        a.addEventListener('click', function () {
          navLinks.classList.remove('open');
          toggle.classList.remove('active');
          document.body.style.overflow = '';
        });
      });
    }
  }

  // ---------- register / login / settings / create-channel forms ----------

  function bindRegister() {
    var form = document.getElementById('register-form');
    if (!form || form.dataset.bound) return;
    form.dataset.bound = '1';
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      setError(form, '');
      var btn = form.querySelector('button[type=submit]');
      btn.disabled = true;
      apiJSON('/api/register', 'POST', {
        username: form.username.value,
        email: form.email.value,
        password: form.password.value
      }).then(function (res) {
        if (res.ok) {
          toast('success', 'Welcome to GoLab');
          setTimeout(function () { window.location.href = '/feed'; }, 300);
        } else {
          btn.disabled = false;
          setError(form, res.data.error || 'Registration failed');
        }
      }).catch(function () {
        btn.disabled = false;
        setError(form, 'Network error');
      });
    });
  }

  function bindLogin() {
    var form = document.getElementById('login-form');
    if (!form || form.dataset.bound) return;
    form.dataset.bound = '1';
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      setError(form, '');
      var btn = form.querySelector('button[type=submit]');
      btn.disabled = true;
      apiJSON('/api/login', 'POST', {
        email: form.email.value,
        password: form.password.value
      }).then(function (res) {
        if (res.ok) {
          toast('success', 'Welcome back');
          setTimeout(function () { window.location.href = '/feed'; }, 300);
        } else {
          btn.disabled = false;
          setError(form, res.data.error || 'Login failed');
        }
      }).catch(function () {
        btn.disabled = false;
        setError(form, 'Network error');
      });
    });
  }

  function bindSettings() {
    var form = document.getElementById('settings-form');
    if (!form || form.dataset.bound) return;
    form.dataset.bound = '1';
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      setError(form, '');
      var btn = form.querySelector('button[type=submit]');
      var label = btn.querySelector('.btn-label');
      btn.disabled = true;
      var original = label ? label.textContent : '';
      if (label) label.innerHTML = '<span class="spin-inline">Saving...</span>';
      apiJSON('/api/users/me', 'PUT', {
        display_name: form.display_name.value,
        bio: form.bio.value,
        avatar_url: form.avatar_url.value
      }).then(function (res) {
        btn.disabled = false;
        if (label) label.textContent = original || 'Save changes';
        if (res.ok) {
          toast('success', 'Profile saved');
          btn.classList.add('success-pop');
          setTimeout(function () { btn.classList.remove('success-pop'); }, 500);
        } else {
          setError(form, res.data.error || 'Could not save');
          form.classList.add('shake');
          setTimeout(function () { form.classList.remove('shake'); }, 500);
        }
      });
    });
  }

  function bindCreateChannel() {
    var form = document.getElementById('create-channel-form');
    if (!form || form.dataset.bound) return;
    form.dataset.bound = '1';
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      setError(form, '');
      var btn = form.querySelector('button[type=submit]');
      btn.disabled = true;
      apiJSON('/api/channels', 'POST', {
        slug: form.slug.value,
        name: form.name.value,
        description: form.description.value,
        channel_type: form.channel_type.value
      }).then(function (res) {
        btn.disabled = false;
        if (res.ok && res.data.channel) {
          toast('success', 'Channel created');
          window.location.href = '/c/' + res.data.channel.slug;
        } else {
          setError(form, res.data.error || 'Could not create channel');
        }
      });
    });
  }

  // Reply form (thread page) still uses a plain textarea so old posts'
  // reply flow keeps working. The main compose form is handled by the
  // composeEditor Alpine component and does NOT bind here.
  function bindCompose() {
    var form = document.getElementById('reply-form');
    if (!form || form.dataset.bound) return;
    form.dataset.bound = '1';
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var textarea = form.querySelector('textarea[name=content]');
      var content = (textarea.value || '').trim();
      if (content.length < 1 || content.length > 5000) return;
      var chanEl = form.querySelector('[name=channel_id]');
      var parentEl = form.querySelector('[name=parent_id]');
      var body = { content: content };
      if (chanEl && chanEl.value) body.channel_id = parseInt(chanEl.value, 10);
      if (parentEl && parentEl.value) body.parent_id = parseInt(parentEl.value, 10);
      var btn = form.querySelector('button[type=submit]');
      btn.disabled = true;
      apiJSON('/api/posts', 'POST', body).then(function (res) {
        btn.disabled = false;
        if (res.ok) {
          textarea.value = '';
          textarea.dispatchEvent(new Event('input'));
          toast('success', 'Reply posted');
          setTimeout(function () { window.location.reload(); }, 250);
        } else {
          toast('error', res.data.error || 'Could not post');
          form.classList.add('shake');
          setTimeout(function () { form.classList.remove('shake'); }, 500);
        }
      });
    });
  }

  // ---------- click-action delegation ----------

  function bindActions() {
    document.querySelectorAll('[data-action]').forEach(function (el) {
      if (el.dataset.bound) return;
      el.dataset.bound = '1';
      var action = el.dataset.action;

      if (action === 'logout') {
        el.addEventListener('click', function (e) {
          e.preventDefault();
          apiJSON('/api/logout', 'POST', null).then(function () {
            window.location.href = '/';
          });
        });
      }

      if (action === 'react') {
        // Sprint 10.5: clicking the react button opens a quick picker
        // with 6 emoji types. The old "like toggle" behaviour becomes
        // one of those 6 (heart).
        el.addEventListener('click', function (e) {
          e.stopPropagation();
          openReactionPicker(el);
        });
      }

      if (action === 'repost') {
        el.addEventListener('click', function () {
          var id = el.dataset.postId;
          apiJSON('/api/posts/' + id + '/repost', 'POST', null).then(function (res) {
            if (res.ok) {
              toast('success', 'Reposted');
            } else {
              toast('error', res.data.error || 'Could not repost');
            }
          });
        });
      }

      if (action === 'delete-post') {
        el.addEventListener('click', function () {
          if (!confirm('Delete this post?')) return;
          var id = el.dataset.postId;
          apiJSON('/api/posts/' + id, 'DELETE', null).then(function (res) {
            if (res.ok) {
              var card = document.getElementById('post-' + id);
              if (card) card.remove();
              toast('info', 'Post deleted');
            }
          });
        });
      }

      if (action === 'follow') {
        el.addEventListener('click', function () {
          var username = el.dataset.username;
          var willFollow = !el.classList.contains('active');
          el.classList.toggle('active');
          el.textContent = willFollow ? 'Following' : 'Follow';
          apiJSON('/api/users/' + username + '/follow', willFollow ? 'POST' : 'DELETE', null);
        });
      }

      if (action === 'channel-toggle') {
        el.addEventListener('click', function () {
          var slug = el.dataset.slug;
          var mode = el.dataset.mode === 'leave' ? 'leave' : 'join';
          apiJSON('/api/channels/' + slug + '/' + mode, 'POST', null).then(function (res) {
            if (res.ok) {
              toast('success', mode === 'join' ? 'Joined #' + slug : 'Left #' + slug);
              setTimeout(function () { window.location.reload(); }, 250);
            }
          });
        });
      }

      if (action === 'admin-ban') {
        el.addEventListener('click', function () {
          if (!confirm('Ban this user? They will be logged out and unable to sign back in.')) return;
          var id = el.dataset.userId;
          apiJSON('/api/admin/users/' + id + '/ban', 'POST', null).then(function (res) {
            if (res.ok) { toast('info', 'User banned'); setTimeout(function () { window.location.reload(); }, 250); }
            else toast('error', res.data.error || 'Failed');
          });
        });
      }

      if (action === 'admin-unban') {
        el.addEventListener('click', function () {
          var id = el.dataset.userId;
          apiJSON('/api/admin/users/' + id + '/unban', 'POST', null).then(function (res) {
            if (res.ok) { toast('success', 'User unbanned'); setTimeout(function () { window.location.reload(); }, 250); }
          });
        });
      }

      if (action === 'admin-set-power') {
        // <select> elements need change events, not click. We also revert
        // the selection visually if the server rejects the request.
        el.addEventListener('change', function () {
          var id = el.dataset.userId;
          var newValue = parseInt(el.value, 10);
          var previous = parseInt(el.dataset.current, 10);
          el.classList.add('saving');
          apiJSON('/api/admin/users/' + id + '/power', 'PUT', { power_level: newValue })
            .then(function (res) {
              el.classList.remove('saving');
              if (res.ok) {
                el.dataset.current = String(newValue);
                el.classList.add('success-pop');
                setTimeout(function () { el.classList.remove('success-pop'); }, 450);
                toast('success', 'Power level updated');
              } else {
                el.value = String(previous); // revert the visible selection
                toast('error', res.data.error || 'Could not update');
                el.classList.add('shake');
                setTimeout(function () { el.classList.remove('shake'); }, 500);
              }
            });
        });
      }
    });
  }

  // ---------- WebSocket client ----------
  //
  // Sprint 10.5: fixed connection leak.
  //
  // Previously each reconnect spawned a fresh WebSocket without closing
  // the existing one, and HTMX page swaps would create another one on
  // top. With a couple of users open for an hour, the server saw 30+
  // concurrent sockets from 2-3 people.
  //
  // Fix:
  //   1. Stash the active ws on window.__golabWS and close the old one
  //      before opening a new one (also catches re-inits after HTMX
  //      hx-boost swaps which re-run this module).
  //   2. Proper exponential backoff from 1s up to 30s; reset to 1s on
  //      a successful open so occasional hiccups don't cascade.
  //   3. Clear the scheduled-reconnect timer on beforeunload so a
  //      stale socket doesn't spawn after the page is gone.

  var wsClosed = false;
  var wsBackoff = 1000;         // current retry delay, doubles each fail
  var wsBackoffMax = 30000;
  var wsRetryTimer = null;

  function wsConnect() {
    // Close any existing socket before opening a new one.
    if (window.__golabWS) {
      try {
        window.__golabWS.onopen = null;
        window.__golabWS.onmessage = null;
        window.__golabWS.onclose = null;
        window.__golabWS.onerror = null;
        if (window.__golabWS.readyState === WebSocket.OPEN ||
            window.__golabWS.readyState === WebSocket.CONNECTING) {
          window.__golabWS.close(1000, 'reconnect');
        }
      } catch (e) { /* ignore */ }
      window.__golabWS = null;
    }

    var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    var sock;
    try {
      sock = new WebSocket(proto + '//' + location.host + '/ws');
    } catch (e) {
      scheduleReconnect();
      return;
    }
    window.__golabWS = sock;

    sock.onopen = function () {
      wsBackoff = 1000; // reset backoff on successful connect
      var ctx = detectContext();
      if (ctx) sock.send(JSON.stringify({ type: 'subscribe', topic: ctx }));
    };

    sock.onmessage = function (ev) {
      var msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      handleWSMessage(msg);
    };

    sock.onclose = function () {
      if (wsClosed) return;
      scheduleReconnect();
    };

    sock.onerror = function () {
      // Let the onclose handler above own the retry scheduling. Close
      // defensively in case the socket is hung in a half-open state.
      try { sock.close(); } catch (e) {}
    };
  }

  function scheduleReconnect() {
    if (wsRetryTimer) clearTimeout(wsRetryTimer);
    wsRetryTimer = setTimeout(function () {
      wsRetryTimer = null;
      wsConnect();
    }, wsBackoff);
    wsBackoff = Math.min(wsBackoff * 2, wsBackoffMax);
  }

  function detectContext() {
    // /c/:slug -> channel topic
    var m = location.pathname.match(/^\/c\/([a-z0-9\-]+)/);
    if (m) return 'channel:' + m[1];
    // default topic already auto-subscribed by server (global + user:N)
    return null;
  }

  function handleWSMessage(msg) {
    switch (msg.type) {
      case 'new_post':
        injectNewPost(msg.html);
        break;
      case 'notification':
        toast(msg.data && msg.data.level ? msg.data.level : 'info', msg.message || '');
        break;
      case 'notification_count':
        var count = msg.data && typeof msg.data.count === 'number' ? msg.data.count : 0;
        window.dispatchEvent(new CustomEvent('notif-count-push', { detail: count }));
        break;
      case 'reaction':
        if (msg.data && msg.data.post_id != null) {
          var counter = document.getElementById('react-count-' + msg.data.post_id);
          if (counter && msg.data.count != null) {
            counter.textContent = msg.data.count;
            counter.classList.add('number-bump');
            setTimeout(function () { counter.classList.remove('number-bump'); }, 320);
          }
        }
        break;
    }
  }

  function injectNewPost(html) {
    if (!html) return;
    var feed = document.getElementById('feed-posts');
    if (!feed) return;
    // Skip if already present
    var temp = document.createElement('div');
    temp.innerHTML = html.trim();
    var newCard = temp.firstElementChild;
    if (!newCard) return;
    if (document.getElementById(newCard.id)) return;
    newCard.classList.add('post-enter');
    feed.insertBefore(newCard, feed.firstChild);
    requestAnimationFrame(function () {
      newCard.classList.add('post-enter-active');
      setTimeout(function () {
        newCard.classList.remove('post-enter');
        newCard.classList.remove('post-enter-active');
      }, 400);
    });
    // Rebind actions for the new card
    bindActions();
  }

  // ---------- HTMX hooks ----------

  document.body.addEventListener('htmx:afterSwap', function () {
    bindAll();
  });

  // ---------- Sprint 10.5 reaction quick picker ----------
  //
  // The 6 types match the server's allowedReactionTypes map. Clicking
  // a type toggles the user's reaction via POST /api/posts/:id/react.
  // The server enforces one reaction per user per post; clicking the
  // currently-held type removes it, a different type switches it.

  var REACTION_EMOJI = {
    heart:     '\u{2764}\u{FE0F}',
    thumbsup:  '\u{1F44D}',
    laugh:     '\u{1F602}',
    surprised: '\u{1F62E}',
    sad:       '\u{1F622}',
    fire:      '\u{1F525}'
  };
  var REACTION_ORDER = ['heart', 'thumbsup', 'laugh', 'surprised', 'sad', 'fire'];

  function openReactionPicker(anchor) {
    var existing = document.querySelector('.reaction-picker');
    if (existing) { existing.remove(); return; }

    var postId = anchor.dataset.postId;
    if (!postId) return;

    var panel = document.createElement('div');
    panel.className = 'reaction-picker';

    REACTION_ORDER.forEach(function (type) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'reaction-picker-item';
      b.title = type;
      b.textContent = REACTION_EMOJI[type];
      b.addEventListener('click', function (ev) {
        ev.stopPropagation();
        panel.remove();
        submitReaction(anchor, postId, type);
      });
      panel.appendChild(b);
    });

    // Anchor above the react button.
    var rect = anchor.getBoundingClientRect();
    panel.style.top = (rect.top + window.scrollY - 48) + 'px';
    panel.style.left = (rect.left + window.scrollX) + 'px';
    document.body.appendChild(panel);

    setTimeout(function () {
      var closer = function (e) {
        if (!panel.contains(e.target) && e.target !== anchor) {
          panel.remove();
          document.removeEventListener('click', closer);
          document.removeEventListener('keydown', keyCloser);
        }
      };
      var keyCloser = function (e) {
        if (e.key === 'Escape') {
          panel.remove();
          document.removeEventListener('click', closer);
          document.removeEventListener('keydown', keyCloser);
        }
      };
      document.addEventListener('click', closer);
      document.addEventListener('keydown', keyCloser);
    }, 10);
  }

  function submitReaction(anchor, postId, type) {
    apiJSON('/api/posts/' + postId + '/react', 'POST', { reaction_type: type })
      .then(function (res) {
        if (!res.ok) {
          toast('error', (res.data && res.data.error) || 'Could not react');
          return;
        }
        // Update the button visuals from the server response.
        var data = res.data || {};
        anchor.classList.toggle('active', !!data.user_type);
        var icon = anchor.querySelector('.reaction-icon');
        var heart = anchor.querySelector('.reaction-heart');
        if (data.user_type) {
          if (icon) icon.textContent = REACTION_EMOJI[data.user_type] || '';
          if (heart) heart.style.display = 'none';
        } else {
          if (icon) icon.textContent = '';
          if (heart) heart.style.display = '';
        }
        var counter = anchor.querySelector('.reaction-count, #react-count-' + postId);
        if (counter && typeof data.count === 'number') {
          counter.textContent = data.count;
          counter.classList.add('number-bump');
          setTimeout(function () { counter.classList.remove('number-bump'); }, 320);
        }
        // Heart-pop micro animation
        anchor.classList.add('heart-pop');
        setTimeout(function () { anchor.classList.remove('heart-pop'); }, 400);
      })
      .catch(function () { toast('error', 'Network error'); });
  }

  // ---------- image lightbox ----------
  //
  // Any <img> inside a .post-content block opens in a full-screen lightbox
  // on click. Backdrop click and Escape both close. We delegate clicks on
  // document so new posts added via WebSocket/HTMX don't need rebinding.

  var lightboxBound = false;
  function initLightbox() {
    if (lightboxBound) return;
    var lb = document.getElementById('img-lightbox');
    var lbImg = document.getElementById('img-lightbox-img');
    if (!lb || !lbImg) return;
    lightboxBound = true;

    function open(src, alt) {
      lbImg.src = src;
      lbImg.alt = alt || '';
      lb.classList.add('active');
      lb.setAttribute('aria-hidden', 'false');
      document.body.style.overflow = 'hidden';
    }
    function close() {
      lb.classList.remove('active');
      lb.setAttribute('aria-hidden', 'true');
      document.body.style.overflow = '';
      // Wait for fade-out to finish, then clear src so memory releases.
      setTimeout(function () {
        if (!lb.classList.contains('active')) lbImg.src = '';
      }, 300);
    }

    // Delegate clicks from anywhere in the document.
    document.addEventListener('click', function (e) {
      var img = e.target && e.target.closest ? e.target.closest('.post-content img') : null;
      if (img) {
        e.preventDefault();
        open(img.getAttribute('src'), img.getAttribute('alt') || '');
        return;
      }
      // Click on backdrop or close button closes.
      if (e.target === lb || (e.target.closest && e.target.closest('.img-lightbox-close'))) {
        close();
      }
    });

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && lb.classList.contains('active')) close();
    });
  }

  // ---------- code block copy buttons + syntax highlighting ----------

  // Sprint 10.5: badge every .gif image in post content with a small
  // "GIF" chip. Runs on init and after HTMX / WebSocket swaps.
  function decorateGifs() {
    document.querySelectorAll('.post-content img').forEach(function (img) {
      if (img.dataset.gifBadgeBound) return;
      var src = (img.getAttribute('src') || '').toLowerCase();
      if (!src.endsWith('.gif') && src.indexOf('.gif?') === -1) return;
      img.dataset.gifBadgeBound = '1';
      // Wrap with a relative-positioned span so the badge anchors.
      var wrap = document.createElement('span');
      wrap.className = 'gif-wrap';
      img.parentNode.insertBefore(wrap, img);
      wrap.appendChild(img);
      var badge = document.createElement('span');
      badge.className = 'gif-badge';
      badge.textContent = 'GIF';
      wrap.appendChild(badge);
    });
  }

  function decorateCodeBlocks() {
    document.querySelectorAll('.post-content pre').forEach(function (pre) {
      // Syntax highlighting for Quill-output code blocks.
      // Quill uses <pre class="ql-syntax">; goldmark uses <pre><code class="language-xxx">.
      // Both work with highlight.js's auto-detect.
      if (window.hljs && !pre.dataset.hljsDone) {
        pre.dataset.hljsDone = '1';
        try { window.hljs.highlightElement(pre); } catch (e) { /* ignore */ }
      }

      if (pre.dataset.copyBound) return;
      pre.dataset.copyBound = '1';
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'code-copy-btn';
      btn.textContent = 'Copy';
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        e.stopPropagation();
        var code = pre.querySelector('code');
        var text = code ? code.textContent : pre.textContent;
        navigator.clipboard.writeText(text).then(function () {
          btn.textContent = 'Copied';
          btn.classList.add('copied', 'success-pop');
          setTimeout(function () {
            btn.textContent = 'Copy';
            btn.classList.remove('copied', 'success-pop');
          }, 1500);
        });
      });
      pre.appendChild(btn);
    });
  }

  // ---------- scroll reveal ----------

  var scrollObserver = null;
  function setupScrollReveal() {
    if (!('IntersectionObserver' in window)) return;
    if (!scrollObserver) {
      scrollObserver = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) {
          if (e.isIntersecting) {
            e.target.classList.add('visible');
            scrollObserver.unobserve(e.target);
          }
        });
      }, { threshold: 0.1 });
    }
    document.querySelectorAll('.scroll-reveal:not(.visible)').forEach(function (el) {
      scrollObserver.observe(el);
    });
  }

  // ---------- animated stat counters (admin dashboard) ----------

  function animateCounters() {
    document.querySelectorAll('.stat-counter[data-counter]').forEach(function (el) {
      if (el.dataset.animated) return;
      el.dataset.animated = '1';
      var target = parseInt(el.dataset.counter, 10) || 0;
      var duration = 900;
      var start = performance.now();
      function frame(now) {
        var p = Math.min((now - start) / duration, 1);
        var eased = 1 - Math.pow(1 - p, 3);
        el.textContent = Math.round(target * eased).toLocaleString();
        if (p < 1) requestAnimationFrame(frame);
      }
      requestAnimationFrame(frame);
    });
  }

  // ---------- search `/` keyboard shortcut ----------

  document.addEventListener('keydown', function (e) {
    if (e.key === '/' && !['INPUT', 'TEXTAREA'].includes(e.target.tagName) && !e.metaKey && !e.ctrlKey) {
      var el = document.querySelector('[data-search-input]');
      if (el) { e.preventDefault(); el.focus(); }
    }
  });

  function bindAll() {
    bindRegister();
    bindLogin();
    bindSettings();
    bindCreateChannel();
    bindCompose();
    bindActions();
    decorateCodeBlocks();
    decorateGifs();
    setupScrollReveal();
    animateCounters();
  }

  // ---------- init ----------

  function init() {
    initHeaderSync();
    initNavbar();
    initLightbox();
    bindAll();
    wsConnect();
    window.addEventListener('beforeunload', function () {
      wsClosed = true;
      if (wsRetryTimer) { clearTimeout(wsRetryTimer); wsRetryTimer = null; }
      if (window.__golabWS) {
        try { window.__golabWS.close(1000, 'unload'); } catch (e) {}
        window.__golabWS = null;
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
