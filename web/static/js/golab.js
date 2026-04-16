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
      btn.disabled = true;
      apiJSON('/api/users/me', 'PUT', {
        display_name: form.display_name.value,
        bio: form.bio.value,
        avatar_url: form.avatar_url.value
      }).then(function (res) {
        btn.disabled = false;
        if (res.ok) {
          toast('success', 'Profile saved');
        } else {
          setError(form, res.data.error || 'Could not save');
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
    var form = document.getElementById('compose-form');
    if (!form || form.dataset.bound) return;
    form.dataset.bound = '1';
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var textarea = form.querySelector('textarea[name=content]');
      var content = (textarea.value || '').trim();
      if (content.length < 1 || content.length > 5000) return;
      var chanEl = form.querySelector('[name=channel_id]');
      var body = { content: content };
      if (chanEl && chanEl.value) body.channel_id = parseInt(chanEl.value, 10);
      var btn = form.querySelector('button[type=submit]');
      btn.disabled = true;
      apiJSON('/api/posts', 'POST', body).then(function (res) {
        btn.disabled = false;
        if (res.ok) {
          textarea.value = '';
          // Trigger Alpine's x-model reactivity
          textarea.dispatchEvent(new Event('input'));
          toast('success', 'Posted');
          // Reload to pick up server-rendered view; WS will handle live updates next time.
          setTimeout(function () { window.location.reload(); }, 250);
        } else {
          toast('error', res.data.error || 'Could not post');
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
      case 'reaction':
        if (msg.data && msg.data.post_id != null) {
          var counter = document.getElementById('react-count-' + msg.data.post_id);
          if (counter && msg.data.count != null) {
            counter.textContent = msg.data.count;
            counter.classList.add('count-update');
            setTimeout(function () { counter.classList.remove('count-update'); }, 320);
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

  function bindAll() {
    bindRegister();
    bindLogin();
    bindSettings();
    bindCreateChannel();
    bindCompose();
    bindActions();
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
