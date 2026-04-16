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
    window.Alpine.data('avatarUploader', function (initialUrl, letter) {
      return {
        preview: initialUrl || '',
        dragging: false,
        initial: letter || '',
        handleDrop: function (ev) {
          this.dragging = false;
          var file = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
          if (file) this.upload(file);
        },
        handleFile: function (ev) {
          var file = ev.target.files && ev.target.files[0];
          if (file) this.upload(file);
        },
        upload: function (file) {
          var self = this;
          if (!file.type.startsWith('image/')) {
            toast('error', 'Please select an image');
            return;
          }
          // Show local preview immediately
          var reader = new FileReader();
          reader.onload = function (e) { self.preview = e.target.result; };
          reader.readAsDataURL(file);

          var form = new FormData();
          form.append('avatar', file);
          fetch('/api/users/me/avatar', {
            method: 'POST',
            body: form,
            credentials: 'same-origin'
          }).then(function (r) {
            return r.json().then(function (d) { return { ok: r.ok, data: d }; });
          }).then(function (res) {
            if (res.ok) {
              self.preview = res.data.avatar_url;
              var hidden = document.getElementById('st-avatar-url');
              if (hidden) hidden.value = res.data.avatar_url;
              toast('success', 'Avatar uploaded');
            } else {
              toast('error', res.data.error || 'Upload failed');
            }
          }).catch(function () { toast('error', 'Upload failed'); });
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

  function bindCompose() {
    var forms = [document.getElementById('compose-form'), document.getElementById('reply-form')];
    forms.forEach(function (form) {
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
            toast('success', body.parent_id ? 'Reply posted' : 'Posted');
            setTimeout(function () { window.location.reload(); }, 250);
          } else {
            toast('error', res.data.error || 'Could not post');
            form.classList.add('shake');
            setTimeout(function () { form.classList.remove('shake'); }, 500);
          }
        });
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
        el.addEventListener('click', function () {
          var id = el.dataset.postId;
          var willLike = !el.classList.contains('active');
          el.classList.toggle('active');
          // optimistic counter
          var counter = document.getElementById('react-count-' + id);
          if (counter) {
            var n = parseInt(counter.textContent || '0', 10);
            counter.textContent = willLike ? n + 1 : Math.max(0, n - 1);
            counter.classList.add('count-update');
            setTimeout(function () { counter.classList.remove('count-update'); }, 320);
          }
          apiJSON('/api/posts/' + id + '/react', willLike ? 'POST' : 'DELETE', null);
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
    });
  }

  // ---------- WebSocket client ----------

  var ws = null;
  var wsAttempt = 0;
  var wsClosed = false;

  function wsConnect() {
    try {
      var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      ws = new WebSocket(proto + '//' + location.host + '/ws');
    } catch (e) {
      scheduleReconnect();
      return;
    }

    ws.onopen = function () {
      wsAttempt = 0;
      // Subscribe to current context
      var ctx = detectContext();
      if (ctx) ws.send(JSON.stringify({ type: 'subscribe', topic: ctx }));
    };

    ws.onmessage = function (ev) {
      var msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      handleWSMessage(msg);
    };

    ws.onclose = function () {
      if (wsClosed) return;
      scheduleReconnect();
    };

    ws.onerror = function () {
      if (ws) try { ws.close(); } catch (e) {}
    };
  }

  function scheduleReconnect() {
    wsAttempt++;
    var delay = Math.min(1000 * Math.pow(1.6, wsAttempt), 20000);
    setTimeout(wsConnect, delay);
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

  // ---------- code block copy buttons ----------

  function decorateCodeBlocks() {
    document.querySelectorAll('.post-content pre').forEach(function (pre) {
      if (pre.dataset.copyBound) return;
      pre.dataset.copyBound = '1';
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'code-copy-btn';
      btn.textContent = 'Copy';
      btn.addEventListener('click', function (e) {
        e.preventDefault();
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
    setupScrollReveal();
    animateCounters();
  }

  // ---------- init ----------

  function init() {
    initNavbar();
    bindAll();
    wsConnect();
    window.addEventListener('beforeunload', function () {
      wsClosed = true;
      if (ws) { try { ws.close(); } catch (e) {} }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
