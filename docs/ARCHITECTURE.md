# GoLab Architecture

**Last updated:** 2026-04-23 (expanded with B7/B8 lessons)
**Scope:** This document describes how GoLab is built *today* (Phase 1): the technology choices behind each layer, the patterns that keep the codebase coherent, and the pitfalls we have already paid for. It is the first document a new contributor should read before touching code.

**Companion document:** [CONCEPT.md](./CONCEPT.md) describes the long-term strategic vision — E2E-encrypted community over SimpleX SMP transport with GoUNITY certificate identity and GoKey hardware security. CONCEPT.md is *where GoLab is going* (Phase 2 onward). This document is *where GoLab is now*.

GoLab is a privacy-first developer community platform positioned as a Twitter + GitHub hybrid: conversational timeline in the style of microblogging, but built around developer workflows (code posts, technical discussion, eventual Git integration). Phase 1 is deliberately server-rendered with progressive enhancement rather than a single-page application. Phase 2 will layer SMP transport beneath the same UI.

---

## 1. System at a glance

```
                      Browser (end user)
                             │
       ┌─────────────────────┼─────────────────────┐
       │ HTML (server render)│ JS (progressive)    │
       │   html/template     │   Alpine.js 3.14    │
       │   goldmark          │   HTMX 2.0          │
       │                     │   Quill 2.0.3       │
       └─────────────────────┼─────────────────────┘
                             │
                        HTTPS (Nginx)
                             │
                   ┌─────────┴─────────┐
                   │                   │
               HTTP POST           WebSocket
               GET / PATCH         /ws  (live feed)
                   │                   │
                   └─────────┬─────────┘
                             │
                       chi router
                             │
        ┌────────────────────┼────────────────────┐
        │                    │                    │
    Handlers              WS Hub              Middleware
    (feed, post,      (topic broker,       (auth, CSRF,
     admin, ws)        broadcast)           ratelimit)
        │                    │                    │
        └────────────────────┼────────────────────┘
                             │
                       Model layer
                       (pgx queries)
                             │
                       PostgreSQL 16
                       goose migrations
```

Everything is one Go binary plus a Postgres container. No Redis, no queue, no external cache, no third-party JS hosts. This keeps the operational surface small and the privacy posture strong.

---

## 2. Technology stack

### Backend

| Technology | Version | Role |
| --- | --- | --- |
| Go | 1.24 | Language and runtime. Single statically linked binary. |
| chi | v5 | HTTP router. Thin, net/http compatible, middleware-friendly. |
| pgx | v5 | PostgreSQL driver. Used directly, no ORM. Prepared statements by default. |
| goose | latest | Schema migrations under `internal/database/migrations/`. Forward-only by policy. |
| goldmark | latest | Markdown rendering for post content with extensions. |
| bluemonday | latest | HTML sanitization for user-generated content. |
| argon2id | stdlib crypto | Password hashing. |
| scs | v2 | Session management with Postgres-backed store. |
| gorilla/websocket | latest | WebSocket implementation for the live feed hub. |

### Frontend

| Technology | Version | Role |
| --- | --- | --- |
| Go html/template | 1.24 stdlib | Primary rendering engine. Every page is server-rendered HTML. |
| Alpine.js | 3.14 | Client-side reactivity. Component-local state, `x-data` directives. |
| HTMX | 2.0 | Partial page updates, `hx-boost` navigation, XHR without JS. |
| Quill | 2.0.3 | Rich-text editor for post composition and editing. |
| Vanilla CSS | — | No framework. Custom design tokens in `golab.css`. |

**No build step for the frontend.** All assets are served directly from `web/static/`. No npm, no webpack, no esbuild. This is a deliberate choice that aligns with the privacy-first stance (no opaque build toolchains) and keeps development friction low.

### Infrastructure

| Component | Role |
| --- | --- |
| Debian 13 | Host OS on the VPS (194.164.197.247). |
| Docker Compose | Orchestrates the `golab` and `db` containers. |
| Nginx | Reverse proxy with TLS termination. Serves static assets directly. |
| Let's Encrypt | Certificate issuance via certbot. RSA certs (not ECDSA) by policy. |
| `deploy.sh` | Git pull + Docker rebuild script. |

---

## 3. Architecture patterns

GoLab follows five patterns consistently. Understanding these makes the codebase predictable.

### 3.1 Server-side rendering as primary

Every page is fully rendered HTML sent from the server. A user with JavaScript disabled can still read posts, sign up, log in, create a post, reply, and navigate. JavaScript enhances but never replaces the baseline.

This is the opposite of a single-page application. There is no client-side router, no state store, no REST-first API design. The server is the source of truth; the browser is a progressively enhanced viewport.

### 3.2 Progressive enhancement via Alpine and HTMX

Alpine.js handles component-local reactivity: dropdowns, modals, reaction toggles, compose editor state. Each Alpine component is defined inside the template with `x-data="componentName()"` and its factory function lives in `web/static/js/golab.js`.

HTMX handles navigation and partial updates: `hx-boost="true"` on the body turns link clicks into XHR body swaps, making the site feel like a SPA without being one. `hx-post` on specific forms submits without a full reload.

Together they cover roughly 90% of the interactive surface. The remaining 10% is plain event handlers in golab.js (WebSocket message routing, keyboard shortcuts, drag-and-drop, clipboard).

### 3.3 WebSocket-driven live updates

A single WebSocket connection per authenticated client connects to `/ws` and subscribes to topics. The hub (`internal/handler/ws.go`) broadcasts typed messages to topic subscribers. Key topics:

- `global` — every logged-in user is auto-subscribed. Used for feed-level events.
- `space:<slug>` — per-space events, only subscribed when viewing that space.
- `user:<id>` — per-user events (notifications).

Events broadcast this way include `new_post`, `post_deleted`, `new_reaction`, `new_comment`, `new_notification`. The client routes these to DOM-manipulation functions in golab.js (`injectNewPost`, `removePostCard`, `updateReactionCount`).

This pattern is why the post-submit flow does not need to reload: the hub echoes the new post back to the author's own WebSocket, and the client injects it at the top of the feed with a fade-in animation.

### 3.4 Progressive component hydration

Alpine components must be initialized on any DOM content that is added to the page after the initial load. GoLab has three sources of injected content:

1. **WebSocket-injected post cards** — `injectNewPost` calls `Alpine.initTree()` on the new element and walks every `[x-data]` descendant.
2. **HTMX body swaps via `hx-boost`** — two listeners in golab.js: `htmx:beforeSwap` calls `Alpine.destroyTree()` on the old content to prevent listener leaks, `htmx:afterSwap` calls `Alpine.initTree()` plus `htmx.process()` to bind the new content.
3. **Modal templates rendered conditionally** — the edit-post modal lives in `base.html` so it mounts once per page and is reused; the admin modals are rendered per admin view.

**This is the single most important pattern in the codebase.** Forgetting to re-init Alpine on injected content was the root cause of Sprint 15a.5.5 (modal ghost bug on every hx-boost navigation).

### 3.5 Forward-only migrations

Schema migrations live in `internal/database/migrations/` and run automatically on container startup via goose. Rollback migrations are explicitly forbidden: production data is real user data, and automated rollbacks destroy it. If a schema change needs to be reverted, it happens via a new forward migration written against a reviewed plan.

This is encoded in the Makefile: there is no `migrate-down` target.

---

## 4. Component map

This maps top-level concerns to files so a new contributor can navigate quickly.

### Backend

| Concern | Location |
| --- | --- |
| HTTP entrypoint, router setup | `cmd/golab/main.go` |
| Route groups, middleware composition | `internal/handler/routes.go` |
| Post CRUD + edit history | `internal/handler/post.go`, `internal/model/post.go`, `internal/model/post_edit_history.go` |
| Feed queries | `internal/handler/feed.go`, `internal/model/feed.go` |
| User auth, sessions, registration | `internal/handler/auth.go`, `internal/model/user.go` |
| Reactions | `internal/handler/reactions.go`, `internal/model/reaction.go` |
| Mentions (@user autocomplete + link rewrite) | `internal/handler/users.go`, `internal/model/mention.go`, `internal/render/mentions.go` |
| Admin panel | `internal/handler/admin.go` |
| WebSocket hub, client registration, broadcast | `internal/handler/ws.go` |
| Rendering helpers (markdown, sanitization, mentions) | `internal/render/` |
| Database connection, migrations | `internal/database/db.go`, `internal/database/migrations/` |
| Configuration, env loading | `internal/config/config.go` |

### Frontend

| Concern | Location |
| --- | --- |
| Page layout, nav, shared modals | `web/templates/base.html` |
| Page templates | `web/templates/*.html` |
| Partials (post card, reply form, modals) | `web/templates/partials/` |
| All client JS | `web/static/js/golab.js` (single file by design) |
| Quill mention plugin | `web/static/js/quill-mention.js` |
| Quill itself | `web/static/js/quill.min.js` |
| Alpine.js | `web/static/js/alpine.min.js` |
| HTMX | `web/static/js/htmx.min.js` |
| All styles | `web/static/css/golab.css` (single file by design) |
| Quill theme CSS | `web/static/css/quill.snow.css` |

Single-file `golab.js` and `golab.css` is intentional: the whole frontend is ~3000 lines of JS and ~2500 lines of CSS, which is small enough to be readable as one file and avoids a build pipeline. If either file grows past ~5000 lines we revisit this.

---

## 5. Request data flow

A typical post-creation request illustrates how the layers collaborate.

```
1. User types in composer, clicks "Post"
2. composeEditor.submit() (Alpine method in golab.js):
     - charCount check
     - assembles body { content, space_id?, post_type, tags[], parent_id? }
     - apiJSON('/api/posts', 'POST', body)
3. HTTP POST /api/posts hits chi router
4. Middleware chain:
     - session middleware (scs) loads user from cookie
     - CSRF middleware validates token
     - ratelimit middleware checks per-user budget
5. PostHandler.Create:
     - validates content with bluemonday
     - renders markdown with goldmark
     - rewrites @mentions via render.LinkMentions
     - inserts row via model.Post.Create (pgx prepared statement)
     - calls hub.PublishNewPost(post, slug)
     - responds 201 with post payload
6. Hub.PublishNewPost:
     - renders post-card.html fragment server-side
     - broadcasts typed message {type: "new_post", html: "..."} to "global" topic
     - every subscribed client receives it over their WebSocket
7. Client WebSocket handler:
     - handleWSMessage routes type "new_post" to injectNewPost
     - injectNewPost prepends the card HTML to the feed container
     - adds post-enter class for fade-in animation
     - calls Alpine.initTree on the new card to bind x-data components
8. composeEditor.submit() .then():
     - clears Quill content
     - resets charCount
     - shows "Posted" toast
     - (no reload; the WebSocket echo already inserted the card)
```

Two points of note:

- **The author's browser receives the post via the same WebSocket path as everyone else.** There is no separate success-render path. This keeps the code simple and guarantees consistency.
- **Server-side rendering of the post-card fragment** means the client does not need to know how to render a post. The hub emits ready-to-insert HTML. The same fragment template is used for the initial feed render and for live injection.

---

## 6. Deployment architecture

### Host

Debian 13 VPS at 194.164.197.247, hostname `smp.simplego.dev`, also serves `lab.simplego.dev` via Nginx virtual host. The same VPS runs other SimpleGo ecosystem services (SMP server, Matrix/Tuwunel, static sites) as separate containers or processes.

### Nginx

Nginx terminates TLS with Let's Encrypt RSA certs and reverse-proxies `lab.simplego.dev` to `golab:3000` inside the Docker network. Static assets under `/static/` are served by the Go binary itself, not Nginx (simpler, and the binary has correct cache headers).

Nginx also runs the **stream module** on port 443 with SNI routing for non-HTTP services like the SMP server (eliminating non-standard ports). Site configs listen on `8443` internally; the stream module routes based on SNI hostname.

### Docker Compose

Two services:

- `golab` — the Go binary. Builds from the project Dockerfile. Exposes 3000 internally.
- `db` — PostgreSQL 16 official image. Named volume `db_data` for persistence.

`docker-compose.yml` contains environment variables including secrets. **Known deploy-time issue:** `deploy.sh` does `git reset --hard` before rebuilding, which wipes VPS-only secrets from `docker-compose.yml`. Current workaround is a `sed` block run manually after each deploy. This is scheduled for refactor into an `.env.production` file that `deploy.sh` does not touch.

### Deploy flow

```
ssh into VPS
cd /opt/GoLab
./deploy.sh
# (restore secrets via sed block)
docker compose up -d --force-recreate
docker compose logs --tail=15 golab  # verify migrations applied + server listening
```

---

## 7. Known pitfalls (read before debugging)

These are lessons learned the hard way. Each is documented inline in the code as well, but collected here for quick reference.

### 7.1 Alpine Reactive Proxy breaks identity checks

Alpine wraps every property assigned to `this` in an Alpine component with a reactive Proxy. Libraries that do `this === other` identity checks on their own instances break when stored as `this.foo` because Alpine creates distinct Proxy wrappers for the same underlying object.

**Symptom:** Quill's internal `scroll.find()` returns `null` for any node because `n.scroll === this` fails.

**Fix:** Store library instances in closure variables, not on `this`. See `editPostModal` and `composeEditor` in golab.js for the pattern.

### 7.2 `x-cloak` without CSS rule is a silent no-op

The `x-cloak` attribute does literally nothing unless there is a global CSS rule `[x-cloak] { display: none !important; }`. DevTools will show the attribute on elements, but the browser ignores it.

**Fix:** The rule is now at the top of golab.css and must stay there.

### 7.3 Alpine x-show + CSS default interplay

Alpine's `_x_doShow` implementation only *removes* inline display, expecting the CSS default to be the visible state. Alpine's `_x_doHide` *sets* inline `display: none`. This asymmetry means:

- If CSS default is `display: none` and `x-show="false"` at init, Alpine writes nothing and the default wins (hidden).
- If CSS default is `display: flex` and `x-show="false"` at init, Alpine must write `display: none` inline. Under some conditions (x-cloak already hiding, subsequent tree rebinding), this does not happen and the element becomes visible.

**Fix:** For full-viewport click-sink modals, use `:style="open ? 'display: flex' : 'display: none'"` instead of `x-show`. Explicit both-state binding eliminates the race. CSS default for these modals is `display: none` as a defense-in-depth fallback.

### 7.4 HTMX swap needs explicit Alpine init and destroy

`alpine:init` fires only once on initial page load. After any HTMX swap (including `hx-boost` link clicks), new DOM content has `x-data` attributes but no Alpine bindings. Elements appear inert, modals lose their visibility logic, event handlers do not attach.

**Fix:** Two listeners in golab.js:

```js
document.body.addEventListener('htmx:beforeSwap', (e) => {
  if (window.Alpine && e.detail.target) {
    Alpine.destroyTree(e.detail.target);
    // walk [x-data] descendants explicitly
  }
});
document.body.addEventListener('htmx:afterSwap', (e) => {
  if (window.Alpine && e.detail.target) {
    Alpine.initTree(e.detail.target);
    // walk [x-data] descendants explicitly
    htmx.process(e.detail.target);
  }
});
```

The explicit descendant walk is necessary because Alpine 3's recursive `initTree` sometimes skips nested `x-data` nodes.

### 7.5 Quill reactive content length bridge

Quill edits do not trigger Alpine reactivity automatically. If a form control's `:disabled` depends on content length, it will be evaluated once at bind time and never update as the user types.

**Fix:** Subscribe to Quill's `text-change` event in `_mountQuill` and write to an Alpine-observed property:

```js
quill.on('text-change', () => {
  self.contentLen = quill.getText().trim().length;
});
```

Then bindings like `:disabled="!contentLen"` update correctly.

### 7.6 Post-submit does not need a reload

The WebSocket hub echoes every new post back to its author's own connection. The client's `injectNewPost` handler prepends the card with animation. A `window.location.reload()` on submit success is redundant and causes visible flash, lost scroll position, and full Alpine re-init.

**Fix:** Remove the reload. Trust the WebSocket echo. Other `window.location.reload()` sites (username rename, admin actions) may still be legitimate because they invalidate cached identifiers like URLs.

### 7.7 `deploy.sh` wipes docker-compose.yml

`git reset --hard` in `deploy.sh` overwrites secret values in `docker-compose.yml`. The current workaround is a manual `sed` block after each deploy. The clean fix is to move secrets into `/opt/GoLab/.env.production` (outside Git, not touched by deploy.sh) and have docker-compose read from it via `env_file:`. This is in the backlog.

### 7.8 WebSocket echo can arrive before the HTTP response

After Sprint 15a.5.6 removed the post-submit reload and the app relied on the WebSocket echo for the new-post insertion, a subtler problem surfaced. The hub broadcast uses `User: nil` when rendering the post card (so non-authors do not get Edit/Delete buttons leaked to them). This means the broadcast version of the card has no dropdown. But for the author's own browser, we want the dropdown to appear.

The original plan was: server includes a second author-context render in the HTTP POST response, client prepends that version (with dropdown). In practice, **the WebSocket frame often arrives at the browser before the HTTP response completes.** The client's `handleWSMessage` runs first, inserts the anonymous (no-dropdown) version, and by the time the HTTP `.then` callback fires, the self-echo guard (`document.getElementById(newCard.id)` is truthy) blocks the author-context render.

**Fix:** The author's own submit path explicitly removes any existing card with the same ID before inserting the author-context version. The normal `injectNewPost` used by the WebSocket handler stays as "insert if not already there". Only the author-own-submit flow knows it has the authoritative render and may overwrite.

**Code pattern (in composeEditor.submit `.then` callback):**
```js
if (res.data && res.data.html && res.data.post) {
  var racedCard = document.getElementById('post-' + res.data.post.id);
  if (racedCard) racedCard.remove();  // let author-context win the race
  injectNewPost(res.data.html);
}
```

**Generalizing:** whenever a WebSocket broadcast renders with less context than the initiating user has access to, and the initiating user needs the higher-privilege render, the HTTP response must carry that richer version and the client must forcibly replace whatever the WebSocket already put in place.

### 7.9 Empty content check must be semantic, not byte-length

The original post-creation path checked `len(content) > 0` to reject empty posts. Quill's empty editor state produces `<p><br></p>` — 11 bytes, passes the check, gets stored as a visually-empty post. Users do not reach this path through the normal UI (the client has its own `hasContent()` check that keeps Save disabled), but a direct curl against `/api/posts` or a scripted attack bypasses both the client check and the byte-length check.

**Fix:** `internal/render/sanitize.go` now exposes `IsSemanticallyEmpty(html string) bool` which:

1. Runs the input through bluemonday's `StrictPolicy` (strips all tags, keeps only text)
2. Strips whitespace including NBSP (`\u00a0`, `&nbsp;`), zero-width characters (`\u200b`, `\u200c`, `\u200d`), and BOM (`\ufeff`)
3. Returns true if nothing is left

Used in both `PostHandler.Create` and `PostHandler.Update`. Unit tests in `sanitize_test.go` lock the behavior against 14 representative edge cases including `<p><br></p>`, entity-NBSP, zero-width variants, and nested-empty markup.

**Known limitation:** an image-only post (no text, one `<img>` tag) is currently rejected because `StrictPolicy` strips the image tag. If image-only posts should be allowed, a separate media-presence check needs to run before the emptiness check. That is a Phase 2 concern when attachments are fully designed.

### 7.10 Editable post response must include edit metadata consistently

When a field like `EditedAt` is introduced on a post, it must appear in **every** read path, not just the primary feed. Sprint 15a added `edited_at` to the feed render and the PATCH response but missed `GET /api/posts/{id}`. API consumers got inconsistent payloads depending on which endpoint they hit.

**Pattern to follow when adding a post-level field:**
1. Extend the model struct and scan path
2. Extend the JSON serialization
3. Grep for every handler that returns a post: `GET /api/posts/:id`, `GET /api/feed`, `POST /api/posts`, `PATCH /api/posts/:id`, WebSocket card renders
4. Confirm each path populates the field

The missing path in 15a was the single-post GET, which is used by the edit modal to seed the editor. It did not fail loudly — it just missed a field that API consumers would expect.

---

## 8. Testing strategy

GoLab has two layers of tests:

- **Unit tests** (`*_test.go`) — model logic, render helpers, mention extraction. Run with `go test ./...`.
- **Template tests** via `templatecheck` — a build step that renders every template against representative data to catch `nil` dereferences and field typos before they hit production.

There are no end-to-end browser tests. Integration testing happens locally (PostgreSQL 16 + Go 1.26 on Windows) and on production with a very short feedback loop.

**Local development workflow established April 2026:**
1. PostgreSQL 16 installed natively on the developer machine (not Docker, simpler for dev).
2. `.env` points `GOLAB_DB_HOST` to `127.0.0.1`.
3. `go run ./cmd/golab` starts the server on `http://localhost:3000`.
4. Browser tests against localhost before any push to a feature branch.

---

## 9. Design decisions and rationale

A few choices that might seem unusual and the reasoning:

**Why not React/Next.js?** A React frontend would add 3-5 MB of JavaScript and require a build pipeline, npm dependency tree, and a separate API-first backend. The privacy-first positioning and the operational simplicity rule this out. HTMX + Alpine cover the interactive needs at ~30 KB combined.

**Why not a separate API?** The server renders HTML and never needs to produce a REST surface for its own UI. A JSON API exists only for the specific calls that JavaScript makes (post create, reaction toggle, mention autocomplete, post edit). This keeps the surface area small.

**Why not an ORM?** pgx gives prepared statements, clean error handling, and lets the SQL be the source of truth. No ORM means no hidden query generation, no N+1 surprises from lazy loading, no learning curve beyond SQL itself.

**Why Quill and not a textarea with syntax?** Post composition needs inline images, links, emoji, @mentions, and rich formatting. A textarea would need all of that built from scratch. Quill provides it with a plugin architecture that fit our mention autocomplete cleanly.

**Why a single `golab.js` file?** Splitting into modules would require a bundler. The file is well-organized with section comments and is small enough to navigate via search. When it grows past 5000 lines we revisit.

**Why WebSocket topics and not Server-Sent Events?** SSE is one-way; WebSocket lets us implement typing indicators, presence, and eventually real-time collaboration without changing the transport.

---

## 10. Roadmap markers

The following are known near-term directions, documented here to provide context for current design choices. Details live in separate sprint documents.

- **Code features (Sprint 16 candidate):** server-side syntax highlighting via Chroma, Ace Editor for code posts, Mermaid/KaTeX extensions, snippet permalinks, developer-friendly Liquid Tags (`{% github %}`, `{% stackblitz %}`).
- **Trust Levels TL0-TL4:** Discourse-style earned permissions as the foundation for community moderation without surveillance.
- **Composable moderation via labelers:** Bluesky-style per-space labeler subscriptions, complementing Trust Levels.
- **Deploy pipeline refactor:** move secrets to `.env.production`, make `deploy.sh` idempotent and secret-preserving.
- **SimpleX SMP migration (Welle 3):** eventually route private messages and small-group communication through the SimpleX Messaging Protocol for end-to-end encryption. Public posts remain server-rendered.

---

## 11. Who to ask

- **Sascha (Der Prinz)** — Project director, all strategic and architectural decisions, VPS admin, PR merges.
- **Claude (Prinzessin Mausi)** — Planning, briefings, documentation, knowledge base maintenance.
- **Claude Code (Der Ritter)** — Implementation on feature branches. All code changes go through PR review by Sascha.

No direct pushes to `main`. No auto-merges. No rollback migrations. No reloads where a WebSocket echo does the job.

---

*This document should be updated whenever a significant architectural decision changes. Small file additions and bug fixes do not require updating this file; new patterns, new layers, or changes to the deployment model do.*
