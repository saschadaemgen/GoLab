# Changelog

All notable changes to GoLab are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Planned for Season 3

- Sprint 16: Project layer between Spaces and Posts
  (Spaces > Projects > Seasons > Posts hierarchy)
- Sprint 17: Argon2id password hashing migration
- Sprint 17: CSP headers with Alpine CSP build
- Sprint 17: Upload encryption at rest (AES-256-GCM with per-file IV)
- Sprint 17: Session timeout enforcement (30min idle, 7d absolute)
- Sprint 17: CSRF token validation beyond SameSite cookies
- Sprint 17: Forgot password flow (passwordless via SimpleGoX considered)
- Sprint 18: Trust Levels TL0-TL4 (Discourse-style)
- Sprint 18: Knowledge questions V2 (dynamic rotation)
- Sprint 18: Re-application cooldown (30-day enforcement)
- Sprint 18: Two-factor authentication (TOTP + backup codes)
- Sprint 18: Application Ratings UI for filtering approved users

### Phase 2 preparation (Season 4+)

- SimpleGoX Plugin specification document
- GoLab protocol design over SMP transport
- simplex-chat CLI integration prototype
- Bot service skeleton in Go
- Tor Onion Service v3 setup
- I2P service setup
- Public/Private content visibility flags
- ActivityStreams 2.0 implementation

## [0.2.0] - 2026-04-27

### Season 2: From open community to curated platform

Season 2 transformed GoLab from an open registration platform into
a curated developer community with application-based write access.
Major work clusters: rich code-tooling features, comprehensive
security audit and hardening via Ultrareview, complete registration
overhaul with brutalist UX, and a full strategic exploration of SMP
migration without committing to implementation.

The platform stayed live throughout. All 18+ pull requests were
deployed to production on lab.simplego.dev. Total: 15 sub-sprints,
7 new database migrations, 1370+ lines of frontend changes.

### Added

#### Sprint 13: Account management

- Password change in Settings with old-password verification
- Session invalidation across all devices on password change
- Username change with live availability check (debounced 500ms)
- Admin can change usernames with audit trail (bypasses platform toggle)
- Settings UI redesign with collapsible sections
- `allow_username_change` platform setting (Owner toggle, default on)
- Power-level protection: admins cannot rename users at or above their level

#### Sprint 14: Code features block

- Mermaid diagram support in posts (server-rendered SVG, no client dependency)
- KaTeX math rendering inline and block
- Liquid Tags syntax for embedded content
- Code snippet permalinks at `/s/:id` with line range support `#L10-L20`
- Diff viewer for code comparisons
- Code block copy-to-clipboard buttons

#### Sprint 14 (preceding): Multi-reactions and @mentions

- GitHub-style multi-emoji reactions (heart, thumbsup, laugh, surprised, sad, fire)
- Persistent chip bar with counts and per-user highlight on every post
- `ReactionStore.StateFor` + `StateForMany` for O(2) batched reaction state
- `ReactionStore.AttachTo` populates reaction data on post slices
- Two ranking indexes on reactions for future ranking sprint
- `@mention` system: `mentions` join table, `MentionStore`,
  `ExtractUsernames`, `RecordMentions`, `SyncMentions`, `ForUser`
- Server-side HTML post-processing via `render.LinkMentions`
- `NotifMention` fan-out on post create (self-mentions excluded)
- `GET /api/users/autocomplete?q=` endpoint (60/min rate limit)
- `quill-mention.js` vendored Quill module (no deps, ~270 lines)
- Test coverage: ExtractUsernames (9), LinkMentions (6), reaction validation

#### Sprint 15: Edit/Delete and threading polish

- Posts editable for 30 minutes after creation
- Edit history visible to admins via `post_edit_history` table
- Soft delete with author/admin recovery window
- Reply thread improvements (collapse, jump-to-parent)
- Post permalinks with anchor scroll

#### Sprint 15a: Real-time edit propagation + bug hunt

- WebSocket broadcasts post edits live to all viewers
- "edited" badge with hover tooltip showing timestamp
- Six bug fixes in Sprint 15a.5 series:
  - Alpine Reactive Proxy breaking Quill identity (closure pattern)
  - x-cloak silent no-op (added CSS rule `[x-cloak] { display: none !important; }`)
  - Quill text-change reactivity bridge for Alpine
  - Save-only-on-changes baseline-after-seed dirty check
  - Modal CSS default `display: none` with template-driven reveal
  - HTMX hx-boost re-init via `htmx:beforeSwap`/`afterSwap` glue
  - Removed redundant `window.location.reload()` after post submit

#### Sprint 15a B7: Ultrareview Round 1

- 5 production bugs uncovered by cloud-based multi-agent review:
  - Race-proof dual-render fix for post-submit author context
    (WebSocket echo arrives before HTTP response)
  - Edit modal Markdown corruption fix (seed with `content_html`)
  - WebSocket edit broadcast via new `PublishPostUpdated`
  - PATCH endpoint rate-limited at 30/min
  - Defensive `postId` capture in edit modal closure

#### Sprint 15a B8: Ultrareview nits cleanup

- Symmetric error logging in `pages.go`
- `post_edit_history` NULL handling via pointer scan
- `IsSemanticallyEmpty` helper with NBSP/zero-width/BOM stripping
- `GET /api/posts/{id}` now includes `edited_at` consistently
- Shared `attachEditedBadge` helper with tooltip
- Edited-badge rendering matches template placement

#### Sprint X: Application-based registration core

- Migration 026 drops `users.email` column (with explicit Prinz authorization)
- Five application fields added to users table:
  - `external_links` (optional)
  - `ecosystem_connection` (required, 30-800 chars)
  - `community_contribution` (required, 30-600 chars)
  - `current_focus` (optional, 0-400 chars)
  - `application_notes` (optional, 0-300 chars)
- Login switched from email lookup to username lookup
- Application content stripped from public profiles even on self-view
- Server-side URL validation for HTTPS-only external links
- Force `require_approval=true` setting in Migration 026

#### Sprint X.1: Application form UX

- External links optional (was required, removed by user feedback)
- Form preservation on validation errors via Alpine + fetch
- Live character counters with neutral/near-limit/over-limit states
- Server returns structured field errors for inline display
- 33 validation test cases for `validateApplication`
- 17 test cases for `hasValidHTTPSURL`

#### Sprint X.2: Critical bug fixes + first-user auto-promote

- Alpine `registerForm` reference fix (template syntax: `name` vs `name()`)
- Counter color from `--text-muted` to `--accent` for visibility
- Submit button text rendering after Alpine fix
- Two-layer first-user auto-promote (UserStore + Handler)
- First user auto-approved as admin (require_approval bypass)
- Integration test scaffolding with build tag

#### Sprint Y: Application ratings system

- Migration 027 with `application_ratings` table
- 5-dimensional star rating widget for admin review:
  - `track_record` (1-10)
  - `ecosystem_fit` (1-10)
  - `contribution_potential` (1-10)
  - `relevance` (1-10)
  - `communication` (1-10)
- Optimistic UI update with rollback on failure
- Toggle-off pattern for clearing ratings
- `ApplicationRatingStore.AttachTo` bulk-load (no N+1 queries)
- SQL injection hardening with double allow-list (column whitelist)
- 8 ApplicationRating average subtests
- 20 admin rating handler subtests

#### Sprint Y.1: Knowledge questions

- Three new questions added to application:
  - Technical depth (a/b/c choice + 100-500 char answer, required)
  - Practical experience (optional, 0-400 chars)
  - Critical thinking (optional, 0-400 chars)
- Migration 028 with CHECK constraint on `technical_depth_choice IN ('', 'a', 'b', 'c')`
- Knowledge answers shown to admin separately from numerical ratings
- "No answer (optional)" muted display for skipped fields
- Editorial-decision design (knowledge filter, not numerical score)

#### Sprint Y.1.1: Goose statement boundary fix

- Migration 028 DO-block split by goose default semicolon parser
- Fixed with `-- +goose StatementBegin` / `-- +goose StatementEnd` markers
- Documented goose internal-semicolon behavior

#### Sprint Y.2: Cinematic wizard (replaced by Y.3)

- First wizard implementation with 5 steps
- Direction-aware step transitions
- Form preservation across steps
- Replaced entirely by Y.3 brutalist redesign after Prinz feedback

#### Sprint Y.3: Brutalist wizard redesign

- Complete redesign as 11-step wizard with full-screen layout
- 280px cyan sidebar with full step list and contextual quote per step
- Dark right column (#0a0a0a) with question content
- Monospace technical accents (step badges "STEP 04 · ECOSYSTEM CONNECTION")
- One question per step, no scrolling within steps
- Keyboard navigation: Enter advances, Esc goes back, Tab navigates form
- Visual hints update contextually ("↵ Press Enter to continue")
- Skip button only visible on optional steps (4 optional, 4 required)
- Direction-aware slide transitions (forward and backward mirrored)
- `.wiz-` namespace fully isolated from existing `.btn-*`/`.form-*` styles
- Y.2 wizard CSS (425 lines) replaced with 688 brutalist lines

#### Sprint Y.4: Polish v1 - live username availability + personalization

- Live username availability check via `/api/auth/username-available`
- 7 status states: idle/checking/available/taken/reserved/invalid/error
- 17-entry `reservedUsernames` blocklist enforced server-side:
  admin, root, system, moderator, mod, support, help, api, www, mail,
  info, golab, simplego, anonymous, null, undefined, test
- 30/min/IP rate limit on availability endpoint
- 400ms debounce with stale-response guard
- Server-side check in both `CheckUsernameAvailable` and `Register` handlers
- @username personalization on 4 strategic wizard steps:
  - Step 3 headline: "Tell us, @username, how you connect to SimpleGo"
  - Step 4 helper: "What perspective would @username bring..."
  - Step 7 helper: "@username, pick ONE sub-question..."
  - Step 10 headline: "Looking good, @username. Look it over."
- Contextual sidebar quotes per step (7 of 11 steps)
- 38 username availability test cases (26 reserved, 7 invalid, 5 patterns)

#### Sprint Y.5: Cinematic stagger + content-driven height

- Cascade animations on step content with timing 60/220/380/540/700ms:
  - Step badge: 60ms delay, 400ms duration
  - Step headline: 220ms delay, 500ms duration
  - Step helper: 380ms delay, 500ms duration
  - Step input area: 540ms delay, 500ms duration
  - Step meta row: 700ms delay, 400ms duration
- Template `<template x-if>` pattern triggers fresh DOM mount per step
- CSS animations fire on each step entry (re-trigger via remount)
- `animation-fill-mode: backwards` on all cascades (critical fix)
- Removed all `min-height: 100dvh` from wizard layout (4 declarations)
- Sidebar quote pinned with `margin-top: auto` in flex column
- Wizard total height now follows content (364px on 1243px viewport)
- `align-items: stretch` keeps sidebar matching right column height

### Changed

- Application content (5 about-you fields + 4 knowledge fields) stripped
  from public profile responses even when user views own profile
- Login form switched from email field to username field after Migration 026
- `require_approval` setting forced to `true` in Migration 026 (was Owner toggle)
- First user auto-promote no longer relies solely on Migration 012's
  blind UPDATE (which fails on empty DB) - now two-layer with handler fallback
- Wizard CSS namespace `.wiz-*` replaces previous `.wizard-*` (Y.2 to Y.3)

### Fixed

- Goose parser greedy on `+goose` mention in any comment text
  (not just real markers) - documented in code comments
- VPS deploy `git reset --hard` wipes docker-compose.yml secrets
  (mandatory secret-restore block via sed after each deploy)
- Browser cache hides bugs after frontend changes
  (always test in incognito after wizard changes)
- Alpine.data() registered components use `x-data="name"` (no parens)
  vs global functions `x-data="name()"` (with parens) - templates
  silently get this wrong
- Direction-aware step transitions need direction set BEFORE step mutation
  so x-transition CSS sees correct data attribute
- `animation-fill-mode: backwards` is critical for staggered cascades
  (without it, delayed elements flash visible during their delay period)
- Content-driven height needs zero `min-height: 100vh/dvh` on parent
  containers (any one forces stretching)
- WebSocket echo can arrive before HTTP response on post submit
  (author-context render must explicitly remove and replace anonymous version)
- `IsSemanticallyEmpty` now strips NBSP, zero-width, and BOM characters
  before checking emptiness (Quill's `<p><br></p>` is 11 bytes but empty)
- `EditedAt` field now appears in every post read path consistently
  (feed, single GET, POST response, PATCH response, WebSocket render)

### Migrations

- `025_post_edit_history.sql` - Sprint 15: posts.edited_at, posts.edit_count,
  post_edit_history table for admin-visible edit log. DOWN no-op.
- `026_email_removal.sql` - Sprint X: drops users.email column with explicit
  authorization, adds 5 application fields, forces require_approval=true.
  Email is permanently gone. DOWN no-op.
- `027_application_ratings.sql` - Sprint Y: application_ratings table with
  user_id PK, 5 nullable INT dimensions (track_record, ecosystem_fit,
  contribution_potential, relevance, communication), reviewer_id, reviewed_at,
  notes, updated_at. DOWN no-op.
- `028_knowledge_questions.sql` - Sprint Y.1: 4 knowledge fields on users
  (technical_depth_choice, technical_depth_answer, practical_experience,
  critical_thinking), CHECK constraint on choice values, wrapped DO-block
  in goose StatementBegin/End markers. DOWN no-op.

### Infrastructure

- GOLAB_ENV moved to "production" on VPS
- Production secrets restored via secret-restore block after each deploy:
  GOLAB_DB_PASSWORD, POSTGRES_PASSWORD, GOLAB_SECRET, GOLAB_BACKUP_KEY
- Email column permanently dropped from production database
- Mermaid 10.x added (server-rendered SVG, no client dependency)
- KaTeX added (client-side, ~280KB but worth it for math)
- Diff library for code comparisons
- Ultrareview CLI tool integrated for cloud-based code review
  (2 free runs used: B7 implemented, B9 deferred)
- Browser preview tool used extensively for design verification

### Strategic discussions (documented, not implemented)

- SMP migration architecture: hybrid Clearnet+SimpleGoX model adopted
- Browser-native SMP confirmed not feasible:
  - WebSocket lacks TLS channel binding
  - WebCrypto missing Curve448/Ed448
  - No Go SDK for SMP
- SimpleGoX plugin identified as proper integration point for write path
- Tor Onion Service v3 + I2P planned for Phase 3
- Identity layer design (Ed25519 + GoUNITY certs + GoKey hardware) discussed
- Account recovery without email reframed around hardware tokens

### Removed

- `users.email` column (Migration 026, deliberate, no recovery)
- All email-related code paths (registration, login, password reset)
- Sprint Y.2 wizard CSS (replaced by Sprint Y.3 brutalist redesign)

### Known issues (deferred to Season 3)

- Argon2id password hashing migration (still bcrypt cost 12)
- Upload encryption at rest (AES-256-GCM)
- CSP headers with Alpine CSP build
- Forgot password / password reset flow (no email infrastructure)
- Two-factor authentication
- Email notifications (no SMTP intentional)
- Automated backups (manual via deploy.sh)
- No monitoring or alerting
- No CI/CD pipeline
- docker-compose.yml still has deprecated "version" key
- secret-restore block still required after every deploy
- Spaces are flat (no Project containers yet - Sprint 16 priority)
- application_ratings has no UI for filtering/sorting by score
- Knowledge questions are static V1 (V2 dynamic rotation planned)
- Wizard state not persisted (no save-as-draft)
- No re-application cooldown enforcement (only stated in copy)

### Metrics at Season 2 close

- Users: 4+ registered (steady from Season 1, curation working)
- Posts: 30+ (grew from Season 1)
- Spaces: 8 thematic (unchanged)
- Tags: 20+ user-extended
- Application fields: 9 total (5 about-you + 4 knowledge)
- Migrations: 28 total (021 + 7 new in Season 2)
- Database tables: 14 (added post_edit_history, application_ratings)
- Sprint count: 15 sub-sprints in Season 2
- Pull requests merged: 18+
- NPM dependencies: 0 (still zero, principle holds)
- Binary size: ~28 MB (grew with code features)

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
- Bluemonday HTML sanitization on all user content
- Rate limiting (registration, login, post, password change, upload)
- Security headers (X-Content-Type-Options, X-Frame-Options,
  Referrer-Policy, Permissions-Policy)
- `__Host-` cookie prefix in production
- Admin power level dropdown with server-enforced protection rules
- Image lightbox with backdrop blur effect
- highlight.js for client-side code block fallback

#### Sprint 9: Security hardening

- All forms converted from GET to POST (no credentials in URL bar)
- Password validation (8-128 chars, NIST SP 800-63B compliant)
- POST-Redirect-GET pattern for login and register
- Avatar display in post cards and profile pages

#### Sprint 10: Spaces, Tags, Post Types

- 8 thematic Spaces (SimpleX, Matrix, Cybersecurity, Privacy,
  Hardware, SimpleGo, Dev Tools, Off-Topic)
- Tag system with autocomplete (max 5 per post, 20 seeded tags)
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
- Power-level protection rules server-enforced
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
  `<select>` dropdown after user feedback

### Removed

- `make migrate-down` target (removed intentionally; live DB
  must not be rolled back)
- All GET-based auth forms (replaced with POST equivalents in
  Sprint 9)

---

*GoLab CHANGELOG - April 27, 2026*
*IT and More Systems, Recklinghausen, Germany*
