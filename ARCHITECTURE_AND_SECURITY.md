# GoLab - Architecture & Security

**Document version:** April 2026 (Season 2 close)
**Project:** GoLab - Privacy-First Developer Community Platform
**License:** AGPL-3.0-or-later
**Copyright:** 2025-2026 Sascha Daemgen, IT and MORE Systems, Recklinghausen

---

## What is GoLab?

GoLab is a developer community platform that combines GitLab-style project collaboration with Twitter-style social interaction. Phase 1 is live today as a conventional server-rendered web application at lab.simplego.dev, providing a curated community where read access is open to everyone and write access is reviewed personally through an application process. Phase 2 will replace the transport layer with SimpleX SMP queues, swap account-based authentication for GoUNITY certificates, and add hardware identity verification via GoKey - turning GoLab from a privacy-respecting platform into a structurally privacy-preserving one where the server cannot read content, identify users, or correlate social activity.

This document describes both phases. Phase 1 is the implementation as it exists today, deployed and serving real users. Phase 2 is the architectural target, designed but not yet built. The two phases share the same UI, the same content model, and the same community of users - what changes is the trust model of the server and the cryptographic primitives underneath.

---

## 1. Phase 1 Architecture (Live)

### 1.1 Why Server-Rendered Go, Not React

Most modern community platforms ship a JavaScript single-page application backed by a REST or GraphQL API. Discourse, Mastodon, Lemmy, and every commercial alternative follow this model. It produces large bundle sizes, requires a build pipeline with hundreds of npm dependencies, and creates a separate API surface that must be secured independently from the rendering layer.

GoLab Phase 1 takes a different approach: every page is fully rendered HTML sent from the server. A user with JavaScript disabled can sign up, log in, browse posts, reply, and navigate the entire site. JavaScript enhances but never replaces the baseline. This is the opposite of a single-page application.

The choice is technical and strategic. Technically, server-rendered HTML is faster on first paint, simpler to debug, and produces a smaller attack surface. Strategically, it aligns with the privacy-first stance: no opaque build toolchains, no third-party JavaScript hosts at runtime, no telemetry baked into a bundler. The complete frontend is one CSS file (~2500 lines) and one JavaScript file (~3000 lines) plus three vendored libraries (HTMX, Alpine.js, Quill.js). All assets are served directly from `web/static/` with no build step.

### 1.2 Technology Stack

| Layer | Technology | Version | Role |
|---|---|---|---|
| Language | Go | 1.24 | Single statically linked binary, ~28 MB |
| Router | chi | v5 | Thin net/http compatible HTTP router |
| Database | PostgreSQL | 16 | Primary store, full-text search, jsonb |
| Driver | pgx | v5.7.4 | Direct SQL with prepared statements |
| Migrations | goose | v3.21.1 | Forward-only schema migrations |
| Sessions | scs | v2 | Postgres-backed session store |
| Markdown | goldmark | latest | Post content rendering with extensions |
| Sanitization | bluemonday | latest | HTML sanitization on all user content |
| Password | bcrypt | stdlib | Cost factor 12 (Argon2id migration planned) |
| Rate limit | httprate | latest | Per-IP and per-user budgets |
| WebSocket | coder/websocket | latest | Live updates, replaces gorilla/websocket |
| Templates | html/template | stdlib | Server-side rendering, no JSX |
| Reactivity | Alpine.js | 3.14 | Client-side component state |
| Page transitions | HTMX | 2.0 | Partial page updates via XHR |
| Editor | Quill.js | 2.0.3 | WYSIWYG with image upload |
| Diagrams | Mermaid | 10.x | Server-rendered SVG |
| Math | KaTeX | latest | Client-side math rendering |
| Syntax highlight | Chroma + highlight.js | latest | Server + client fallback |

Zero npm dependencies. All client-side libraries are self-hosted in `web/static/js/` and `web/static/css/`. No CDN calls at runtime.

### 1.3 System Architecture

```
                Browser (end user)
                        |
        +---------------+----------------+
        | HTML (server) | JS (progressive)|
        | html/template | Alpine.js       |
        | goldmark      | HTMX 2.0        |
        | bluemonday    | Quill 2.0.3     |
        +---------------+----------------+
                        |
                  HTTPS (Nginx)
                        |
              +---------+---------+
              |                   |
         HTTP POST            WebSocket
         GET / PATCH          /ws (live feed)
              |                   |
              +---------+---------+
                        |
                   chi router
                        |
        +---------------+----------------+
        |               |                |
    Handlers         WS Hub         Middleware
    (feed, post,    (topic broker, (auth, CSRF,
     admin, ws)      broadcast)     ratelimit)
        |               |                |
        +---------------+----------------+
                        |
                  Model layer
                  (pgx queries)
                        |
                  PostgreSQL 16
                  goose migrations
```

Everything is one Go binary plus a Postgres container. No Redis, no message queue, no external cache, no third-party services. This minimizes operational surface and aligns with the privacy posture.

### 1.4 Architectural Patterns

GoLab Phase 1 follows five patterns consistently:

**Server-side rendering as primary.** Every page is fully rendered HTML. The server is the source of truth, the browser is a progressively enhanced viewport. There is no client-side router, no state store, no REST-first API design.

**Progressive enhancement via Alpine and HTMX.** Alpine handles component-local reactivity (dropdowns, modals, reaction toggles, compose editor state). HTMX handles navigation and partial updates via `hx-boost="true"` on the body. Together they cover ~90% of the interactive surface. The remaining 10% is plain event handlers.

**WebSocket-driven live updates.** A single WebSocket connection per authenticated client subscribes to topics: `global` (every logged-in user, feed-level events), `space:<slug>` (per-space events), `user:<id>` (per-user notifications). The hub broadcasts typed messages (`new_post`, `post_deleted`, `new_reaction`, `new_comment`, `new_notification`) to topic subscribers. The client routes these to DOM-manipulation functions in `golab.js`.

**Progressive component hydration.** Alpine components must be initialized on any DOM content added after initial load. Three sources of injected content: WebSocket-injected post cards (calls `Alpine.initTree()` on the new element), HTMX body swaps via `hx-boost` (two listeners: `htmx:beforeSwap` calls `Alpine.destroyTree()`, `htmx:afterSwap` calls `Alpine.initTree()` plus `htmx.process()`), and modal templates rendered conditionally (mounted once per page in `base.html`). Forgetting to re-init Alpine on injected content is the single most common bug pattern.

**Forward-only migrations.** Schema migrations live in `internal/database/migrations/` and run automatically on container startup via goose. Rollback migrations are explicitly forbidden. If a schema change needs reverting, it happens via a new forward migration written against a reviewed plan. The Makefile has no `migrate-down` target.

### 1.5 Application-Based Registration (Season 2)

Open registration with email is gone. Phase 1 implements a curated access model where read access is public but write access requires an application. The application flow is an 11-step brutalist wizard:

1. Welcome (introduction)
2. Account (username + password)
3. Where you build (external links, optional)
4. Ecosystem (how you connect to SimpleGo, required, 30-800 chars)
5. Contribution (what you bring, required, 30-600 chars)
6. Current focus (optional, 0-400 chars)
7. Notes (optional, 0-300 chars)
8. Knowledge 1 - Technical depth (a/b/c choice + 100-500 char answer, required)
9. Knowledge 2 - Practical experience (optional, 0-400 chars)
10. Knowledge 3 - Critical thinking (optional, 0-400 chars)
11. Review and submit

Username availability is checked live during step 2 against a 17-entry reservedUsernames blocklist plus the active users table. Live debounced API calls return one of seven status states (idle, checking, available, taken, reserved, invalid, error). Continue is gated on `usernameStatus === "available"` plus password validity.

Knowledge questions are designed to be unanswerable by general-purpose AI assistants. The questions target deep SimpleX/Matrix/SMP protocol knowledge that produces wrong, generic, or shallow output from large language models. Admin reviews the answers separately from the rating system - the questions are editorial filters, not numerical scores.

Admins evaluate applications via a 5-dimensional rating widget:

| Dimension | Question |
|---|---|
| track_record | Does this person have demonstrable history in the space? |
| ecosystem_fit | Are they aligned with SimpleGo philosophy and goals? |
| contribution_potential | Will they meaningfully contribute, not just consume? |
| relevance | Are their interests relevant to the community focus? |
| communication | Can they articulate technical thoughts clearly? |

Each dimension is scored 1-10 with optimistic UI updates and rollback on failure. Ratings are stored in the `application_ratings` table with the reviewer's user_id, timestamp, and optional notes. The first user to register is auto-promoted to admin (power_level 100) and auto-approved via a two-layer mechanism (UserStore migration + handler-level fallback).

### 1.6 Data Model

The Phase 1 database has 14 tables across 28 migrations:

**Identity:**
- `users` - id, username, password_hash, display_name, bio, avatar_url, power_level (0-100), status (pending/active/rejected), banned, banned_at, banned_reason, reviewed_at, reviewed_by, hardware_verified, plus 5 application fields (external_links, ecosystem_connection, community_contribution, current_focus, application_notes), plus 4 knowledge fields (technical_depth_choice with CHECK constraint, technical_depth_answer, practical_experience, critical_thinking), created_at, updated_at
- `sessions` - id, user_id, token, created_at, expires_at
- `application_ratings` - user_id (PK), 5 nullable INT dimensions, reviewer_id, reviewed_at, notes, updated_at

**Content:**
- `posts` - id, as_type, author_id, channel_id, parent_id, space_id, post_type, content, content_html, search_vector, reaction_count, reply_count, repost_count, edited_at, edit_count, created_at, updated_at
- `post_edit_history` - id, post_id, edited_at, previous_content
- `channels` - id, name, slug, description, channel_type, space_id, creator_id, created_at (kept in DB but hidden from UI)
- `channel_members` - id, channel_id, user_id, joined_at
- `spaces` - id, name, slug, description, icon, color, sort_order, created_at (8 thematic spaces seeded)
- `tags` - id, name, slug, use_count, created_by, created_at
- `post_tags` - post_id, tag_id (composite PK)

**Activity:**
- `follows` - id, follower_id, following_id, created_at
- `reactions` - id, user_id, post_id, reaction_type, created_at (toggle-only, one per user per post per type)
- `notifications` - id, user_id, actor_id, notif_type, post_id, read, created_at

**Storage:**
- `uploads` - id, user_id, filename, mime_type, size_bytes, url, created_at
- `settings` - key (PK), value, updated_at (require_approval forced to true since Migration 026)

The `users.email` column was dropped in Migration 026 as part of the application overhaul. There is no email recovery, no SMTP, no email notifications - account recovery in Phase 1 requires admin intervention, and is reframed in Phase 2 around hardware identity.

### 1.7 Request Data Flow

A typical post-creation request illustrates how the layers collaborate:

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
   - broadcasts {type: "new_post", html: "..."} to "global" topic
   - every subscribed client receives over their WebSocket

7. Client WebSocket handler:
   - handleWSMessage routes type "new_post" to injectNewPost
   - injectNewPost prepends the card HTML to the feed container
   - adds post-enter class for fade-in animation
   - calls Alpine.initTree on the new card

8. composeEditor.submit() .then():
   - clears Quill content
   - resets charCount
   - shows "Posted" toast
   - (no reload; the WebSocket echo already inserted the card)
```

Two architectural notes: the author's own browser receives the post through the same WebSocket path as everyone else (no separate success-render path), and the server renders the post-card fragment so the client never needs to know how to render a post.

The same WebSocket path enables Sprint 15a's real-time edit propagation: when a user edits their post, all viewers see the update live, complete with an "edited" badge that shows the timestamp on hover.

### 1.8 Deployment Architecture

**Host:** Debian 13 VPS at 194.164.197.247, hostname `smp.simplego.dev`, also serving `lab.simplego.dev` via Nginx virtual host. The same VPS runs other SimpleGo ecosystem services as separate containers.

**Nginx:** Terminates TLS with Let's Encrypt RSA certificates and reverse-proxies `lab.simplego.dev` to `golab:3000` inside the Docker network. Static assets under `/static/` are served by the Go binary itself. Nginx also runs the stream module on port 443 with SNI routing for non-HTTP services like the SMP server.

**Docker Compose:** Two services - `golab` (the Go binary, builds from project Dockerfile, exposes 3000 internally) and `db` (PostgreSQL 16 official image, named volume `db_data` for persistence).

**Deploy flow:**

```bash
ssh root@194.164.197.247
cd /opt/GoLab
./deploy.sh
# secret-restore block (sed) to repopulate docker-compose.yml secrets
# wiped by git reset --hard inside deploy.sh
docker compose up -d --force-recreate
docker compose logs --tail=15 golab
```

The secret-restore block is a known wart. `deploy.sh` does `git reset --hard` before rebuilding, which wipes VPS-only secrets from `docker-compose.yml`. The clean fix (move secrets to `.env.production` outside Git, read via `env_file:` in compose) is in the backlog.

---

## 2. Phase 1 Security

### 2.1 Threat Model

Phase 1 is a conventional web application with a trusted server. The server operator (Sascha Daemgen / IT and MORE Systems) sees post content, user identities, IP addresses, social graphs, and activity patterns. This is the same trust model as Discourse, Mastodon, or any self-hosted forum.

Phase 1 protects against:

- External attackers attempting account takeover via password guessing, session hijacking, CSRF, XSS, SQL injection
- Malicious users attempting to inject scripts or markup into posts that would execute in other users' browsers
- Bot registration attempting to spam the community
- Authenticated users attempting privilege escalation to moderator or admin powers

Phase 1 does not protect against:

- A compromised server (attacker with shell access reads everything)
- Legal compulsion (the operator can read any post)
- Network surveillance at the server level (the operator sees IP-to-user mappings)
- A malicious operator (this is the trust model itself)

Phase 2 is designed to address all four of those, by removing the server's ability to see plaintext content, removing accounts entirely, and making content storage end-to-end encrypted.

### 2.2 Authentication and Sessions

Passwords are hashed with bcrypt at cost factor 12. Argon2id migration is on the Season 3 priority list. Sessions are managed by `scs` v2 with a Postgres-backed store - cookies contain only an opaque session ID, not user data. Cookie configuration:

- `Secure` flag on production (HTTPS only)
- `HttpOnly` (no JavaScript access)
- `SameSite=Lax` (CSRF defense, GET requests from same site allowed)
- `__Host-` prefix in production (cookie scoped to exact host, no subdomain leakage)

Session timeout is currently not enforced (idle timeout, absolute timeout). This is on the Season 3 security backlog.

Login uses username (post-Migration 026, after the email column was dropped). Username availability checks during registration use a 17-entry reservedUsernames list plus the active users table:

```
admin, root, system, moderator, mod, support, help,
api, www, mail, info, golab, simplego, anonymous,
null, undefined, test
```

Reserved-name enforcement runs both in the public availability endpoint and in the registration handler itself, so a direct API submitter cannot bypass the frontend check.

### 2.3 CSRF Protection

CSRF tokens are issued per session and validated on all state-changing requests (POST, PATCH, DELETE). The token is included in form submissions via a hidden field and in JSON requests via the `X-CSRF-Token` header. Validation happens in middleware before any handler logic runs.

`SameSite=Lax` cookies provide a second layer - even without explicit token validation, browsers do not send cookies on cross-site POST requests. Both layers exist because `SameSite=Lax` is opt-in and not yet universal across older user agents.

CSP headers with nonce-based script execution are not yet deployed. Alpine's CSP build is on the Season 3 security block.

### 2.4 Content Sanitization

All user-submitted HTML passes through bluemonday before storage and again before render. The policy allows a curated subset of tags appropriate for posts (paragraphs, headings, lists, code blocks, links with `rel="nofollow noopener"` rewriting, images with src validation) and strips everything else.

The `IsSemanticallyEmpty` helper in `internal/render/sanitize.go` detects content that appears empty after sanitization:

1. Run input through bluemonday's `StrictPolicy` (strips all tags, keeps only text)
2. Strip whitespace including NBSP (`\u00a0`, `&nbsp;`), zero-width characters (`\u200b`, `\u200c`, `\u200d`), and BOM (`\ufeff`)
3. Return true if nothing is left

Used in both `PostHandler.Create` and `PostHandler.Update`. Unit tests in `sanitize_test.go` lock the behavior against 14 representative edge cases including `<p><br></p>` (Quill's default empty state), entity-encoded NBSP, zero-width variants, and nested-empty markup.

Markdown is rendered server-side via goldmark with extensions for tables, strikethrough, autolinks, task lists, and footnotes. Code blocks pass through Chroma for syntax highlighting before storage as `content_html`. The client falls back to highlight.js if a code block is rendered without server-side highlighting (e.g., live-edited content).

### 2.5 Rate Limiting

Per-IP and per-user rate limits are enforced via httprate middleware:

| Endpoint | Limit | Scope |
|---|---|---|
| Registration | 5/hour | Per IP |
| Login | 10/minute | Per IP |
| Post create | 30/minute | Per user |
| Post edit (PATCH) | 30/minute | Per user |
| Username availability | 30/minute | Per IP |
| Reactions | 60/minute | Per user |

Rate limits return 429 with a Retry-After header. There is no captcha, no JavaScript challenge - rate limits plus the application-based registration flow are considered sufficient for a community of this size.

### 2.6 Image Uploads

User-uploaded images go through a server-side resize pipeline before storage:

1. Multipart form upload, validated against a 5 MB size limit
2. MIME type sniffed from content (not Content-Type header)
3. Allowed types: image/png, image/jpeg, image/gif, image/webp
4. Decoded with `image/jpeg`, `image/png`, etc. from stdlib
5. Resized to max dimension 1200px preserving aspect ratio
6. Re-encoded as JPEG quality 85
7. Filename: UUID v4 plus extension
8. Stored in Docker volume `golab-uploads` mounted at `/uploads`
9. Served via `/uploads/<filename>` route with cache headers

Uploads are not currently encrypted at rest. AES-256-GCM encryption with per-file IVs is planned for Season 3.

Image-only posts (text empty, only `<img>`) are currently rejected by the semantic-empty check because `StrictPolicy` strips the image tag. This is a known limitation - if image-only posts should be allowed, a media-presence check needs to run before the emptiness check. That is a Phase 2 concern when attachments are fully designed.

### 2.7 Security Headers

The following headers are set on all responses:

- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Permissions-Policy: geolocation=(), microphone=(), camera=()`
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` (production only)

Content-Security-Policy is not yet enforced. Alpine.js requires either `unsafe-eval` or a CSP-build variant - the CSP-build migration is on the Season 3 backlog.

### 2.8 Admin Privileges and Power Levels

Power levels (0-100) gate moderator and admin actions:

| Level | Role | Capabilities |
|---|---|---|
| 0 | Guest (read-only) | Browse, view profiles |
| 10 | Member | Post, comment, react, edit own content |
| 50 | Moderator | Delete posts, ban users, approve applications |
| 100 | Owner / Admin | All above, plus settings, plus role assignment |

The first registered user is auto-promoted to power level 100 via a two-layer mechanism (UserStore code path plus handler-level fallback). Power level changes are enforced server-side - the dropdown in the admin UI is a hint, not the authority. Direct DB modification or curl bypass would still trigger the server-side validation.

Bans set `users.banned = true`, `banned_at`, and `banned_reason`. Banned users see a generic "your account is suspended" page on every request and cannot post. Their existing content is preserved by default but can be admin-deleted.

Application reviewers (admins) score applications across 5 dimensions. Reviews are stored in `application_ratings` with the reviewer's user_id and timestamp. There is no formula that auto-approves based on ratings - approval remains a manual decision by admins after considering both ratings and knowledge answers.

### 2.9 Known Phase 1 Security Gaps

| Gap | Severity | Status |
|---|---|---|
| bcrypt instead of Argon2id | Medium | Migration planned for Season 3 |
| No CSP headers | Medium | Alpine CSP build migration pending |
| No CSRF beyond SameSite | Low (defense in depth) | Token validation planned |
| Uploads not encrypted at rest | Medium | AES-256-GCM planned for Season 3 |
| No session idle timeout | Low | Configurable timeout planned |
| No 2FA | Medium | TOTP planned for Season 3 |
| No forgot-password flow | High UX | Hardware-backed recovery in Phase 2 |
| docker-compose secrets wiped on deploy | Low (operational) | `.env.production` refactor planned |

None of these are exploitable in their current state - they are defense-in-depth gaps that should close before any growth phase.

---

## 3. Phase 2 Architecture (Planned)

### 3.1 The Phase 2 Trust Model

Phase 1 trusts the server. Phase 2 does not. The architectural target is a system where:

- Post content is end-to-end encrypted in transit and at rest
- The server cannot identify users (no accounts, no profiles)
- The server cannot map social graphs (pairwise queue isolation)
- Identity is verifiable but pseudonymous (Ed25519 certificates)
- Moderation works without surveillance (certificate-linked, not account-linked)
- Hardware identity is optional but supported (GoKey / SimpleGo)

This is achieved by composing four existing SimpleGo components rather than building each from scratch:

| Component | Provides | Status |
|---|---|---|
| simplex-js | SMP transport (anonymous, E2E, queue-based) | Published, working |
| GoUNITY | Ed25519 certificate authority | Planned (Season 4) |
| GoBot | Community relay, fan-out, moderation | In development |
| GoKey | Hardware identity (ESP32-S3) | Planned (Season 3+) |

GoLab Phase 2 adds the application layer: channels, posts, projects, issues, feeds, profiles, search, permissions.

### 3.2 The Hybrid Model (Decided in Season 2)

A pure SMP-only architecture was considered and rejected after extensive Season 2 research. Browser-native SimpleX SMP is not feasible because WebSocket lacks TLS channel binding, WebCrypto lacks Curve448/Ed448, and there is no Go SDK for SMP. The simplex-chat CLI is the reference implementation but requires Haskell runtime.

The adopted hybrid model preserves both privacy and discoverability:

**Public path (Clearnet):** Read-only access at lab.simplego.dev. Standard HTTPS. SEO-friendly, indexable. Serves as the discovery layer and the entry point for new applicants. Posts marked `visibility=public` are rendered here. Posts marked `visibility=private` are not.

**Privacy path (SimpleGoX Plugin):** Native plugin in the SimpleGoX Multi-Messenger desktop application. Login via existing SimpleX profile. Notifications native. Full read+write access. All content E2E encrypted in transit through SimpleGoX's native SMP v9 client.

**Tor Onion Service v3:** Same content as Clearnet but accessed via .onion address. Alternative network path for users who prefer Tor anonymization at the network layer.

**I2P Service:** Alternative path for I2P users. Same content, same access.

Posts have a visibility flag. Public posts are visible everywhere. Private posts are only visible to authenticated users in the SimpleGoX plugin context. This separation lets the platform grow on Clearnet (good for discovery) while preserving full E2E privacy for users who choose it.

### 3.3 Phase 2 Architecture Diagram

```
[Browser]                  [SimpleGoX Plugin]              [Tor / I2P Browser]
    |                              |                              |
    | HTTPS (read public)          | SMP v9 (read+write)          | HTTPS over Tor/I2P
    |                              |                              |
    +------------------------------+------------------------------+
                                   |
                          [Nginx reverse proxy]
                                   |
                                   |  Standard HTTP routes
                                   |  for Clearnet path
                                   |
                          [GoLab Application Server]
                                   |
                                   | gRPC / Unix socket
                                   |
                          [GoLab SMP Adapter]
                                   |
                                   | SMP v9 wire protocol
                                   |
                          [GoBot Community Relay]
                                   |
                                   | E2E encrypted blocks
                                   | Per-channel queue fan-out
                                   |
            +----------------------+-----------------------+
            |                      |                       |
   [Subscriber A queue]   [Subscriber B queue]   [Subscriber C queue]
   Pairwise SMP queue     Pairwise SMP queue      Pairwise SMP queue
   GoBot cannot           GoBot cannot            GoBot cannot
   correlate across       correlate across        correlate across
```

The SMP Adapter is a new GoLab component - it bridges the conventional Go application server with the SMP wire protocol via either a sidecar process running simplex-chat or a custom JSON-RPC client. This adapter is what allows the same content model and UI to operate over both transports.

### 3.4 Posting in a Channel (Phase 2)

```
1. User writes "Fixed the memory leak in GoChat" in #gochat-dev

2. SimpleGoX plugin creates ActivityStreams object:
   {"type": "Create", "object": {"type": "Note", "content": "..."}}
   Signs it with Ed25519 private key from GoUNITY certificate

3. SimpleGoX's native SMP v9 client encrypts and sends via SMP queue
   to GoBot relay address

4. GoBot receives encrypted block
   In GoKey mode: forwards to ESP32 for decryption and command check
   GoBot itself never sees the plaintext

5. GoBot fans out the encrypted message to all #gochat-dev subscribers
   Each subscriber receives via their own pairwise SMP queue
   Relay cannot correlate subscribers across channels

6. Subscriber clients decrypt, verify Ed25519 signature, display post
   Signature proves: this post is from "CryptoNinja42"
   Certificate proves: "CryptoNinja42" is GoUNITY-verified

7. (Optional) GoLab Clearnet server receives a public-visibility copy
   via a separate signed feed for indexing on lab.simplego.dev
   Only posts marked visibility=public are sent here
   Private posts stay entirely within the SMP path
```

### 3.5 Identity Layer (Phase 2)

Identity in Phase 2 is decoupled from the GoLab server entirely:

```
1. User registers at id.simplego.dev (GoUNITY)
   Receives Ed25519 certificate + private key
   Optionally: hardware-backs the key via GoKey ESP32

2. User joins GoLab community via SimpleGoX plugin
   Sends certificate to GoBot via DM (E2E encrypted)

3. GoBot/GoKey verifies CA signature (local, offline)
   Sends challenge nonce
   User's client signs nonce with private key
   GoBot verifies signature
   Proof: user holds the key, sharing impossible

4. User is verified as "CryptoNinja42"
   Can post, create projects, moderate (based on role assigned by GoBot)
   GoLab server never knows the real identity
   GoLab server only sees the SMP queue address and the certificate fingerprint
```

This solves the account-recovery problem differently from email-based platforms: there is no email to recover. Instead:

- Software-only certificates: backup the private key file on first issue
- Hardware-backed certificates: GoKey device IS the identity. Lose the device, lose the identity. Buy a new GoKey, get a new identity. Old reputation is lost - this is by design, since portable identity would be a deanonymization vector.

### 3.6 ActivityStreams 2.0 Message Format

Phase 2 standardizes on ActivityStreams 2.0 for all message payloads. This is the same format used by Mastodon and the wider Fediverse, allowing potential future federation if desired. Standard activity types map to GoLab features:

| ActivityStreams type | GoLab feature |
|---|---|
| Create + Note | Post / Comment |
| Create + Article | Long-form post / Wiki page |
| Announce | Repost |
| Like | Reaction / Upvote |
| Follow | Subscribe to user or channel |
| Block | Ban (moderator action) |
| Remove | Delete content (moderator action) |
| Update | Edit post / Update issue |
| Add | Add member to project / Assign issue |

Example post payload:

```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Create",
  "actor": "did:key:z6Mkf5rGMoatrSj1f4CyvuHBeXJELe9RPdzo2PKGNCKVtZxP",
  "published": "2026-04-16T10:30:00Z",
  "to": ["golab:channel:gochat-dev"],
  "object": {
    "type": "Note",
    "content": "Fixed the memory leak in GoChat v1.0.1"
  },
  "proof": {
    "type": "Ed25519Signature2020",
    "proofValue": "z..."
  }
}
```

Decentralized identifiers (DIDs) of the form `did:key:...` link to public keys. The `proof` block contains the Ed25519 signature over the canonical JSON of the activity. The `to` field can be a channel address, a user, or a list - this is what enables both broadcast and direct messaging in the same format.

---

## 4. Phase 2 Security

### 4.1 Phase 2 Threat Model

Phase 2 protects against everything Phase 1 protects against, plus the four scenarios Phase 1 explicitly does not:

- A compromised GoLab server cannot read post content (E2E encrypted in SMP queues, server only sees encrypted blocks)
- Legal compulsion to disclose content fails (the operator does not have plaintext)
- Network surveillance at the GoLab server level reveals only encrypted SMP traffic (no IP-to-user mapping possible because there are no user accounts)
- A malicious operator can deny service but cannot read or modify content (Ed25519 signatures detect tampering)

What Phase 2 still does not protect against:

- A compromised user device (the device holds private keys; no software can prevent local theft)
- A global passive adversary observing all SMP traffic simultaneously (traffic confirmation attacks, mitigated by Tor/I2P transport but not eliminated)
- Social engineering of users into revealing their certificate or surrendering hardware keys
- Side-channel attacks on the SMP relay infrastructure that reveal queue subscription patterns

These limits are inherent to the architecture and are documented openly rather than hidden.

### 4.2 What the GoLab Server Knows in Phase 2

| Data | Visible to server? |
|---|---|
| Post content | No (E2E encrypted via SMP) |
| User identity | No (only queue addresses, no accounts) |
| Who posted what | No (GoBot relay mode hides authorship) |
| Who follows whom | No (pairwise SMP queues prevent correlation) |
| Channel membership list | Only queue addresses, never identities |
| Public-visibility posts | Yes (indexed on Clearnet for discovery) |
| Private-visibility posts | No (never leave the SMP path) |
| IP addresses | SMP server sees connection IPs, GoLab application server does not |

Compared to existing "encrypted" platforms:

| Property | Mastodon | Matrix | Nostr | Phase 2 GoLab |
|---|---|---|---|---|
| E2E encrypted by default | No | Optional | No | Yes |
| Server reads content | Yes | Server-encrypted | Relay reads it | No |
| Server knows your identity | Yes | Yes | Public key visible | No |
| Server maps social graph | Yes | Yes | Yes | No |
| Account-free | No | No | Public-key based | Yes |
| Hardware-verified identity | No | No | No | Optional |
| Content survives operator turn-over | No (operator reads) | No (operator reads) | Variable | Yes (encrypted) |

### 4.3 SMP Queue Properties (Inherited from SimpleX)

GoLab Phase 2 inherits the privacy properties of SimpleX SMP:

- **No user identifiers.** SMP queues are addressed by ephemeral Curve25519 public keys. There is no username, no phone number, no email, no DID exposed at the transport layer.
- **Pairwise queues.** Every conversation, every channel subscription, every follow uses its own queue pair. The relay cannot correlate one user's activity across multiple channels.
- **Per-queue ephemeral keys.** Each SMP queue uses a fresh Curve25519 keypair generated at queue creation. Compromising one queue's keys does not expose any other.
- **Double Ratchet with X448.** Forward secrecy and post-compromise security via per-message key rotation. SimpleX has integrated sntrup761 post-quantum KEM into every ratchet step; the SimpleGoX plugin is ready to exercise the full post-quantum ratchet when the send path is complete.
- **NaCl cryptobox.** Authenticated encryption for queue messages. Standard, audited primitive.

### 4.4 Certificate Identity (GoUNITY)

GoUNITY is a separate component (separate repo, separate development track) that issues Ed25519 certificates. The certificate binds a stable pseudonymous identity (e.g., "CryptoNinja42") to a public key, signed by the GoUNITY CA. Verification is local and offline - any participant can verify a certificate's chain without contacting GoUNITY.

Properties:

- **Pseudonymous.** The certificate proves "this is the same person who registered as CryptoNinja42." It does not reveal who CryptoNinja42 is in real life.
- **Persistent.** The same certificate carries the same reputation across channels and across time.
- **Revocable.** GoUNITY publishes a certificate revocation list (CRL) that GoBot consults. A compromised or banned certificate can be revoked.
- **Hardware-bindable.** The certificate's private key can live in a GoKey device (ESP32-S3 with secure element). Verification then requires the physical device to sign challenges.

The interaction between GoLab, GoBot, and GoUNITY:

```
GoUNITY: "I issue Ed25519 certificates and maintain the revocation list."
GoBot:   "I verify certificates against the CRL and route messages
          based on the certificate's permissions."
GoLab:   "I render the community UI based on what GoBot reports about
          the authenticated certificate. I never see the certificate
          private key, never see the SMP queue contents in plaintext,
          and never know the real identity behind the pseudonym."
```

### 4.5 Hardware Identity (GoKey)

GoKey is an optional ESP32-S3 device that stores the certificate private key in hardware-isolated memory. The device performs Ed25519 signatures internally - the private key never leaves the chip. Pulling the GoKey from USB makes the user's identity inactive immediately.

Properties:

- **Tamper-evident.** The ESP32-S3 secure boot ensures only GoKey firmware runs. Modifying the firmware breaks signature verification.
- **Side-channel hardened.** Constant-time Ed25519 implementation. No timing leaks, no power analysis vulnerabilities at the threat model GoLab targets.
- **Air-gapped signing.** The signing operation happens on the device, not on the host computer. A compromised host can request signatures but cannot extract the key.
- **Self-revoking.** Removing the GoKey from USB makes the identity inactive. No admin action required.

GoKey is optional. Software-only certificates (private key in a file) work for the majority of users. Hardware identity is for users who require defense against device compromise.

### 4.6 Moderation in Phase 2

Moderation in a system without accounts requires a different model. Phase 2 uses certificate-linked moderation:

- **Bans target certificates, not accounts.** A banned certificate is added to the GoBot block list. The user can request a new certificate from GoUNITY, but the new certificate has no reputation and is subject to TL0 trust restrictions.
- **Moderation is GoBot's responsibility.** GoBot enforces channel rules: rate limits, content filters (in non-encrypted-content mode), permission checks against the certificate's role. In GoKey mode, GoBot routes commands to a hardware ESP32 for execution but cannot read content.
- **Trust Levels (planned, Season 3).** Discourse-style TL0-TL4 progression: new certificates start at TL0 (rate-limited, no flagging power), earn higher TLs via time and contribution. Promotion is automatic for TL0-TL3 based on metrics, manual for TL4 (full moderator).
- **Composable labelers (long-term).** Bluesky-style per-channel labeler subscriptions. Each channel can subscribe to multiple labelers (community-run reputation services) and apply their labels (spam, low-quality, off-topic) without revealing user identities.

The combination of certificate-linked bans, Trust Levels, and composable labelers provides moderation without surveillance. The key insight: moderators do not need to know who you are, they need to know whether your certificate is in good standing.

### 4.7 Account Recovery in Phase 2

There is no email recovery in Phase 2 because there is no email. The recovery model differs by certificate type:

**Software-only certificates:**

- User backs up the private key file on first issue (the GoUNITY UI prompts for this)
- Loss of the file = loss of the identity. Reputation is lost. New certificate available from GoUNITY but starts at TL0.
- Phrase-based backup (e.g., BIP-39) is on the GoUNITY roadmap to make this less brittle.

**Hardware-backed certificates (GoKey):**

- The GoKey device IS the identity. The private key cannot be exported.
- Loss of the device = loss of the identity. Buy new GoKey, register new identity.
- Some users will operate two GoKeys with the same certificate as a hot/cold backup pair. GoUNITY supports issuing multiple devices for one identity.

This is intentionally less convenient than email recovery. The trade-off: portable identity recovery is a deanonymization vector. If the platform can recover your identity for you, so can anyone who compromises the recovery channel.

---

## 5. Phase 2 Implementation Status

| Component | Status | Required for |
|---|---|---|
| GoLab Phase 1 application server | LIVE | Foundation |
| GoLab Phase 1 community features | LIVE | User onboarding for Phase 2 migration |
| simplex-js transport | Published (v1.0.0) | Browser SMP path (deprioritized) |
| simplex-chat CLI integration | Not started | Server-side SMP adapter |
| SimpleGoX Multi-Messenger | Pre-alpha | Plugin host for write path |
| SimpleGoX Plugin spec | Not started | Defines GoLab plugin contract |
| GoBot community relay | In development | Phase 2 transport |
| GoBot Season 2-3 community features | Not started | Channel fan-out, moderation |
| GoUNITY certificate authority | Planned (Season 4) | Identity layer |
| GoKey hardware device | Planned (Season 3+) | Hardware identity (optional) |
| Tor Onion Service v3 setup | Not started | Anonymization transport |
| I2P service setup | Not started | Alternative anonymization |
| ActivityStreams 2.0 message format | Researched, not implemented | Phase 2 message payload |
| Public/private visibility flags | Not implemented | Hybrid model content split |

The realistic Phase 2 timeline spans Seasons 4-7. SMP migration is not a single sprint; it requires GoBot to mature, SimpleGoX to ship a stable plugin API, GoUNITY to issue real certificates, and GoLab to gain a server-side SMP adapter. None of those are blocked on each other completely, so they can develop in parallel, but the dependencies mean the full Phase 2 vision lights up only after all four components are production-ready.

---

## 6. Known Pitfalls and Hard-Won Lessons

These are encoded in the codebase as comments and tests but collected here for quick reference. Each lesson cost real debugging time.

### 6.1 Alpine Reactive Proxy Breaks Identity Checks

Alpine wraps every property assigned to `this` in a component with a reactive Proxy. Libraries that do `this === other` identity checks on their own instances break when stored as `this.foo` because Alpine creates distinct Proxy wrappers for the same underlying object.

**Symptom:** Quill's internal `scroll.find()` returns `null` because `n.scroll === this` fails.

**Fix:** Store library instances in closure variables, not on `this`. See `editPostModal` and `composeEditor` in `golab.js` for the pattern.

### 6.2 `x-cloak` Without CSS Rule is a Silent No-Op

The `x-cloak` attribute does literally nothing unless there is a global CSS rule `[x-cloak] { display: none !important; }`. DevTools shows the attribute on elements, but the browser ignores it.

**Fix:** The rule is at the top of `golab.css` and must stay there.

### 6.3 Alpine x-show + CSS Default Interplay

Alpine's `_x_doShow` only removes inline display, expecting the CSS default to be the visible state. `_x_doHide` sets inline `display: none`. This asymmetry means CSS default and `x-show` initial value must align, or modals stay visible when they should be hidden.

**Fix:** For full-viewport modals, use `:style="open ? 'display: flex' : 'display: none'"` instead of `x-show`. CSS default is `display: none` as defense-in-depth.

### 6.4 HTMX Swap Needs Explicit Alpine Init and Destroy

`alpine:init` fires only on initial page load. After any HTMX swap (including `hx-boost` link clicks), new DOM has `x-data` attributes but no Alpine bindings.

**Fix:** Two listeners in `golab.js`:

```js
document.body.addEventListener('htmx:beforeSwap', (e) => {
  if (window.Alpine && e.detail.target) {
    Alpine.destroyTree(e.detail.target);
  }
});
document.body.addEventListener('htmx:afterSwap', (e) => {
  if (window.Alpine && e.detail.target) {
    Alpine.initTree(e.detail.target);
    htmx.process(e.detail.target);
  }
});
```

The explicit descendant walk is necessary because Alpine 3's recursive `initTree` sometimes skips nested `x-data` nodes.

### 6.5 Quill Content Length Bridge

Quill edits do not trigger Alpine reactivity automatically. If a form control's `:disabled` depends on content length, it will be evaluated once at bind time and never update.

**Fix:** Subscribe to Quill's `text-change` event in `_mountQuill` and write to an Alpine-observed property:

```js
quill.on('text-change', () => {
  self.contentLen = quill.getText().trim().length;
});
```

### 6.6 Post-Submit Does Not Need a Reload

The WebSocket hub echoes every new post back to its author's connection. The client's `injectNewPost` handler prepends the card with animation. A `window.location.reload()` on submit success is redundant and causes visible flash, lost scroll position, and full Alpine re-init.

**Fix:** Remove the reload. Trust the WebSocket echo.

### 6.7 deploy.sh Wipes docker-compose.yml

`git reset --hard` in `deploy.sh` overwrites secret values in `docker-compose.yml`. The current workaround is a manual `sed` block after each deploy. The clean fix is `.env.production` outside Git.

### 6.8 WebSocket Echo Can Arrive Before HTTP Response

The hub broadcasts a no-author-context post card render to all subscribers including the author. The author's HTTP POST response carries an author-context render with edit/delete buttons. The WebSocket frame often arrives first and the client inserts the anonymous version before the HTTP response can deliver the privileged version.

**Fix:** The author's submit `.then` callback explicitly removes any existing card with the same ID before inserting the author-context version. WebSocket-driven `injectNewPost` stays as "insert if not already there".

### 6.9 Empty Content Check Must Be Semantic, Not Byte-Length

Quill's empty editor produces `<p><br></p>` (11 bytes, passes `len(content) > 0`). A scripted attack against `/api/posts` bypasses both client and server byte-length checks.

**Fix:** `IsSemanticallyEmpty` runs `bluemonday.StrictPolicy` then strips NBSP/zero-width/BOM. 14 unit tests lock the behavior.

### 6.10 Editable Post Response Must Include Edit Metadata Consistently

When a field like `EditedAt` is introduced, it must appear in every read path (feed, single-post GET, POST response, PATCH response, WebSocket card render). Sprint 15a missed `GET /api/posts/:id`, so API consumers got inconsistent payloads.

**Pattern:** When adding a post-level field, grep every handler that returns a post and verify each path populates it.

### 6.11 Goose Statement Boundary for DO-Blocks

Goose splits SQL on top-level semicolons by default. PostgreSQL DO-blocks contain semicolons inside dollar-quoted bodies. Without explicit markers, goose splits inside the dollar-quote and Postgres reports an unterminated dollar-quote error.

**Fix:** Wrap DO-blocks in `-- +goose StatementBegin` / `-- +goose StatementEnd` markers. Goose ships everything between as one statement.

**Critical second lesson (Sprint Y.1.1):** the goose parser is greedy on any `+goose` mention. Even an explanatory comment like `-- The +goose StatementBegin / StatementEnd markers tell goose to ship...` is parsed as an annotation and breaks the migration. Never write `+goose` in any comment text outside the actual markers.

### 6.12 First-User Auto-Promote Needs Handler-Level Fallback

Migration 012's blind `UPDATE users SET power_level = 100 WHERE id = 1` runs once at migration time. On a fresh database with no users, it hits zero rows, and the first registrant ends up at power_level 10 with no admin.

**Fix:** Two-layer mechanism. UserStore checks `COUNT(*) FROM users` on registration; if zero, the new user is created with power_level 100. Handler-level fallback in `RegisterHandler` does the same check after creation as defense-in-depth.

### 6.13 VPS Deploys Lose Secrets

Every `./deploy.sh` invocation runs `git reset --hard origin/main`, which wipes production secrets from `docker-compose.yml`. The post-deploy secret-restore block (sed commands re-injecting GOLAB_DB_PASSWORD, POSTGRES_PASSWORD, GOLAB_SECRET, GOLAB_BACKUP_KEY, GOLAB_ENV=production) is mandatory after every deploy until the `.env.production` refactor lands.

### 6.14 Direction-Aware Step Transitions Need State Set Before Mutation

Wizard step transitions read `direction` from a data attribute on the parent. The CSS selector matches `[data-direction="forward"]` vs `[data-direction="backward"]` to determine slide direction. If `step++` happens before `direction = "forward"`, x-transition reads the stale value.

**Fix:** Always set direction before mutating step. `next()` sets direction first, then increments. `back()` sets direction backward, then decrements.

### 6.15 animation-fill-mode: backwards is Critical for Cascade

Staggered cascade animations with delay (60ms, 220ms, 380ms, etc.) need `backwards` fill mode. Without it, elements are visible during their delay period (CSS default behavior), then animate from visible to visible (no effect). With `backwards`, elements are invisible during the delay then animate from `from` to `to`.

### 6.16 Content-Driven Height Needs Zero min-height on Parents

Setting `min-height: 100dvh` on any wizard parent forces the container to viewport height. Short content then has huge empty space below. The fix is removing all `min-height` declarations from `.wiz-root`, `.wiz-layout`, and `.wiz-main`. The grid `align-items: stretch` keeps sidebar and main column at matched heights based on content.

---

## 7. Component Map (Phase 1 Code Layout)

This maps top-level concerns to files for fast navigation.

### Backend

| Concern | Location |
|---|---|
| HTTP entrypoint, router setup | `cmd/golab/main.go` |
| Route groups, middleware composition | `internal/handler/routes.go` |
| Post CRUD + edit history | `internal/handler/post.go`, `internal/model/post.go`, `internal/model/post_edit_history.go` |
| Feed queries | `internal/handler/feed.go`, `internal/model/feed.go` |
| User auth, sessions, registration | `internal/handler/auth.go`, `internal/model/user.go` |
| Application ratings | `internal/handler/admin.go`, `internal/model/application_rating.go` |
| Reactions | `internal/handler/reactions.go`, `internal/model/reaction.go` |
| Mentions | `internal/handler/users.go`, `internal/model/mention.go`, `internal/render/mentions.go` |
| Admin panel | `internal/handler/admin.go` |
| WebSocket hub | `internal/handler/ws.go` |
| Rendering helpers | `internal/render/` |
| Database connection, migrations | `internal/database/db.go`, `internal/database/migrations/` |
| Configuration | `internal/config/config.go` |

### Frontend

| Concern | Location |
|---|---|
| Page layout, nav, shared modals | `web/templates/base.html` |
| Page templates | `web/templates/*.html` |
| Partials | `web/templates/partials/` |
| All client JS | `web/static/js/golab.js` |
| Quill mention plugin | `web/static/js/quill-mention.js` |
| Quill | `web/static/js/quill.min.js` |
| Alpine.js | `web/static/js/alpine.min.js` |
| HTMX | `web/static/js/htmx.min.js` |
| All styles | `web/static/css/golab.css` |

Single-file `golab.js` and `golab.css` is intentional. The frontend is ~3000 lines of JS and ~2500 lines of CSS - small enough to read as one file, no build pipeline. If either grows past ~5000 lines, we revisit.

---

## 8. Roadmap Markers

Near-term directions that provide context for current design choices. Details live in season-specific documents.

**Sprint 16 - Project System (Season 3 priority).** Add a Project layer between Spaces and Posts. New hierarchy: Spaces → Projects → Seasons → Posts. Each project has docs (Concept, Architecture, Workflow, Roadmap as Markdown), sequential Seasons with closing documents, member roles (owner, contributor, viewer). Posts gain a `season_id` field, NULL for free Space posts.

**Sprint 17 - Security Block.** Argon2id migration, CSP headers with Alpine CSP build, upload encryption at rest (AES-256-GCM with per-file IV), session timeout enforcement (idle + absolute), CSRF token validation beyond SameSite, forgot password flow.

**Sprint 18 - Polish.** Application ratings UI for filtering approved users, Trust Levels TL0-TL4, knowledge questions V2 (dynamic rotation), re-application cooldown enforcement, two-factor authentication (TOTP + backup codes).

**Phase 2 Preparation (Sprint 19+).** SimpleGoX Plugin specification, GoLab protocol design over SMP transport, simplex-chat CLI integration prototype, bot service skeleton, Tor Onion Service v3 setup, I2P service setup, public/private content visibility flags, ActivityStreams 2.0 implementation.

---

## 9. Who to Ask

- **Sascha (Der Prinz)** - Project director, all strategic and architectural decisions, VPS administration, PR merges. Final authority on every change.
- **Claude (Prinzessin Mausi)** - Planning, briefings, documentation, knowledge base maintenance. Never writes code.
- **Claude Code (Der Ritter / Zauberer)** - Implementation on feature branches. All code changes go through PR review by Sascha.
- **Micky Maus on ACID** - Frontend designer for special UX work. Brought in for high-impact visual sprints like the brutalist wizard redesign.

No direct pushes to `main`. No auto-merges. No rollback migrations. No reloads where a WebSocket echo does the job.

---

## 10. License

GoLab is licensed under AGPL-3.0-or-later.

The AGPL applies to the GoLab codebase itself. Phase 2 dependencies (simplex-js, GoBot, GoUNITY, GoKey firmware) carry their own licenses, all permissive or AGPL-compatible.

Self-hosted deployments must respect AGPL-3.0 obligations: the source code modifications must be available to users of the deployed service.

---

*This document should be updated whenever a significant architectural decision changes. Small file additions and bug fixes do not require updating this file. New patterns, new layers, or changes to the deployment model do.*

*GoLab Architecture and Security - April 2026 - IT and More Systems*
