# GoLab - Technical Concept
# Privacy-First Developer Community Platform

**Project:** GoLab (part of SimpleGo ecosystem)
**Author:** Sascha Daemgen / IT and More Systems
**Original date:** April 16, 2026
**Last review:** April 23, 2026
**Status:** Phase 1 live at lab.simplego.dev — Phase 2 planning

**Companion document:** [ARCHITECTURE.md](./ARCHITECTURE.md) describes the Phase 1 implementation in detail: Go + PostgreSQL + HTMX + Alpine + WebSocket, the patterns that keep the code coherent, and the pitfalls already paid for. Read ARCHITECTURE.md first if you want to touch code today; read this document if you want to understand where GoLab is going.

---

## 1. Overview

GoLab is a developer community platform that combines two things
that have never been combined before: GitLab-style project
collaboration and Twitter-style social interaction - transported
entirely over SimpleX SMP queues with E2E encryption.

Every existing developer community platform - GitHub, GitLab,
Discourse, Stack Overflow, Reddit, Twitter/X - operates on the
same assumption: the server is trustworthy. You create an account,
the server stores your data, the admin can read everything, and
your activity profile follows you forever. Even "decentralized"
alternatives like Mastodon and Nostr still expose metadata, social
graphs, and often content to server operators.

GoLab rejects this assumption. The server that relays your posts
cannot read them. The relay that distributes your messages cannot
identify you. The certificate that proves your identity does not
reveal who you are. And if you unplug your hardware key, your
identity disappears from the network in seconds.

This is possible because GoLab does not build its own transport,
identity, or moderation layer. It composes four existing SimpleGo
components that are already proven or in active development:

- **simplex-js** provides the anonymous SMP transport (published, working)
- **GoUNITY** provides certificate-based pseudonymous identity
- **GoBot** provides relay fan-out and moderation
- **GoKey** provides optional hardware-backed security

GoLab adds the community application layer on top: channels, posts,
projects, issues, feeds, profiles, search, and permissions.

---

## 2. The problem

### Developer communities today

Every major developer community has the same architecture:

```
User -> Account (email, password, real name) -> Server (stores everything)
```

The server operator sees:
- Every post you write
- Every project you contribute to
- Every issue you comment on
- Who you follow and who follows you
- When you are online and how long you stay
- Your IP address, browser, location

This data is monetized (GitHub/Microsoft, Twitter/X), subpoenaed
(by governments), breached (regularly), and used for profiling
(by everyone). Developers working on privacy tools, security
research, or politically sensitive projects have no way to
participate in community discussions without exposing themselves.

### "Decentralized" alternatives fail at privacy

| Platform | What it claims | What it actually does |
|:---------|:-------------|:---------------------|
| Mastodon | Decentralized, federated | Server admin reads all posts, knows all users |
| Nostr | Censorship resistant | Relay sees all content, public key links all activity |
| Matrix | E2E encrypted | Server knows room membership, message timing, social graph |
| Discourse | Self-hosted | Server operator has full access to everything |
| Lemmy | Federated Reddit | Server admin reads all posts, federated metadata |

None of these solve the fundamental problem: the server operator
is a privileged observer.

### What is missing

A platform where:
- The server cannot read your posts (E2E encrypted transport)
- The server cannot identify you (no accounts, anonymous queues)
- The server cannot map your social graph (pairwise queue isolation)
- You still have a persistent, verifiable identity (certificate-based)
- Moderation still works (certificate-linked bans, not account bans)
- It scales beyond 100 people (relay fan-out, not client fan-out)
- Project collaboration features exist (issues, MRs, wikis)
- Social features exist (feeds, posts, follows, reactions)

GoLab is this platform.

---

## 3. Design philosophy

### Compose, do not build

GoLab does not implement its own cryptography, transport, identity,
or moderation. It composes existing components:

| Need | Component | Status |
|:-----|:---------|:-------|
| Anonymous transport | simplex-js (SMP over WebSocket) | Published (npm 1.0.0) |
| E2E encryption | SMP protocol (NaCl + Double Ratchet) | Proven (GoChat Season 9+) |
| Pseudonymous identity | GoUNITY (Ed25519 certificates) | In development |
| Moderation + relay | GoBot (Go service) | In development (Season 2) |
| Hardware security | GoKey (ESP32-S3) | Planned (Season 3) |
| Community application | **GoLab (this project)** | Concept phase |

This means GoLab inherits the security properties of each component
without reimplementing them. A bug fix in simplex-js improves GoLab
transport security. A new GoBot feature becomes available to GoLab.
A GoUNITY certificate upgrade strengthens GoLab identity.

### Separate what you know

No single component in the GoLab stack has the complete picture:

| Component | Knows | Does not know |
|:----------|:------|:-------------|
| SMP server | Queue addresses, block sizes | Content, identities, channels |
| GoBot relay | Queue addresses, channel mappings | Content (GoKey mode), real identities |
| GoLab app server | Channel registry, stored posts | SMP queue mappings, which user is which queue |
| GoUNITY | Usernames, emails, certificates | Which communities users join |
| GoKey | Decrypted content, commands | Nothing leaves the device |
| Browser client | Everything about THIS user | Other users' private data |

An attacker must compromise multiple independent systems to get
meaningful surveillance capability. This is defense in depth applied
to community software.

### ActivityStreams as lingua franca

GoLab uses W3C ActivityStreams 2.0 as its message format. This is
not an arbitrary choice:

- **Proven at scale:** The Fediverse (Mastodon, Pleroma, Misskey)
  has millions of users on ActivityStreams/ActivityPub
- **Extensible:** Custom properties (golab:objectType, golab:labels)
  integrate cleanly with the standard vocabulary
- **Tool support:** Libraries exist in every major language
- **Future interoperability:** GoLab messages could theoretically
  bridge to the Fediverse (with appropriate gateway)
- **No lock-in:** The message format is an open standard, not
  a proprietary protocol

The key insight: ActivityPub assumes HTTP transport. GoLab replaces
HTTP with SMP queues. The message format stays the same - only the
delivery mechanism changes from "POST to inbox URL" to "enqueue
to SMP receive queue". This is a clean separation of concerns.

---

## 4. Architecture decisions

### 4.1 GoLab as separate service (not a GoBot plugin)

GoBot is a moderation proxy. Its job is to relay encrypted blocks
and execute signed commands. Adding community application logic
(post storage, search indexes, project management) to GoBot would
violate the principle of minimal privilege and increase GoBot's
attack surface.

GoLab runs as a separate Go service that communicates with GoBot
via internal API. GoBot remains a dumb relay. GoLab handles the
smart application logic.

```
Rejected: GoBot + Community Plugin (monolith)
  -> GoBot attack surface increases
  -> GoBot standalone mode becomes complex
  -> GoBot GoKey mode needs changes

Accepted: GoLab + GoBot (two services)
  -> GoBot stays focused on relay + moderation
  -> GoLab handles application logic
  -> Independent release cycles
  -> Independent scaling
```

### 4.2 Relay fan-out (not client-side fan-out)

SimpleX uses client-side fan-out for groups: each member sends
to every other member. This creates O(n^2) connections and O(n)
messages per post. It works for groups under 100 but fails for
communities.

GoLab uses GoBot as a relay node. The sender sends once to GoBot.
GoBot copies to all subscriber queues. This is O(n) connections
and O(1) send per post.

```
Client-side (SimpleX groups):     Relay (GoLab):
  User -> User A                    User -> GoBot -> User A
  User -> User B                              |---> User B
  User -> User C                              |---> User C
  User -> User D                              |---> User D
  ...N-1 sends                      1 send, GoBot fans out
```

The tradeoff: GoBot is a semi-trusted relay. In standalone mode,
it sees message content. In GoKey mode, it does not. This is
acceptable because the alternative (client-side fan-out) does not
scale to communities of thousands.

### 4.3 DID:key identifiers (not custom format)

GoLab identifies users with W3C DID:key identifiers derived from
their Ed25519 public keys. This was chosen over custom identifier
formats because:

- Self-describing: the public key IS the identifier, no lookup needed
- Standard: W3C Decentralized Identifier specification
- Interoperable: compatible with the broader DID ecosystem
- Compact: a single string contains all information needed to verify

GoUNITY certificates bind a human-readable username to a DID:key.
The username is for display. The DID:key is for cryptographic
operations.

### 4.4 Three storage modes (not one-size-fits-all)

Different communities have different privacy needs. A public
open-source project wants searchable, discoverable content.
A security research group wants E2E encryption for everything.
GoLab supports three modes per channel:

| Mode | Server reads content | Searchable | Use case |
|:-----|:--------------------|:-----------|:---------|
| Plaintext | Yes | Yes (server-side) | Public channels, open projects |
| Encrypted at rest | With key | Yes (server-side) | Default for most channels |
| E2E persistent | No | Client-side only | Maximum privacy channels |

Channel creators choose the mode. Users see which mode a channel
uses before joining. The application server enforces the mode.

### 4.5 Power levels (not simple roles)

Matrix's numeric power level system is more flexible than simple
role names. A power level of 50 means "moderator" by default, but
communities can redefine thresholds. This allows:

- Different channels within a community to have different thresholds
- Gradual privilege escalation (25 -> 35 -> 50 -> 75)
- Fine-grained control per action type
- Custom roles without code changes

Power level assignments are signed ActivityStreams messages. Any
client can verify them independently. No server trust required.

---

## 5. The two faces of GoLab

### 5.1 Twitter face: Social activity

The social layer gives every user a presence in the community:

- **Personal feed:** Posts from followed users and subscribed channels
- **Discovery feed:** Trending posts, new projects, active discussions
- **Profile page:** Bio, activity history, projects, reputation
- **Posts:** Short-form updates with text, code, links, mentions, hashtags
- **Threads:** Reply chains with nested conversations
- **Reactions:** Like, upvote, or custom reactions
- **Reposts:** Share content to your followers (ActivityStreams Announce)
- **Follows:** Subscribe to users, channels, or projects

This is familiar UX for anyone who has used Twitter, Mastodon,
or Bluesky. The difference is invisible: everything is E2E
encrypted over SMP queues. The feed aggregation happens on the
application server, but the server works with encrypted content
(in E2E mode) or signed plaintext (in public mode).

### 5.2 GitLab face: Project collaboration

The collaboration layer gives teams tools to build software:

- **Projects:** Named containers for code, issues, and documentation
- **Issues:** Bug reports, feature requests with labels, milestones, assignees
- **Merge Requests:** Code review workflow with inline comments and approvals
- **Wikis:** Collaborative documentation per project
- **Milestones:** Group issues and MRs into release targets
- **Labels:** Organize and filter across projects
- **Teams:** Role-based access (owner, maintainer, developer, reporter, guest)
- **Activity log:** All project activity as an ActivityStreams collection

Note: GoLab is NOT a git hosting platform. It does not store
repositories or run CI/CD. It is a project management and
discussion platform - like GitHub Issues + GitHub Discussions +
GitHub Projects, without the git hosting part. Code hosting
can be self-hosted (Gitea, GitLab) or external - GoLab links
to it but does not replace it.

---

## 6. Comparison with existing systems

| Feature | GoLab | GitHub | GitLab | Nostr | Mastodon | Discourse |
|:--------|:------|:-------|:-------|:------|:---------|:----------|
| Social feeds | Yes | Limited | No | Yes | Yes | No |
| Project management | Yes | Yes | Yes | No | No | No |
| E2E encrypted | Yes | No | No | No | No | No |
| No server accounts | Yes | No | No | Yes | No | No |
| Server blind to content | Yes | No | No | No | No | No |
| Metadata protection | SMP queues | None | None | None | None | None |
| Identity system | Certificates | Account | Account | Pubkey | Account | Account |
| Ban evasion resistance | Certificate-based | Medium | Medium | None | Medium | Medium |
| Hardware identity | Optional | No | No | No | No | No |
| Self-hosted | Yes | No | Yes | Relays | Yes | Yes |
| Scales to 10k+ members | Planned | Yes | Yes | Yes | Yes | Yes |
| Open source | AGPL-3.0 | No | Partial | Yes | AGPL-3.0 | GPL-2.0 |

---

## 7. What GoLab does NOT do

Being clear about scope prevents feature creep:

- **GoLab is not a git host.** No repository storage, no CI/CD, no code
  browsing. Use Gitea, GitLab, or GitHub for that. GoLab links to
  external repositories.
- **GoLab is not a real-time chat.** GoChat exists for that. GoLab is
  for persistent, threaded discussions and project management.
- **GoLab is not a social media clone.** No algorithmic feeds, no ads,
  no engagement metrics. Chronological timeline, user-controlled.
- **GoLab is not a blockchain project.** No tokens, no mining, no
  consensus mechanism. Ed25519 certificates are standard PKI.
- **GoLab is not anonymous by default.** Users have persistent pseudonyms.
  True anonymity is possible (one-time certificates) but not the default.

---

## 8. Dependencies and timeline

### Prerequisites (must exist before GoLab development)

| Component | What GoLab needs from it | Target season |
|:----------|:------------------------|:-------------|
| simplex-js | SMP transport in browser | Done (published) |
| GoChat architecture | Widget build system, Shadow DOM | Done (Season 12) |
| GoBot Go service | SMP connections, command system | GoBot Season 2-3 |
| GoBot community relay | Fan-out, channel management | GoBot Season 3-4 |
| GoUNITY certificates | Ed25519 cert issuance + CRL | GoUNITY Season 4 |
| GoKey wire protocol | Hardware security integration | GoBot Season 2 (spec done) |
| GoKey firmware | ESP32 crypto operations | GoKey Season 3 |

### GoLab development phases

| Phase | Focus | Prerequisites |
|:------|:------|:-------------|
| Phase 1 | Concept, architecture, documentation | None (this phase) |
| Phase 2 | Application server core (channels, posts) | GoBot relay mode |
| Phase 3 | Browser client (feeds, posting, profiles) | simplex-js, Phase 2 |
| Phase 4 | Identity integration (GoUNITY certificates) | GoUNITY operational |
| Phase 5 | Project management (issues, MRs, wikis) | Phase 3 stable |
| Phase 6 | Search, discovery, activity aggregation | Phase 5 stable |
| Phase 7 | Hardware identity (GoKey integration) | GoKey firmware |
| Phase 8 | Federation and bridging (optional) | Phase 6 stable |

---

## 9. Comparable architectures

| System | Architecture pattern | How GoLab differs |
|:-------|:--------------------|:-----------------|
| GitHub/GitLab | Centralized server, full trust | GoLab: server is blind relay |
| Mastodon/Fediverse | Federated HTTP, ActivityPub | GoLab: SMP transport, E2E encrypted |
| Nostr | Relay-based, signed events | GoLab: SMP anonymous transport, certificate identity |
| Matrix | Federated rooms, event DAG | GoLab: no server identity, relay fan-out |
| Keybase | Signed chains, team management | GoLab: no central server, SMP transport |
| Secure Scuttlebutt | P2P gossip, append-only logs | GoLab: relay-assisted, scales beyond P2P |
| Cwtch | Tor onion services, untrusted relays | GoLab: SMP transport, certificate identity |

GoLab sits at a unique intersection: it combines the social features
of Nostr/Mastodon, the project management of GitLab, the transport
privacy of SimpleX, the identity model of GoUNITY certificates,
and the hardware security option of GoKey. No existing system
occupies this design space.

---

## 10. Open questions - answered in Phase 1

Every question below was resolved during Season 1. This section
now documents the decisions that shaped the running Phase 1
platform at lab.simplego.dev.

| Question | Answer |
|:---------|:-------|
| Application server language | **Go 1.24** (confirmed, ecosystem consistency with GoBot) |
| Database for persistence | **PostgreSQL 16** (confirmed, scales + full-text search) |
| Channel key distribution | **Deferred to Phase 2** (Phase 1 uses HTTPS, no channel keys yet) |
| Federation between GoLab instances | **Phase 2+** (deferred until single-instance UX is solid) |
| Code hosting integration | **Link-only** (confirmed, GoLab is discussion + project mgmt, not git host) |
| Mobile client | **Responsive web** (confirmed for Phase 1, 375px to ultrawide) |
| Content addressing | **UUID for posts, SHA-256 for uploads** (confirmed) |
| Offline support | **Online-required for Phase 1** (Phase 2 may revisit after SMP migration) |

---

## 11. Phase 1 implementation notes

Phase 1 is the live Go + PostgreSQL platform at lab.simplego.dev.
This section documents the specific technical decisions made during
Season 1 development so future contributors understand the "why".

### 11.1 HTMX + Alpine.js instead of React / TypeScript

The original concept sketched a TypeScript browser client built
on simplex-js. Phase 1 ships a server-rendered HTML UI with HTMX
for page transitions and Alpine.js for local interactivity.

Why:
- No build step. `docker-compose up -d` and you are running.
- Zero npm dependencies in production. Nothing to audit monthly.
- SSR means every page works without JS (graceful degradation).
- HTMX + Alpine is ~40 KB minified, the whole UI layer.
- When Phase 2 arrives, simplex-js can be dropped in without
  rewriting the UI (the SSR fallback keeps everything usable).

### 11.2 Quill.js 2.0.3 for the editor

A plain textarea was tried first. Users wanted rich text, image
upload, syntax highlighting, and emoji. Quill 2.0.3 provides all
of that with a stable API and no CDN dependency.

Why Quill and not alternatives:
- TipTap pulls in ProseMirror + a Node build chain (rejected).
- CKEditor is AGPL-incompatible at the enterprise tier (rejected).
- Trix is too minimal for code blocks (rejected).
- Quill 2.0.3 is MIT-licensed, ~200 KB, self-hostable.

The editor output is sanitized with bluemonday before storage.
Quill's raw HTML is NEVER trusted.

### 11.3 Spaces instead of Channels

The concept uses "Channels" throughout. Phase 1 ships "Spaces"
as the primary content organization. The underlying database
still has a `channels` table (kept for Phase 2), but the UI and
routes are space-oriented.

Why:
- Channels in the Matrix sense imply user creation and per-space
  permissions. Phase 1 needs stability, not UGC organization.
- 8 admin-curated Spaces give newcomers a clear map: "where do
  I post about SimpleX?" -> SimpleX Protocol space.
- Post Types (Discussion / Question / Tutorial / Code / Showcase
  / Link) give the flexibility Channels would have, without the
  permission complexity.
- If Phase 2 wants user-created Channels back, the `channels`
  table is still there.

### 11.4 Admin-fixed categories, not user-created

The 8 Spaces are seeded by migration 018 (later 020). Users
cannot create new Spaces. Tags ARE user-created and provide
cross-Space discovery.

Why:
- New communities die in empty categories. Fewer, well-used
  Spaces beat dozens of ghost towns.
- Spam defense: a spammer cannot flood the platform with 100
  fake Spaces. They can create tags, but tags lurk in a pool,
  not in the navigation.
- Migration cost is low: a new Space is one INSERT statement.

### 11.5 No GIF API (Tenor shutdown, GIPHY paid)

The editor has an emoji picker but no GIF picker.

Why:
- Tenor's public API was shut down in 2024.
- GIPHY's remaining API requires commercial licensing.
- Self-hosting a GIF library has content-moderation implications
  that Phase 1 is not built to handle.
- Users CAN upload GIFs as images; they just lack a search UI.

This may be revisited if a privacy-respecting GIF source emerges.

### 11.6 Self-hosted all JS / CSS libraries

Zero runtime CDN calls. Every library (Quill, HTMX, Alpine,
highlight.js, emoji picker) is vendored into `web/static/` and
served from the same origin as the app.

Why:
- A CDN is a tracking vector. `unpkg.com` sees every page load.
- A CDN is a supply-chain vector. Compromise cdnjs, compromise
  GoLab.
- A CDN is a privacy leak. Users' IPs flow to third parties.
- A CDN is a dependency risk. Phase 2 promises "no server reads
  your content" - CDNs would silently break that promise.

The `Dockerfile` copies `web/static/` into the final image.
There is no `npm install` at runtime.

### 11.7 User moderation via status field, not a separate table

Users have a `status` column (`active` / `pending` / `rejected`)
plus `reviewed_at` and `reviewed_by`. When `require_approval`
is enabled, new registrations land in `pending` and cannot post
until an admin approves them. Rejected users see a read-only
view and get logged out on their next session touch.

Why:
- Adding a column is reversible via the existing live-DB rules.
- A separate table would duplicate user references and make the
  approval queue a join.
- The `SetStatus` method is the single write path; no chance of
  three tables drifting.

### 11.8 Session management via database rows

Sessions are bcrypt-gated but NOT JWT-based. The `sessions` table
stores `(id, user_id, expires_at)` rows. Every request re-reads
the session and the user. Password change = `DELETE FROM sessions
WHERE user_id = $1`, which revokes every device.

Why:
- JWTs cannot be revoked without a blocklist table, which puts
  us back at DB-per-request.
- DB-per-request is ~1 ms and trivial to optimize later.
- The `__Host-` cookie prefix gives us path confinement in
  production; dev uses a plain `session_id` cookie because
  `__Host-` requires HTTPS.

---

*GoLab Technical Concept v2 - April 2026 (Phase 1 live)*
*IT and More Systems, Recklinghausen, Germany*
