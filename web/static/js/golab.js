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

  // Sprint 15a.1 P1 / P2: null-safe Quill selection helper.
  //
  // Sprint 15a's first attempt called quill.focus() and synthesised a
  // range. The synthesised index was correct, but insertEmbed STILL
  // threw "Cannot read properties of null (reading 'offset')" because
  // Quill 2.0.3's internal selection update (called as a side effect
  // of insertEmbed mutating the doc) fetched the live DOM selection,
  // which was still null - quill.focus() does not reliably move the
  // browser focus into the editor's DOM root in time.
  //
  // The new approach has three layers, applied by every caller:
  //
  //   1. Focus the EDITOR DOM ELEMENT (quill.root) directly. This is
  //      the actual contenteditable node and accepts focus deterministically.
  //   2. Commit the synthesised range to Quill's internal state via
  //      setSelection(idx, 0, 'silent'). The 'silent' source skips
  //      the selection-change event so we don't trigger the same
  //      update path that crashed.
  //   3. Schedule the actual insertion via setTimeout(fn, 0) so the
  //      browser finishes its post-file-dialog focus / blur shuffle
  //      before Quill mutates anything.
  //
  // quillSafeRange just returns the chosen range; the focus + commit
  // happen inside it so callers don't have to remember the dance.
  function quillSafeRange(quill) {
    if (!quill) return null;
    // 1. Focus the actual DOM editor element. quill.focus() is the
    //    Quill API call which we ALSO try, but the DOM call is what
    //    actually puts the cursor into a state where getSelection
    //    returns a real range.
    try {
      if (quill.root && typeof quill.root.focus === 'function') {
        quill.root.focus();
      }
      quill.focus();
    } catch (e) { /* ignore */ }

    var range = null;
    try { range = quill.getSelection(); } catch (e) { range = null; }
    if (range && typeof range.index === 'number') {
      return range;
    }

    // 2. Synthesise an end-of-document range. getLength() includes a
    //    trailing newline so the editable position is length - 1.
    var len = 0;
    try { len = quill.getLength(); } catch (e) { len = 0; }
    var end = len > 0 ? len - 1 : 0;
    var fallback = { index: end, length: 0 };

    // 3. Commit the synthesised range to Quill's internal state so
    //    its next getSelection() (called from inside insertEmbed's
    //    update pipeline) returns a real value instead of null. The
    //    'silent' source avoids re-firing the update we just escaped.
    try { quill.setSelection(end, 0, 'silent'); } catch (e) { /* ignore */ }
    return fallback;
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

    // Sprint 13: live username availability probe for the settings
    // form. Debounced on the input side (@input.debounce.500ms in the
    // template) so we don't hammer /api/users/check-username while
    // the user is mid-type. The server is the source of truth; this
    // is purely UX sugar to avoid a failed submit.
    //
    // Args:
    //   originalUsername - current handle, shown pre-filled
    //   allow            - platform flag allow_username_change (bool)
    //   isAdmin          - admins bypass the platform flag
    window.Alpine.data('usernameEditor', function (originalUsername, allow, isAdmin) {
      return {
        username: originalUsername || '',
        original: originalUsername || '',
        canEdit: !!(allow || isAdmin),
        statusText: '',
        statusClass: '',
        available: true,   // true means the submit button is not blocked by us
        checking: false,
        init: function () {
          var form = this.$root;
          if (form && form.tagName === 'FORM') {
            form.dataset.originalUsername = this.original;
          }
        },
        get canSubmit() {
          // The submit button listens to this. We only hard-block when
          // a username change is in flight AND the server said it's
          // invalid or taken. Same-as-current and empty (i.e. user
          // reverted) are fine - they just mean "don't touch it".
          if (!this.canEdit) return true;
          if (!this.username || this.username === this.original) return true;
          return this.available && !this.checking;
        },
        check: function () {
          var self = this;
          if (!self.canEdit) return;
          var value = (self.username || '').trim();
          if (!value || value === self.original) {
            self.statusText = '';
            self.statusClass = '';
            self.available = true;
            return;
          }
          if (!/^[a-zA-Z0-9_]{3,32}$/.test(value)) {
            self.statusText = 'Use 3-32 letters, numbers, or underscore.';
            self.statusClass = 'status-error';
            self.available = false;
            return;
          }
          self.checking = true;
          self.statusText = 'Checking...';
          self.statusClass = 'status-checking';
          fetch('/api/users/check-username?username=' + encodeURIComponent(value), {
            credentials: 'same-origin'
          }).then(function (r) { return r.json(); }).then(function (d) {
            self.checking = false;
            if (d && d.available) {
              self.statusText = 'Available';
              self.statusClass = 'status-ok';
              self.available = true;
              return;
            }
            self.available = false;
            if (d && d.reason === 'taken') {
              self.statusText = 'Already taken';
            } else if (d && d.reason === 'invalid') {
              self.statusText = 'Invalid format';
            } else if (d && d.reason === 'same') {
              // Shouldn't reach here thanks to the early return above,
              // but handle anyway.
              self.statusText = '';
              self.statusClass = '';
              self.available = true;
            } else {
              self.statusText = 'Not available';
            }
            self.statusClass = 'status-error';
          }).catch(function () {
            self.checking = false;
            self.statusText = '';
            self.statusClass = '';
            self.available = true; // don't block submit on a network blip
          });
        }
      };
    });

    // Sprint 13: password change form. Client-side confirm match +
    // POST to /api/users/me/password. On success the server revokes
    // every session (including this one) so we end up logged out and
    // hop to /login. The server also redirects form submissions to
    // /login?msg=password-changed, so if JS is off the flow still
    // works through the native form submit.
    window.Alpine.data('passwordForm', function () {
      return {
        current: '',
        next: '',
        confirm: '',
        busy: false,
        error: '',
        get mismatch() {
          return this.next && this.confirm && this.next !== this.confirm;
        },
        get canSubmit() {
          if (!this.current || !this.next || !this.confirm) return false;
          if (this.next.length < 8) return false;
          if (this.next !== this.confirm) return false;
          if (this.next === this.current) return false;
          return true;
        },
        onSubmit: function (ev) {
          ev.preventDefault();
          var self = this;
          if (!self.canSubmit || self.busy) return;
          self.error = '';
          self.busy = true;
          apiJSON('/api/users/me/password', 'POST', {
            current_password: self.current,
            new_password: self.next
          }).then(function (res) {
            self.busy = false;
            if (res.ok) {
              toast('success', 'Password updated. Please log in again.');
              // Server already cleared the session cookie. Give the
              // toast a beat, then hop to /login with the success flag.
              setTimeout(function () {
                window.location.href = '/login?msg=password-changed';
              }, 600);
              return;
            }
            if (res.status === 429) {
              self.error = 'Too many attempts. Try again in an hour.';
            } else {
              self.error = (res.data && res.data.error) || 'Could not update password';
            }
          }).catch(function () {
            self.busy = false;
            self.error = 'Network error';
          });
        }
      };
    });

    // Sprint 15a B6: edit-post modal component. Opens in response
    // to a golab:open-edit-post window event (fired by the
    // data-action="edit-post" click handler in bindActions), fetches
    // the post, mounts a fresh Quill instance pre-seeded with the
    // current content, and PATCHes /api/posts/{id} on save. The
    // Quill instance is created on each open and destroyed on
    // close so closing + reopening doesn't stack toolbars.
    window.Alpine.data('editPostModal', function () {
      return {
        open: false,
        loading: false,
        saving: false,
        error: '',
        postId: 0,
        quill: null,
        _initialHTML: '',

        openForPost: function (postId) {
          if (!postId) return;
          var self = this;
          self.postId = postId;
          self.error = '';
          self.loading = true;
          self.open = true;
          // Fetch the freshest post text (an admin might have
          // edited since the user's feed loaded).
          apiJSON('/api/posts/' + postId, 'GET', null).then(function (res) {
            self.loading = false;
            if (!res.ok || !res.data || !res.data.post) {
              self.error = (res.data && res.data.error) || 'Could not load post';
              return;
            }
            self._initialHTML = res.data.post.content || '';
            // Wait one frame so x-show has painted the editor div
            // before Quill measures its host.
            requestAnimationFrame(function () { self._mountQuill(); });
          });
        },

        _mountQuill: function () {
          if (this.quill) return; // already mounted
          var host = this.$refs.editor;
          if (!host || !window.Quill) return;
          this.quill = new window.Quill(host, {
            theme: 'snow',
            placeholder: "What do you want to say?",
            modules: {
              toolbar: [
                [{ header: [1, 2, 3, false] }],
                ['bold', 'italic', 'underline', 'strike'],
                ['link', 'blockquote', 'code-block'],
                [{ list: 'ordered' }, { list: 'bullet' }],
                ['clean']
              ]
            }
          });
          // Seed with the current post text. If it looks like HTML
          // (Quill was the original editor) paste it through the
          // clipboard pipeline; otherwise drop it in as plain text
          // so legacy Markdown posts don't render as mangled HTML.
          if (this._initialHTML) {
            if (/^\s*</.test(this._initialHTML)) {
              this.quill.clipboard.dangerouslyPasteHTML(this._initialHTML);
            } else {
              this.quill.setText(this._initialHTML);
            }
          }
        },

        hasContent: function () {
          if (!this.quill) return false;
          return (this.quill.getText() || '').replace(/\n$/, '').length > 0;
        },

        save: function () {
          var self = this;
          if (!self.quill || self.saving) return;
          var content = self.quill.root.innerHTML;
          if (content === '<p><br></p>') {
            self.error = 'Content cannot be empty';
            return;
          }
          self.saving = true;
          self.error = '';
          apiJSON('/api/posts/' + self.postId, 'PATCH', { content: content })
            .then(function (res) {
              self.saving = false;
              if (!res.ok) {
                self.error = (res.data && res.data.error) || 'Save failed';
                return;
              }
              toast('success', 'Post updated');
              // Update the card in place if it's visible on the
              // current page. Full re-render would be cleaner but
              // also more invasive; this keeps scroll position.
              var card = document.getElementById('post-' + self.postId);
              if (card && res.data.post) {
                var body = card.querySelector('.post-content');
                if (body) body.innerHTML = res.data.post.content_html || '';
                var meta = card.querySelector('.post-meta');
                if (meta && res.data.post.edited_at && !meta.querySelector('.post-edited')) {
                  var span = document.createElement('span');
                  span.className = 'post-edited';
                  span.textContent = 'edited';
                  meta.appendChild(span);
                }
              }
              self.close();
            })
            .catch(function () {
              self.saving = false;
              self.error = 'Network error';
            });
        },

        close: function () {
          this.open = false;
          this.error = '';
          this.saving = false;
          this.postId = 0;
          this._initialHTML = '';
          // Tear down Quill so the next open starts clean. Quill
          // doesn't expose a formal destroy, but removing the DOM
          // children clears its toolbar and editor wrappers.
          this.quill = null;
          var host = this.$refs && this.$refs.editor;
          if (host) host.innerHTML = '';
          var parent = host && host.parentElement;
          if (parent) {
            parent.querySelectorAll('.ql-toolbar').forEach(function (t) { t.remove(); });
          }
        }
      };
    });

    // Sprint 13: admin Database panel. Lists recent backups, creates
    // new ones, drives the import modal (with CONFIRM typing gate),
    // drives the per-row delete confirm, and paginates the list at
    // 10 rows per page. Every network call here hits /api/admin/db/*;
    // RequireAdmin middleware upstream blocks non-admins, Owner-only
    // gate for import is both server-side AND visual (the import
    // button is wrapped in a Go-template guard).
    window.Alpine.data('dbManager', function () {
      return {
        backups: [],
        loading: false,
        busy: false,
        action: '',           // 'backup' | 'import' | 'delete' | ''

        // Pagination - everything client-side. 10 per page is enough
        // to keep the table short; the API returns at most 50 entries
        // so 5 pages is the worst case.
        perPage: 10,
        currentPage: 1,

        // Import modal state.
        importOpen: false,
        importConfirmText: '',
        importFileName: '',
        importError: '',

        // Delete-confirm modal state.
        deleteOpen: false,
        deleteTarget: '',
        deleteError: '',

        // ------- list loading + pagination helpers -------

        loadBackups: function () {
          var self = this;
          self.loading = true;
          fetch('/api/admin/db/backups', { credentials: 'same-origin' })
            .then(function (r) { return r.json().catch(function () { return {}; }); })
            .then(function (d) {
              self.loading = false;
              self.backups = (d && d.backups) || [];
              // Clamp currentPage if a delete or refresh shrinks the list.
              var total = self.totalPages();
              if (self.currentPage > total) self.currentPage = total || 1;
            })
            .catch(function () { self.loading = false; });
        },
        totalPages: function () {
          if (!this.backups || this.backups.length === 0) return 1;
          return Math.max(1, Math.ceil(this.backups.length / this.perPage));
        },
        pagedBackups: function () {
          var start = (this.currentPage - 1) * this.perPage;
          return this.backups.slice(start, start + this.perPage);
        },
        pageList: function () {
          // For now we render every page number. With 50 max backups
          // at 10 per page we get at most 5 buttons - no need for
          // ellipsis logic yet. Revisit if the cap ever rises.
          var n = this.totalPages();
          var out = [];
          for (var i = 1; i <= n; i++) out.push(i);
          return out;
        },
        goToPage: function (p) {
          if (p < 1) p = 1;
          var max = this.totalPages();
          if (p > max) p = max;
          this.currentPage = p;
        },

        // ------- create backup -------

        createBackup: function () {
          var self = this;
          if (self.busy) return;
          self.busy = true; self.action = 'backup';
          apiJSON('/api/admin/db/backup', 'POST', null).then(function (res) {
            self.busy = false; self.action = '';
            if (res.ok) {
              toast('success', 'Backup created: ' + (res.data && res.data.name));
              self.loadBackups();
            } else {
              toast('error', (res.data && res.data.error) || 'Backup failed');
            }
          });
        },

        // ------- import modal -------

        openImportModal: function () {
          this.importOpen = true;
          this.importConfirmText = '';
          this.importError = '';
          this.importFileName = '';
          // Clear any previously staged file so re-opening starts fresh.
          if (this.$refs.importFile) this.$refs.importFile.value = '';
        },
        closeImportModal: function () {
          this.importOpen = false;
          this.importConfirmText = '';
          this.importError = '';
          this.importFileName = '';
          if (this.$refs.importFile) this.$refs.importFile.value = '';
        },
        runImport: function () {
          var self = this;
          if (self.importConfirmText !== 'CONFIRM' || self.busy) return;
          var file = self.$refs.importFile && self.$refs.importFile.files &&
                     self.$refs.importFile.files[0];
          if (!file) {
            self.importError = 'No file selected';
            return;
          }
          self.busy = true; self.action = 'import'; self.importError = '';
          var form = new FormData();
          form.append('file', file);
          fetch('/api/admin/db/import', {
            method: 'POST',
            body: form,
            credentials: 'same-origin'
          }).then(function (r) {
            return r.json().then(function (d) { return { ok: r.ok, status: r.status, data: d }; })
              .catch(function () { return { ok: r.ok, status: r.status, data: {} }; });
          }).then(function (res) {
            self.busy = false; self.action = '';
            if (res.ok) {
              toast('success', 'Database imported. Pre-import backup: ' +
                    (res.data && res.data.pre_backup));
              self.closeImportModal();
              self.loadBackups();
            } else {
              self.importError = (res.data && res.data.error) || 'Import failed';
            }
          }).catch(function () {
            self.busy = false; self.action = '';
            self.importError = 'Network error';
          });
        },

        // ------- delete modal -------

        openDeleteConfirm: function (name) {
          this.deleteTarget = name;
          this.deleteError = '';
          this.deleteOpen = true;
        },
        closeDeleteConfirm: function () {
          this.deleteOpen = false;
          this.deleteTarget = '';
          this.deleteError = '';
        },
        runDelete: function () {
          var self = this;
          if (!self.deleteTarget || self.busy) return;
          self.busy = true; self.action = 'delete'; self.deleteError = '';
          var name = self.deleteTarget;
          fetch('/api/admin/db/backups/' + encodeURIComponent(name), {
            method: 'DELETE',
            credentials: 'same-origin'
          }).then(function (r) {
            return r.json().then(function (d) { return { ok: r.ok, status: r.status, data: d }; })
              .catch(function () { return { ok: r.ok, status: r.status, data: {} }; });
          }).then(function (res) {
            self.busy = false; self.action = '';
            if (res.ok) {
              toast('info', 'Backup deleted: ' + name);
              self.closeDeleteConfirm();
              self.loadBackups();
            } else {
              self.deleteError = (res.data && res.data.error) || 'Delete failed';
            }
          }).catch(function () {
            self.busy = false; self.action = '';
            self.deleteError = 'Network error';
          });
        },

        // ------- formatting helpers -------

        formatSize: function (bytes) {
          if (bytes < 1024) return bytes + ' B';
          if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
          if (bytes < 1024 * 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB';
          return (bytes / 1024 / 1024 / 1024).toFixed(2) + ' GB';
        },
        formatDate: function (iso) {
          if (!iso) return '';
          try {
            var d = new Date(iso);
            return d.toLocaleString();
          } catch (e) { return iso; }
        },
        downloadUrl: function (name) {
          // Build the per-file download URL. encodeURIComponent
          // guards against stray characters; the server rejects
          // anything that isn't golab-*.sql with no path separators,
          // but URL-encoding on the client side keeps the path
          // clean and standards-compliant.
          return '/api/admin/db/backups/' + encodeURIComponent(name) + '/download';
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
        powerLevel: cfg.powerLevel || 0,

        // onSpaceChange is retained as a hook for the future in case
        // we ever gate spaces client-side again. For now it's a no-op
        // so the template's @change still works without errors.
        onSpaceChange: function () {},

        canSubmit: function () {
          if (this.submitting) return false;
          if (this.charCount < 1 || this.charCount > this.max) return false;
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

          // Sprint 14: attach @mention autocomplete. The module
          // is vendored in quill-mention.js and exposed as the
          // GolabQuillMention global. We tie it to this Alpine
          // component's lifecycle so teardown cleans up listeners
          // (see destroy path below for the .destroy() call).
          if (typeof window.GolabQuillMention === 'function') {
            try {
              this.mention = new window.GolabQuillMention(this.quill, {
                fetchUrl: '/api/users/autocomplete'
              });
            } catch (err) { /* autocomplete is best-effort */ }
          }

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
              panel.remove();
              if (!self.quill) return;
              // Sprint 15a.1 P2: same defer-and-paste-fallback pattern
              // as the image upload (P1). The picker also steals focus
              // and the post-Sprint-15a fix was insufficient on its own.
              var emojiText = e;
              setTimeout(function () {
                if (!self.quill) return;
                var range = quillSafeRange(self.quill);
                if (!range) return;
                try {
                  self.quill.insertText(range.index, emojiText, 'user');
                  self.quill.setSelection(range.index + emojiText.length, 0, 'user');
                } catch (err) {
                  console.error('Quill emoji insertText failed, trying paste fallback', err);
                  try {
                    self.quill.clipboard.dangerouslyPasteHTML(
                      range.index, emojiText, 'user',
                    );
                    self.quill.setSelection(range.index + emojiText.length, 0, 'user');
                  } catch (err2) {
                    console.error('Quill emoji paste fallback also failed', err2);
                  }
                }
              }, 0);
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
          // Sprint 14: tear down the mention autocomplete so its
          // document-level listeners and dropdown element don't
          // leak past the editor.
          if (this.mention && typeof this.mention.destroy === 'function') {
            try { this.mention.destroy(); } catch (e) {}
          }
          this.mention = null;
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
              // Sprint 15a.1 P1: defer to next tick so the file dialog's
              // post-close focus / blur events have settled before
              // Quill mutates anything. quillSafeRange already focused
              // the editor and committed a synthesised selection, but
              // insertEmbed still ran while the focus was in flight on
              // the live deploy and crashed inside Quill's selection
              // update. The setTimeout buys one event-loop turn for
              // the browser to settle.
              var imageUrl = res.data.url;
              setTimeout(function () {
                if (!self.quill) return;
                var range = quillSafeRange(self.quill);
                if (!range) {
                  toast('info', 'Image uploaded but could not be inserted - paste the URL: ' + imageUrl);
                  return;
                }
                try {
                  self.quill.insertEmbed(range.index, 'image', imageUrl, 'user');
                  self.quill.setSelection(range.index + 1, 0, 'user');
                } catch (e) {
                  // Sprint 15a.1 P1 fallback: insertEmbed's internal
                  // selection update is the line that crashed on live.
                  // dangerouslyPasteHTML goes through the HTML -> Delta
                  // path which doesn't touch the same selection code,
                  // so it's our last-ditch attempt before showing the
                  // "paste the URL" instruction toast.
                  console.error('Quill insertEmbed failed, trying paste fallback', e);
                  try {
                    self.quill.clipboard.dangerouslyPasteHTML(
                      range.index,
                      '<img src="' + imageUrl + '">',
                      'user',
                    );
                    self.quill.setSelection(range.index + 1, 0, 'user');
                  } catch (e2) {
                    console.error('Quill paste fallback also failed', e2);
                    toast('info', 'Image uploaded but could not be inserted - paste the URL: ' + imageUrl);
                  }
                }
              }, 0);
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

  // ---------- navbar scroll + mobile overlay ----------

  function initNavbar() {
    var nav = document.getElementById('navbar');
    if (nav) {
      var onScroll = function () {
        nav.classList.toggle('scrolled', window.scrollY > 60);
      };
      window.addEventListener('scroll', onScroll, { passive: true });
      onScroll();
    }

    // Sprint 10.5: fullscreen #mobile-menu overlay replaces the old
    // .nav-links slide-in. Hamburger opens, X button / backdrop /
    // ESC / link-click all close. Body scroll is locked while open.
    var toggle = document.getElementById('nav-toggle');
    var menu   = document.getElementById('mobile-menu');
    if (!toggle || !menu) return;
    var closeBtn = document.getElementById('mobile-menu-close');

    function open() {
      menu.classList.add('open');
      toggle.classList.add('active');
      toggle.setAttribute('aria-expanded', 'true');
      menu.setAttribute('aria-hidden', 'false');
      document.body.style.overflow = 'hidden';
    }
    function close() {
      menu.classList.remove('open');
      toggle.classList.remove('active');
      toggle.setAttribute('aria-expanded', 'false');
      menu.setAttribute('aria-hidden', 'true');
      document.body.style.overflow = '';
    }
    toggle.addEventListener('click', function () {
      if (menu.classList.contains('open')) close(); else open();
    });
    if (closeBtn) closeBtn.addEventListener('click', close);

    // Clicking any link inside the menu auto-closes. Logout button is
    // a button, not an <a>, so include [data-action="logout"] too.
    menu.querySelectorAll('a, [data-action=logout]').forEach(function (el) {
      el.addEventListener('click', function () {
        setTimeout(close, 0); // let the link's own handler run first
      });
    });

    // Escape closes the overlay from anywhere.
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && menu.classList.contains('open')) close();
    });

    // If the viewport grows back to desktop while the overlay is open
    // (orientation change, dev-tools resize), close it cleanly.
    window.addEventListener('resize', function () {
      if (window.innerWidth > 900 && menu.classList.contains('open')) close();
    });
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
      // Sprint 13: username is optional in the payload. Send it only
      // when the user actually changed it so "profile save" stays a
      // no-op for the username side. The server treats empty Username
      // as "don't touch it".
      var payload = {
        display_name: form.display_name.value,
        bio: form.bio.value,
        avatar_url: form.avatar_url.value
      };
      if (form.username && form.username.value) {
        payload.username = form.username.value;
      }
      apiJSON('/api/users/me', 'PUT', payload).then(function (res) {
        btn.disabled = false;
        if (label) label.textContent = original || 'Save changes';
        if (res.ok) {
          toast('success', 'Profile saved');
          btn.classList.add('success-pop');
          setTimeout(function () { btn.classList.remove('success-pop'); }, 500);
          // If the username changed, every existing /u/<old> link on
          // this page is stale. A soft reload keeps things simple.
          if (payload.username && form.dataset.originalUsername &&
              payload.username !== form.dataset.originalUsername) {
            setTimeout(function () { window.location.reload(); }, 400);
          }
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

  // Hide the pending-approval section when its last card is removed,
  // and keep the count badge in sync after every approval/rejection.
  function maybeHidePendingSection() {
    var section = document.querySelector('.admin-pending');
    if (!section) return;
    var remaining = section.querySelectorAll('.admin-pending-card').length;
    var badge = section.querySelector('.admin-pending-count');
    if (badge) badge.textContent = remaining;
    if (remaining === 0) {
      section.classList.add('collapse-height');
      setTimeout(function () { section.remove(); }, 240);
    }
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
        // Sprint 14: every emoji chip carries its own data-action="react"
        // and data-reaction-type. Clicking toggles that specific triple
        // on the server and updates the chip inline - no popup picker.
        el.addEventListener('click', function (e) {
          e.stopPropagation();
          submitReaction(el);
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

      if (action === 'edit-post') {
        // Sprint 15a B6: fire a window CustomEvent instead of poking
        // the modal DOM directly. editPostModal() in the base
        // template listens for it, fetches the post, and mounts a
        // Quill instance pre-filled with the current content.
        el.addEventListener('click', function () {
          var id = el.dataset.postId;
          if (!id) return;
          window.dispatchEvent(new CustomEvent('golab:open-edit-post', {
            detail: { postId: parseInt(id, 10) }
          }));
        });
      }

      if (action === 'delete-post') {
        el.addEventListener('click', function () {
          if (!confirm('Delete this post? This cannot be undone.')) return;
          var id = el.dataset.postId;
          apiJSON('/api/posts/' + id, 'DELETE', null).then(function (res) {
            if (res.ok) {
              // Sprint 15a B5: fade the card out locally. The server
              // WS broadcast also removes it for every other open
              // feed via the post_deleted case in handleWSMessage,
              // so a peer browser sees the same animation.
              var card = document.getElementById('post-' + id);
              if (card) {
                card.classList.add('post-leave');
                setTimeout(function () { card.remove(); }, 220);
              }
              toast('info', 'Post deleted');
            } else {
              toast('error', (res.data && res.data.error) || 'Could not delete post');
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

      if (action === 'admin-approve') {
        el.addEventListener('click', function () {
          var id = el.dataset.userId;
          var card = document.getElementById('pending-user-' + id);
          el.disabled = true;
          apiJSON('/api/admin/users/' + id + '/approve', 'POST', null).then(function (res) {
            if (res.ok) {
              toast('success', 'User approved');
              if (card) {
                card.classList.add('slide-out-left');
                setTimeout(function () { card.remove(); maybeHidePendingSection(); }, 260);
              }
            } else {
              el.disabled = false;
              toast('error', (res.data && res.data.error) || 'Could not approve');
            }
          });
        });
      }

      if (action === 'admin-reject') {
        el.addEventListener('click', function () {
          if (!confirm('Reject this user? They will be locked out.')) return;
          var id = el.dataset.userId;
          var card = document.getElementById('pending-user-' + id);
          el.disabled = true;
          apiJSON('/api/admin/users/' + id + '/reject', 'POST', null).then(function (res) {
            if (res.ok) {
              toast('info', 'User rejected');
              if (card) {
                card.classList.add('slide-out-left');
                setTimeout(function () { card.remove(); maybeHidePendingSection(); }, 260);
              }
            } else {
              el.disabled = false;
              toast('error', (res.data && res.data.error) || 'Could not reject');
            }
          });
        });
      }

      if (action === 'admin-toggle-setting') {
        el.addEventListener('change', function () {
          var key = el.dataset.key;
          var value = el.checked ? 'true' : 'false';
          el.disabled = true;
          apiJSON('/api/admin/settings/' + encodeURIComponent(key), 'PUT', { value: value })
            .then(function (res) {
              el.disabled = false;
              if (res.ok) {
                toast('success', 'Setting updated');
              } else {
                // Revert on failure so the UI mirrors server state.
                el.checked = !el.checked;
                toast('error', (res.data && res.data.error) || 'Could not save');
              }
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

      // Sprint 13: admin rename. A native prompt() is plenty for the
      // occasional rename action, keeps the admin table simple, and
      // sidesteps building a modal for a near-never-used path.
      if (action === 'admin-rename') {
        el.addEventListener('click', function () {
          var id = el.dataset.userId;
          var current = el.dataset.currentUsername || '';
          var next = window.prompt('New username for @' + current + ':', current);
          if (next === null) return; // cancelled
          next = next.trim();
          if (!next || next === current) return;
          if (!/^[a-zA-Z0-9_]{3,32}$/.test(next)) {
            toast('error', 'Invalid format (3-32 letters, numbers, underscore).');
            return;
          }
          el.disabled = true;
          apiJSON('/api/admin/users/' + id + '/username', 'PUT', { username: next })
            .then(function (res) {
              el.disabled = false;
              if (res.ok) {
                toast('success', 'Username updated');
                setTimeout(function () { window.location.reload(); }, 300);
              } else {
                toast('error', (res.data && res.data.error) || 'Could not rename');
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
      case 'post_deleted':
        // Sprint 15a B5: remove the card when another user deletes
        // a post we're looking at. Server sends msg.data.id as the
        // post id; we match on DOM id `post-<n>` which post-card.html
        // writes when the card is rendered.
        if (msg.data && msg.data.id != null) {
          var card = document.getElementById('post-' + msg.data.id);
          if (card) {
            card.classList.add('post-leave');
            setTimeout(function () { card.remove(); }, 220);
          }
        }
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
    var temp = document.createElement('div');
    temp.innerHTML = html.trim();
    var newCard = temp.firstElementChild;
    if (!newCard) return;
    // Skip if we already rendered this post (can happen when the
    // sender's own socket echoes the broadcast back).
    if (newCard.id && document.getElementById(newCard.id)) return;

    newCard.classList.add('post-enter');
    feed.insertBefore(newCard, feed.firstChild);
    requestAnimationFrame(function () {
      newCard.classList.add('post-enter-active');
      setTimeout(function () {
        newCard.classList.remove('post-enter');
        newCard.classList.remove('post-enter-active');
      }, 400);
    });

    // Sprint 15a B2 / B3: walk the new subtree with Alpine so every
    // x-data on the injected card (post actions menu, reaction bar
    // state, reply compose forms) initialises. Without this the
    // three-dot dropdown stays dead until a full page reload.
    // Alpine.initTree is a no-op on trees already initialised, so
    // running it on the subtree is safe even if Alpine ever walked
    // it earlier through some other mechanism.
    if (window.Alpine && typeof window.Alpine.initTree === 'function') {
      try { window.Alpine.initTree(newCard); } catch (e) {
        console.error('Alpine.initTree failed on new post card', e);
      }
    }
    // Bind data-action click handlers (react chips, delete, etc.)
    // on the new card. bindActions is idempotent via dataset.bound.
    bindActions();
  }

  // ---------- HTMX hooks ----------

  document.body.addEventListener('htmx:afterSwap', function () {
    bindAll();
  });

  // ---------- Sprint 14 multi-reaction chip handler ----------
  //
  // Each .reaction-chip carries data-post-id and data-reaction-type
  // and invokes submitReaction on click. The server response returns
  // the full post state (all 6 counts + the caller's active types),
  // which we fold back into every chip in the same bar. Optimistic
  // UI toggles the clicked chip immediately and rolls back on error.
  // A custom event golab:reaction-updated fires on success so future
  // ranking widgets can hook without touching this code.

  function submitReaction(chip) {
    var postId = chip.dataset.postId;
    var type   = chip.dataset.reactionType;
    if (!postId || !type) return;

    var bar = chip.closest('.reaction-bar');
    if (!bar) return;

    // Optimistic toggle so the click feels instant. We remember the
    // pre-click state to roll back on network / server error.
    var wasActive = chip.classList.contains('is-active');
    chip.classList.toggle('is-active', !wasActive);
    chip.setAttribute('aria-pressed', String(!wasActive));
    var countEl = chip.querySelector('.reaction-count');
    var prevCount = parseInt(countEl && countEl.textContent, 10) || 0;
    var optimisticCount = wasActive ? Math.max(0, prevCount - 1) : prevCount + 1;
    if (countEl) {
      countEl.textContent = String(optimisticCount);
      chip.classList.toggle('is-empty', optimisticCount === 0);
    }

    apiJSON('/api/posts/' + postId + '/react', 'POST', { reaction_type: type })
      .then(function (res) {
        if (!res.ok) {
          // Roll back the optimistic update.
          chip.classList.toggle('is-active', wasActive);
          chip.setAttribute('aria-pressed', String(wasActive));
          if (countEl) {
            countEl.textContent = String(prevCount);
            chip.classList.toggle('is-empty', prevCount === 0);
          }
          toast('error', (res.data && res.data.error) || 'Could not react');
          return;
        }
        var data = res.data || {};
        var counts = data.counts || {};
        var userTypes = data.user_types || [];

        // Sync every chip in the bar from the authoritative server
        // state. Optimistic value may have been wrong if the server
        // deduped a concurrent reaction.
        bar.querySelectorAll('.reaction-chip').forEach(function (other) {
          var t = other.dataset.reactionType;
          var n = counts[t];
          if (typeof n === 'number') {
            var otherCount = other.querySelector('.reaction-count');
            if (otherCount) {
              var before = parseInt(otherCount.textContent, 10) || 0;
              otherCount.textContent = String(n);
              if (n !== before) {
                otherCount.classList.add('bump');
                setTimeout(function () { otherCount.classList.remove('bump'); }, 340);
              }
            }
            other.classList.toggle('is-empty', n === 0);
          }
          var active = userTypes.indexOf(t) >= 0;
          other.classList.toggle('is-active', active);
          other.setAttribute('aria-pressed', String(active));
        });

        window.dispatchEvent(new CustomEvent('golab:reaction-updated', {
          detail: { postId: postId, type: type, result: data.result,
                    counts: counts, userTypes: userTypes }
        }));
      })
      .catch(function () {
        chip.classList.toggle('is-active', wasActive);
        chip.setAttribute('aria-pressed', String(wasActive));
        if (countEl) {
          countEl.textContent = String(prevCount);
          chip.classList.toggle('is-empty', prevCount === 0);
        }
        toast('error', 'Network error');
      });
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
