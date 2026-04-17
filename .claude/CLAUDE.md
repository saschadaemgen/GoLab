# GoLab - Claude Code Instructions

## Project

GoLab is a privacy-first developer community platform combining GitLab-style
collaboration with Twitter-style social feeds. Built with Go, HTMX, Alpine.js,
and PostgreSQL. Designed for Phase 2 migration to SimpleX SMP protocol.

- Repository: github.com/saschadaemgen/GoLab
- License: AGPL-3.0
- Author: Sascha Daemgen, IT and More Systems, Recklinghausen
- Live: https://lab.simplego.dev
- Server: VPS 194.164.197.247

## The Three-Role Workflow (NON-NEGOTIABLE)

- **Prinzessin Mausi** (Claude in planning chat) - planning, briefings, documentation ONLY. Never writes code.
- **Der Prinz / Sascha** - direction, testing, server administration, PR merging, final decisions.
- **Der Zauberer / Claude Code** - ALL implementation on feature branches (or main when explicitly permitted).

All conversation with Der Prinz in German. All code, commits, and documentation in English.

## Tech Stack

### Backend
- Language: Go 1.24 (NEVER use Go 1.25+ dependencies)
- Router: go-chi/chi v5
- Database driver: jackc/pgx/v5 (v5.7.4 - NOT v5.9+)
- Migrations: pressly/goose/v3 (v3.21.1 - NOT v3.27+)
- Session: cookie-based (custom implementation)
- Rate limiting: go-chi/httprate
- HTML sanitization: microcosm-cc/bluemonday
- Markdown: yuin/goldmark + goldmark-highlighting (Chroma)
- WebSocket: coder/websocket
- Password hashing: bcrypt (migration to argon2id planned)

### Frontend
- Templates: Go html/template (server-side rendered)
- Interactions: HTMX (page transitions, partial updates)
- Reactivity: Alpine.js (dropdowns, compose, notifications)
- Rich text editor: Quill.js 2.0.3 (self-hosted, no CDN)
- Syntax highlighting: highlight.js 11.9.0 (self-hosted)
- Particles: Custom canvas background
- Design system: SimpleGo (cyan #45BDD1 dark, green #1A7D5A light)

### Infrastructure
- Server: Debian VPS, Nginx reverse proxy, Docker Compose
- Database: PostgreSQL 16 (Alpine)
- SSL: Let's Encrypt (certonly, NEVER certbot --nginx)
- Deploy: /opt/GoLab/deploy.sh (backup, pull, build, health check)
- Backups: /opt/backups/golab-*.sql

## Project Structure

```
GoLab/
├── cmd/golab/
│   ├── main.go                    # Entry point, router, middleware
│   └── templatecheck/main.go      # Template smoke test tool
├── internal/
│   ├── auth/
│   │   ├── auth.go                # Login, register, logout handlers
│   │   ├── middleware.go          # RequireAuth middleware
│   │   └── optional.go           # OptionalAuth + RequireAuthRedirect
│   ├── database/
│   │   └── migrations/            # Goose SQL migrations (001-013)
│   ├── handler/
│   │   ├── admin.go               # Admin dashboard + user management
│   │   ├── notification.go        # Notification dispatch + handlers
│   │   ├── pages.go               # Page handlers (feed, channel, etc.)
│   │   ├── post.go                # Post CRUD + reactions + reposts
│   │   ├── profile.go             # Follow/unfollow
│   │   ├── search.go              # Full-text search
│   │   ├── upload.go              # Image upload endpoint
│   │   ├── avatar.go              # Avatar upload
│   │   └── ws.go                  # WebSocket hub + client
│   ├── model/
│   │   ├── user.go                # User model + queries
│   │   ├── post.go                # Post model + queries
│   │   ├── channel.go             # Channel model + queries
│   │   └── notification.go        # Notification model + queries
│   └── render/
│       ├── render.go              # Template engine
│       ├── markdown.go            # Goldmark + Chroma rendering
│       └── sanitize.go            # Bluemonday HTML sanitizer
├── web/
│   ├── static/
│   │   ├── css/                   # golab.css, quill.snow.css, etc.
│   │   ├── js/                    # golab.js, htmx, alpine, quill, etc.
│   │   ├── img/                   # default-avatar.svg
│   │   └── uploads/               # User uploads (avatars, images)
│   └── templates/
│       ├── base.html              # Base layout
│       ├── home.html              # Landing page
│       ├── feed.html              # User feed
│       ├── channel.html           # Channel view
│       ├── thread.html            # Thread/reply view
│       ├── admin.html             # Admin dashboard
│       ├── partials/              # Reusable components
│       └── fragments/             # HTMX fragments
├── Dockerfile                     # Multi-stage Go build
├── docker-compose.yml             # GoLab + PostgreSQL
├── go.mod / go.sum
└── docs/
    ├── seasons/                   # Season documentation
    └── internal/                  # GITIGNORED - briefings, handoffs
```

## Rules (NON-NEGOTIABLE)

### Git
- Conventional Commits ONLY: `feat(scope): description`, `fix(scope): description`
- Valid types: feat, fix, docs, test, refactor, ci, chore
- Valid scopes: editor, admin, security, ui, auth, docker, db, search, ws
- NEVER push to remote without explicit permission from Der Prinz
- NEVER change version numbers without explicit permission
- NEVER use em dashes (---) - use regular hyphens (-) or rewrite

### Go Version Compatibility (CRITICAL)
- go.mod MUST stay at `go 1.24`
- NEVER add dependencies that require Go 1.25 or higher
- Before adding ANY dependency: check its go.mod for the Go version requirement
- Known safe versions:
  - jackc/pgx/v5 v5.7.4 (Go 1.21)
  - pressly/goose/v3 v3.21.1 (Go 1.21)
  - golang.org/x/crypto v0.31.0 (Go 1.20)
  - golang.org/x/sync v0.10.0 (Go 1.18)
  - golang.org/x/text v0.21.0 (Go 1.18)
  - golang.org/x/image v0.23.0 (Go 1.18)
- After ANY dependency change: run `go mod tidy` and verify `go 1.24` stays in go.mod

### Database (CRITICAL - LIVE DATA)
- GoLab is LIVE with real users. 4+ users, active content.
- ONLY use `ALTER TABLE ADD COLUMN` with DEFAULT values
- NEVER use DROP TABLE, DROP COLUMN, or ALTER COLUMN
- NEVER rename tables or columns
- All new columns MUST have DEFAULT or be nullable
- All migration DOWN sections MUST be `SELECT 1;` (no-op)
- The `migrate-down` target was removed from Makefile intentionally
- ALWAYS backup before deploying: `docker-compose exec -T db pg_dump -U golab golab > /opt/backups/golab-$(date +%Y%m%d-%H%M%S).sql`

### Frontend
- Self-host ALL JavaScript/CSS libraries - NEVER use CDN links at runtime
- Download files to web/static/js/ and web/static/css/
- Script order in base.html matters:
  1. htmx.min.js (sync)
  2. quill.min.js (sync)
  3. highlight.min.js (sync)
  4. particles.js (defer)
  5. theme.js (defer)
  6. golab.js (defer) - MUST be before alpine
  7. alpine.min.js (defer) - MUST be last
- Alpine.js components register via `alpine:init` event in golab.js
- If Alpine x-data object has init() method, do NOT add x-init="init()"
- HTMX hx-boost="true" on body causes page re-swaps - guard against re-initialization
- All animations use cubic-bezier(0.4, 0, 0.2, 1), GPU-accelerated (transform/opacity only)

### Security
- Sanitize ALL user HTML with bluemonday before storage
- goldmark must NOT use html.WithUnsafe()
- Rate limiting on all mutation endpoints
- Security headers on all responses
- Session cookies use __Host- prefix in production
- NEVER log passwords, tokens, or secrets

### Dockerfile
- MUST include: `COPY --from=builder /app/web ./web`
- MUST include: `RUN go mod tidy` before build
- MUST include: `RUN go mod download || true` for transient errors
- Base image: golang:1.24-alpine (builder), alpine:3.21 (runtime)

### Code Quality
- `go build ./...` must be clean
- `go vet ./...` must be clean
- Template smoke test: `go run -tags templatecheck ./cmd/golab/templatecheck`
- All templates must render without error
- Use `console.log` not `console.debug` for browser debugging

## Database Schema (current: version 13)

### Tables
- users (id, username, email, password_hash, display_name, bio, avatar_url, power_level, banned, banned_at, banned_reason, hardware_verified, created_at, updated_at)
- posts (id, as_type, author_id, channel_id, parent_id, content, content_html, search_vector, reaction_count, reply_count, repost_count, created_at, updated_at)
- channels (id, name, slug, description, channel_type, creator_id, created_at)
- channel_members (id, channel_id, user_id, joined_at)
- follows (id, follower_id, following_id, created_at)
- reactions (id, user_id, post_id, reaction_type, created_at)
- sessions (id, user_id, token, created_at, expires_at)
- notifications (id, user_id, actor_id, notif_type, post_id, read, created_at)
- uploads (id, user_id, filename, mime_type, size_bytes, url, created_at)

### Power Levels
- 0: Guest
- 10: Member (default)
- 25: Contributor
- 50: Moderator
- 75: Admin
- 100: Owner (user ID 1 only)

## API Endpoints

### Auth
- POST /api/register
- POST /api/login
- POST /api/logout

### Posts
- GET /api/posts (feed)
- POST /api/posts (create)
- DELETE /api/posts/:id
- POST /api/posts/:id/react
- POST /api/posts/:id/repost
- POST /api/preview (Markdown preview)

### Channels
- GET /api/channels
- POST /api/channels
- POST /api/channels/:id/join
- POST /api/channels/:id/leave

### Users
- GET /api/users/me
- PUT /api/users/me
- POST /api/users/me/avatar
- DELETE /api/users/me/avatar
- POST /api/users/:id/follow
- DELETE /api/users/:id/follow

### Search
- GET /api/search?q=term

### Notifications
- GET /api/notifications
- GET /api/notifications/count
- POST /api/notifications/read-all
- POST /api/notifications/:id/read

### Uploads
- POST /api/upload/image

### Admin (power_level >= 100)
- GET /api/admin/stats
- GET /api/admin/users
- POST /api/admin/users/:id/ban
- POST /api/admin/users/:id/unban
- PUT /api/admin/users/:id/power

## Deploy Process

```bash
cd /opt/GoLab
./deploy.sh
# Which does:
# 1. pg_dump backup to /opt/backups/
# 2. git pull
# 3. docker-compose up -d --build
# 4. curl health check
```

## Season History

- Season 1 Sprint 1-5: Backend (Go + PostgreSQL, REST API, all models)
- Season 1 Sprint 6: Frontend (HTMX + Alpine.js + WebSocket + Design System)
- Season 1 Sprint 7: Features (Markdown, Avatars, Notifications, Threads, Search, Admin)
- Season 1 Sprint 8: Editor (Quill.js WYSIWYG, Security Hardening, Bugfixes)

## Known Issues

- hx-boost="true" on body requires guards against component re-initialization
- Quill init() uses __quillMounted flag to prevent double-mount
- FileReader race condition in avatar upload fixed with serverResolved flag
- Docker build caches aggressively - use --no-cache when code changes don't appear
- VPS files edited manually will block git checkout/pull - always use git checkout -- . first
