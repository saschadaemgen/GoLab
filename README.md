<p align="center">
  <strong>GoLab</strong>
</p>

<p align="center">
  <strong>The world's first privacy-first developer community platform on encrypted messaging.</strong><br>
  GitLab-style collaboration meets Twitter-style activity feeds - over SimpleX SMP.<br>
  Curated write access. No tracking. No admin reads your private posts. E2E encrypted by design (Phase 2).<br>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg" alt="License"></a>
  <a href="https://lab.simplego.dev"><img src="https://img.shields.io/badge/status-phase--1--live-brightgreen.svg" alt="Status"></a>
  <a href="https://lab.simplego.dev"><img src="https://img.shields.io/badge/season-2--close-cyan.svg" alt="Season"></a>
  <a href="https://github.com/saschadaemgen/SimpleGo"><img src="https://img.shields.io/badge/ecosystem-SimpleGo-green.svg" alt="SimpleGo"></a>
</p>

---

> "Every developer community platform works the same way: you create an account, the server stores your data, the admin can read everything, and your activity profile follows you forever. GitLab, GitHub, Discourse, Reddit - they all assume that the server is trustworthy. GoLab assumes it is not."

GoLab is a developer community platform that combines GitLab-style project collaboration (issues, merge requests, wikis) with Twitter-style social features (activity feeds, posts, follows, reposts) - on a curated platform where read access is open to everyone but write access goes through a structured application process. Phase 2 will migrate the transport layer to SimpleX SMP queues with full E2E encryption.

Your identity in Phase 2 will not be an account on a server. It will be an Ed25519 certificate issued by [GoUNITY](https://github.com/saschadaemgen/GoUNITY) - a cryptographic proof that you are who you claim to be, without revealing who that is. Moderation will be handled by [GoBot](https://github.com/saschadaemgen/GoBot), which enforces community rules without ever seeing message content in its hardware-secured mode. And if you want physical proof of identity, plug in a [SimpleGo](https://github.com/saschadaemgen/SimpleGo) device and verify with a hardware challenge-response that no software can fake.

This architecture has no precedent. No existing platform combines anonymous transport, persistent pseudonymous identity, scalable community features, hardware-backed verification, and a curated knowledge-filtered application process in a single system.

---

## What GoLab is today (Phase 1)

GoLab Phase 1 is live at [lab.simplego.dev](https://lab.simplego.dev).
A fully functional developer community platform with curated write access, built with:

- **Backend:** Go 1.24, chi router, PostgreSQL 16
- **Frontend:** Server-rendered HTML with HTMX + Alpine.js
- **Editor:** Quill.js 2.0.3 WYSIWYG with emoji picker
- **Real-time:** WebSocket notifications, live edit propagation
- **Search:** PostgreSQL full-text search
- **Auth:** bcrypt with rate limiting and security headers
- **Deploy:** Docker Compose on Debian VPS

### Current features (Phase 1)

**Curated access:**
- 11-step brutalist application wizard with cinematic stagger animations
- Three knowledge questions designed to filter for technical depth (anti-AI-bot)
- Live username availability check with 7 status states
- 17-entry reserved-name blocklist enforced server-side
- @username personalization on 4 strategic wizard steps
- 5-dimensional admin rating system (track_record, ecosystem_fit, contribution_potential, relevance, communication)
- Personal review within 7 days regardless of outcome

**Content:**
- 8 thematic Spaces (SimpleX Protocol, Matrix / Element, Cybersecurity, Privacy, Hardware, SimpleGo Ecosystem, Dev Tools, Off-Topic / Meta)
- Post Types (Discussion, Question, Tutorial, Code, Showcase, Link, Announcement)
- Tag system with autocomplete and cross-Space discovery
- Rich text editor with image upload and syntax highlighting
- Mermaid diagrams (server-rendered SVG)
- KaTeX math rendering inline and block
- Code permalinks with line ranges
- 30-minute edit window with edit history visible to admins
- Real-time edit propagation via WebSocket
- Soft-delete with author/admin recovery window

**Social:**
- Real-time notifications via WebSocket
- Threaded conversations with reply chains
- Reactions with toggle (one per user per post per type)
- Follows for users and spaces
- Full-text search across posts

**Account management:**
- Username + password login (no email field by design)
- Password change with session invalidation across devices
- Username change with live availability check
- First-user auto-promote to admin (UserStore + Handler dual-layer)

**Admin tooling:**
- Admin dashboard with rating widgets, knowledge answers display, approve/reject queue
- User management with power levels (0-100)
- Ban system with reason tracking and audit trail
- Application approval workflow

**UI / UX:**
- Fullscreen mobile menu with dark/cyan theme
- Responsive design from 375px to ultrawide
- Light/dark theme support
- Particle background with respect for prefers-reduced-motion

**Operations:**
- Rate limiting (per IP and per user)
- bluemonday HTML sanitization on all user content
- Security headers (X-Content-Type-Options, X-Frame-Options, Referrer-Policy, etc.)
- Production environment correctly configured (GOLAB_ENV=production)
- Zero npm dependencies, zero CDN calls, self-hosted everything

### Phase 1 architecture

```
[Browser] --> [Nginx reverse proxy]
                    |
              [Go Application Server]
                    |
              [PostgreSQL 16]
```

Phase 1 uses standard HTTPS transport. Posts are stored in PostgreSQL. The server CAN read content - this is the same trust model as Discourse or any self-hosted forum. Phase 1 protects against external attackers, malicious users, and bot registration. Phase 1 does not protect against a compromised server, legal compulsion, or a malicious operator. Those are Phase 2 problems.

The sections below describe the Phase 2 vision: a hybrid Clearnet+SimpleGoX architecture that adds a server-blind privacy path while preserving public discoverability.

---

## What GoLab does

GoLab is two things in one:

**A social platform (like Twitter):** Post updates, follow people, build an activity feed, react to posts, repost content, discover communities. In Phase 2, every interaction is an ActivityStreams 2.0 object transported over SMP queues - standardized, extensible, and fully encrypted on the private path.

**A collaboration platform (like GitLab):** Create projects, track issues, review code, discuss in threads, manage teams with role-based permissions. In Phase 2, every collaboration artifact is signed by the author's Ed25519 certificate - verifiable, tamper-proof, and independent of any server.

**What makes it different from everything else:**

| Feature | GitHub/GitLab | Twitter/X | Mastodon | Nostr | Discourse | GoLab |
|:--------|:-------------|:----------|:---------|:------|:----------|:------|
| Project management | Yes | No | No | No | No | Yes (Sprint 16+) |
| Activity feeds | Limited | Yes | Yes | Yes | No | Yes |
| Curated write access | No | No | No | No | Optional | Yes |
| Knowledge filter at signup | No | No | No | No | No | Yes |
| E2E encrypted (Phase 2) | No | No | No | No | No | Yes |
| Server cannot read content (Phase 2) | No | No | No | No | No | Yes (private path) |
| Persistent identity (Phase 2) | Server account | Server account | Server account | Public key (visible) | Account | Ed25519 cert (anonymous) |
| Metadata protection (Phase 2) | No | No | No | No | No | SMP queues |
| Hardware identity (Phase 2) | No | No | No | No | No | Optional (GoKey/SimpleGo) |
| Ban evasion resistant | Weak | Weak | Weak | None | Weak | Certificate-based |

---

## How it works (Phase 2 - Future)

*The architecture below is the Phase 2 target. Phase 1 (today) uses standard HTTPS to a Go server with PostgreSQL with curated access; see the "What GoLab is today" section above. Phase 2 adopts a hybrid model: public Clearnet read path stays HTTPS-based, plus a private SMP path through the SimpleGoX desktop application.*

### The hybrid model (decided in Season 2)

Browser-native SimpleX SMP is not feasible (WebSocket lacks TLS channel binding, WebCrypto lacks Curve448/Ed448, no Go SDK). Phase 2 adapts:

**Public path (Clearnet):** lab.simplego.dev for read-only browsing. Standard HTTPS, SEO-friendly, indexable. The discovery layer where applications start. Posts marked `visibility=public` are rendered here.

**Privacy path (SimpleGoX Plugin):** Native plugin in the SimpleGoX Multi-Messenger desktop application, which has a working native SMP v9 client. Login via existing SimpleX profile. Full read+write access. All content E2E encrypted in transit through real SMP. Posts marked `visibility=private` only travel here.

**Tor Onion Service v3 + I2P:** Same content as Clearnet, alternative network paths.

```
[Browser]                  [SimpleGoX Plugin]              [Tor / I2P Browser]
    |                              |                              |
    | HTTPS (read public)          | SMP v9 (read+write)          | HTTPS over Tor/I2P
    |                              |                              |
    +------------------------------+------------------------------+
                                   |
                          [Nginx reverse proxy]
                                   |
                          [GoLab Application Server]
                                   |
                                   | gRPC / Unix socket
                                   |
                          [GoLab SMP Adapter]
                                   |
                          [GoBot Community Relay]
                                   |
                                   | E2E encrypted blocks
                                   | Per-channel queue fan-out
                                   |
            +----------------------+-----------------------+
            |                      |                       |
   [Subscriber A queue]   [Subscriber B queue]   [Subscriber C queue]
   Pairwise SMP queues    Pairwise SMP queues     Pairwise SMP queues
```

### Posting in a channel (Phase 2)

```
1. User writes "Fixed the memory leak in GoChat" in #gochat-dev
   from inside the SimpleGoX plugin

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
   for indexing on lab.simplego.dev
   Only posts marked visibility=public are sent here
   Private posts stay entirely within the SMP path
```

### Identity verification (Phase 2)

```
1. User registers at id.simplego.dev (GoUNITY)
   Receives Ed25519 certificate + private key

2. User joins GoLab community via SimpleGoX plugin
   Sends certificate to GoBot via DM (E2E encrypted)

3. GoBot/GoKey verifies CA signature (local, offline)
   Sends challenge nonce
   User signs nonce with private key
   Proof: user holds the key, sharing impossible

4. User is verified as "CryptoNinja42"
   Can post, create projects, moderate (based on role)
   GoLab server never knows the real identity
```

---

## Platform features

### Social features (Twitter-style)

- **Activity feeds** - personalized timeline of followed users and spaces
- **Posts** - short-form updates with text, links, code snippets
- **Reposts** - share others' posts to your followers (ActivityStreams Announce)
- **Reactions** - like, upvote, or custom reactions on any post (toggle, one per user per post per type)
- **Follows** - subscribe to users or spaces
- **Threads** - reply chains with nested conversations
- **Discovery** - find communities, users, and projects by topic
- **Profiles** - pseudonymous identity with bio, activity history, reputation

### Curation features (Phase 1)

- **Application wizard** - 11-step brutalist UX with knowledge questions
- **5-dimensional admin ratings** - track_record, ecosystem_fit, contribution_potential, relevance, communication
- **Reserved-name blocklist** - 17 entries, enforced server-side
- **First-user auto-promote** - bootstrapping admin without manual DB editing
- **Approval queue** - pending users browse but cannot post

### Collaboration features (Sprint 16+)

- **Projects** - named containers between Spaces and Posts
- **Project Docs** - Concept, Architecture, Workflow, Roadmap as Markdown
- **Project Seasons** - sequential development phases with closing documents
- **Issues** - bug reports, feature requests, discussions with labels and milestones
- **Merge requests (Phase 2)** - code review workflow with inline comments
- **Wikis** - collaborative documentation per project
- **Teams** - role-based access control (owner, contributor, viewer)
- **Milestones** - group issues and seasons into release targets
- **Labels and tags** - organize and filter across projects

### Moderation and identity (Phase 2)

- **GoUNITY certificates** - verified pseudonymous identity, ban-evasion resistant
- **GoBot moderation** - automated rule enforcement without reading content
- **Role-based permissions** - Matrix-style power levels per channel and project
- **Trust Levels TL0-TL4 (Season 3)** - earned permissions, Discourse-style
- **Reports** - community members flag content, moderators review
- **CRL enforcement** - revoked certificates are rejected across all communities
- **Hardware verification** - optional GoKey/SimpleGo challenge-response for physical trust

---

## Architecture (Phase 2 - Future)

*Phase 1 is a single Go binary + PostgreSQL container with curated access. The hybrid composition below applies once Phase 2 migrates the private content layer to SMP. The Phase 1 stack lives in this repository.*

GoLab is not a monolith. It is composed of existing SimpleGo ecosystem components:

| Component | Role in GoLab | Repository |
|:----------|:-------------|:-----------|
| **GoLab** | Community application server + Clearnet UI | This repo |
| [GoBot](https://github.com/saschadaemgen/GoBot) | Community relay + moderation engine | [GoBot repo](https://github.com/saschadaemgen/GoBot) |
| [SimpleGoX](https://github.com/saschadaemgen/SimpleGoX) | Multi-Messenger plugin host with native SMP v9 client | [SimpleGoX repo](https://github.com/saschadaemgen/SimpleGoX) |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware crypto for GoBot (optional) | [SimpleGo repo](https://github.com/saschadaemgen/SimpleGo) |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) | Certificate authority for identity | [GoUNITY repo](https://github.com/saschadaemgen/GoUNITY) |
| [simplex-js](https://www.npmjs.com/package/simplex-js) | SMP transport for browser clients (deprioritized in hybrid model) | [GoChat repo](https://github.com/saschadaemgen/GoChat) |

### Message format

GoLab Phase 2 uses [ActivityStreams 2.0](https://www.w3.org/TR/activitystreams-core/) as its message vocabulary, transported over SMP queues instead of HTTP. Every message is a signed JSON object:

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

Standard ActivityStreams types mapped to GoLab features:

| ActivityStreams type | GoLab feature |
|:--------------------|:-------------|
| Create + Note | Post / Comment |
| Create + Article | Long-form post / Wiki page |
| Announce | Repost |
| Like | Reaction / Upvote |
| Follow | Subscribe to user or channel |
| Block | Ban (moderator action) |
| Remove | Delete content (moderator action) |
| Update | Edit post / Update issue |
| Add | Add member to project / Assign issue |

---

## Security

*Phase 1 uses HTTPS + PostgreSQL with curated access. The server CAN read content, knows user identities (bcrypt-hashed passwords), and knows the social graph. Phase 1 protects against external attackers, malicious users, and bot registration - it does not protect against a compromised or malicious server.*

*Phase 2 adds the zero-knowledge properties below for the private content path while preserving the public Clearnet path for discoverability.*

### What the GoLab server knows (Phase 2)

| Data | Visible to server? |
|:-----|:-------------------|
| Public-visibility post content | Yes (indexed for Clearnet) |
| Private-visibility post content | No (E2E encrypted via SMP) |
| User identity | No (only queue addresses, no accounts) |
| Who posted what (private) | No (GoBot relay mode) |
| Who follows whom | No (pairwise SMP queues) |
| Channel membership list | Queue addresses only, not identities |
| IP addresses | SMP server sees them, GoLab does not |

### What makes GoLab different from "encrypted" platforms

Most platforms that claim encryption still have a server that manages accounts, stores metadata, and knows the social graph. GoLab Phase 2 has none of that for private content:

- **Private content has no accounts on the server.** Identity is a certificate in your browser/device.
- **No social graph for private content on the server.** Follow relationships exist as SMP queue pairs that the server cannot correlate.
- **No private content on the server.** Posts are encrypted in transit and only stored on subscribers' devices.
- **No metadata correlation for private content.** Each channel subscription uses a separate SMP queue pair.

Public content is intentionally indexable - that is the trade-off the hybrid model makes for discoverability.

See [ARCHITECTURE_AND_SECURITY.md](ARCHITECTURE_AND_SECURITY.md) for the full threat model covering both Phase 1 and Phase 2.

---

## Current status

| Component | Status |
|:----------|:-------|
| GoLab concept and architecture | **Updated** at Season 2 close |
| GoLab Phase 1 application server | **LIVE** at [lab.simplego.dev](https://lab.simplego.dev) (Go 1.24 + PostgreSQL 16) |
| GoLab Phase 1 UI | **LIVE** (Go html/template + HTMX + Alpine.js) |
| GoLab Phase 1 content features | **LIVE** (8 Spaces, post types, tags, editor, notifications, search, code tools) |
| GoLab Phase 1 curated access | **LIVE** (11-step wizard, knowledge questions, 5-dim ratings) |
| GoLab Phase 1 moderation | **LIVE** (power levels 0-100, ban system, approval queue) |
| GoLab Phase 1 deployment | **LIVE** (Docker Compose on Debian VPS, Nginx, Let's Encrypt) |
| Sprint 16 - Project System | **Season 3 priority** (Spaces > Projects > Seasons > Posts) |
| Sprint 17 - Security hardening | Season 3 (Argon2id, CSP, encryption at rest, session timeout, 2FA) |
| Sprint 18 - Trust Levels + dynamic knowledge questions | Season 3-4 |
| GoLab Phase 2 hybrid Clearnet+SMP | Decided in Season 2, implementation Season 4+ |
| GoLab SMP adapter (simplex-chat CLI integration) | Season 4 prototype |
| SimpleGoX plugin specification | Season 4-5 |
| GoLab Phase 2 certificate identity | Season 5 (after GoUNITY) |
| Tor Onion Service + I2P deployment | Season 5-6 |
| Hardware identity (GoKey integration) | Season 6+ |

### Dependencies

GoLab builds on components that are in active development:

| Dependency | Required for | Status |
|:-----------|:------------|:-------|
| [GoBot](https://github.com/saschadaemgen/GoBot) Season 2-3 | Community relay + moderation | In development |
| [SimpleGoX](https://github.com/saschadaemgen/SimpleGoX) plugin API | Phase 2 private write path | Pre-alpha |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) Season 4 | Certificate-based identity | Planned |
| [simplex-js](https://www.npmjs.com/package/simplex-js) | Browser SMP transport (deprioritized) | Published (1.0.0) |
| [GoChat](https://github.com/saschadaemgen/GoChat) | Browser-native SMP messenger | Published (1.0.0) |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware identity (optional) | Planned |

---

## Setup

### Phase 1 (today)

```bash
git clone https://github.com/saschadaemgen/GoLab.git
cd GoLab
docker-compose up -d
```

GoLab is now running at http://localhost:3000.

**Requirements:**
- Docker and Docker Compose
- That is it. No Go, no Node, no npm.

The first user to register becomes the Owner (power level 100) and is auto-approved. All subsequent users go through the application wizard and require admin review when `require_approval` is enabled (default true since Migration 026).

### Local development

For development without Docker:

```bash
# Install Go 1.24 and PostgreSQL 16 natively
# Set up local DB:
createdb golab
psql golab < internal/database/migrations/init.sql

# Run:
go run ./cmd/golab
```

The Phase 1 stack runs natively on Windows, macOS, and Linux. PostgreSQL is the only external dependency.

### Phase 2 (planned)

```bash
git clone https://github.com/saschadaemgen/GoLab.git
cd GoLab

# GoLab application server (Clearnet path)
make build
make run

# Or with Docker
docker-compose up -d
```

**Phase 2 requirements:**
- GoBot instance (community relay)
- GoUNITY instance (certificate authority)
- SimpleGoX desktop application (for the plugin write path)
- SMP server with WebSocket support (e.g., smp.simplego.dev)
- Optional: Tor and I2P daemons for alternative network paths

---

## Documentation

| Document | Description |
|:---------|:-----------|
| [Architecture and Security](ARCHITECTURE_AND_SECURITY.md) | Technical architecture, threat model, Phase 1 and Phase 2 security analysis |
| [Concept](CONCEPT.md) | High-level vision, design decisions, technology choices |
| [Changelog](CHANGELOG.md) | Release history with all sprints documented |

### Related documentation in other repos

| Document | Description |
|:---------|:-----------|
| [GoBot System Architecture](https://github.com/saschadaemgen/GoBot/blob/main/docs/SYSTEM-ARCHITECTURE.md) | Full GoBot + GoKey + GoUNITY system design |
| [GoBot Concept](https://github.com/saschadaemgen/GoBot/blob/main/docs/CONCEPT.md) | GoBot technical concept with GoLab integration |
| [GoKey Wire Protocol](https://github.com/saschadaemgen/GoBot/blob/main/docs/GOKEY-WIRE-PROTOCOL.md) | Communication protocol between GoBot and GoKey |
| [GoUNITY Architecture](https://github.com/saschadaemgen/GoUNITY/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) | Certificate authority design and security |
| [SimpleGoX Architecture](https://github.com/saschadaemgen/SimpleGoX/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) | Multi-Messenger plugin host design and security |

---

## SimpleGo ecosystem

| Project | What it does |
|:--------|:-------------|
| [SimpleGo](https://github.com/saschadaemgen/SimpleGo) | Dedicated hardware messenger on ESP32-S3 |
| [GoRelay](https://github.com/saschadaemgen/GoRelay) | Encrypted relay server (SMP + GRP) |
| [GoChat](https://github.com/saschadaemgen/GoChat) | Browser-native encrypted chat widget |
| [GoBot](https://github.com/saschadaemgen/GoBot) | Hardware-secured moderation bot |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware crypto engine for GoBot (ESP32-S3) |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) | Certificate authority for identity verification |
| [SimpleGoX](https://github.com/saschadaemgen/SimpleGoX) | Multi-Messenger desktop client (Tauri 2.x, native SMP v9) |
| [GoLab](https://github.com/saschadaemgen/GoLab) | Privacy-first developer community platform |
| [GoShop](https://github.com/saschadaemgen/GoShop) | End-to-end encrypted e-commerce |
| [GoTube](https://github.com/saschadaemgen/GoTube) | Encrypted video platform |
| [GoBook](https://github.com/saschadaemgen/GoBook) | Encrypted publishing platform |
| [GoOS](https://github.com/saschadaemgen/GoOS) | Privacy-focused Linux (Buildroot, RK3566) |

---

## License

AGPL-3.0

---

<p align="center">
  <i>GoLab is part of the <a href="https://github.com/saschadaemgen/SimpleGo">SimpleGo ecosystem</a> by IT and More Systems, Recklinghausen, Germany.</i>
</p>

<p align="center">
  <strong>Read access is open. Write access is curated. Phase 2 brings server-blind private content over SMP.</strong>
</p>
