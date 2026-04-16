# GoLab Architecture & Security

**Document version:** Season 1 | April 2026
**Component:** GoLab community platform (application server + browser client)
**Copyright:** 2026 Sascha Daemgen, IT and More Systems, Recklinghausen
**License:** AGPL-3.0

---

## Overview

GoLab is a privacy-first developer community platform that combines GitLab-style project collaboration with Twitter-style social features. All communication is transported over SimpleX SMP queues with E2E encryption. Identity is certificate-based via GoUNITY. Moderation is handled by GoBot with optional GoKey hardware security.

| Property | Details |
|:---------|:--------|
| Application server | Go |
| Browser client | TypeScript (built on simplex-js + GoChat architecture) |
| Message format | ActivityStreams 2.0 JSON over SMP |
| Identity | Ed25519 certificates (GoUNITY) |
| Transport | SimpleX SMP queues (E2E encrypted) |
| Relay | GoBot (community relay mode) |
| Moderation | GoBot commands + GoUNITY certificate enforcement |
| Hardware identity | GoKey/SimpleGo ESP32-S3 (optional) |
| Persistence | Application server database (posts, channels, indexes) |
| Domain | lab.simplego.dev (planned) |

---

## 1. System architecture

### 1.1 Component overview

GoLab consists of four layers, each handled by a separate component:

```
Layer 4: GoLab Application         Channels, posts, projects, feeds
Layer 3: GoBot Community Relay     Fan-out, permissions, moderation
Layer 2: GoUNITY Identity          Certificates, verification, bans
Layer 1: SMP Transport             E2E encrypted queues (simplex-js)
```

No single component has access to all data. The SMP server sees only encrypted blocks. GoBot (in GoKey mode) sees only encrypted blocks. GoUNITY sees only certificate registrations, not community activity. Only the end-user client has the complete picture.

### 1.2 Full system diagram

```
+------------------------------------------------------------------+
|                                                                  |
|  GoLab Application Server (Go)          lab.simplego.dev         |
|                                                                  |
|  - Channel registry (which channels exist, metadata)             |
|  - Post persistence (encrypted or plaintext, configurable)       |
|  - Activity stream aggregation                                   |
|  - Search index                                                  |
|  - Project management (issues, MRs, wikis)                       |
|  - REST/WebSocket API for browser client                         |
|                                                                  |
+---------------------------+--------------------------------------+
                            |
                    internal API (localhost or mTLS)
                            |
+---------------------------v--------------------------------------+
|                                                                  |
|  GoBot Community Relay (Go)             VPS service              |
|                                                                  |
|  - Holds SMP connections to all subscribers                      |
|  - Receives posts via SMP queues                                 |
|  - Fans out to channel subscribers                               |
|  - Enforces permissions (role checks)                            |
|  - Executes moderation commands                                  |
|  - Verifies GoUNITY certificates                                 |
|  - In GoKey mode: forwards encrypted blocks to ESP32             |
|                                                                  |
+---------------------------+--------------------------------------+
                            |
              SMP queues (E2E encrypted, per-subscriber)
                            |
         +------------------+------------------+
         |                  |                  |
+--------v----+   +---------v---+   +----------v--+
|             |   |             |   |             |
| Browser     |   | Browser     |   | SimpleGo    |
| Client A    |   | Client B    |   | Device C    |
| (simplex-js)|   | (simplex-js)|   | (ESP32)     |
|             |   |             |   |             |
+-------------+   +-------------+   +-------------+

+-------------------------------+  +-------------------------------+
|                               |  |                               |
|  GoUNITY CA                   |  |  GoKey (optional)             |
|  id.simplego.dev              |  |  ESP32-S3 at home             |
|                               |  |                               |
|  - Certificate issuance       |  |  - Decrypt blocks for GoBot   |
|  - CRL distribution           |  |  - Hardware identity verify   |
|  - Challenge-response API     |  |  - Sign moderation commands   |
|  - Contacted only at          |  |  - Message plaintext never    |
|    registration + CRL sync    |  |    leaves the device          |
|                               |  |                               |
+-------------------------------+  +-------------------------------+
```

### 1.3 GoLab Application Server

The application server is the only new component. It handles community-specific logic that does not belong in GoBot (which remains a general-purpose moderation relay).

| Responsibility | Details |
|:---------------|:--------|
| Channel registry | Tracks which channels exist, their metadata, and configuration |
| Post persistence | Stores posts for history and search (encrypted at rest) |
| Activity streams | Aggregates feeds per user based on subscriptions |
| Project management | Issues, merge requests, wikis, milestones, labels |
| Search | Full-text search across posts and projects |
| API | REST + WebSocket for browser clients |

The application server communicates with GoBot via internal API. It does NOT hold SMP connections directly - all SMP communication goes through GoBot.

### 1.4 GoBot as community relay

GoBot receives new community relay responsibilities in addition to its existing moderation role:

| Existing (moderation) | New (community relay) |
|:----------------------|:----------------------|
| !kick, !ban, !mute | Channel create/delete/configure |
| !verify (GoUNITY) | Subscribe/unsubscribe management |
| Permission checks | Fan-out posts to subscribers |
| CRL enforcement | Forward posts to application server |
| GoKey integration | Activity stream events |

GoBot manages the SMP queue pairs. When a user subscribes to a channel, GoBot establishes an SMP queue pair between itself and the user's client. When a post arrives, GoBot fans it out to all subscriber queues.

---

## 2. Message protocol

### 2.1 ActivityStreams 2.0 over SMP

All GoLab messages follow the [W3C ActivityStreams 2.0](https://www.w3.org/TR/activitystreams-core/) specification. The transport changes from HTTP (as in ActivityPub) to SMP queues, but the message format remains standard ActivityStreams JSON.

Every message has three layers:

```
Layer 1: SMP envelope (16 KB padded, encrypted)
  Layer 2: GoLab routing header (channel, message type)
    Layer 3: ActivityStreams payload (signed JSON)
```

### 2.2 GoLab routing header

```json
{
  "golab_version": 1,
  "channel_id": "golab:channel:gochat-dev",
  "msg_type": "activity",
  "timestamp": "2026-04-16T10:30:00.000Z",
  "payload": { ... ActivityStreams object ... }
}
```

| Field | Type | Required | Description |
|:------|:-----|:---------|:------------|
| golab_version | integer | yes | Protocol version (1) |
| channel_id | string | yes | Target channel or user DM |
| msg_type | string | yes | activity, subscribe, unsubscribe, system |
| timestamp | string | yes | ISO 8601 with milliseconds |
| payload | object | yes | ActivityStreams 2.0 object |

### 2.3 Message types

#### Social messages (Twitter-style)

**Post (Create + Note)**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Create",
  "id": "golab:activity:sha256-content-hash",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "published": "2026-04-16T10:30:00Z",
  "to": ["golab:channel:gochat-dev"],
  "object": {
    "type": "Note",
    "content": "Fixed the memory leak in the WebSocket handler",
    "tag": [
      {"type": "Hashtag", "name": "#bugfix"},
      {"type": "Mention", "href": "did:key:z6Mkother..."}
    ]
  },
  "proof": {
    "type": "Ed25519Signature2020",
    "verificationMethod": "did:key:z6Mkf5rGMoatr...",
    "proofValue": "z..."
  }
}
```

**Repost (Announce)**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Announce",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "object": "golab:activity:sha256-original-hash",
  "to": ["golab:channel:general"],
  "proof": { ... }
}
```

**Reaction (Like)**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Like",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "object": "golab:activity:sha256-target-hash",
  "proof": { ... }
}
```

**Follow**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Follow",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "object": "did:key:z6Mkother...",
  "proof": { ... }
}
```

#### Collaboration messages (GitLab-style)

**Create Issue**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Create",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "to": ["golab:project:gochat"],
  "object": {
    "type": "Note",
    "golab:objectType": "Issue",
    "name": "WebSocket disconnects after 30 minutes idle",
    "content": "Steps to reproduce: ...",
    "golab:labels": ["bug", "websocket"],
    "golab:milestone": "v1.1.0",
    "golab:assignee": "did:key:z6Mkother..."
  },
  "proof": { ... }
}
```

**Close Issue (Update)**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Update",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "object": {
    "id": "golab:issue:gochat-42",
    "golab:status": "closed",
    "golab:resolution": "fixed"
  },
  "proof": { ... }
}
```

**Merge Request**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Create",
  "actor": "did:key:z6Mkf5rGMoatr...",
  "to": ["golab:project:gochat"],
  "object": {
    "type": "Note",
    "golab:objectType": "MergeRequest",
    "name": "fix: resolve WebSocket idle timeout",
    "content": "This MR fixes #42 by implementing keepalive pings...",
    "golab:sourceBranch": "fix/ws-timeout",
    "golab:targetBranch": "main",
    "golab:reviewers": ["did:key:z6Mkreviewer..."]
  },
  "proof": { ... }
}
```

#### Moderation messages

**Ban (via GoBot)**
```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Block",
  "actor": "golab:bot:gobot-instance-1",
  "object": "did:key:z6Mkbanned...",
  "target": "golab:channel:general",
  "summary": "Spam",
  "golab:certificate_username": "SpamUser42",
  "golab:ban_type": "permanent",
  "proof": { ... }
}
```

### 2.4 Message signing

Every GoLab message is signed with the sender's Ed25519 private key (from their GoUNITY certificate).

**Signed data:** Canonical JSON of the ActivityStreams object (keys sorted alphabetically, no whitespace, UTF-8).

**Verification:** Any recipient can verify the signature using the sender's public key from their GoUNITY certificate. No server interaction needed.

**Integrity chain:** Each message includes a content-addressed ID (`sha256` hash of the canonical payload). Replies reference parent IDs, creating a verifiable thread structure.

---

## 3. Channel architecture

### 3.1 Channel types

| Type | Description | Visibility | Who can post |
|:-----|:-----------|:-----------|:-------------|
| Public | Open to all, discoverable | Everyone | Verified members |
| Private | Invite-only, hidden | Members only | Members only |
| Project | Tied to a project | Project team | Project team |
| DM | Direct message (1:1) | Two parties | Two parties |
| Announce | Read-only broadcast | Everyone | Admins only |

### 3.2 Channel lifecycle

```
Create:
  1. Admin sends Create+Group activity to GoBot
  2. GoBot registers channel in application server
  3. GoBot creates SMP queue pair for the channel
  4. Channel appears in discovery (if public)

Subscribe:
  1. User sends Follow activity targeting channel
  2. GoBot verifies: user has valid GoUNITY certificate
  3. GoBot checks: channel allows this user (public/invite)
  4. GoBot establishes SMP queue pair with user's client
  5. User receives confirmation + channel history

Post:
  1. User sends Create+Note to channel via SMP
  2. GoBot receives, verifies certificate + permissions
  3. GoBot forwards to application server (persistence)
  4. GoBot fans out to all subscriber queues
  5. Subscribers receive, verify signature, display

Unsubscribe:
  1. User sends Undo+Follow activity
  2. GoBot removes SMP queue pair
  3. User no longer receives channel messages
```

### 3.3 Fan-out architecture

GoBot acts as a relay node. Instead of SimpleX's client-side fan-out (every member sends to every other member), GoBot handles distribution:

```
Client-side fan-out (SimpleX groups):
  N members = N*(N-1)/2 queue pairs = O(n^2)
  100 members = 4,950 queue pairs
  Each message sent N-1 times by the sender

GoBot relay fan-out (GoLab channels):
  N members = N queue pairs to GoBot = O(n)
  100 members = 100 queue pairs
  Each message sent once by sender, GoBot copies to N-1 queues
  10,000 members = 10,000 queue pairs (still manageable)
```

This is the same pattern that SimpleX itself plans with "super-peers", but formalized and managed by GoBot.

### 3.4 Unlinkability across channels

Each channel subscription creates a separate SMP queue pair between the user and GoBot. This means:

- GoBot knows Queue-A is subscribed to #gochat-dev
- GoBot knows Queue-B is subscribed to #simplego-hardware
- GoBot does NOT know Queue-A and Queue-B belong to the same person
- The SMP server sees even less - only encrypted block deliveries

Users who want maximum privacy use a separate SMP connection per channel. Users who prefer convenience can multiplex channels over fewer connections (reduced privacy but still E2E encrypted).

---

## 4. Permission system

### 4.1 Power levels

GoLab uses a numeric power level system inspired by Matrix, enforced via GoUNITY certificates and GoBot:

| Level | Role | Permissions |
|:------|:-----|:------------|
| 100 | Owner | All actions, transfer ownership, delete community |
| 75 | Admin | Manage channels, manage members, configure settings |
| 50 | Moderator | Kick, ban, mute, delete posts, review reports |
| 25 | Contributor | Post, create issues, submit MRs, comment |
| 10 | Member | Post in open channels, react, follow |
| 0 | Guest | Read public channels only |

### 4.2 Permission enforcement

Permissions are checked at two points:

**GoBot (relay layer):** Checks sender's certificate and role before accepting a message for fan-out. Rejects unauthorized messages immediately - they never reach subscribers.

**Client (display layer):** Verifies signatures and certificates locally. Even if GoBot is compromised and forwards an unauthorized message, the client rejects it because the signature does not match the required permission level.

### 4.3 Role assignment

Roles are stored as signed statements by community administrators:

```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Add",
  "actor": "did:key:z6Mkadmin...",
  "object": "did:key:z6Mknewmod...",
  "target": "golab:channel:general",
  "golab:role": "moderator",
  "golab:power_level": 50,
  "proof": { ... }
}
```

Role changes are ActivityStreams Add/Remove activities, signed by an actor with sufficient power level. They are distributed like any other message and stored by the application server.

---

## 5. Identity integration

### 5.1 GoUNITY certificate usage

GoLab uses GoUNITY certificates at three points:

| Point | What happens |
|:------|:-------------|
| Registration | User presents GoUNITY certificate to GoBot via DM |
| Every message | Message signed with certificate's private key |
| Moderation | Bans linked to certificate username, not SMP queue |

GoUNITY is contacted ONLY during initial certificate issuance (id.simplego.dev) and daily CRL sync. GoUNITY never knows which communities a user joins.

### 5.2 DID:key identifiers

User identities in GoLab messages use the W3C [DID:key](https://w3c-ccg.github.io/did-method-key/) format:

```
did:key:z6Mkf5rGMoatrSj1f4CyvuHBeXJELe9RPdzo2PKGNCKVtZxP
        ^^^^ Ed25519 multicodec prefix + 32-byte public key
```

This is a self-describing identifier derived directly from the Ed25519 public key. No resolution service needed - the public key IS the identifier.

### 5.3 Hardware identity (optional)

Users with a GoKey or SimpleGo device can bind their GoUNITY certificate to physical hardware:

```
1. Device certificate signed by GoUNITY CA
   Contains: device Ed25519 public key (from eFuse)
   Linked to: user's GoUNITY identity certificate

2. Challenge-response verification
   GoBot sends random 32-byte nonce
   Device signs nonce (private key never leaves hardware)
   GoBot verifies: signature matches device certificate
   Proof: user has physical possession of the device

3. Display in GoLab
   "CryptoNinja42" [verified] [hardware] 
   Other users see: this identity is backed by physical hardware
```

Hardware verification is always optional. It provides stronger identity guarantees for high-trust communities but is not required for basic participation.

### 5.4 Profile data

Profiles are NOT stored on the server. They are ActivityStreams Actor objects distributed via SMP:

```json
{
  "@context": "https://www.w3.org/ns/activitystreams",
  "type": "Person",
  "id": "did:key:z6Mkf5rGMoatr...",
  "name": "CryptoNinja42",
  "summary": "GoChat contributor. Privacy advocate.",
  "golab:verification": "gounity-verified",
  "golab:hardware": true,
  "golab:joined": "2026-04-01",
  "proof": { ... }
}
```

Profile updates are signed Update activities. Any recipient can cache the latest profile, but the authoritative version always comes from the user's own signed messages.

---

## 6. Persistence and search

### 6.1 Storage model

The GoLab application server stores community data for history and search. Three storage modes are planned:

| Mode | Content on server | Who can read | Use case |
|:-----|:-----------------|:-------------|:---------|
| **Plaintext** | Posts stored in cleartext | Server operator | Public channels, maximum searchability |
| **Encrypted at rest** | Posts encrypted with server key | Server operator with key | Default for most channels |
| **E2E persistent** | Posts encrypted with channel key | Channel members only | Maximum privacy channels |

For E2E persistent mode, the channel key is distributed to members via SMP and never touches the server. The server stores ciphertext it cannot read. Search is only possible client-side.

### 6.2 Search architecture

| Storage mode | Server search | Client search |
|:-------------|:-------------|:-------------|
| Plaintext | Full-text index | Also available |
| Encrypted at rest | Full-text index (server decrypts) | Also available |
| E2E persistent | NOT possible | Client-side only |

---

## 7. Security analysis

### 7.1 Threat model

| Attacker | What they get | What they cannot get |
|:---------|:-------------|:--------------------|
| SMP server operator | Encrypted blocks, queue addresses, timing | Content, identities, channel membership |
| GoBot VPS compromise (with GoKey) | Encrypted blocks, queue addresses | Content, keys, identities |
| GoBot VPS compromise (standalone) | Message content, queue mappings | GoUNITY private keys |
| GoLab app server compromise | Stored posts (mode-dependent), channel registry | User private keys, SMP queue mappings |
| GoUNITY compromise | Certificate database, usernames, emails | Private keys (not stored), community activity |
| Network observer | Encrypted traffic, IP addresses | Content, identities (with SMP padding) |
| Malicious channel member | Channel content they are subscribed to | Other channels, other users' identities |

### 7.2 Security properties

| Property | Guaranteed by |
|:---------|:-------------|
| Message confidentiality | SMP E2E encryption (NaCl + Double Ratchet) |
| Message integrity | Ed25519 signatures on every message |
| Message authenticity | GoUNITY certificate chain verification |
| Sender anonymity (transport) | SMP pairwise queues, no user IDs |
| Sender pseudonymity (application) | GoUNITY certificates, DID:key identifiers |
| Channel unlinkability | Separate SMP queue pairs per channel |
| Forward secrecy | Double Ratchet key derivation |
| Ban enforcement | GoUNITY CRL + certificate-based identity |
| Hardware trust (optional) | GoKey/SimpleGo eFuse-bound keys |

### 7.3 Comparison with other platforms

| Threat | GitHub/GitLab | Mastodon | Nostr | GoLab |
|:-------|:-------------|:---------|:------|:------|
| Server reads all content | Yes | Yes | Yes (relay) | No (E2E) |
| Server knows social graph | Yes | Yes | Yes | No (pairwise queues) |
| Server knows real identity | Yes (account) | Yes (account) | No (pubkey) | No (certificate) |
| Admin can surveil users | Yes | Yes | Relay can | No (GoKey mode) |
| Ban evasion easy | Medium | Medium | Very easy | Hard (certificate cost) |
| Metadata protection | None | None | None | SMP queue isolation |

### 7.4 Known weaknesses

| ID | Severity | Description | Status |
|:---|:---------|:------------|:-------|
| GL-SEC-01 | HIGH | GoBot standalone mode: VPS sees all content | By design - GoKey mode resolves |
| GL-SEC-02 | MEDIUM | Application server stores posts (mode-dependent) | Configurable - E2E mode available |
| GL-SEC-03 | MEDIUM | GoBot relay can selectively drop messages | Mitigated by sequence monitoring |
| GL-SEC-04 | MEDIUM | Channel key distribution for E2E persistent mode | Sender-key rotation protocol needed |
| GL-SEC-05 | LOW | Search not available in E2E persistent mode | By design - privacy tradeoff |
| GL-SEC-06 | LOW | GoUNITY registration requires email + payment | By design - anti-spam barrier |
| GL-SEC-07 | LOW | CRL propagation delay (up to 24h) | Configurable sync interval |

---

## 8. Technology decisions

| Decision | Choice | Reason |
|:---------|:-------|:-------|
| Message format | ActivityStreams 2.0 | W3C standard, proven by Fediverse, extensible |
| Identity format | DID:key (Ed25519) | Self-describing, no resolution service, W3C compatible |
| Transport | SMP via simplex-js | Proven E2E, no user IDs, metadata resistant |
| Relay | GoBot (extended) | Already handles SMP connections and moderation |
| Certificate authority | GoUNITY (step-ca fork) | Production-grade, Ed25519 native, HSM support |
| Application server | Go | Same as GoBot and GoRelay, single binary, fast |
| Browser client | TypeScript | Built on simplex-js and GoChat widget proven architecture |
| Permission model | Power levels (Matrix-style) | Most mature permission system, certificate-enforced |
| Post signatures | Ed25519Signature2020 | W3C linked data proof standard |
| Channel scaling | Relay fan-out via GoBot | O(n) vs O(n^2), proven pattern |

---

## 9. Related components

| Component | Role in GoLab | Documentation |
|:----------|:-------------|:-------------|
| [GoBot](https://github.com/saschadaemgen/GoBot) | Community relay + moderation | [System Architecture](https://github.com/saschadaemgen/GoBot/blob/main/docs/SYSTEM-ARCHITECTURE.md) |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware crypto for GoBot | [Wire Protocol](https://github.com/saschadaemgen/GoBot/blob/main/docs/GOKEY-WIRE-PROTOCOL.md) |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) | Certificate authority | [Architecture](https://github.com/saschadaemgen/GoUNITY/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) |
| [GoChat](https://github.com/saschadaemgen/GoChat) | Browser SMP foundation | [GoChat repo](https://github.com/saschadaemgen/GoChat) |
| [simplex-js](https://www.npmjs.com/package/simplex-js) | SMP transport library | [npm package](https://www.npmjs.com/package/simplex-js) |

---

*GoLab Architecture & Security v1 - April 2026*
*IT and More Systems, Recklinghausen, Germany*
