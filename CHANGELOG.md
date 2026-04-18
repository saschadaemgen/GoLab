# Changelog

All notable changes to GoLab are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Planned

- Argon2id password hashing migration (Phase 1, 0.2.x)
- Upload encryption at rest (AES-256-GCM)
- Email verification on registration
- 2FA via TOTP
- Account export (GDPR Art. 20)
- Phase 2: SMP transport layer migration (simplex-js + GoBot relay)
- Phase 2: GoUNITY certificate-based identity
- Phase 2: Server becomes a blind relay (no plaintext visibility)

## [0.1.0] - 2026-04-18

### Season 1: From concept to live platform

GoLab went from a README.md in January to a live production platform
at [lab.simplego.dev](https://lab.simplego.dev) by April. This release
captures Season 1 in its entirety: 13 sprints, 22 database migrations,
and a working privacy-first developer community.

First user registered on the live instance on day one of Sprint 8.
By Sprint 13 the platform had real users posting real content.

### Added

#### Sprint 1-5: Backend foundation

- Go 1.24 application server with chi router and graceful shutdown
- PostgreSQL 16 persistence via pgx/v5 connection pool
- Database migrations via pressly/goose v3
- User registration and login with bcrypt (cost 12)
- Cookie-based session management with 30-day expiry
- Channel system (create, join, leave, list)
- Post system (create, delete, feed, list by channel, list by author)
- Threaded replies via `parent_id` self-reference
- Reaction system with per-user per-post uniqueness
- Follow / unfollow with counts and `IsFollowing` check
- Feed query combining followed users and joined channels
- Public / private / invite-only channel types
- Config via environment variables (`config.Load()`)
- REST API at `/api/*` with JSON request / response
- Health check endpoint at `/api/health`

#### Sprint 6: Frontend

- Server-side rendering with Go `html/template` and a custom engine
- Base layout with slot-based content blocks
- HTMX for page transitions (`hx-boost="true"`) and partial updates
- Alpine.js for interactive components (dropdowns, compose, forms)
- WebSocket hub (`coder/websocket`) for real-time fan-out
- Per-user subscription model with `PublishToUser` routing
- SimpleGo Design System with dark (cyan #45BDD1) and light
  (green #1A7D5A) themes
- Canvas particle background (self-contained, no library)
- Toast notification component via custom `notify` event
- Responsive navbar with search, notifications, avatar dropdown
- Mobile hamburger menu (initial version, later redesigned)

#### Sprint 7: Community features

- Markdown rendering via goldmark with Chroma syntax highlighting
- Avatar upload endpoint with 256x256 resize (JPEG q85)
- User profile pages at `/u/:username` with recent posts
- Profile edit form at `/settings` with display name, bio, avatar
- Real-time notifications for reactions, replies, follows
- Notification bell with unread count
- Thread view at `/p/:id` with full reply tree
- PostgreSQL full-text search via `tsvector` + GIN index
- Global search at `/api/search` with live typeahead
- Admin dashboard at `/admin` with stats (users, posts, channels)
- Admin user management (ban, unban, set power level)
- Admin channel list with member and post counts

#### Sprint 8: Rich editor and security hardening

- Quill.js 2.0.3 WYSIWYG editor (self-hosted, no CDN)
- Image upload in posts with 1200px resize, JPEG q85
- HTML sanitization via bluemonday UGCPolicy
- goldmark `html.WithUnsafe()` explicitly disabled
- Rate limiting via go-chi/httprate:
  - Registration: 5/hour per IP
  - Login: 10/minute per IP
  - Post creation: 30/minute per user
  - Avatar upload: 10/hour per user
  - Image upload: 10/hour per user
- Security headers middleware: CSP, XFO, XCTO, Referrer-Policy,
  Permissions-Policy
- Session cookie `__Host-` prefix in production (HTTPS-only,
  Path=/, no Domain attribute)
- Admin power level management with server-enforced rules
- Image lightbox with backdrop blur on click
- highlight.js 11.9.0 for code block rendering (self-hosted)
- Production deploy script with automatic `pg_dump` backup

#### Sprint 9: Security hardening

- All auth forms converted from GET to POST (no credentials in
  URL bar or browser history)
- Password validation per NIST SP 800-63B: 8-128 chars, length
  only, no composition rules
- POST-Redirect-GET pattern for all auth flows
- `wantsFormResponse`, `redirectOrJSON`, `errorRedirectOrJSON`
  helpers so a single handler serves both JSON and HTML clients
- Avatar display in post cards and profile headers
- `initial` template function for avatar letter fallback

#### Sprint 10: Content organization

- 8 thematic Spaces seeded via migration 018 (later 020):
  SimpleX Protocol, Matrix / Element, Cybersecurity, Privacy,
  Hardware, SimpleGo Ecosystem, Dev Tools, Off-Topic / Meta
- Tag system with autocomplete and 20+ seeded tags
- Post Types: Discussion, Question, Tutorial, Code, Showcase, Link
- Space bar navigation rendered on every page via `base.html`
- Space pages at `/s/:slug` with post type filter tabs
- Tag pages at `/t/:slug` for cross-Space discovery
- Compose box with space / type / tag dropdowns
- Post badges showing Space, type, and tags
- `SpaceStore`, `TagStore` with list + lookup queries

#### Sprint 10.5: Community restructure and polish

- Channels hidden from the primary UI (Spaces replace them as the
  user-facing organization)
- Single dropdown compose box instead of separate editors per page
- LC-Emoji-Picker integrated into Quill toolbar (self-hosted)
- Custom quick-picker grid for frequently used emoji
- Docker named volume `golab_uploads` for persistent user uploads
  (previously lost on every container rebuild)
- WebSocket reconnect logic with exponential backoff (1s -> 30s)
  and single-socket handle on `window.__golabWS`
- `beforeunload` handler clears reconnect timer to prevent
  post-navigation socket spawn
- Reaction toggle via UPSERT: same type = DELETE, different type
  = UPDATE, no row = INSERT
- Fix: sidebar Spaces bug where `{{ template "sidebar.html" . }}`
  inside `{{ with .Content }}` bound `$.Spaces` to `feedContent`
  instead of `PageData`. `feedContent` now carries Spaces +
  CurrentSpace explicitly.

#### Sprint 11: Design system refactor

- 5 CSS design tokens: `--content-max`, `--space-N`, `--font-N`,
  themed vars, motion tokens
- Responsive layout primitives: `.page-container`, `.two-col`,
  `.single-col`, `.card`
- Typography scale based on CSS custom properties
- Spacing scale (space-1 through space-8) for consistent rhythm
- Responsive breakpoints 375px / 768px / 1024px / 1440px
- Mobile space bar converted to native `<select>` dropdown
  (replaces horizontal scroll)
- Press-effect on interactive elements (cubic-bezier ease)
- GPU-accelerated animations (transform / opacity only)
- `hero-page` class to opt out of global navbar padding
- JS-measured navbar heights written to CSS variables to prevent
  content-under-navbar on mobile

#### Sprint 12: Moderation and mobile menu

- Fullscreen mobile menu: dark / cyan monochrome palette,
  `position: fixed; inset: 0; z-index: 10500`, `100dvh`,
  staggered slide-in animations
- User moderation statuses: `active` / `pending` / `rejected`
- New-user approval queue on /admin
- Admin approve / reject actions with notification fan-out
- `require_approval` platform setting (Owner toggle, default off)
- First user (id=1) always bootstraps as `active` + `power=100`
  regardless of setting, so the platform is never DOA
- `RequireActiveUser` middleware blocks mutations for pending /
  rejected users while letting reads through
- `/pending` page for users caught in the approval queue
- Admin notification fan-out on new-user registration
- Rejected users get logged out on next session touch

#### Sprint 13: Account security

- Password change from /settings (current / new / confirm fields)
- `POST /api/users/me/password` endpoint with bcrypt verify,
  length validation (8-128), and full session revocation
- Rate limit 3 attempts per hour per user on password change
- All sessions revoked on password change (including the caller's)
- Redirect to `/login?msg=password-changed` with a success flash
- Client-side confirm-password match check (Alpine `passwordForm`)
- Username change from /settings (case-insensitive unique check,
  3-32 chars, `^[a-zA-Z0-9_]+$`)
- Live availability check via `GET /api/users/check-username`
  (debounced 500ms, 60/min rate limit)
- Alpine `usernameEditor` component with status indicator
  (available / taken / invalid / checking)
- `allow_username_change` platform setting (Owner toggle, default on)
- Admin rename via `PUT /api/admin/users/:id/username` (bypasses
  the platform toggle, enforced power-level hierarchy)
- "Rename" button in admin user table (only shown when the admin
  outranks the target)
- `allow_username_change` toggle added to admin dashboard

### Infrastructure

- Docker Compose deployment (Go 1.24-alpine build stage, alpine:3.21
  runtime stage)
- Nginx reverse proxy with HTTP/2 and Let's Encrypt TLS
- `certbot certonly` for cert renewal (never `certbot --nginx`,
  which breaks the custom server block)
- deploy.sh: backup + pull + build + health check
- `/opt/backups/golab-YYYYMMDD-HHMMSS.sql` retained per deploy
- `web/` directory explicitly copied into runtime container
  (`COPY --from=builder /app/web ./web`)
- Docker `--no-cache` used when static asset changes don't appear
- Migrations 001 through 022, all idempotent, all with
  `SELECT 1;` DOWN sections (no destructive rollback possible)

### Security

- bcrypt password hashing at cost 12
- NIST SP 800-63B password policy (length-only, 8-128 chars)
- Rate limiting on registration, login, post, password change,
  avatar upload, image upload, username availability check
- Bluemonday UGCPolicy sanitizer on all user HTML
- goldmark unsafe mode disabled (no raw HTML through Markdown)
- Security headers on every response (CSP, XFO, XCTO,
  Referrer-Policy, Permissions-Policy)
- Session cookies with `HttpOnly`, `Secure`, `SameSite=Lax`,
  `__Host-` prefix in production
- Input validation: username regex, email regex, password length
- POST-only forms (no credentials in URLs or browser history)
- Admin endpoints gated by `RequireAdmin` middleware (power >= 75)
- Owner-only operations gated inside handlers (`actor.ID == 1`)
- Power-level protection rules server-enforced:
  - Cannot change own power level
  - Cannot assign higher than own level
  - Only id=1 can promote to Owner
  - Admins cannot rename users at or above their level
- Identical error messages for "no such email" and "wrong
  password" to prevent account enumeration
- `DeleteAllForUser` on password change, ban, and reject revokes
  every device's session immediately
- Image upload re-encodes bytes through `golang.org/x/image`
  (original never touches the filesystem as-is)

### Fixed

- Avatar upload double-dialog bug (wrapped input in `<label>` so
  native semantics fire picker exactly once)
- Avatar upload race between FileReader preview and server URL
  (added `serverResolved` flag)
- WebSocket connection leak (30+ sockets per user across HTMX
  page swaps) fixed with single `window.__golabWS` handle and
  close-before-open discipline
- Navbar overlap on mobile (body padding-top + hero-page class
  + JS-measured heights)
- Alpine re-initialization after HTMX `hx-boost` page swap
  (guard via `dataset.bound` and `__quillMounted`)
- Script load order: `golab.js` now loads before `alpine.min.js`
  so `alpine:init` listener is registered before Alpine fires
- Sidebar `$.Spaces` binding bug inside `{{ with .Content }}`
- Forms leaking GET data into URL bar (all converted to POST
  with `redirectOrJSON` fallback)
- `certbot --nginx` overwriting custom Nginx config (procedure
  changed to `certonly` + manual reload)

### Changed

- Initial Sprint 10 rollout seeded 5 operational Spaces; Sprint
  10.5 reverted to 8 thematic Spaces after user feedback
- Channels no longer surfaced in top-level navigation (Spaces
  replaced them); `channels` table retained for Phase 2
- Mobile space bar converted from horizontal scroll to native
  `<select>` dropdown after user feedback ("ZUM LETZTEN MAL")

### Removed

- `make migrate-down` target (removed intentionally; live DB
  must not be rolled back)
- All GET-based auth forms (replaced with POST equivalents in
  Sprint 9)

### Known issues (deferred to future sprints)

- No email verification on registration
- No password reset flow (admin must reset manually)
- No 2FA
- bcrypt instead of argon2id (migration planned)
- No account export (GDPR Art. 20 compliance TBD)
- Uploads stored unencrypted in the `golab_uploads` volume

---

*GoLab CHANGELOG v1 - April 2026*
*IT and More Systems, Recklinghausen, Germany*
