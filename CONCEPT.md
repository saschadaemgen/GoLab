# GoLab - Technical Concept
# Privacy-First Developer Community Platform

**Project:** GoLab (part of SimpleGo ecosystem)
**Author:** Sascha Daemgen / IT and More Systems
**Original date:** April 16, 2026
**Last review:** April 27, 2026 (Season 2 close)
**Status:** Phase 1 live at lab.simplego.dev with curated access - Phase 2 hybrid model decided

**Companion document:** [ARCHITECTURE_AND_SECURITY.md](ARCHITECTURE_AND_SECURITY.md) describes both the Phase 1 implementation (Go + PostgreSQL + HTMX + Alpine + WebSocket) and the Phase 2 architectural target (hybrid Clearnet + SimpleGoX SMP plugin). Read ARCHITECTURE_AND_SECURITY.md first for the technical details. Read this document for the strategic vision and design rationale.

---

## 1. Overview

GoLab is a developer community platform that combines two things that have never been combined before: GitLab-style project collaboration and Twitter-style social interaction - on a curated platform where read access is open to everyone but write access is reviewed personally through a structured application process.

Phase 1 is live today at lab.simplego.dev as a conventional server-rendered web application with curated access. Phase 2 will preserve the same UI and content model while migrating the transport layer to SimpleX SMP queues with E2E encryption, replacing account-based authentication with GoUNITY certificates.

Every existing developer community platform - GitHub, GitLab, Discourse, Stack Overflow, Reddit, Twitter/X - operates on the same assumption: the server is trustworthy. You create an account, the server stores your data, the admin can read everything, and your activity profile follows you forever. Even decentralized alternatives like Mastodon and Nostr still expose metadata, social graphs, and often content to server operators.

GoLab Phase 2 rejects this assumption. The server that relays your posts cannot read them. The relay that distributes your messages cannot identify you. The certificate that proves your identity does not reveal who you are. And if you unplug your hardware key, your identity disappears from the network in seconds.

This is possible because GoLab does not build its own transport, identity, or moderation layer. It composes four existing SimpleGo components that are already proven or in active development:

- **simplex-js** provides the anonymous SMP transport (published, working)
- **GoUNITY** provides certificate-based pseudonymous identity (planned Season 4)
- **GoBot** provides relay fan-out and moderation (in development)
- **GoKey** provides optional hardware-backed security (planned Season 3+)

GoLab adds the community application layer on top: spaces, posts, projects, issues, feeds, profiles, search, and permissions.

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

This data is monetized (GitHub/Microsoft, Twitter/X), subpoenaed (by governments), breached (regularly), and used for profiling (by everyone). Developers working on privacy tools, security research, or politically sensitive projects have no way to participate in community discussions without exposing themselves.

### Decentralized alternatives fail at privacy

| Platform | What it claims | What it actually does |
|:---------|:-------------|:---------------------|
| Mastodon | Decentralized, federated | Server admin reads all posts, knows all users |
| Nostr | Censorship resistant | Relay sees all content, public key links all activity |
| Matrix | E2E encrypted | Server knows room membership, message timing, social graph |
| Discourse | Self-hosted | Server operator has full access to everything |
| Lemmy | Federated Reddit | Server admin reads all posts, federated metadata |

None of these solve the fundamental problem: the server operator is a privileged observer.

### Open registration platforms attract noise

Beyond the privacy problem, there is a quality problem. Platforms with open registration accumulate low-effort accounts faster than meaningful contributors. Reddit, Discord, Mastodon, and every forum software ever written struggle with the same pattern: the loudest voices are not the most informed ones, and the moderation cost grows linearly with user count.

GoLab Phase 1 addresses this by making write access opt-in through review. Read access stays open - SEO, discoverability, knowledge sharing all benefit from public content. Write access requires a structured application that filters for genuine technical interest before any account is created.

### What is missing

A platform where:
- The server cannot read your posts (E2E encrypted transport, Phase 2)
- The server cannot identify you (no accounts, anonymous queues, Phase 2)
- The server cannot map your social graph (pairwise queue isolation, Phase 2)
- You still have a persistent, verifiable identity (certificate-based, Phase 2)
- Moderation still works (certificate-linked bans, not account bans, Phase 2)
- It scales beyond 100 people (relay fan-out, not client fan-out, Phase 2)
- Quality is curated, not crowdsourced (application-based write access, Phase 1)
- Project collaboration features exist (issues, MRs, wikis, Phase 1+)
- Social features exist (feeds, posts, follows, reactions, Phase 1)

GoLab is this platform.

---

## 3. Design philosophy

### Curate, do not crowdsource

GoLab Phase 1 introduced something most community platforms avoid: a real application process for write access. Read access is open to everyone, indexable by search engines, browsable without an account. Write access requires:

- An 11-step structured application wizard (about 10 minutes to complete)
- Five about-you questions covering background, ecosystem connection, contribution intent, current focus, and free-form notes
- Three knowledge questions designed to filter for technical depth (described below)
- Personal review by an admin who scores the application across 5 dimensions
- A response within 7 days regardless of outcome

The result is a community where every poster has been seen by another human before posting. Quality scales with care, not with user count. This is the opposite of growth-optimized platforms - and it is intentional.

### Knowledge questions as anti-AI filter

Three questions in the application target deep SimpleX/Matrix/SMP protocol knowledge. The questions are chosen so that general-purpose AI assistants either give wrong answers, give generic standard answers that any reader has seen a hundred times before, or give shallow surface-level answers that fail to address the substance.

The point is not to test memorization. Wrong-but-thoughtful beats right-but-shallow. An applicant who admits they are wrong about a Double Ratchet detail but explains their reasoning shows technical depth. An applicant who copy-pastes a textbook definition of Megolm shows surface engagement.

This filter works because the topics are too niche for current LLMs to have substantial training data on. As this changes, the questions rotate. Version 2 (planned for Season 3) introduces dynamic question rotation. Version 3 (long-term) might integrate the SimpleGo project AI's collective memory of past discussions to generate questions on the fly that no general-purpose model has seen.

### Compose, do not build

GoLab does not implement its own cryptography, transport, identity, or moderation. It composes existing components:

| Need | Component | Status |
|:-----|:---------|:-------|
| Anonymous transport (Phase 2) | simplex-js (SMP over WebSocket) | Published (npm 1.0.0) |
| Native SMP client (Phase 2) | SimpleGoX sgx-simplex sidecar | Pre-alpha |
| E2E encryption (Phase 2) | SMP protocol (NaCl + Double Ratchet + sntrup761) | Proven (GoChat Season 9+) |
| Pseudonymous identity (Phase 2) | GoUNITY (Ed25519 certificates) | Planned (Season 4) |
| Moderation + relay (Phase 2) | GoBot (Go service) | In development |
| Hardware security (Phase 2) | GoKey (ESP32-S3) | Planned (Season 3+) |
| Community application (Phase 1+2) | **GoLab (this project)** | Live |

This means GoLab inherits the security properties of each component without reimplementing them. A bug fix in simplex-js improves GoLab transport security. A new GoBot feature becomes available to GoLab. A GoUNITY certificate upgrade strengthens GoLab identity.

### Hybrid model: public read, private write

Season 2 research established that browser-native SimpleX SMP is not feasible. WebSocket lacks the TLS channel binding that SMP needs for forward secrecy. WebCrypto does not support the Curve448/Ed448 primitives that SMP uses. There is no Go SDK for SMP, only the Haskell-based simplex-chat reference implementation.

The Phase 2 architecture adapts to this reality with a hybrid model:

**Public path (Clearnet):** lab.simplego.dev for read-only browsing. Standard HTTPS. SEO-friendly, indexable. The discovery layer where applications start.

**Privacy path (SimpleGoX Plugin):** Native plugin in the SimpleGoX Multi-Messenger desktop application, which has a working native SMP v9 client (sgx-simplex sidecar in Rust). Login via existing SimpleX profile. Full read+write access. All write operations E2E encrypted in transit through real SMP.

**Tor Onion Service v3:** Same content as Clearnet but accessed via .onion address. Alternative network path for users who prefer Tor anonymization at the network layer.

**I2P Service:** Alternative path for I2P users.

Posts have a visibility flag: `public` (rendered on Clearnet, indexed) or `private` (only visible in SimpleGoX-authenticated context). This split lets the platform grow on Clearnet while preserving full E2E privacy for users who choose it. SEO benefits, content stays discoverable, and the privacy-conscious users get a transport path the Clearnet server literally cannot intercept.

### Separate what you know

No single component in the GoLab Phase 2 stack has the complete picture:

| Component | Knows | Does not know |
|:----------|:------|:-------------|
| SMP server | Queue addresses, block sizes | Content, identities, channels |
| GoBot relay | Queue addresses, channel mappings | Content (GoKey mode), real identities |
| GoLab Clearnet server | Public posts, application metadata | Private posts, SMP plugin traffic |
| GoLab SMP adapter | Encrypted blocks, queue routing | Plaintext content, real identities |
| GoUNITY | Usernames, emails, certificates | Which communities users join |
| GoKey | Decrypted content, commands | Nothing leaves the device |
| SimpleGoX plugin | Everything about THIS user | Other users' private data |

An attacker must compromise multiple independent systems to get meaningful surveillance capability. This is defense in depth applied to community software.

### ActivityStreams as lingua franca

GoLab uses W3C ActivityStreams 2.0 as its message format. This is not an arbitrary choice:

- **Proven at scale:** The Fediverse (Mastodon, Pleroma, Misskey) has millions of users on ActivityStreams/ActivityPub
- **Extensible:** Custom properties (golab:objectType, golab:labels) integrate cleanly with the standard vocabulary
- **Tool support:** Libraries exist in every major language
- **Future interoperability:** GoLab messages could theoretically bridge to the Fediverse (with appropriate gateway)
- **No lock-in:** The message format is an open standard, not a proprietary protocol

The key insight: ActivityPub assumes HTTP transport. GoLab Phase 2 replaces HTTP with SMP queues for the privacy path. The message format stays the same - only the delivery mechanism changes from "POST to inbox URL" to "enqueue to SMP receive queue". This is a clean separation of concerns.

---

## 4. Architecture decisions

### 4.1 GoLab as separate service (not a GoBot plugin)

GoBot is a moderation proxy. Its job is to relay encrypted blocks and execute signed commands. Adding community application logic (post storage, search indexes, project management) to GoBot would violate the principle of minimal privilege and increase GoBot's attack surface.

GoLab runs as a separate Go service. Phase 2 introduces a GoLab SMP adapter that bridges the conventional Go application server with the SMP wire protocol via gRPC to a sidecar process. GoBot remains a dumb relay, GoLab handles the smart application logic, and the SMP adapter handles transport translation.

```
Rejected: GoBot + Community Plugin (monolith)
  -> GoBot attack surface increases
  -> GoBot standalone mode becomes complex
  -> GoBot GoKey mode needs changes

Accepted: GoLab + GoBot + SMP Adapter (three concerns)
  -> GoBot stays focused on relay + moderation
  -> GoLab handles application logic
  -> SMP adapter translates transport
  -> Independent release cycles
  -> Independent scaling
```

### 4.2 Relay fan-out (not client-side fan-out)

SimpleX uses client-side fan-out for groups: each member sends to every other member. This creates O(n^2) connections and O(n) messages per post. It works for groups under 100 but fails for communities.

GoLab uses GoBot as a relay node. The sender sends once to GoBot. GoBot copies to all subscriber queues. This is O(n) connections and O(1) send per post.

```
Client-side (SimpleX groups):     Relay (GoLab):
  User -> User A                    User -> GoBot -> User A
  User -> User B                              |---> User B
  User -> User C                              |---> User C
  User -> User D                              |---> User D
  ...N-1 sends                      1 send, GoBot fans out
```

The tradeoff: GoBot is a semi-trusted relay. In standalone mode, it sees message content. In GoKey mode, it does not. This is acceptable because the alternative (client-side fan-out) does not scale to communities of thousands.

### 4.3 DID:key identifiers (not custom format)

GoLab Phase 2 identifies users with W3C DID:key identifiers derived from their Ed25519 public keys. This was chosen over custom identifier formats because:

- Self-describing: the public key IS the identifier, no lookup needed
- Standard: W3C Decentralized Identifier specification
- Interoperable: compatible with the broader DID ecosystem
- Compact: a single string contains all information needed to verify

GoUNITY certificates bind a human-readable username to a DID:key. The username is for display. The DID:key is for cryptographic operations.

### 4.4 Public + Private visibility (not three storage modes)

The original concept proposed three storage modes per channel: plaintext, encrypted-at-rest, and E2E persistent. Season 2 simplified this to a binary visibility flag per post in the hybrid model:

| Visibility | Visible on Clearnet? | Stored on GoLab server? | Use case |
|:-----------|:-------------------|:----------------------|:---------|
| public | Yes | Yes (plaintext, indexed) | Open discussions, knowledge sharing |
| private | No | Encrypted only (SMP path) | Sensitive discussions, members-only |

A post's visibility is set at creation time. The Clearnet server only ever sees public posts. Private posts travel exclusively through the SMP plugin path and are stored encrypted on subscribers' devices.

This is simpler than three modes because the choice maps to a real user question ("should this be findable on Google?") rather than three layers of cryptographic complexity.

### 4.5 Power levels (not simple roles)

Matrix's numeric power level system is more flexible than simple role names. A power level of 50 means "moderator" by default, but communities can redefine thresholds. This allows:

- Different channels within a community to have different thresholds
- Gradual privilege escalation (25 -> 35 -> 50 -> 75)
- Fine-grained control per action type
- Custom roles without code changes

Phase 1 implements four power level tiers (0 guest, 10 member, 50 moderator, 100 owner). Phase 2 will add Trust Levels TL0-TL4 layered on top, which automate progression based on contribution metrics rather than admin decisions.

### 4.6 Curated registration (not open signup)

The largest shift between concept and Phase 1 implementation: registration is not open. The original concept assumed self-service signup, like every other forum platform. Season 2 revealed that this conflicts with the platform's character.

The application-based model works because:

- The community is small enough that human review is feasible (under 100 active users)
- The topic is niche enough that low-quality applicants self-select out before submitting
- Knowledge questions filter for technical depth that AI cannot easily fake
- Personal response within 7 days makes the process feel handled, not bureaucratic
- Read access stays open, so applicants can sample the community before applying

This is not a model that scales to millions of users. It is not meant to. GoLab targets focused developer communities where quality matters more than count.

---

## 5. The two faces of GoLab

### 5.1 Twitter face: Social activity

The social layer gives every approved user a presence in the community:

- **Personal feed:** Posts from followed users and subscribed spaces
- **Discovery feed:** Trending posts, new projects, active discussions
- **Profile page:** Bio, activity history, projects, reputation
- **Posts:** Short-form updates with text, code, links, mentions, hashtags
- **Threads:** Reply chains with nested conversations
- **Reactions:** Like, upvote, or custom reactions (toggle, one per user per post per type)
- **Reposts:** Share content to your followers (ActivityStreams Announce)
- **Follows:** Subscribe to users, spaces, or projects

This is familiar UX for anyone who has used Twitter, Mastodon, or Bluesky. The difference will be invisible in Phase 2: everything is E2E encrypted over SMP queues. The feed aggregation happens on the application server, but the server works with encrypted content (in private mode) or signed plaintext (in public mode).

### 5.2 GitLab face: Project collaboration

The collaboration layer gives teams tools to build software:

- **Projects (Sprint 16):** Named containers between Spaces and Posts with dedicated docs (Concept, Architecture, Workflow, Roadmap as Markdown)
- **Project Seasons:** Sequential development phases with closing documents - the same season model used for tracking GoLab itself
- **Issues:** Bug reports, feature requests with labels, milestones, assignees
- **Merge Requests:** Code review workflow with inline comments and approvals (Phase 2)
- **Wikis:** Collaborative documentation per project
- **Milestones:** Group issues and MRs into release targets
- **Labels:** Organize and filter across projects
- **Teams:** Role-based access (owner, contributor, viewer)
- **Activity log:** All project activity as an ActivityStreams collection

The Project layer is the major Season 3 addition (Sprint 16). It transforms GoLab from a flat post stream into structured collaborative work. Each project has its own header, its own member list, its own document set, and its own posts grouped by season.

Once Sprint 16 ships, GoLab itself becomes a project on GoLab. From Season 4 onward, GoLab development happens publicly inside its own platform - the tool is the workshop. Sister projects (GoBot Season 2-3, GoUNITY Season 4, GoKey Season 3, the SimpleGoX plugin spec) all get their own GoLab projects with their own seasons running in parallel.

Note: GoLab is NOT a git hosting platform. It does not store repositories or run CI/CD. It is a project management and discussion platform - like GitHub Issues + GitHub Discussions + GitHub Projects, without the git hosting part. Code hosting can be self-hosted (Gitea, GitLab) or external - GoLab links to it but does not replace it.

---

## 6. Comparison with existing systems

| Feature | GoLab Phase 1 | GoLab Phase 2 | GitHub | Mastodon | Discourse |
|:--------|:------|:-------|:-------|:---------|:----------|
| Social feeds | Yes | Yes | Limited | Yes | No |
| Project management | Sprint 16+ | Yes | Yes | No | No |
| E2E encrypted | No | Yes | No | No | No |
| Curated access | Yes | Yes | No | No | Optional |
| Server reads content | Yes | Public only | Yes | Yes | Yes |
| Anonymous transport | No | Yes | No | No | No |
| Identity system | Username | Certificate | Account | Account | Account |
| Knowledge filter at signup | Yes | Yes | No | No | No |
| Hardware identity | No | Optional | No | No | No |
| Self-hosted | Yes | Yes | No | Yes | Yes |
| Tor + I2P transport | No | Yes | No | Limited | Limited |
| Open source | AGPL-3.0 | AGPL-3.0 | No | AGPL-3.0 | GPL-2.0 |

---

## 7. What GoLab does NOT do

Being clear about scope prevents feature creep:

- **GoLab is not a git host.** No repository storage, no CI/CD, no code browsing. Use Gitea, GitLab, or GitHub for that. GoLab links to external repositories.
- **GoLab is not a real-time chat.** GoChat exists for that. GoLab is for persistent, threaded discussions and project management.
- **GoLab is not a social media clone.** No algorithmic feeds, no ads, no engagement metrics. Chronological timeline, user-controlled.
- **GoLab is not a blockchain project.** No tokens, no mining, no consensus mechanism. Ed25519 certificates are standard PKI.
- **GoLab is not anonymous by default.** Users have persistent pseudonyms. True anonymity is possible (one-time certificates) but not the default.
- **GoLab is not for mass adoption.** The curation model deliberately scales sub-linearly. Quality and small focused communities are the goal, not user count.
- **GoLab does not federate (yet).** Single-instance focus until the UX is solid. Phase 2+ may introduce federation if the demand is real.

---

## 8. Dependencies and timeline

### Prerequisites for Phase 2 SMP migration

| Component | What GoLab needs from it | Status |
|:----------|:------------------------|:-------|
| simplex-js | SMP transport in browser (deprioritized) | Published |
| SimpleGoX Multi-Messenger | Native plugin host | Pre-alpha |
| SimpleGoX sgx-simplex | Native SMP v9 client | Working (read path) |
| GoBot Go service | SMP connections, command system | In development |
| GoBot community relay | Fan-out, channel management | Not started |
| GoUNITY certificates | Ed25519 cert issuance + CRL | Planned (Season 4) |
| GoKey wire protocol | Hardware security integration | Spec done |
| GoKey firmware | ESP32 crypto operations | Planned (Season 3) |

### GoLab development phases

| Phase | Focus | Status |
|:------|:------|:-------|
| Phase 1 | Concept, application server, Phase 1 features, curated access | **Live** |
| Phase 2a | Project system (Sprint 16) | Season 3 priority |
| Phase 2b | Security hardening (Argon2id, CSP, encryption at rest) | Season 3 |
| Phase 2c | Trust Levels, dynamic knowledge questions | Season 3-4 |
| Phase 2d | SMP adapter prototype, simplex-chat CLI integration | Season 4+ |
| Phase 2e | SimpleGoX plugin specification and reference plugin | Season 4-5 |
| Phase 2f | GoUNITY certificate integration | Season 5 |
| Phase 2g | Tor Onion + I2P deployment | Season 5-6 |
| Phase 3 | Hardware identity (GoKey) | Season 6+ |
| Phase 4 | Federation and bridging (optional, demand-driven) | TBD |

This timeline is conservative. Phase 2 is a multi-season effort, not a single sprint. Each prerequisite component (GoBot, GoUNITY, GoKey, SimpleGoX) has its own development track that GoLab does not control.

---

## 9. Comparable architectures

| System | Architecture pattern | How GoLab differs |
|:-------|:--------------------|:-----------------|
| GitHub/GitLab | Centralized server, full trust | GoLab Phase 2: server is blind for private content |
| Mastodon/Fediverse | Federated HTTP, ActivityPub | GoLab Phase 2: SMP transport, E2E encrypted private path |
| Nostr | Relay-based, signed events | GoLab: SMP anonymous transport, certificate identity, curated |
| Matrix | Federated rooms, event DAG | GoLab: no server identity, relay fan-out, hybrid public/private |
| Keybase | Signed chains, team management | GoLab: no central server, SMP transport, focused community |
| Secure Scuttlebutt | P2P gossip, append-only logs | GoLab: relay-assisted, scales beyond P2P, curated entry |
| Cwtch | Tor onion services, untrusted relays | GoLab: SMP + Tor + I2P, certificate identity, application layer |
| Discourse | Open registration, full server trust | GoLab: curated access, knowledge filter, Phase 2 zero-trust |

GoLab sits at a unique intersection: it combines the social features of Nostr/Mastodon, the project management of GitLab, the transport privacy of SimpleX, the identity model of GoUNITY certificates, the hardware security option of GoKey, and the curation discipline of an old-school invite-only forum. No existing system occupies this design space.

---

## 10. Decisions resolved in Season 2

The Season 1 concept document had a list of open questions. Most are now decided:

| Question | Decision |
|:---------|:---------|
| Application server language | **Go 1.24** (confirmed, ecosystem consistency with GoBot) |
| Database for persistence | **PostgreSQL 16** (confirmed, scales + full-text search) |
| Open vs curated registration | **Curated** (Season 2: 11-step wizard, knowledge questions, admin review) |
| Email field | **Permanently dropped** (Migration 026, no SMTP infrastructure intentional) |
| Login method | **Username** (after email column drop) |
| Password hashing | **bcrypt cost 12** (Argon2id migration planned for Season 3) |
| Phase 2 transport approach | **Hybrid: Clearnet read + SimpleGoX write** (browser-native SMP not feasible) |
| Visibility model | **Per-post public/private flag** (replaces three-mode storage proposal) |
| Federation between GoLab instances | **Deferred to Phase 4+** |
| Code hosting integration | **Link-only** (confirmed, GoLab is discussion + project mgmt) |
| Mobile client | **Responsive web** (confirmed for Phase 1, 375px to ultrawide) |
| Project layer (Sprint 16) | **Adopted** (Spaces > Projects > Seasons > Posts) |
| Anti-AI bot strategy | **Knowledge questions** (V1 static, V2 dynamic, V3 AI-generated) |
| Account recovery without email | **Hardware-backed in Phase 2** (GoKey + new cert from GoUNITY) |

---

## 11. Phase 1 implementation notes

Phase 1 is the live Go + PostgreSQL platform at lab.simplego.dev. This section documents the specific technical decisions made during Season 1 and Season 2 development.

### 11.1 HTMX + Alpine.js instead of React / TypeScript

The original concept sketched a TypeScript browser client built on simplex-js. Phase 1 ships a server-rendered HTML UI with HTMX for page transitions and Alpine.js for local interactivity.

Why:
- No build step. `docker-compose up -d` and you are running.
- Zero npm dependencies in production. Nothing to audit monthly.
- SSR means every page works without JS (graceful degradation).
- HTMX + Alpine is ~40 KB minified, the whole UI layer.
- When Phase 2 arrives, the SimpleGoX plugin can layer on top without rewriting the UI.

### 11.2 Quill.js 2.0.3 for the editor

A plain textarea was tried first. Users wanted rich text, image upload, syntax highlighting, and emoji. Quill 2.0.3 provides all of that with a stable API and no CDN dependency.

Why Quill and not alternatives:
- TipTap pulls in ProseMirror + a Node build chain (rejected)
- CKEditor is AGPL-incompatible at the enterprise tier (rejected)
- Trix is too minimal for code blocks (rejected)
- Quill 2.0.3 is MIT-licensed, ~200 KB, self-hostable

The editor output is sanitized with bluemonday before storage. Quill's raw HTML is NEVER trusted.

### 11.3 Spaces instead of Channels

The concept uses "Channels" throughout. Phase 1 ships "Spaces" as the primary content organization. The underlying database still has a `channels` table (kept for Phase 2), but the UI and routes are space-oriented.

Why:
- Channels in the Matrix sense imply user creation and per-space permissions. Phase 1 needs stability, not UGC organization.
- 8 admin-curated Spaces give newcomers a clear map: "where do I post about SimpleX?" -> SimpleX Protocol space.
- Post Types (Discussion / Question / Tutorial / Code / Showcase / Link / Announcement) give the flexibility Channels would have, without the permission complexity.
- If Phase 2 wants user-created Channels back, the `channels` table is still there.

Sprint 16 adds a Project layer between Spaces and Posts, refining the hierarchy further: Spaces > Projects > Seasons > Posts. Spaces stay admin-fixed. Projects can be created by approved members.

### 11.4 Admin-fixed categories, not user-created

The 8 Spaces are seeded by migration 020. Users cannot create new Spaces. Tags ARE user-created and provide cross-Space discovery.

Why:
- New communities die in empty categories. Fewer, well-used Spaces beat dozens of ghost towns.
- Spam defense: a spammer cannot flood the platform with 100 fake Spaces. They can create tags, but tags lurk in a pool, not in the navigation.
- Migration cost is low: a new Space is one INSERT statement.

### 11.5 No GIF API (Tenor shutdown, GIPHY paid)

The editor has an emoji picker but no GIF picker.

Why:
- Tenor's public API was shut down in 2024
- GIPHY's remaining API requires commercial licensing
- Self-hosting a GIF library has content-moderation implications that Phase 1 is not built to handle
- Users CAN upload GIFs as images; they just lack a search UI

This may be revisited if a privacy-respecting GIF source emerges.

### 11.6 Self-hosted all JS / CSS libraries

Zero runtime CDN calls. Every library (Quill, HTMX, Alpine, highlight.js, Mermaid, KaTeX, emoji picker) is vendored into `web/static/` and served from the same origin as the app.

Why:
- A CDN is a tracking vector. unpkg.com sees every page load.
- A CDN is a supply-chain vector. Compromise cdnjs, compromise GoLab.
- A CDN is a privacy leak. Users' IPs flow to third parties.
- A CDN is a dependency risk. Phase 2 promises "no server reads your private content" - CDNs would silently break that promise.

The Dockerfile copies `web/static/` into the final image. There is no `npm install` at runtime.

### 11.7 User moderation via status field, not a separate table

Users have a `status` column (`active` / `pending` / `rejected`) plus `reviewed_at` and `reviewed_by`. New registrations land in `pending` until an admin reviews their application. Rejected users get a clear notification and can re-apply after a cooldown (planned Season 3).

Why:
- Adding a column is reversible via the existing live-DB rules
- A separate table would duplicate user references and make the approval queue a join
- The `SetStatus` method is the single write path; no chance of three tables drifting

### 11.8 Session management via database rows

Sessions are bcrypt-gated but NOT JWT-based. The `sessions` table stores `(id, user_id, expires_at)` rows. Every request re-reads the session and the user. Password change = `DELETE FROM sessions WHERE user_id = $1`, which revokes every device.

Why:
- JWTs cannot be revoked without a blocklist table, which puts us back at DB-per-request
- DB-per-request is ~1 ms and trivial to optimize later
- The `__Host-` cookie prefix gives us path confinement in production; dev uses a plain `session_id` cookie because `__Host-` requires HTTPS

### 11.9 First-user auto-promote with handler-level fallback

The first registered user is auto-promoted to power_level 100 (owner) and auto-approved (skipping the application queue). This is implemented in two layers:

- UserStore code path: checks `COUNT(*) FROM users` before insert; if zero, the new user is created with power_level 100 and status active
- Handler-level fallback in RegisterHandler: same check after creation, defense-in-depth for edge cases

Why two layers:
- Migration 012's blind `UPDATE users SET power_level = 100 WHERE id = 1` runs once at migration time. On a fresh database with no users, it hits zero rows, and the first registrant ends up at power_level 10 with no admin
- Belt-and-suspenders prevents the no-admin failure mode that bricked early test deployments

### 11.10 Brutalist registration wizard

Sprint Y.3 redesigned the registration flow as an 11-step brutalist wizard inspired by Stripe Atlas, Linear's atmosphere, and Vercel's typography discipline. Key choices:

- **One question per step.** No scrolling within a step. Each question gets full attention.
- **280px cyan sidebar with full step list.** Always-visible progress, contextual quotes per step.
- **Direction-aware transitions.** Forward and backward have mirrored slide animations.
- **Cinematic stagger on element entry.** Cascade timing 60/220/380/540/700ms. animation-fill-mode: backwards is critical (without it, elements flash visible during their delay period).
- **Content-driven height.** No min-height: 100dvh anywhere. Wizard expands to fit its content, footer sits naturally below input rather than at viewport bottom.
- **Live username availability check.** 7 status states (idle/checking/available/taken/reserved/invalid/error). 17-entry reserved-name blocklist enforced server-side.
- **@username personalization.** Once a user picks a handle in step 2, subsequent steps reference them by name on 4 strategic touchpoints (3, 4, 7, 10).
- **Keyboard-first navigation.** Enter advances, Esc goes back, Tab navigates form. Visual hints update contextually.

The full implementation history is in the Season 2 protocol. The lessons from getting it wrong (cramped centered cards, generic animations, missing layout fixes, all-at-once element appearance) shaped the principles above.

---

*GoLab Technical Concept v3 - April 2026 (Season 2 close)*
*IT and More Systems, Recklinghausen, Germany*
