<p align="center">
  <strong>GoLab</strong>
</p>

<p align="center">
  <strong>The world's first privacy-first developer community platform on encrypted messaging.</strong><br>
  GitLab-style collaboration meets Twitter-style activity feeds - over SimpleX SMP.<br>
  No accounts. No tracking. No admin reads your posts. E2E encrypted by design.<br>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg" alt="License"></a>
  <a href="#status"><img src="https://img.shields.io/badge/status-concept-yellow.svg" alt="Status"></a>
  <a href="https://github.com/saschadaemgen/SimpleGo"><img src="https://img.shields.io/badge/ecosystem-SimpleGo-green.svg" alt="SimpleGo"></a>
</p>

---

> "Every developer community platform works the same way: you create an account, the server stores your data, the admin can read everything, and your activity profile follows you forever. GitLab, GitHub, Discourse, Reddit - they all assume that the server is trustworthy. GoLab assumes it is not."

GoLab is a developer community platform that combines GitLab-style project collaboration (repositories, issues, merge requests, wikis) with Twitter-style social features (activity feeds, posts, follows, reposts) - but every message travels through SimpleX SMP queues with full E2E encryption. The server that relays your posts cannot read them, cannot identify you, and cannot build a profile of your activity.

Your identity is not an account on a server. It is an Ed25519 certificate issued by [GoUNITY](https://github.com/saschadaemgen/GoUNITY) - a cryptographic proof that you are who you claim to be, without revealing who that is. Moderation is handled by [GoBot](https://github.com/saschadaemgen/GoBot), which enforces community rules without ever seeing message content in its hardware-secured mode. And if you want physical proof of identity, plug in a [SimpleGo](https://github.com/saschadaemgen/SimpleGo) device and verify with a hardware challenge-response that no software can fake.

This architecture has no precedent. No existing platform combines anonymous transport, persistent pseudonymous identity, scalable community features, and hardware-backed verification in a single system.

---

## What GoLab does

GoLab is two things in one:

**A social platform (like Twitter):** Post updates, follow people, build an activity feed, react to posts, repost content, discover communities. Every interaction is an ActivityStreams 2.0 object transported over SMP queues - standardized, extensible, and fully encrypted.

**A collaboration platform (like GitLab):** Create projects, track issues, review code, discuss in threads, manage teams with role-based permissions. Every collaboration artifact is signed by the author's Ed25519 certificate - verifiable, tamper-proof, and independent of any server.

**What makes it different from everything else:**

| Feature | GitHub/GitLab | Twitter/X | Mastodon | Nostr | GoLab |
|:--------|:-------------|:----------|:---------|:------|:------|
| Project management | Yes | No | No | No | Yes |
| Activity feeds | Limited | Yes | Yes | Yes | Yes |
| E2E encrypted | No | No | No | No | Yes |
| No user accounts on server | No | No | No | No | Yes |
| Server cannot read content | No | No | No | No | Yes |
| Persistent identity | Server account | Server account | Server account | Public key (visible) | Ed25519 cert (anonymous) |
| Metadata protection | No | No | No | No | SMP queues |
| Hardware identity | No | No | No | No | Optional (GoKey/SimpleGo) |
| Ban evasion resistant | Weak | Weak | Weak | None | Certificate-based |

---

## How it works

```
[Browser / GoLab Client]
  |
  | ActivityStreams messages (Create/Note, Follow, Like, ...)
  | signed with Ed25519 certificate from GoUNITY
  |
  v
[simplex-js / SMP Transport]
  |
  | E2E encrypted, no user IDs, queue-based
  | Server sees only encrypted 16 KB blocks
  |
  v
[GoBot Community Relay]
  |
  | Receives encrypted blocks
  | Fans out to all channel subscribers
  | Enforces permissions and moderation
  | In GoKey mode: cannot read any content
  |
  +---> [Subscriber A via SMP queue]
  +---> [Subscriber B via SMP queue]
  +---> [Subscriber C via SMP queue]
        Each subscriber has a unique queue pair
        Relay cannot correlate subscribers across channels
```

### Posting in a channel

```
1. User writes "Fixed the memory leak in GoChat" in #gochat-dev

2. GoLab client creates ActivityStreams object:
   {"type": "Create", "object": {"type": "Note", "content": "..."}}
   Signs it with Ed25519 private key from GoUNITY certificate

3. simplex-js encrypts and sends via SMP queue to GoBot relay

4. GoBot receives encrypted block
   In GoKey mode: forwards to ESP32 for decryption and command check
   GoBot itself never sees the plaintext

5. GoBot fans out the encrypted message to all #gochat-dev subscribers
   Each subscriber receives via their own SMP queue
   Subscriber queues are pairwise - relay cannot link users

6. Subscriber clients decrypt, verify Ed25519 signature, display post
   Signature proves: this post is from "CryptoNinja42"
   Certificate proves: "CryptoNinja42" is GoUNITY-verified
```

### Identity verification

```
1. User registers at id.simplego.dev (GoUNITY)
   Receives Ed25519 certificate + private key

2. User joins GoLab community
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

- **Activity feeds** - personalized timeline of followed users and channels
- **Posts** - short-form updates with text, links, code snippets
- **Reposts** - share others' posts to your followers (ActivityStreams Announce)
- **Reactions** - like, upvote, or custom reactions on any post
- **Follows** - subscribe to users or channels
- **Threads** - reply chains with nested conversations
- **Discovery** - find communities, users, and projects by topic
- **Profiles** - pseudonymous identity with bio, activity history, reputation

### Collaboration features (GitLab-style)

- **Projects** - repositories with description, README, team members
- **Issues** - bug reports, feature requests, discussions with labels and milestones
- **Merge requests** - code review workflow with inline comments
- **Wikis** - collaborative documentation per project
- **Teams** - role-based access control (owner, maintainer, developer, reporter, guest)
- **Milestones** - group issues and MRs into release targets
- **Labels and tags** - organize and filter across projects

### Moderation and identity

- **GoUNITY certificates** - verified pseudonymous identity, ban-evasion resistant
- **GoBot moderation** - automated rule enforcement without reading content
- **Role-based permissions** - Matrix-style power levels per channel and project
- **Reports** - community members flag content, moderators review
- **CRL enforcement** - revoked certificates are rejected across all communities
- **Hardware verification** - optional GoKey/SimpleGo challenge-response for physical trust

---

## Architecture

GoLab is not a monolith. It is composed of existing SimpleGo ecosystem components:

| Component | Role in GoLab | Repository |
|:----------|:-------------|:-----------|
| **GoLab** | Community application server + browser client | This repo |
| [GoBot](https://github.com/saschadaemgen/GoBot) | Community relay + moderation engine | [GoBot repo](https://github.com/saschadaemgen/GoBot) |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware crypto for GoBot (optional) | [SimpleGo repo](https://github.com/saschadaemgen/SimpleGo) |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) | Certificate authority for identity | [GoUNITY repo](https://github.com/saschadaemgen/GoUNITY) |
| [simplex-js](https://www.npmjs.com/package/simplex-js) | SMP transport for browser clients | [GoChat repo](https://github.com/saschadaemgen/GoChat) |

```
+------------------------------------------------------------------+
|                                                                  |
|  GoLab Application Server (Go)                                   |
|  Community logic, channel registry, post persistence,            |
|  activity stream aggregation, search index                       |
|                                                                  |
+---------------------------+--------------------------------------+
                            |
                    internal API
                            |
+---------------------------v--------------------------------------+
|                                                                  |
|  GoBot (Go service on VPS)                                       |
|  Community relay: SMP connections, fan-out, moderation,          |
|  permission enforcement, GoUNITY certificate verification        |
|                                                                  |
+---------------------------+--------------------------------------+
                            |
                     SMP queues (E2E encrypted)
                            |
+---------------------------v--------------------------------------+
|                                                                  |
|  GoLab Browser Client (TypeScript)                               |
|  Built on simplex-js + GoChat widget architecture                |
|  Activity feeds, project views, post composer, profiles          |
|                                                                  |
+------------------------------------------------------------------+

+-------------------------------+  +-------------------------------+
|                               |  |                               |
|  GoUNITY (separate server)    |  |  GoKey (ESP32, optional)      |
|  Certificate issuance         |  |  Hardware crypto for GoBot    |
|  CRL distribution             |  |  Hardware identity for users  |
|  id.simplego.dev              |  |  Challenge-response verify    |
|                               |  |                               |
+-------------------------------+  +-------------------------------+
```

### Message format

GoLab uses [ActivityStreams 2.0](https://www.w3.org/TR/activitystreams-core/) as its message vocabulary, transported over SMP queues instead of HTTP. Every message is a signed JSON object:

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

### What the GoLab server knows

| Data | Visible to server? |
|:-----|:-------------------|
| Post content | No (E2E encrypted via SMP) |
| User identity | No (only queue addresses) |
| Who posted what | No (GoBot relay mode) |
| Who follows whom | No (pairwise SMP queues) |
| Channel membership list | Queue addresses only, not identities |
| IP addresses | SMP server sees them, GoLab does not |

### What makes GoLab different from "encrypted" platforms

Most platforms that claim encryption still have a server that manages accounts, stores metadata, and knows the social graph. GoLab has none of that:

- **No accounts on the server.** Your identity is a certificate in your browser/device.
- **No social graph on the server.** Follow relationships exist as SMP queue pairs that the server cannot correlate.
- **No content on the server.** Posts are encrypted in transit and only stored on subscribers' devices (or optionally on encrypted persistence nodes).
- **No metadata correlation.** Each channel subscription uses a separate SMP queue pair. The relay cannot link your activity across channels.

See [Architecture and Security](docs/ARCHITECTURE_AND_SECURITY.md) for the full threat model.

---

## Current status

| Component | Status |
|:----------|:-------|
| GoLab concept and architecture | Season 1 - this document |
| GoLab application server | Planned |
| GoLab browser client | Planned |
| GoBot community relay extensions | Planned (after GoBot Season 2-3) |
| GoUNITY certificate integration | Planned (after GoUNITY Season 4) |
| simplex-js transport layer | Available (npm: simplex-js@1.0.0) |
| GoKey hardware identity | Planned (after GoKey Season 3) |

### Dependencies

GoLab builds on components that are in active development:

| Dependency | Required for | Status |
|:-----------|:------------|:-------|
| [GoBot](https://github.com/saschadaemgen/GoBot) Season 2-3 | Community relay + moderation | In development |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) Season 4 | Certificate-based identity | Planned |
| [simplex-js](https://www.npmjs.com/package/simplex-js) | Browser SMP transport | Published (1.0.0) |
| [GoChat](https://github.com/saschadaemgen/GoChat) widget architecture | Browser client foundation | Published (1.0.0) |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware identity (optional) | Planned |

---

## Setup (planned)

```bash
git clone https://github.com/saschadaemgen/GoLab.git
cd GoLab

# GoLab application server
make build
make run

# Or with Docker
docker-compose up -d
```

**Requirements:**
- GoBot instance (community relay)
- GoUNITY instance (certificate authority)
- SMP server with WebSocket support (e.g., smp.simplego.dev)

---

## Documentation

| Document | Description |
|:---------|:-----------|
| [Architecture and Security](docs/ARCHITECTURE_AND_SECURITY.md) | Technical architecture, threat model, security analysis |
| [Concept](docs/CONCEPT.md) | High-level vision, design decisions, technology choices |
| [Season Index](docs/seasons/SEASON-INDEX.md) | Links to all season documentation |

### Related documentation in other repos

| Document | Description |
|:---------|:-----------|
| [GoBot System Architecture](https://github.com/saschadaemgen/GoBot/blob/main/docs/SYSTEM-ARCHITECTURE.md) | Full GoBot + GoKey + GoUNITY system design |
| [GoBot Concept](https://github.com/saschadaemgen/GoBot/blob/main/docs/CONCEPT.md) | GoBot technical concept with GoLab integration |
| [GoKey Wire Protocol](https://github.com/saschadaemgen/GoBot/blob/main/docs/GOKEY-WIRE-PROTOCOL.md) | Communication protocol between GoBot and GoKey |
| [GoUNITY Architecture](https://github.com/saschadaemgen/GoUNITY/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) | Certificate authority design and security |

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
  <strong>Your server relays the messages. Your certificate proves your identity. Nobody reads your posts.</strong>
</p>
