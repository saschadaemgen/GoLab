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

    // Sprint Y.3 brutalist registration wizard. Replaces the
    // Sprint Y.2 5-step cinematic-card variant. Same eleven
    // form fields, but now split across 11 wizard indices so
    // each step focuses on exactly one prompt. Carries every
    // Sprint X.1 / Y.1 contract forward (fetch submit, typed
    // {field, code, message} errors, optional external_links,
    // four knowledge fields).
    //
    // The whole wizard is one component; per-step Alpine sub-
    // scopes are only used for transient UI state. All form
    // values live on this.data so the Review step can reflect
    // them and the final submit() can serialize them all in
    // one POST.
    //
    // 11 step indices:
    //   0 Welcome              required = false (no fields)
    //   1 Account               required (username + password)
    //   2 External links        optional
    //   3 Ecosystem connection  required
    //   4 Community contribution required
    //   5 Current focus         optional
    //   6 Application notes     optional
    //   7 Technical depth       required (choice + answer)
    //   8 Practical experience  optional
    //   9 Critical thinking     optional
    //  10 Review                no fields, just the submit
    //
    // direction is "forward" or "backward"; the wizard root has
    // :data-direction so CSS in golab.css can pick the right
    // slide animation per step transition. Set BEFORE the step
    // mutation so the attribute is in place when x-transition
    // reads the surrounding state.
    //
    // Keyboard handling: Esc anywhere -> back(). Enter inside
    // an INPUT (not TEXTAREA - the latter must keep newline
    // behaviour) -> next(). Wired via @keydown in the template
    // root.
    window.Alpine.data('registrationWizardBrutalist', function () {
      return {
        step: 0,
        direction: 'forward',
        submitting: false,
        submitted: false,
        submitError: '',
        fieldErrors: {},

        // Sprint Y.4: live username availability check. Status is
        // one of: idle | checking | available | taken | reserved
        // | invalid | error. Drives the UI pills on step 1 and
        // gates the Continue button (only "available" lets the
        // user advance). Debounced 400ms via usernameCheckTimer
        // so we do not slam the endpoint on every keystroke.
        usernameStatus: 'idle',
        usernameCheckTimer: null,

        // The step list drives the sidebar render and the
        // Skip/Continue button visibility. `required: true`
        // hides the Skip button on that step.
        steps: [
          { label: 'Welcome',        required: false },
          { label: 'Account',        required: true  },
          { label: 'External links', required: false },
          { label: 'Ecosystem',      required: true  },
          { label: 'Contribution',   required: true  },
          { label: 'Current focus',  required: false },
          { label: 'Notes',          required: false },
          { label: 'Technical',      required: true  },
          { label: 'Practical',      required: false },
          { label: 'Critical',       required: false },
          { label: 'Review',         required: false }
        ],

        data: {
          username: '',
          password: '',
          external_links: '',
          ecosystem_connection: '',
          community_contribution: '',
          current_focus: '',
          application_notes: '',
          technical_depth_choice: '',
          technical_depth_answer: '',
          practical_experience: '',
          critical_thinking: ''
        },

        init: function () {
          // No-op for now; placeholder for any future deep-link
          // hash handling (e.g. /register#step=4 to jump back
          // into a partial application).
        },

        // ---- Progress + sidebar quote ----

        progressPct: function () {
          // Step 0 (welcome) reads 0%. Step N>0 reads (N / 10) * 100.
          // 10 because step 10 is the review-and-submit step; once
          // the user hits review we say 100% complete.
          return Math.round((this.step / (this.steps.length - 1)) * 100);
        },

        // sidebarQuote returns a contextual one-liner displayed
        // in the bottom of the sidebar. Each step has its own
        // editorial line; from step 3 onward we drop the
        // applicant's @handle into the quote so the panel feels
        // like a private side-channel ("welcome notes" for the
        // reviewer, but addressed at this specific applicant).
        // Steps 0-2 stay neutral because we may not have a
        // valid handle yet at those points.
        sidebarQuote: function () {
          var name = this.displayName();
          var quotes = [
            '"Read access is open. Write access is reviewed personally. We do not optimise for growth."',
            '"Your handle is permanent. Pick something you would want to read on a security advisory three years from now."',
            '"A blank links field is fine. Padding it with random GitHub stars is not."',
            '"Substance, not buzzwords ' + name + '. Be specific."',
            '"What perspective will ' + name + ' bring? \'I care about privacy\' is not one."',
            '"Tell us, ' + name + ', what you would do - not what you would consume."',
            '"Optional. If there is nothing extra, do not invent some."',
            '"We test how ' + name + ' thinks, not what ' + name + ' has memorised."',
            '"\'No\' is a complete answer, ' + name + '. Honesty weighs more than padding."',
            '"Be specific or skip. \'Big Tech\' is a non-answer."',
            '"Looking good, ' + name + '. Read it once. Submit. We respond within seven days."'
          ];
          return quotes[this.step] || quotes[0];
        },

        // linkCount counts how many https URLs are present in
        // the external_links blob. Used by the step-2 link
        // detector. Same tokenization the server uses
        // (whitespace + comma split, https scheme required).
        linkCount: function () {
          var s = this.data.external_links || '';
          if (!s) return 0;
          var n = 0;
          var tokens = s.split(/[\s,]+/);
          for (var i = 0; i < tokens.length; i++) {
            var t = tokens[i].trim();
            if (!t) continue;
            try {
              var u = new URL(t);
              if (u.protocol === 'https:' && u.host) n++;
            } catch (e) { /* not a URL, skip */ }
          }
          return n;
        },

        // ---- Per-step validation ----

        // Sprint Y.4: usernameValid is now driven by the live
        // availability check, not just the format regex. The
        // pattern is enforced on the watchUsername() side so
        // the local-validation pill can flip to "INVALID FORMAT"
        // before the network round trip; here we only return
        // true when the server has confirmed the name is free.
        usernameValid: function () {
          return this.usernameStatus === 'available';
        },
        // usernameFormatValid is the cheap regex check used by
        // watchUsername to short-circuit the network call.
        usernameFormatValid: function () {
          return /^[A-Za-z0-9_]{3,32}$/.test(this.data.username || '');
        },
        passwordValid: function () {
          var p = this.data.password || '';
          return p.length >= 8 && p.length <= 128;
        },

        // watchUsername fires on @input on the username field.
        // Cancels any pending check, runs the cheap regex check
        // immediately, then debounces 400ms before hitting the
        // backend. The 400ms is enough time for a typing burst
        // to settle without the user feeling like they have to
        // wait. Status transitions: idle -> checking -> one of
        // available / taken / reserved / invalid / error.
        watchUsername: function () {
          var self = this;
          if (self.usernameCheckTimer) {
            clearTimeout(self.usernameCheckTimer);
            self.usernameCheckTimer = null;
          }
          var u = (self.data.username || '').trim();
          if (!u) {
            self.usernameStatus = 'idle';
            return;
          }
          if (!self.usernameFormatValid()) {
            self.usernameStatus = 'invalid';
            return;
          }
          self.usernameStatus = 'checking';
          self.usernameCheckTimer = setTimeout(function () {
            fetch('/api/auth/username-available?u=' + encodeURIComponent(u), {
              credentials: 'same-origin'
            }).then(function (r) {
              if (r.status === 429) {
                self.usernameStatus = 'error';
                return null;
              }
              return r.text().then(function (text) {
                var body = {};
                try { body = JSON.parse(text); } catch (e) { /* ignore */ }
                return { ok: r.ok, body: body };
              });
            }).then(function (res) {
              if (!res) return; // 429 already handled
              // Stale-response guard: if the user kept typing while
              // the check was in flight, only honour the response
              // when the value still matches what we sent.
              if ((self.data.username || '').trim() !== u) return;
              if (res.body && res.body.available) {
                self.usernameStatus = 'available';
                return;
              }
              self.usernameStatus = (res.body && res.body.reason) || 'taken';
            }).catch(function () {
              self.usernameStatus = 'error';
            });
          }, 400);
        },

        // displayName powers the Sprint Y.4 personalization in step
        // headlines + sidebar quotes. Returns "@<username>" once the
        // applicant has typed a valid handle, "you" otherwise so
        // copy reads naturally on the empty-input case.
        displayName: function () {
          var u = (this.data.username || '').trim();
          if (!u) return 'you';
          return '@' + u;
        },

        isCurrentStepValid: function () {
          var d = this.data;
          switch (this.step) {
            case 0: return true; // Welcome (no fields)
            // Sprint Y.4: usernameValid() now demands the live
            // availability check has resolved to 'available' -
            // an applicant with a "TAKEN" or "RESERVED" status
            // pill cannot click Continue.
            case 1: return this.usernameValid() && this.passwordValid();
            case 2: return d.external_links.length <= 500;
            case 3: return d.ecosystem_connection.length >= 30 && d.ecosystem_connection.length <= 800;
            case 4: return d.community_contribution.length >= 30 && d.community_contribution.length <= 600;
            case 5: return d.current_focus.length <= 400;
            case 6: return d.application_notes.length <= 300;
            case 7:
              if (!{ a: 1, b: 1, c: 1 }[d.technical_depth_choice]) return false;
              var len = (d.technical_depth_answer || '').length;
              return len >= 100 && len <= 500;
            case 8: return d.practical_experience.length <= 400;
            case 9: return d.critical_thinking.length <= 400;
            case 10: return true;
          }
          return true;
        },

        // ---- Navigation ----

        next: function () {
          if (!this.isCurrentStepValid()) return;
          this.direction = 'forward';
          if (this.step < this.steps.length - 1) this.step++;
        },
        back: function () {
          if (this.step <= 0) return;
          this.direction = 'backward';
          this.step--;
        },
        skip: function () {
          // Only optional steps may skip. The Skip button is
          // hidden via x-show on required steps; this guard is
          // belt-and-braces.
          if (!this.steps[this.step] || this.steps[this.step].required) return;
          this.direction = 'forward';
          if (this.step < this.steps.length - 1) this.step++;
        },
        goToStep: function (n) {
          if (n === this.step) return;
          if (n < 0 || n >= this.steps.length) return;
          this.direction = n > this.step ? 'forward' : 'backward';
          this.step = n;
        },

        // onEnter fires from @keydown.enter on the wizard root.
        // We forward to next() ONLY when the focused element is
        // a non-textarea form field, so newlines in textareas
        // continue to work normally.
        onEnter: function (e) {
          var t = e.target;
          if (t && t.tagName === 'TEXTAREA') return;
          // Don't hijack Enter inside the choice picker buttons;
          // those fire on click, and Enter on a focused button
          // already triggers click natively.
          if (t && t.tagName === 'BUTTON') return;
          // Welcome step has no inputs; let Enter advance.
          if (!this.isCurrentStepValid()) return;
          e.preventDefault();
          this.next();
        },

        // stepForField maps a server-returned field name back
        // to the wizard step that owns it.
        stepForField: function (field) {
          var map = {
            username: 1, password: 1,
            external_links: 2,
            ecosystem_connection: 3,
            community_contribution: 4,
            current_focus: 5,
            application_notes: 6,
            technical_depth_choice: 7, technical_depth_answer: 7,
            practical_experience: 8,
            critical_thinking: 9
          };
          return Object.prototype.hasOwnProperty.call(map, field) ? map[field] : 0;
        },

        // ---- Final submit ----

        submit: function () {
          var self = this;
          if (self.submitting || self.submitted) return;
          self.submitting = true;
          self.submitError = '';
          self.fieldErrors = {};

          fetch('/api/register', {
            method: 'POST',
            headers: {
              'Accept': 'application/json',
              'Content-Type': 'application/json'
            },
            credentials: 'same-origin',
            body: JSON.stringify(self.data)
          }).then(function (r) {
            return r.text().then(function (text) {
              var body = {};
              try { body = JSON.parse(text); } catch (e) { /* non-JSON */ }
              return { ok: r.ok, status: r.status, body: body };
            });
          }).then(function (res) {
            self.submitting = false;
            if (res.ok) {
              self.submitted = true;
              setTimeout(function () {
                window.location.href = '/pending';
              }, 600);
              return;
            }
            if (res.status === 400 && res.body && res.body.field) {
              var msg = res.body.message || res.body.code || 'invalid input';
              self.fieldErrors[res.body.field] = msg;
              self.goToStep(self.stepForField(res.body.field));
              return;
            }
            self.submitError =
              (res.body && (res.body.error || res.body.message)) ||
              'Registration failed (' + res.status + ')';
          }).catch(function () {
            self.submitting = false;
            self.submitError = 'Network error, please try again';
          });
        }
      };
    });

    // Sprint Y application rating widget. Each application field on
    // the admin pending-users panel gets ten star buttons backed by
    // this component. Per-click auto-save: the click fires PUT
    // /api/admin/users/{id}/rating, the server returns the live
    // average + rated count, the widget dispatches a
    // "rating-changed" event so the card-level summary scope can
    // refresh both numbers. Clicking the currently-selected value
    // clears it (sends null) so an admin can correct an accidental
    // pick without choosing a different number.
    window.Alpine.data('ratingWidget', function (userId, dimension, initial) {
      return {
        userId: userId,
        dimension: dimension,
        // value is the saved rating; null means unrated. We keep
        // the local state optimistic - on success the server
        // confirms, on failure we roll back to the previous value.
        value: typeof initial === 'number' ? initial : null,
        saved: false,
        error: '',
        savedTimer: null,

        rate: function (n) {
          var self = this;
          // Toggle off when clicking the already-selected value.
          var nextValue = (self.value === n) ? null : n;
          var prev = self.value;
          self.value = nextValue;
          self.error = '';
          fetch('/api/admin/users/' + self.userId + '/rating', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
            body: JSON.stringify({
              dimension: self.dimension,
              value: nextValue
            })
          }).then(function (r) {
            return r.text().then(function (text) {
              var body = {};
              try { body = JSON.parse(text); } catch (e) { /* ignore */ }
              return { ok: r.ok, status: r.status, body: body };
            });
          }).then(function (res) {
            if (!res.ok) {
              self.value = prev;
              self.error = (res.body && res.body.error) ||
                ('save failed (' + res.status + ')');
              return;
            }
            self.saved = true;
            if (self.savedTimer) clearTimeout(self.savedTimer);
            self.savedTimer = setTimeout(function () {
              self.saved = false;
            }, 1500);
            // Notify the card-level rating-summary scope so the
            // average + rated_count refresh without a roundtrip.
            self.$dispatch('rating-changed', {
              userId: self.userId,
              average: (res.body && typeof res.body.average === 'number') ? res.body.average : 0,
              ratedCount: (res.body && typeof res.body.rated_count === 'number') ? res.body.rated_count : 0
            });
          }).catch(function () {
            self.value = prev;
            self.error = 'network error';
          });
        }
      };
    });

    // Sprint Y rating notes Alpine component. Auto-saves the admin's
    // free-form moderation notes 500ms after the last keystroke
    // (debounce on the textarea's @input directive). Mirrors the
    // ratingWidget save / saved / error flag pattern.
    window.Alpine.data('ratingNotes', function (userId, initial) {
      return {
        userId: userId,
        value: typeof initial === 'string' ? initial : '',
        saving: false,
        saved: false,
        error: '',
        savedTimer: null,

        save: function () {
          var self = this;
          self.saving = true;
          self.saved = false;
          self.error = '';
          fetch('/api/admin/users/' + self.userId + '/rating/notes', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
            body: JSON.stringify({ notes: self.value })
          }).then(function (r) {
            self.saving = false;
            if (!r.ok) {
              self.error = 'save failed (' + r.status + ')';
              return;
            }
            self.saved = true;
            if (self.savedTimer) clearTimeout(self.savedTimer);
            self.savedTimer = setTimeout(function () {
              self.saved = false;
            }, 1500);
          }).catch(function () {
            self.saving = false;
            self.error = 'network error';
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
    //
    // Sprint 15a.5 P1 / P2 ROOT-CAUSE FIX: the Quill instance
    // lives in a CLOSURE variable, not on `this`. Alpine 3 wraps
    // every property assigned to a component in a deep reactive
    // Proxy. Quill's internal scroll.find() compares blots via
    // `n.scroll === this` identity; through Alpine's Proxy those
    // comparisons fail (the Proxy and its target are not ===),
    // so scroll.find returns null and normalizedToRange crashes
    // dereferencing .offset. Storing quill OUTSIDE the Alpine
    // component state bypasses the Proxy entirely. Alpine can't
    // wrap what it can't see.
    window.Alpine.data('editPostModal', function () {
      // Closure-owned, never on `this`. Alpine-invisible.
      var quill = null;
      var initialHTML = '';
      // Sprint 15a.5.3: baselineHTML is the RENDERED state of the
      // editor right after the seed, NOT the raw server string. Quill's
      // dangerouslyPasteHTML normalises the markup (collapses empty
      // <p>, strips unknown tags, canonicalises attributes), so
      // comparing the live editor innerHTML against initialHTML would
      // treat every open as "dirty". The baseline is captured in
      // _mountQuill after the seed runs, and hasChanges flips true
      // when the user mutates away from it, false when they revert to
      // byte-identical. See the hasChanges property comment for the
      // full rationale.
      var baselineHTML = '';

      return {
        open: false,
        loading: false,
        saving: false,
        error: '',
        postId: 0,
        // Sprint 15a.5.1 follow-up: Alpine-reactive mirror of Quill's
        // content length. Since 15a.5 moved the Quill instance into a
        // closure variable Alpine can't see, any binding that read
        // `quill.getText()` directly (here: the save-button's
        // :disabled="saving || !hasContent()") only evaluated at
        // initial render and never re-ran. We maintain this length
        // counter by subscribing to Quill's text-change event in
        // _mountQuill and writing into a reactive property. The same
        // pattern works in composeEditor() where `charCount` already
        // wired this correctly.
        contentLen: 0,
        // Sprint 15a.5.3: Alpine-reactive dirty flag. The save button
        // is disabled while `!hasChanges` so the user can't resubmit
        // an unchanged post. Flipped true by the text-change handler
        // whenever quill.root.innerHTML differs from the baseline
        // captured at seed time, flipped back to false by the same
        // handler when the user edits a change back to baseline. Any
        // :disabled binding that reads hasChanges will re-run on that
        // transition because hasChanges is a tracked property.
        hasChanges: false,
        _opening: false, // re-entrancy guard: ignore back-to-back opens

        openForPost: function (postId) {
          // Hard guards: only proceed when called with a real numeric
          // post id. The listener passes
          // `$event.detail && $event.detail.postId` so a custom event
          // dispatched without a detail payload now becomes a no-op
          // here instead of opening an empty modal. Kept as defence
          // in depth after the Sprint 15a.4 CSS root-cause fix landed.
          if (!postId || typeof postId !== 'number' || postId <= 0) {
            return;
          }
          if (this._opening) {
            return;
          }
          this._opening = true;
          var self = this;
          self.postId = postId;
          self.error = '';
          self.loading = true;
          self.open = true;
          // Fetch the freshest post text (an admin might have
          // edited since the user's feed loaded).
          apiJSON('/api/posts/' + postId, 'GET', null).then(function (res) {
            self.loading = false;
            self._opening = false;
            if (!res.ok || !res.data || !res.data.post) {
              self.error = (res.data && res.data.error) || 'Could not load post';
              return;
            }
            // Sprint 15a B7 Bug 2: seed with content_html (rendered +
            // sanitised) NOT content (the raw markdown the server
            // originally received). The raw markdown was being
            // detected as "not HTML" by _mountQuill's regex
            // `/^\s*</` test, dropped into Quill as plain text via
            // setText, and then echoed back to the server on save as
            // `<p># Heading</p>` - at which point LooksLikeHTML on
            // the server returned true and goldmark was bypassed, so
            // the original heading / bold / list markup was lost on
            // the first edit. content_html is the same bytes the
            // feed renders from, round-trips cleanly through
            // Quill's clipboard pipeline, and preserves formatting.
            // The `|| post.content` fallback handles the edge case
            // of posts created before content_html population
            // (there shouldn't be any in prod, but keep the fallback
            // so a historical nil does not crash the edit flow).
            initialHTML = res.data.post.content_html ||
                          res.data.post.content || '';
            // Wait one frame so x-show has painted the editor div
            // before Quill measures its host.
            requestAnimationFrame(function () { self._mountQuill(); });
          }).catch(function () {
            self.loading = false;
            self._opening = false;
            self.error = 'Network error';
          });
        },

        _mountQuill: function () {
          if (quill) return; // already mounted
          var host = this.$refs.editor;
          if (!host || !window.Quill) return;
          var self = this;
          quill = new window.Quill(host, {
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
          // Seed with the current post text FIRST, before subscribing
          // to text-change. The subscription below computes hasChanges
          // against baselineHTML; if we seed after subscribing, the
          // seed itself would fire text-change events with an empty
          // baseline and flip hasChanges true during open. Seeding
          // first keeps the baseline capture and the subscription in
          // the right order.
          if (initialHTML) {
            if (/^\s*</.test(initialHTML)) {
              quill.clipboard.dangerouslyPasteHTML(initialHTML);
            } else {
              quill.setText(initialHTML);
            }
          }
          // Capture the baseline AFTER Quill has normalised the seed.
          // See the baselineHTML closure comment: raw initialHTML is
          // not a safe baseline because Quill rewrites markup at paste
          // time (empty paragraphs, attribute order, whitespace), so
          // the first post-seed text-change event would always see
          // `innerHTML !== initialHTML` and falsely report hasChanges.
          baselineHTML = quill.root.innerHTML;
          // Prime contentLen synchronously from the seeded text so the
          // Save button reflects the seeded state before any user
          // interaction. Same defence-in-depth rationale as 15a.5.2:
          // we do not rely on text-change firing during seed.
          self.contentLen = self._computeLen();
          // Subscribe AFTER seed + baseline capture. The handler
          // mirrors content length into reactive contentLen (so
          // hasContent() updates) and sets reactive hasChanges by
          // comparing live HTML against the captured baseline (so the
          // Save button reflects dirty state). Byte-identical HTML ==
          // not dirty, so editing a change back to baseline flips
          // hasChanges false again without any explicit revert path.
          quill.on('text-change', function () {
            self.contentLen = self._computeLen();
            self.hasChanges = (quill.root.innerHTML !== baselineHTML);
          });
        },

        _computeLen: function () {
          if (!quill) return 0;
          // getText() always terminates in a trailing newline; strip it
          // plus any surrounding whitespace so a document of only
          // whitespace / newlines does not look like content.
          return (quill.getText() || '').replace(/\n$/, '').trim().length;
        },

        hasContent: function () {
          return this.contentLen > 0;
        },

        save: function () {
          var self = this;
          if (!quill || self.saving) return;
          // Sprint 15a.5.3 defence-in-depth: the Save button's
          // :disabled binding already blocks unchanged / empty
          // submits, but we mirror the same gates here so a keyboard
          // trigger or a stale binding can never send a redundant
          // PATCH. Silent return, not an error toast - the user's
          // intent (nothing to save) is not actually an error.
          if (!self.hasChanges) return;
          var content = quill.root.innerHTML;
          if (content === '<p><br></p>') {
            self.error = 'Content cannot be empty';
            return;
          }
          // Sprint 15a B7 Bug 5: capture the post id we are saving
          // into a local const. self.postId is reactive Alpine state
          // and will flip to another value if the user closes this
          // modal and reopens it on a different post while this
          // PATCH is in flight (close() resets to 0, the next
          // openForPost sets it to the new id). Without the capture
          // the .then() callback would update the wrong card's
          // DOM and we'd silently overwrite post B's content block
          // with post A's new HTML.
          var savingPostId = self.postId;
          self.saving = true;
          self.error = '';
          apiJSON('/api/posts/' + savingPostId, 'PATCH', { content: content })
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
              // Uses the captured savingPostId, not self.postId, so
              // a mid-flight modal swap cannot redirect this DOM
              // write to the wrong card.
              var card = document.getElementById('post-' + savingPostId);
              if (card && res.data.post) {
                var body = card.querySelector('.post-content');
                if (body) body.innerHTML = res.data.post.content_html || '';
                // Sprint 15a B8 Nit 2: use attachEditedBadge so the
                // in-session badge position + title match the
                // post-card.html template.
                if (res.data.post.edited_at) {
                  attachEditedBadge(
                    card.querySelector('.post-meta'),
                    res.data.post.edited_at
                  );
                }
              }
              // Only close the modal if the user has not already
              // moved on to a different post. If they have, the
              // modal is showing a new edit session for post B and
              // closing it here would throw away their in-progress
              // edits of B. self.postId is the currently-open post
              // in the modal at this moment; compare to what we
              // were saving.
              if (self.postId === savingPostId) {
                self.close();
              }
            })
            .catch(function () {
              self.saving = false;
              self.error = 'Network error';
            });
        },

        close: function () {
          // Sprint 15a.1 P4 defence-in-depth kept after the 15a.4
          // CSS root-cause fix: every state field reset, in order.
          // Cancel, the X button, the backdrop's @click.self, and
          // Escape on the window all route through here, so a full
          // reset means a stale field can't leak into the next open.
          this.open = false;
          this.loading = false;
          this.saving = false;
          this.error = '';
          this.postId = 0;
          this.contentLen = 0;
          this.hasChanges = false;
          this._opening = false;
          initialHTML = '';
          baselineHTML = '';
          // Tear down Quill so the next open starts clean. Quill
          // doesn't expose a formal destroy, but removing the DOM
          // children clears its toolbar and editor wrappers.
          quill = null;
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

      // Sprint 15a.5 P1 / P2 ROOT-CAUSE FIX: Quill and its
      // GolabQuillMention satellite live in CLOSURE variables, not
      // on `this`. Alpine 3 wraps every reactive-component property
      // in a deep Proxy. Quill's internal scroll.find() compares
      // blots via `n.scroll === this` identity; through Alpine's
      // Proxy those comparisons fail because the Proxy object and
      // its target are not === equal. scroll.find then returns null
      // and normalizedToRange crashes dereferencing .offset on it.
      // That is the "Cannot read properties of null (reading
      // 'offset')" stack we chased across Sprints 15a / 15a.1 /
      // 15a.2 / 15a.3. Der Prinz pinned it with Prinzessin Mausi
      // in devtools: `cd.quill === Alpine.raw(cd.quill)` is false,
      // `cd.quill.scroll.find(p)` returns null, but
      // `Alpine.raw(cd.quill).scroll.find(p)` finds the blot.
      //
      // Storing quill outside the Alpine-visible state bypasses the
      // Proxy entirely. Alpine can't wrap what it can't see, so
      // every Quill internal operation runs on the raw instance.
      // No Alpine.raw() call needed at any insertion site, no
      // defensive layers, no setTimeout defers.
      var quill = null;
      var mention = null;

      return {
        charCount: 0,
        max: 5000,
        submitting: false,
        powerLevel: cfg.powerLevel || 0,

        // Sprint 16b Phase 4: cascading Project + Season selects.
        // The lists live in reactive state so the <template x-for>
        // rebuilds the option DOM when the user picks a Space.
        projects: [],
        seasons: [],
        selectedProjectID: null,

        // Sprint 16b Phase 4: when the user picks a Space, fetch the
        // visible projects for that space. Reset the downstream
        // selects so a stale Project / Season pick doesn't survive.
        onSpaceChange: function (ev) {
          var self = this;
          self.projects = [];
          self.seasons = [];
          self.selectedProjectID = null;
          if (self.$refs.projectSelect) self.$refs.projectSelect.value = '';
          if (self.$refs.seasonSelect) self.$refs.seasonSelect.value = '';
          var spaceID = parseInt(ev.target.value, 10) || 0;
          if (!spaceID) return;
          var opt = ev.target.options[ev.target.selectedIndex];
          var slug = opt && opt.dataset && opt.dataset.slug ? opt.dataset.slug : '';
          if (!slug) return;
          fetch('/api/spaces/' + encodeURIComponent(slug) + '/projects', {
            credentials: 'same-origin',
            headers: { Accept: 'application/json' }
          }).then(function (r) { return r.ok ? r.json() : null; })
            .then(function (d) {
              self.projects = (d && d.projects) || [];
            })
            .catch(function () { self.projects = []; });
        },

        // Sprint 16b Phase 4: when the user picks a Project, fetch
        // its seasons. We hide closed seasons because the server
        // rejects assignments to them anyway; not showing them keeps
        // the picker focused on the actionable choices.
        onProjectChange: function (ev) {
          var self = this;
          self.seasons = [];
          if (self.$refs.seasonSelect) self.$refs.seasonSelect.value = '';
          var projectID = parseInt(ev.target.value, 10) || 0;
          self.selectedProjectID = projectID || null;
          if (!projectID) return;
          fetch('/api/projects/' + projectID + '/seasons', {
            credentials: 'same-origin',
            headers: { Accept: 'application/json' }
          }).then(function (r) { return r.ok ? r.json() : null; })
            .then(function (d) {
              var all = (d && d.seasons) || [];
              self.seasons = all.filter(function (s) {
                return s.status !== 'closed';
              });
            })
            .catch(function () { self.seasons = []; });
        },

        // Format the option label - includes Planned/Active state so
        // the user sees which season is the live one.
        seasonOptionLabel: function (s) {
          var label = 'Season ' + s.season_number + ': ' + s.title;
          if (s.status === 'planned') label += ' (Planned)';
          else if (s.status === 'active') label += ' (Active)';
          return label;
        },

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
          // container state and the closure-owned Quill reference, and
          // bail out early if either indicates an existing mount.
          if (container.__quillMounted || container.classList.contains('ql-container')) {
            return;
          }
          if (quill) {
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
          quill = new window.Quill(container, {
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
          quill.on('text-change', function () {
            self.charCount = self.textLength();
          });

          // Sprint 14: attach @mention autocomplete. The module
          // is vendored in quill-mention.js and exposed as the
          // GolabQuillMention global. We tie it to this Alpine
          // component's lifecycle so teardown cleans up listeners
          // (see destroy path below for the .destroy() call).
          // Closure-owned for the same Proxy-identity reason as quill.
          if (typeof window.GolabQuillMention === 'function') {
            try {
              mention = new window.GolabQuillMention(quill, {
                fetchUrl: '/api/users/autocomplete'
              });
            } catch (err) { /* autocomplete is best-effort */ }
          }

          // Paste a .gif URL -> convert to an <img> embed automatically.
          // Quill's matchers fire during clipboard processing so the URL
          // never lands as plain text first.
          try {
            quill.clipboard.addMatcher(Node.TEXT_NODE, function (node, delta) {
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
        toggleEmojiPicker: function (anchor) {
          var existing = document.querySelector('.emoji-quickpicker');
          if (existing) { existing.remove(); return; }

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
              if (!quill) return;
              // Sprint 15a.5: direct call on the closure-owned Quill.
              // No Alpine Proxy in the way, no scroll.find null, no
              // normalizedToRange crash. Use the current selection
              // if Quill has one, else insert at the end of the doc.
              var range = quill.getSelection();
              var idx = range ? range.index : Math.max(0, quill.getLength() - 1);
              quill.insertText(idx, e, 'user');
              quill.setSelection(idx + e.length, 0, 'user');
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
          if (mention && typeof mention.destroy === 'function') {
            try { mention.destroy(); } catch (e) {}
          }
          mention = null;
          var container = this.$refs && this.$refs.editor;
          if (container) {
            delete container.__quillMounted;
          }
          quill = null;
        },

        textLength: function () {
          if (!quill) return 0;
          // Quill's getText() includes trailing newline; trim it.
          return (quill.getText() || '').replace(/\n$/, '').length;
        },

        htmlContent: function () {
          if (!quill) return this.$refs.editor.innerHTML || '';
          var html = quill.root.innerHTML;
          // Empty Quill document is '<p><br></p>'. Treat that as empty.
          if (html === '<p><br></p>') return '';
          return html;
        },

        uploadImage: function () {
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
              if (!quill) {
                toast('info', 'Image uploaded but editor is gone. URL: ' + res.data.url);
                return;
              }
              // Sprint 15a.5: direct call on the closure-owned Quill.
              // Use the current selection if Quill still has one (user
              // clicked image straight from within the editor), else
              // insert at the end. No setTimeout, no Proxy unwrap, no
              // clipboard fallback - the root cause was the Alpine
              // Proxy around this.quill, and routing through the closure
              // variable removes it from the code path.
              var range = quill.getSelection();
              var idx = range ? range.index : Math.max(0, quill.getLength() - 1);
              quill.insertEmbed(idx, 'image', res.data.url, 'user');
              quill.setSelection(idx + 1, 0, 'user');
              toast('success', 'Image added');
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

          // Sprint 16b Phase 4: optional season_id from the cascading
          // Project / Season selects. Only sent when the user picked a
          // season; otherwise the post stays at the Space level.
          var seasonEl = form.querySelector('[name=season_id]');
          if (seasonEl && seasonEl.value) {
            body.season_id = parseInt(seasonEl.value, 10);
          }

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
              if (quill) quill.setContents([]);
              else self.$refs.editor.innerHTML = '';
              self.charCount = 0;
              toast('success', 'Posted');
              // Sprint 15a B7 Bug 1: the POST response carries a
              // post-card fragment rendered with the author's user
              // context (dropdown included), because the WS broadcast
              // fragment is anonymous by design (one render, N
              // viewers).
              //
              // Sprint 15a B7 Bug 1 follow-up: race-proof variant.
              // The initial implementation assumed the HTTP response
              // would always arrive before the WS echo, so a simple
              // injectNewPost(res.data.html) combined with the
              // self-echo guard would do the job. In practice the
              // WS frame sometimes beats the HTTP response (the
              // broadcast is a non-blocking channel send right after
              // Posts.Create returns, often before the JSON body
              // finishes writing), so the anonymous render landed
              // first and the author-context version was then
              // dropped by the self-echo guard. Result: author sees
              // the no-dropdown render on their own card.
              //
              // Fix: when the HTTP response arrives, if a card with
              // the same id is already in the DOM, remove it first.
              // The author-context version then always wins the
              // race. Non-authors never hit this path (no HTTP
              // response to race with); they keep the WS render.
              //
              // Sprint 15a.5.6: no window.location.reload() here.
              // For non-authors, the WebSocket hub still broadcasts
              // new_post to the "global" topic and injectNewPost does
              // the insert + Alpine.initTree on their side.
              if (res.data && res.data.html) {
                if (res.data.post && res.data.post.id) {
                  var racedCard = document.getElementById(
                    'post-' + res.data.post.id
                  );
                  if (racedCard) racedCard.remove();
                }
                injectNewPost(res.data.html);
              }
            } else {
              toast('error', res.data.error || 'Could not post');
              form.classList.add('shake');
              setTimeout(function () { form.classList.remove('shake'); }, 500);
            }
          });
        }
      };
    });

    // ============================================================
    // Sprint 16e visual polish: animated KPI counter.
    // Counts the displayed value from 0 to `target` once the host
    // element scrolls into view. Pure ease-out cubic, ~1200ms by
    // default. The component disconnects its observer after firing
    // so HTMX page swaps can re-mount cleanly.
    //
    // Usage:
    //   <span x-data="kpiCounter(73)" x-text="current">0</span>
    //   <span x-data="kpiCounter(73, 800)" x-text="current">0</span>
    // ============================================================
    window.Alpine.data('kpiCounter', function (target, duration) {
      target = parseInt(target, 10) || 0;
      duration = parseInt(duration, 10) || 1200;
      var prefersReduced = window.matchMedia &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      return {
        current: prefersReduced ? target : 0,
        init: function () {
          if (prefersReduced) return; // user opted out of motion
          var self = this;
          // Fall back to a synchronous animate() if the browser is
          // ancient enough to lack IntersectionObserver - the count-up
          // still fires; it just doesn't wait for visibility.
          if (!('IntersectionObserver' in window)) {
            self._animate(target, duration);
            return;
          }
          var observer = new IntersectionObserver(function (entries) {
            if (entries[0] && entries[0].isIntersecting) {
              self._animate(target, duration);
              observer.disconnect();
            }
          });
          observer.observe(this.$el);
        },
        _animate: function (target, duration) {
          var self = this;
          var start = performance.now();
          var step = function (now) {
            var t = Math.min((now - start) / duration, 1);
            var eased = 1 - Math.pow(1 - t, 3); // ease-out cubic
            self.current = Math.floor(target * eased);
            if (t < 1) {
              requestAnimationFrame(step);
            } else {
              self.current = target;
            }
          };
          requestAnimationFrame(step);
        }
      };
    });

    // ============================================================
    // Sprint 16b visual polish: Chart.js panels.
    // Each component reads its dataset from a `data-chart` JSON
    // attribute on the host element, instantiates the matching
    // Chart.js chart, and tears it down on Alpine destroy so HTMX
    // page swaps don't leak canvases.
    // ============================================================

    function readChartData(el) {
      try {
        return JSON.parse(el.dataset.chart || '{}');
      } catch (e) {
        return null;
      }
    }

    function chartThemeColors() {
      var s = getComputedStyle(document.documentElement);
      return {
        text:   (s.getPropertyValue('--text-dim')   || '#9aa5b1').trim(),
        bright: (s.getPropertyValue('--text-bright')|| '#e6edf3').trim(),
        muted:  (s.getPropertyValue('--text-muted') || '#7a8590').trim(),
        grid:   (s.getPropertyValue('--border')     || 'rgba(149,165,166,0.15)').trim(),
        accent: (s.getPropertyValue('--accent')     || '#45BDD1').trim()
      };
    }

    // Stacked-area chart inside the parent-project cockpit. Same
    // dataset shape as spaceActivityChart but rendered as smooth-
    // tensioned area bands so the cockpit reads as ambient motion
    // instead of a hard bar histogram. One band per child project,
    // child accent colour as fill at 25% opacity, line at 90%.
    window.Alpine.data('cockpitActivityChart', function () {
      var chart = null;
      return {
        init: function () {
          var self = this;
          requestAnimationFrame(function () { self._mount(); });
        },
        _mount: function () {
          if (chart || !window.Chart) return;
          var canvas = this.$refs.canvas;
          if (!canvas) return;
          var data = readChartData(this.$el);
          if (!data || !data.datasets || data.datasets.length === 0) return;
          var t = chartThemeColors();

          // Translate the (label, backgroundColor, data) triples into
          // line-chart datasets with stacked area fills. The
          // backgroundColor we got from the server is the project's
          // accent colour; we derive the fill (25% alpha) and line
          // (90% alpha) inline so the server stays palette-neutral.
          var datasets = data.datasets.map(function (ds, i) {
            var c = ds.backgroundColor || t.accent;
            return {
              label: ds.label,
              data: ds.data,
              borderColor: hexWithAlpha(c, 0.9),
              backgroundColor: hexWithAlpha(c, 0.25),
              fill: i === 0 ? 'origin' : '-1',
              tension: 0.4,
              borderWidth: 2,
              pointRadius: 0,
              pointHoverRadius: 4
            };
          });

          chart = new window.Chart(canvas, {
            type: 'line',
            data: { labels: data.labels, datasets: datasets },
            options: {
              responsive: true,
              maintainAspectRatio: false,
              animation: { duration: 1200, easing: 'easeOutCubic' },
              plugins: {
                legend: {
                  position: 'bottom',
                  labels: {
                    color: t.text, boxWidth: 10, padding: 12,
                    font: { size: 11 }, usePointStyle: true,
                    pointStyle: 'circle'
                  }
                },
                tooltip: { mode: 'index', intersect: false }
              },
              scales: {
                x: {
                  ticks: {
                    color: t.text, font: { size: 10 },
                    maxRotation: 0, autoSkip: true, maxTicksLimit: 8
                  },
                  grid: { color: t.grid, display: false }
                },
                y: {
                  stacked: true,
                  beginAtZero: true,
                  display: false,
                  grid: { display: false }
                }
              }
            }
          });
        },
        destroy: function () {
          if (chart) { try { chart.destroy(); } catch (e) {} chart = null; }
        }
      };
    });

    // hexWithAlpha takes a hex / rgba colour and returns an rgba
    // string with the given alpha. Used by cockpitActivityChart so
    // the server can ship plain accent hexes and we layer stacked
    // bands on top with the right transparency.
    function hexWithAlpha(input, alpha) {
      if (!input) return 'rgba(69,189,209,' + alpha + ')';
      var s = String(input).trim();
      // Already rgba? Replace the alpha component.
      var m = s.match(/^rgba?\(([^)]+)\)$/i);
      if (m) {
        var parts = m[1].split(',').map(function (p) { return p.trim(); });
        if (parts.length >= 3) {
          return 'rgba(' + parts[0] + ',' + parts[1] + ',' + parts[2] + ',' + alpha + ')';
        }
      }
      // Hex? #RGB or #RRGGBB.
      if (s.charAt(0) === '#') {
        var hex = s.slice(1);
        if (hex.length === 3) {
          hex = hex[0]+hex[0] + hex[1]+hex[1] + hex[2]+hex[2];
        }
        if (hex.length === 6) {
          var r = parseInt(hex.slice(0, 2), 16);
          var g = parseInt(hex.slice(2, 4), 16);
          var b = parseInt(hex.slice(4, 6), 16);
          return 'rgba(' + r + ',' + g + ',' + b + ',' + alpha + ')';
        }
      }
      return s; // fall back to raw input - caller gets whatever it gave us
    }

    // Stacked bar chart on the Space landing page. Posts per week per
    // project, last 90 days. Server pre-aggregates the dataset; this
    // component only handles theme + Chart.js wiring.
    window.Alpine.data('spaceActivityChart', function () {
      var chart = null;
      return {
        init: function () {
          var self = this;
          requestAnimationFrame(function () { self._mount(); });
        },
        _mount: function () {
          if (chart || !window.Chart) return;
          var canvas = this.$refs.canvas;
          if (!canvas) return;
          var data = readChartData(this.$el);
          if (!data || !data.datasets || data.datasets.length === 0) return;
          var t = chartThemeColors();
          chart = new window.Chart(canvas, {
            type: 'bar',
            data: data,
            options: {
              responsive: true,
              maintainAspectRatio: false,
              animation: { duration: 350 },
              plugins: {
                legend: {
                  position: 'bottom',
                  labels: { color: t.text, boxWidth: 10, padding: 12, font: { size: 11 } }
                },
                tooltip: { mode: 'index', intersect: false }
              },
              scales: {
                x: {
                  stacked: true,
                  ticks: { color: t.text, font: { size: 10 }, maxRotation: 0, autoSkip: true },
                  grid:  { color: t.grid, display: false }
                },
                y: {
                  stacked: true,
                  beginAtZero: true,
                  ticks: { color: t.text, font: { size: 10 }, precision: 0 },
                  grid:  { color: t.grid }
                }
              }
            }
          });
        },
        destroy: function () {
          if (chart) { try { chart.destroy(); } catch (e) {} chart = null; }
        }
      };
    });

    // Bar chart "posts per season" on the project landing dashboard.
    window.Alpine.data('projectSeasonsChart', function () {
      var chart = null;
      return {
        init: function () {
          var self = this;
          requestAnimationFrame(function () { self._mount(); });
        },
        _mount: function () {
          if (chart || !window.Chart) return;
          var canvas = this.$refs.canvas;
          if (!canvas) return;
          var data = readChartData(this.$el);
          if (!data || !data.data || data.data.length === 0) return;
          var t = chartThemeColors();
          chart = new window.Chart(canvas, {
            type: 'bar',
            data: {
              labels: data.labels,
              datasets: [{
                label: 'Posts',
                data: data.data,
                backgroundColor: data.backgroundColor,
                borderRadius: 6,
                maxBarThickness: 36
              }]
            },
            options: {
              responsive: true,
              maintainAspectRatio: false,
              animation: { duration: 350 },
              plugins: {
                legend: { display: false },
                tooltip: { intersect: false }
              },
              scales: {
                x: {
                  ticks: { color: t.text, font: { size: 11 } },
                  grid:  { color: t.grid, display: false }
                },
                y: {
                  beginAtZero: true,
                  ticks: { color: t.text, font: { size: 10 }, precision: 0 },
                  grid:  { color: t.grid }
                }
              }
            }
          });
        },
        destroy: function () {
          if (chart) { try { chart.destroy(); } catch (e) {} chart = null; }
        }
      };
    });

    // Line chart "posts over time" on the season detail dashboard.
    window.Alpine.data('seasonDailyChart', function () {
      var chart = null;
      return {
        init: function () {
          var self = this;
          requestAnimationFrame(function () { self._mount(); });
        },
        _mount: function () {
          if (chart || !window.Chart) return;
          var canvas = this.$refs.canvas;
          if (!canvas) return;
          var data = readChartData(this.$el);
          if (!data || !data.labels) return;
          var t = chartThemeColors();
          chart = new window.Chart(canvas, {
            type: 'line',
            data: {
              labels: data.labels,
              datasets: [{
                label: 'Posts',
                data: data.data,
                borderColor: t.accent,
                backgroundColor: t.accent + '33',
                fill: true,
                tension: 0.3,
                borderWidth: 2,
                pointRadius: 2,
                pointHoverRadius: 4
              }]
            },
            options: {
              responsive: true,
              maintainAspectRatio: false,
              animation: { duration: 350 },
              plugins: {
                legend: { display: false },
                tooltip: { intersect: false, mode: 'index' }
              },
              scales: {
                x: {
                  ticks: { color: t.text, font: { size: 10 }, maxRotation: 0, autoSkip: true, maxTicksLimit: 8 },
                  grid:  { color: t.grid, display: false }
                },
                y: {
                  beginAtZero: true,
                  ticks: { color: t.text, font: { size: 10 }, precision: 0 },
                  grid:  { color: t.grid }
                }
              }
            }
          });
        },
        destroy: function () {
          if (chart) { try { chart.destroy(); } catch (e) {} chart = null; }
        }
      };
    });

    // Donut chart "posts by type" on the season detail dashboard.
    window.Alpine.data('seasonTypeChart', function () {
      var chart = null;
      return {
        init: function () {
          var self = this;
          requestAnimationFrame(function () { self._mount(); });
        },
        _mount: function () {
          if (chart || !window.Chart) return;
          var canvas = this.$refs.canvas;
          if (!canvas) return;
          var data = readChartData(this.$el);
          if (!data || !data.data || data.data.length === 0) return;
          var t = chartThemeColors();
          chart = new window.Chart(canvas, {
            type: 'doughnut',
            data: {
              labels: data.labels,
              datasets: [{
                data: data.data,
                backgroundColor: data.backgroundColor,
                borderWidth: 1,
                borderColor: 'rgba(0,0,0,0.0)'
              }]
            },
            options: {
              responsive: true,
              maintainAspectRatio: false,
              cutout: '62%',
              animation: { duration: 350 },
              plugins: {
                legend: {
                  position: 'bottom',
                  labels: { color: t.text, boxWidth: 10, padding: 10, font: { size: 11 } }
                }
              }
            }
          });
        },
        destroy: function () {
          if (chart) { try { chart.destroy(); } catch (e) {} chart = null; }
        }
      };
    });

    // Sprint 16b Phase 2: Project form helper. Auto-derives the slug
    // from the name on type, stops auto-deriving once the user types
    // into the slug field directly. Slug rules mirror the server-side
    // ValidateProjectSlug regex: lowercase, digits, hyphens, no
    // leading/trailing/consecutive hyphens, 3-64 chars.
    window.Alpine.data('projectForm', function () {
      return {
        autoSlug: true,

        init: function () {
          var nameEl = this.$refs.nameInput;
          var slugEl = this.$refs.slugInput;
          if (slugEl && slugEl.value &&
              slugEl.value !== this._slugify(nameEl ? nameEl.value : '')) {
            // Server round-tripped a slug that doesn't match the
            // auto-derived one - the user wrote it themselves.
            // Stop overwriting it from name input.
            this.autoSlug = false;
          }
        },

        _slugify: function (s) {
          return (s || '').toString().toLowerCase().trim()
            .replace(/[^a-z0-9]+/g, '-')
            .replace(/-+/g, '-')
            .replace(/^-+|-+$/g, '')
            .slice(0, 64)
            .replace(/-+$/, '');
        },

        onNameInput: function (ev) {
          if (!this.autoSlug) return;
          var slugEl = this.$refs.slugInput;
          if (!slugEl) return;
          slugEl.value = this._slugify(ev.target.value);
        },

        onSlugInput: function () {
          // User typed in the slug field. Stop auto-deriving.
          this.autoSlug = false;
        }
      };
    });

    // Sprint 16b Phase 2: Quill-based doc editor. Used by the
    // /docs/{type}/edit pages. Mirrors composeEditor's closure pattern
    // for the Quill instance (Sprint 15a.5 P1/P2 fix - Alpine's
    // Proxy breaks Quill's blot identity comparisons; keeping Quill
    // off `this` bypasses the Proxy entirely).
    //
    // The form posts traditionally (form-encoded). Before submit we
    // copy quill.root.innerHTML into a hidden <input name="content_html">
    // so the page handler receives the rendered HTML. The handler
    // sanitizes via bluemonday before storage.
    window.Alpine.data('docEditor', function () {
      var quill = null;

      return {
        submitting: false,

        init: function () {
          var container = this.$refs.editor;
          if (!container) return;
          // Guard against double-mount across HTMX page swaps.
          if (container.__quillMounted ||
              container.classList.contains('ql-container')) {
            return;
          }
          if (quill) return;

          var initialHTML = container.dataset.initialHtml || '';

          // Fallback for browsers / environments where Quill failed
          // to load. The hidden input still picks up the contenteditable
          // div's innerHTML on submit.
          if (!window.Quill) {
            container.setAttribute('contenteditable', 'true');
            container.style.minHeight = '320px';
            container.innerHTML = initialHTML;
            container.__quillMounted = true;
            return;
          }

          quill = new window.Quill(container, {
            theme: 'snow',
            placeholder: 'Write the document here...',
            modules: {
              toolbar: [
                [{ header: [1, 2, 3, false] }],
                ['bold', 'italic', 'underline', 'strike'],
                [{ list: 'ordered' }, { list: 'bullet' }],
                ['blockquote', 'code-block'],
                ['link', 'image'],
                ['clean']
              ]
            }
          });
          container.__quillMounted = true;

          if (initialHTML) {
            if (/^\s*</.test(initialHTML)) {
              quill.clipboard.dangerouslyPasteHTML(initialHTML);
            } else {
              quill.setText(initialHTML);
            }
          }
        },

        onSubmit: function (ev) {
          if (this.submitting) return;
          this.submitting = true;
          var form = ev.target;
          var contentField = this.$refs.contentField;
          if (contentField) {
            if (quill) {
              contentField.value = quill.root.innerHTML;
            } else {
              // Fallback path: pull from contenteditable div.
              var c = this.$refs.editor;
              contentField.value = c ? c.innerHTML : '';
            }
          }
          // form.submit() bypasses the @submit.prevent handler, so we
          // don't re-enter onSubmit. The page handler does the PRG
          // redirect on success.
          form.submit();
        },

        destroy: function () {
          var container = this.$refs && this.$refs.editor;
          if (container) {
            delete container.__quillMounted;
          }
          quill = null;
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

    // ============================================================
    // Sprint 17 reading tracker.
    // ============================================================
    // Observes which post-card articles are visible (>= 50% in
    // viewport, intersection observer threshold), accumulates per-
    // post seconds while the user is active, and posts a heartbeat
    // to /api/reading/heartbeat every 15s. Active = tab visible AND
    // mouse/key/scroll/touch event in the last 30s. Anything else
    // pauses the accumulators so background tabs and idle tabs do
    // not accrue fake reading time.
    //
    // Server caps everything anyway (active_seconds <= 60/min,
    // visible_posts <= 20/heartbeat, per-post seconds <= heartbeat
    // interval) so a misbehaving client can never inflate stats.
    //
    // Mount via x-data="readingTracker" on a top-level element
    // (base.html puts it on <main>). Only authenticated users get
    // the x-data attribute, so anonymous visitors never load this
    // component.
    window.Alpine.data('readingTracker', function () {
      var heartbeatTimer = null;
      var tickTimer = null;
      var observer = null;

      return {
        // post_id -> { secondsVisible: number, isVisible: bool }
        // Entries stay in the map between heartbeats until they
        // both leave the viewport AND have already had their
        // seconds reported. This way we never double-count a post
        // that scrolls in and out within a single heartbeat window.
        visiblePosts: new Map(),
        // Topic ids opened since the last heartbeat. The server
        // de-dupes via ON CONFLICT DO NOTHING but we keep the
        // local Set so we don't waste round-trips.
        topicsEnteredThisHeartbeat: new Set(),
        // Wall-clock timestamp of the last user-input event.
        lastActivityAt: Date.now(),
        // Active-second accumulator since the last heartbeat.
        activeSecondsAccumulated: 0,
        // document.visibilityState mirror (cheaper than reading
        // it every tick).
        isTabVisible: !document.hidden,
        // Hardcoded for v1; the server has the real numbers from
        // the settings table and applies them as caps anyway.
        cfg: {
          intervalMs: 15000,
          idleTimeoutMs: 30000
        },

        init: function () {
          var self = this;

          ['mousemove', 'keydown', 'scroll', 'touchstart'].forEach(function (evt) {
            document.addEventListener(evt, function () {
              self.lastActivityAt = Date.now();
            }, { passive: true });
          });

          document.addEventListener('visibilitychange', function () {
            self.isTabVisible = !document.hidden;
            // Resetting lastActivityAt when the tab returns means
            // we do not credit the time spent in a background tab.
            if (self.isTabVisible) {
              self.lastActivityAt = Date.now();
            }
          });

          self.setupPostObserver();

          var topicId = self.detectCurrentTopicId();
          if (topicId) self.topicsEnteredThisHeartbeat.add(topicId);

          // hx-boost swaps body content without a full reload, so
          // re-observe new post cards and re-detect the URL topic
          // after every swap.
          document.addEventListener('htmx:afterSwap', function () {
            self.observeAllPostCards();
            var t = self.detectCurrentTopicId();
            if (t) self.topicsEnteredThisHeartbeat.add(t);
          });

          tickTimer = setInterval(function () { self.tick(); }, 1000);
          heartbeatTimer = setInterval(function () { self.sendHeartbeat(false); }, self.cfg.intervalMs);

          // beforeunload uses sendBeacon (if available) so the
          // last heartbeat reaches the server even though the page
          // is unloading and an async fetch would be aborted.
          window.addEventListener('beforeunload', function () {
            self.sendHeartbeat(true);
          });
        },

        destroy: function () {
          // x-data on a hx-boost-swapped element: Alpine calls
          // destroy on unmount. Flush any accumulators and tear
          // down timers so we don't leak intervals.
          this.sendHeartbeat(true);
          if (tickTimer) { clearInterval(tickTimer); tickTimer = null; }
          if (heartbeatTimer) { clearInterval(heartbeatTimer); heartbeatTimer = null; }
          if (observer) { observer.disconnect(); observer = null; }
        },

        setupPostObserver: function () {
          var self = this;
          observer = new IntersectionObserver(function (entries) {
            entries.forEach(function (e) {
              var idStr = e.target.dataset.postId;
              if (!idStr) return;
              var postId = parseInt(idStr, 10);
              if (!postId) return;

              var entry = self.visiblePosts.get(postId);
              if (e.isIntersecting && e.intersectionRatio >= 0.5) {
                if (!entry) {
                  self.visiblePosts.set(postId, { secondsVisible: 0, isVisible: true });
                } else {
                  entry.isVisible = true;
                }
              } else if (entry) {
                entry.isVisible = false;
              }
            });
          }, { threshold: [0, 0.5, 1.0] });

          self.observeAllPostCards();
        },

        observeAllPostCards: function () {
          if (!observer) return;
          document.querySelectorAll('[data-post-id]').forEach(function (el) {
            // article elements with data-post-id are the only
            // observation target. The reaction-bar inside post-
            // card.html also carries data-post-id but we filter
            // to the wrapping <article> so the threshold reflects
            // "is the post visible" not "is the reaction strip
            // visible".
            if (el.tagName === 'ARTICLE') observer.observe(el);
          });
        },

        isUserActive: function () {
          var idleMs = Date.now() - this.lastActivityAt;
          return this.isTabVisible && idleMs < this.cfg.idleTimeoutMs;
        },

        tick: function () {
          if (!this.isUserActive()) return;
          this.activeSecondsAccumulated += 1;
          this.visiblePosts.forEach(function (data) {
            if (data.isVisible) data.secondsVisible += 1;
          });
        },

        sendHeartbeat: function (useBeacon) {
          // Skip empty heartbeats so we never wake the server up
          // for nothing (keeps the rate-limit budget intact when
          // the user is idle).
          var hasPosts = false;
          this.visiblePosts.forEach(function (d) { if (d.secondsVisible > 0) hasPosts = true; });
          if (this.activeSecondsAccumulated === 0
              && !hasPosts
              && this.topicsEnteredThisHeartbeat.size === 0) {
            return;
          }

          var posts = [];
          this.visiblePosts.forEach(function (d, id) {
            if (d.secondsVisible > 0) {
              posts.push({ post_id: id, seconds_visible: d.secondsVisible });
            }
          });

          var payload = {
            active_seconds: this.activeSecondsAccumulated,
            visible_posts: posts,
            topics_entered: Array.from(this.topicsEnteredThisHeartbeat)
          };

          this.activeSecondsAccumulated = 0;
          // Reset per-post accumulators; entries that left the
          // viewport are dropped here so we do not keep sending
          // empty rows for them.
          var toDelete = [];
          this.visiblePosts.forEach(function (d, id) {
            d.secondsVisible = 0;
            if (!d.isVisible) toDelete.push(id);
          });
          var self = this;
          toDelete.forEach(function (id) { self.visiblePosts.delete(id); });
          this.topicsEnteredThisHeartbeat.clear();

          try {
            if (useBeacon && navigator.sendBeacon) {
              var blob = new Blob([JSON.stringify(payload)], { type: 'application/json' });
              navigator.sendBeacon('/api/reading/heartbeat', blob);
            } else {
              fetch('/api/reading/heartbeat', {
                method: 'POST',
                credentials: 'same-origin',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
              }).catch(function () { /* non-fatal */ });
            }
          } catch (e) { /* non-fatal */ }
        },

        detectCurrentTopicId: function () {
          var m = window.location.pathname.match(/^\/p\/(\d+)/);
          return m ? parseInt(m[1], 10) : null;
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
        //
        // Sprint 15a.1 P4 hardening: stop propagation + prevent
        // default + validate the parsed id BEFORE dispatch. If a
        // bubble-up from a nested element ever ends up triggering
        // this handler with a stray dataset, NaN or 0 is filtered
        // here and the modal's own openForPost guard catches the
        // rest.
        el.addEventListener('click', function (e) {
          if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
          if (e && typeof e.preventDefault === 'function') e.preventDefault();
          var raw = el.dataset.postId;
          if (!raw) return;
          var id = parseInt(raw, 10);
          if (!id || isNaN(id) || id <= 0) return;
          window.dispatchEvent(new CustomEvent('golab:open-edit-post', {
            detail: { postId: id }
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
      case 'post_updated':
        // Sprint 15a B7 Bug 3: patch the content block in place when
        // another tab / device edits a post we're currently viewing.
        // Payload is structured: {id, content_html, edited_at}. We
        // only touch the .post-content block and the edited badge;
        // dropdown / reactions / header are already correct for the
        // current viewer from the original render, so we do not need
        // the full card re-rendered (which would drag in the
        // dropdown-user-context issue PublishNewPost deals with).
        if (msg.data && msg.data.id != null) {
          var editCard = document.getElementById('post-' + msg.data.id);
          if (editCard) {
            var editBody = editCard.querySelector('.post-content');
            if (editBody && typeof msg.data.content_html === 'string') {
              editBody.innerHTML = msg.data.content_html;
              editBody.classList.add('number-bump');
              setTimeout(function () {
                editBody.classList.remove('number-bump');
              }, 320);
            }
            // Sprint 15a B8 Nit 2: use attachEditedBadge. Also
            // refreshes the title tooltip when the post is
            // edited a second time (old code bailed via the
            // duplicate-guard, leaving a stale timestamp).
            attachEditedBadge(
              editCard.querySelector('.post-meta'),
              msg.data.edited_at
            );
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

  // Sprint 15a B8 Nit 2: shared helper for the in-session "edited"
  // badge placement. Two call sites (editPostModal.save and the
  // post_updated WS handler) previously used `.post-meta
  // .appendChild(span)`, which put the badge AFTER .post-meta-row
  // on a new visual line when the post had a space or non-discussion
  // post_type badge, not inline with the author / handle / time
  // like the server template renders it. The duplicate-guard also
  // meant a second edit in the same session left a stale title
  // attribute.
  //
  // post-card.html (server template) puts the badge at the same
  // level as .post-handle and .post-time, BEFORE any .post-meta-row
  // child that holds the space / post_type badges:
  //
  //   <div class="post-meta">
  //     <a class="post-author">...</a>
  //     <span class="post-handle">...</span>
  //     <span class="post-time">...</span>
  //     <span class="post-edited" title="...">edited</span>
  //     <div class="post-meta-row"> ... space + type badges ... </div>
  //   </div>
  //
  // This helper mirrors that placement: reuse an existing .post-
  // edited if one is already in the DOM, otherwise insert before
  // .post-meta-row when it exists, otherwise append to .post-meta.
  // title is always set to the server-provided timestamp so a
  // repeat edit refreshes the tooltip. Accepts edited_at as either
  // a Unix-seconds number (post_updated WS payload) or an ISO
  // string (PATCH response .post.edited_at).
  function attachEditedBadge(meta, editedAtValue) {
    if (!meta) return;
    var isoTitle = 'Edited';
    if (editedAtValue) {
      var d = typeof editedAtValue === 'number'
        ? new Date(editedAtValue * 1000)
        : new Date(editedAtValue);
      if (!isNaN(d.getTime())) {
        isoTitle = 'Edited ' + d.toISOString();
      }
    }
    var existing = meta.querySelector('.post-edited');
    if (existing) {
      existing.setAttribute('title', isoTitle);
      return;
    }
    var span = document.createElement('span');
    span.className = 'post-edited';
    span.textContent = 'edited';
    span.setAttribute('title', isoTitle);
    var row = meta.querySelector('.post-meta-row');
    if (row) {
      meta.insertBefore(span, row);
    } else {
      meta.appendChild(span);
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

    // Sprint 15a.1 P3: walk every x-data carrier in the subtree
    // explicitly. The Sprint 15a fix called Alpine.initTree(newCard)
    // on the article root, expecting Alpine to discover nested
    // x-data declarations (the dropdown's `x-data="{open: false}"`
    // is several levels deep). On the live deploy that walk did NOT
    // pick up the nested element - the dropdown stayed dead until
    // a full page reload.
    //
    // Alpine 3's recursive walk has a quirk where, depending on the
    // initialisation timing of the root, descendant x-data nodes
    // can be skipped. Iterating every [x-data] inside the card
    // and initTree-ing each one individually is the documented
    // safer pattern. We also initTree the card itself when its
    // root has x-data (the post-card article does:
    // `x-data="{ liked: false, count: ... }"`).
    if (window.Alpine && typeof window.Alpine.initTree === 'function') {
      try {
        if (newCard.hasAttribute && newCard.hasAttribute('x-data')) {
          window.Alpine.initTree(newCard);
        }
        var xdataChildren = newCard.querySelectorAll
          ? newCard.querySelectorAll('[x-data]')
          : [];
        xdataChildren.forEach(function (el) {
          try { window.Alpine.initTree(el); } catch (e) {
            console.error('Alpine.initTree failed on injected x-data child', e, el);
          }
        });
      } catch (e) {
        console.error('Alpine.initTree failed on new post card', e);
      }
    }
    // Bind data-action click handlers (react chips, delete, etc.)
    // on the new card. bindActions is idempotent via dataset.bound.
    bindActions();
  }

  // ---------- HTMX hooks ----------

  // Sprint 15a.5.5: Alpine re-init on HTMX swap.
  //
  // base.html sets `hx-boost="true"` on the body so every internal
  // link fetches the destination via AJAX and swaps the rendered
  // HTML into place. HTMX by default does an innerHTML swap of the
  // target (for boosted links that's effectively the whole body),
  // which destroys the old DOM and inserts a new tree. Alpine 3's
  // `alpine:init` event ONLY fires on initial page load; it does
  // NOT re-run on swapped-in HTML. Without explicit wiring the
  // swapped-in components stay as static markup: x-data is never
  // evaluated, x-show / :style bindings never run, x-cloak never
  // gets stripped, and custom event listeners like
  // `@golab:open-edit-post.window` never register.
  //
  // Der Prinz confirmed the failure on lab.simplego.dev:
  //   document.querySelector('.edit-post-modal-backdrop')._x_dataStack
  //   // -> undefined AFTER the first htmx:afterSwap
  //
  // The eventually-visible ghost modal that chased us across
  // 15a.1 / 15a.3 / 15a.4 / 15a.5.4 happened when a LATER swap or
  // some unrelated DOM activity stripped the stale x-cloak
  // attribute off the orphaned modal. With no Alpine binding
  // driving inline display, the CSS default became the rendered
  // state (display:flex pre-15a.5.4; harmless display:none now)
  // and the full-viewport backdrop painted over the page.
  //
  // Fix pattern from Sprint 15a.1 P3 (WS-injected post cards):
  // walk every [x-data] descendant of the swapped target and call
  // Alpine.initTree on each. The explicit walk is needed because
  // Alpine 3's recursive initTree can skip nested x-data carriers
  // depending on the root's state at call time (documented above
  // in the WS-injected injection path). Also destroyTree the old
  // subtree before swap so the outgoing components release their
  // listeners and effects; without this, long-lived subscriptions
  // (e.g. GolabQuillMention's document-level mousedown, Quill's
  // internal observers) would leak across every boost navigation.
  document.body.addEventListener('htmx:beforeSwap', function (evt) {
    var target = evt && evt.detail && evt.detail.target;
    if (!target || !window.Alpine ||
        typeof window.Alpine.destroyTree !== 'function') return;
    try {
      if (target.hasAttribute && target.hasAttribute('x-data')) {
        window.Alpine.destroyTree(target);
      }
      var nodes = target.querySelectorAll
        ? target.querySelectorAll('[x-data]')
        : [];
      nodes.forEach(function (el) {
        try { window.Alpine.destroyTree(el); } catch (e) {
          console.error('Alpine.destroyTree failed on x-data child', e, el);
        }
      });
    } catch (e) {
      console.error('Alpine.destroyTree failed on swap target', e);
    }
  });

  document.body.addEventListener('htmx:afterSwap', function (evt) {
    var target = evt && evt.detail && evt.detail.target;
    if (target && window.Alpine &&
        typeof window.Alpine.initTree === 'function') {
      try {
        if (target.hasAttribute && target.hasAttribute('x-data')) {
          window.Alpine.initTree(target);
        }
        var nodes = target.querySelectorAll
          ? target.querySelectorAll('[x-data]')
          : [];
        nodes.forEach(function (el) {
          try { window.Alpine.initTree(el); } catch (e) {
            console.error('Alpine.initTree failed on x-data child', e, el);
          }
        });
      } catch (e) {
        console.error('Alpine.initTree failed on swap target', e);
      }
    }
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
