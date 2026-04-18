# GoNode Architecture & Security

**Document version:** Season 1 | April 2026
**Component:** GoNode decentralised infrastructure network
**Copyright:** 2026 Sascha Daemgen, IT and More Systems, Recklinghausen
**License:** AGPL-3.0

---

## Overview

GoNode is a decentralised, Sybil-resistant, economically self-sustaining network of independent nodes that provides relay and file storage infrastructure for the SimpleGo messaging ecosystem. This document specifies the technical architecture, cryptographic pipeline, wire protocols, smart contract interfaces, threat model, and security properties of GoNode.

| Property | Details |
|:---------|:--------|
| Language | Go (service node runtime, client integration) |
| Smart contract language | Solidity 0.8.24+ (Arbitrum One) |
| Consensus | Pulse-style BFT appchain (service node layer) + Arbitrum One (economic layer) |
| Protocol base | SimpleX SMP and XFTP (unchanged) |
| Message block size | 16 KB padded (inherited from SimpleGo) |
| File chunk sizes | 256 KB / 1 MB / 4 MB (inherited from XFTP) |
| Replication factor | 5 (messages), 16 of 10 Reed-Solomon (files) |
| Transport | TLS 1.3 with SHA-256 key pinning |
| Routing | 3-hop onion routing (Sphinx-style) |
| Node stake | 10,000 GoCoin locked on Arbitrum One |
| Audit frequency | ~10 challenges per hour per node |
| Epoch duration | 24 hours (swarm reassignment) |
| Bootstrap | Foundation-operated for first year, then distributed |
| Deployment | Go binary (Linux / macOS / Windows / BSD) |

---

## 1. System architecture

### 1.1 Four-layer design

GoNode's architecture separates concerns across four distinct layers. Each layer has a specific role and communicates with adjacent layers through well-defined interfaces.

```
+-------------------------------------------------------------+
|  Layer 4: Economic Layer (Arbitrum One)                     |
|  - GoCoin ERC-20 contract                                   |
|  - Staking contract (10k GoCoin per node)                   |
|  - Reward distribution contract (monthly emission)          |
|  - Pro subscription contract (EURC -> burn)                 |
|  - Slashing contract (misbehaviour evidence submission)     |
|  - Governance contract (parameter updates)                  |
+-------------------------------------------------------------+
|  Layer 3: Consensus Layer (Pulse-style BFT appchain)        |
|  - VRF beacon (epoch randomness for swarm assignment)       |
|  - Audit challenge coordination                             |
|  - Fast slashing quorums                                    |
|  - Node liveness tracking                                   |
|  - Daily checkpointing to Arbitrum One                      |
+-------------------------------------------------------------+
|  Layer 2: Service Node Layer (swarm fabric)                 |
|  - Swarm assignment (5-10 nodes per queue subset)           |
|  - Message replication (3-5x)                               |
|  - File erasure coding (Reed-Solomon RS(10,16))             |
|  - Onion routing (3 hops)                                   |
|  - Audit response                                           |
|  - Per-block HMAC verification                              |
+-------------------------------------------------------------+
|  Layer 1: Protocol Layer (unchanged SimpleX)                |
|  - SMP (Simplex Messaging Protocol)                         |
|  - XFTP (Simplex File Transfer Protocol)                    |
|  - Five-layer encryption (E2E, PQ, per-queue, server,       |
|    transport)                                               |
|  - Double Ratchet with post-quantum KEM hybrid              |
+-------------------------------------------------------------+
```

### 1.2 Component responsibilities

| Component | Responsibility |
|:----------|:--------------|
| `gonode-core` | Service node runtime, network I/O, storage management |
| `gonode-swarm` | Swarm coordination, replication, audit response |
| `gonode-onion` | Onion routing (Sphinx-style packet forwarding) |
| `gonode-audit` | Audit challenge distribution and verification |
| `gonode-consensus` | BFT appchain client (validator or full node) |
| `gonode-economic` | Arbitrum RPC client, stake and reward operations |
| `gonode-client` | Client library for integration with SimpleGo apps |
| `gonode-contracts` | Solidity smart contracts (Arbitrum deployment) |

### 1.3 Data flow: user sends a message

```
1. Alice composes message "Hello Bob" in her SimpleGo app
2. App client-side encrypts message with fresh random AEAD key
   using XChaCha20-Poly1305 (SimpleX protocol layer)
3. App wraps ciphertext in SMP protocol frame
4. App determines target swarm from Bob's queue identifier
   (deterministic hash function, no directory needed)
5. App opens 3-hop onion route to swarm member
6. Onion packet travels through 3 random service nodes
7. Final swarm member receives ciphertext
8. Swarm member replicates ciphertext to 4 other swarm members
9. Each swarm member stores ciphertext in local SMP queue
10. Bob's SimpleGo app polls the swarm, finds new message
11. Bob's app retrieves ciphertext from any responsive swarm member
12. Bob's app decrypts using Double Ratchet state
13. Message displayed to Bob
```

Key property: between steps 3 and 12, no single entity sees both sender and recipient information, and no entity can decrypt the ciphertext.

### 1.4 Data flow: user sends a file

```
1. Alice selects a 50 MB file to send
2. App splits file into 50 chunks of 1 MB each (XFTP chunking)
3. App encrypts each chunk with fresh random AEAD key
4. App Reed-Solomon codes each chunk: 10 data + 6 parity = 16 shards
   per chunk, shards ~100 KB each
5. App selects 16 service nodes (one per shard, from a diverse
   swarm pool for redundancy)
6. App uploads one shard to each node via onion route
7. App creates a "file description" containing:
   - 50 chunk manifests, each with 16 shard locations and hashes
   - Per-chunk AEAD keys (encrypted for Bob's keys)
   - Total file hash for integrity verification
8. App sends file description to Bob via SMP queue
9. Bob's app fetches any 10 of 16 shards per chunk
10. Bob's app reconstructs each chunk using Reed-Solomon decoding
11. Bob's app decrypts each chunk and assembles the file
12. Bob sees the file
```

---

## 2. Service node architecture

### 2.1 Node runtime structure

A GoNode service node is a single Go binary running three goroutine pools:

```go
// Simplified goroutine structure

type ServiceNode struct {
    // Network I/O
    tlsListener    net.Listener        // incoming TLS connections
    onionRouter    *OnionRouter        // Sphinx packet handling
    clientHandler  *ClientHandler      // user client connections

    // Swarm coordination
    swarmMgr       *SwarmManager       // current swarm memberships
    replicator     *Replicator         // replication to peers
    auditResponder *AuditResponder     // respond to challenges

    // Storage
    messageStore   *BadgerDB           // 16 KB message blocks
    shardStore     *BadgerDB           // file shards
    stateStore     *BadgerDB           // ratchet state, metadata

    // Consensus
    bftClient      *BFTClient          // Pulse appchain client
    arbitrumClient *ArbitrumClient     // Arbitrum RPC
    stakeManager   *StakeManager       // monitor stake, rewards
}
```

### 2.2 Storage layout

GoNode uses BadgerDB (pure Go, embedded key-value store) for all persistent storage. Three separate databases:

```
~/.gonode/
├── messages.db/           # SMP message queue contents
│   ├── key: queue_id || message_index
│   └── value: 16 KB ciphertext
├── shards.db/             # XFTP file shards
│   ├── key: shard_id (SHA-256 of shard content)
│   └── value: shard bytes (~256 KB to ~400 KB)
└── state.db/              # metadata and state
    ├── current_swarms: []SwarmID
    ├── peer_connections: map[NodeID]ConnState
    ├── audit_challenges_pending: []Challenge
    ├── rewards_earned: map[epoch]uint256
    └── operator_identity: ed25519.PrivateKey
```

**Storage capacity requirements** (at target network scale):

| Role | Minimum | Recommended |
|:-----|:--------|:------------|
| Entry-level node | 100 GB | 500 GB SSD |
| Standard node | 500 GB | 2 TB SSD |
| Heavy storage node | 2 TB | 10 TB SSD |

### 2.3 Network configuration

```
Inbound connections (server mode):
  - Port 7000: TLS 1.3 for peer-to-peer (node-to-node)
  - Port 7001: TLS 1.3 for client connections
  - Port 7002: HTTPS for audit response (internal Foundation only)

Outbound connections (client mode):
  - Arbitrum One RPC (HTTPS)
  - Pulse BFT appchain RPC
  - Peer nodes (TLS 1.3 on port 7000)
  - Bootstrap nodes (on first start)

Bandwidth requirements:
  - Minimum: 50 Mbps symmetric
  - Recommended: 100 Mbps symmetric
  - Heavy operator: 1 Gbps symmetric
```

### 2.4 Hardware identity (optional GoKey integration)

Operators can optionally bind their node identity to a GoKey ESP32-S3 device. This provides hardware-backed proof that a specific physical device controls the operator identity.

```
Software-only operator identity:
  Ed25519 keypair stored in ~/.gonode/state.db (encrypted at rest
  with operating system keychain)

Hardware operator identity (with GoKey):
  Ed25519 keypair burned into ESP32-S3 eFuse (irreversible)
  GoNode runtime sends operations to GoKey via WSS/mTLS
  GoKey signs operations, returns signatures
  Private key never leaves the hardware
```

Benefits of hardware identity:
- Private key cannot be extracted by software exploits
- Operator theft requires physical access to the GoKey
- "Hardware verified" badge in operator directory
- Required for highest-tier enterprise SLA contracts

---

## 3. Cryptographic pipeline

### 3.1 Five-layer encryption (inherited from SimpleGo)

GoNode inherits the SimpleGo/SimpleX five-layer encryption stack without modification. Every message and file passes through all five layers before leaving the client.

| Layer | Algorithm | Key Derivation | Protects Against |
|:------|:----------|:--------------|:-----------------|
| 1. End-to-End | XChaCha20-Poly1305 AEAD | Double Ratchet (X3DH + Curve25519) | Message content interception |
| 2. Post-Quantum | ML-KEM-768 hybrid with X25519 | CRYSTALS-Kyber hybrid KEM | Future quantum computers on recorded traffic |
| 3. Per-Queue | XSalsa20-Poly1305 | X25519 per queue | Traffic correlation across queues |
| 4. Server-to-Recipient | NaCl cryptobox | X25519 | Server correlating in/out traffic |
| 5. Transport | TLS 1.3 AES-256-GCM or ChaCha20-Poly1305 | ECDHE-ECDSA | Network attackers, MITM, downgrade |

Note: GoNode uses ML-KEM-768 in Layer 2 rather than sntrup761 used in SimpleGo firmware. Both are NIST-approved post-quantum KEMs. Server-side ML-KEM has broader library support and better performance characteristics on x86 servers.

### 3.2 Onion routing (Layer 6 for GoNode)

GoNode adds a sixth layer above the SimpleX stack: 3-hop onion routing for all message delivery and file shard transfers. This prevents any single service node from seeing both sender and recipient.

**Sphinx packet format** (following the Sphinx paper by Danezis and Goldberg):

```
+-------------------+----------------+----------------+
|   Header (300B)   |  Payload (n B) |  MAC (32 B)    |
+-------------------+----------------+----------------+

Header structure:
- Ephemeral Curve25519 public key (32 bytes)
- Routing information encrypted per-hop (256 bytes)
- Header MAC per-hop (32 bytes per hop × 3 = 96 total)

Payload:
- Inner encrypted message
- Padding to constant size
- Size: 16 KB for message packets, 400 KB for file shard packets
```

Each hop:
1. Verifies the header MAC
2. Decrypts one layer using ECDH with its own private key
3. Extracts the next hop address
4. Forwards the packet to the next hop
5. Waits a random delay (50-200ms) to prevent timing correlation

The final hop delivers the inner message to the target swarm. Only the final hop knows the destination. Only the first hop knows the sender's IP. No hop knows both.

### 3.3 Random-key AEAD encryption for storage

GoNode uses client-side random-key AEAD for all stored data. Each message and file shard is encrypted with a fresh random key, never reused.

```
// Encryption (client side)
message_plaintext := "Hello Bob"
encryption_key := random_bytes(32)           // fresh per message
nonce := random_bytes(24)
ciphertext, tag := XChaCha20_Poly1305_Encrypt(
    key: encryption_key,
    nonce: nonce,
    plaintext: message_plaintext,
    aad: queue_id || message_index,
)

// The key is transmitted separately through the double ratchet
// mechanism, never alongside the ciphertext

// Storage (service node side)
node_stores(queue_id, message_index, ciphertext || nonce || tag)

// Service node sees: random-looking bytes. Cannot decrypt.
```

**Why NOT convergent encryption:** Convergent encryption uses a deterministic key derived from the plaintext itself, enabling deduplication. This creates a devastating "confirmation-of-a-file" attack where an attacker with a candidate plaintext can encrypt it and check whether the resulting ciphertext exists in the network. For small, partially predictable messages (common chat patterns, timestamps, user names), this attack succeeds. Random-key encryption prevents it completely.

### 3.4 Reed-Solomon erasure coding (files only)

XFTP file chunks are erasure-coded with Reed-Solomon RS(10, 16) before distribution.

**Parameters:**
- k = 10 (data shards)
- n = 16 (total shards, so 6 parity shards)
- Any 10 of 16 shards reconstruct the original
- Storage overhead: 16/10 = 1.6x
- Durability at 20 percent node failure rate: 99.99999999% (10 nines)
- Durability at 10 percent node failure rate: 99.999999999% (11 nines)

**Implementation:** `klauspost/reedsolomon` (production-proven Go library used by Minio, Cloudflare).

```go
// File shard generation
chunk_size := 4 * 1024 * 1024  // 4 MB
chunk := read_file_chunk(file, offset, chunk_size)

rs, _ := reedsolomon.New(10, 6)
shards := make([][]byte, 16)
for i := 0; i < 10; i++ {
    shards[i] = chunk[i*chunk_size/10 : (i+1)*chunk_size/10]
}
rs.Encode(shards)

// Now shards[0..9] are data, shards[10..15] are parity
// Each shard is ~400 KB
// Any 10 of 16 can reconstruct the original 4 MB chunk
```

### 3.5 Ed25519 signatures for operator identity

Every service node has an Ed25519 keypair that identifies the operator across the network. This key signs:

- Audit challenge responses
- Stake deposit transactions
- Reward withdrawal transactions
- Governance votes
- Swarm coordination messages

Ed25519 was chosen over ECDSA because:
- Deterministic signing (no RNG required, no nonce-reuse bugs)
- Constant-time by design (resistant to timing side-channels)
- 32-byte public keys (compact for swarm membership lists)
- 64-byte signatures (compact for audit proofs)
- Fast on commodity hardware (tens of thousands of sig/sec)

### 3.6 VRF for epoch randomness

Swarm assignment uses a Verifiable Random Function (VRF) computed by a rotating committee of service nodes every 24 hours. The VRF output is cryptographically unpredictable, publicly verifiable, and deterministic given the inputs.

**VRF construction:** ECVRF-EDWARDS25519-SHA512-ELL2 (RFC 9381).

```
Epoch N begins:
  1. A committee of 20 randomly-selected service nodes runs
     a threshold VRF protocol
  2. Each committee member submits a VRF proof based on
     their Ed25519 key and the block hash of Arbitrum block
     corresponding to epoch N start
  3. The aggregated VRF output is the "epoch randomness"
  4. All service nodes use this randomness to compute swarm
     assignments for epoch N

Swarm assignment:
  For each queue_id Q:
    swarm_index = Hash(epoch_randomness || Q) mod num_swarms
    Q is assigned to swarm at swarm_index for epoch N

  For each node N:
    N's swarms for epoch N = { S : N's node_id is in the
                                   5-10 nodes nearest to hash(S) }
```

This ensures swarm assignment is:
- Unpredictable before the epoch starts (resistant to preparation attacks)
- Verifiable by anyone (can't be manipulated by a subset of nodes)
- Uniform over the queue space (no swarm is overloaded)
- Stable within an epoch (no churning during active sessions)

---

## 4. Swarm protocol

### 4.1 Swarm assignment

The network partitions queue identifier space into a number of swarms. At launch, the network supports 256 swarms. As the network grows, swarms are split to maintain an average of 50,000 queues per swarm.

```
Initial configuration:
  - 256 swarms
  - 5-10 service nodes per swarm
  - Maximum 2,560 active service nodes

Growth configuration (year 2):
  - 1,024 swarms
  - 5-10 service nodes per swarm
  - Maximum 10,240 active service nodes

Mature configuration (year 5+):
  - 4,096+ swarms dynamically split/merged based on load
  - Swarm split when queue count > 100,000
  - Swarm merge when queue count < 10,000 for 30 days
```

### 4.2 Swarm membership protocol

When a service node starts operating:

```
1. Node submits 10,000 GoCoin stake to Arbitrum staking contract
2. Stake confirmation triggers emission of `NodeRegistered` event
3. Node listens to Pulse BFT appchain for next epoch boundary
4. At next epoch:
   - Node fetches current VRF randomness
   - Node computes which swarms it is assigned to
     (typically 5-15 swarms for a typical node)
5. Node connects to other members of each assigned swarm
6. Node announces availability to swarm peers via BFT gossip
7. Node begins accepting and replicating messages/shards
```

When a service node exits gracefully:

```
1. Node submits `NodeExitRequested` transaction on Arbitrum
2. Node continues serving current swarm for 48 hours
3. New swarm members are assigned to replace exiting node
4. Exiting node transfers relevant data to replacements
5. After 48 hours, node's stake becomes unlockable (9-month
   cooldown for held-back rewards)
```

### 4.3 Message replication protocol

When a swarm receives a new message:

```
Protocol: SWARM_REPLICATE

Node A receives message M from client:
  1. Verify: M is valid 16 KB SMP frame (syntactically correct)
  2. Verify: target queue Q is assigned to this swarm in epoch E
  3. Generate replication_id = SHA-256(M || timestamp)
  4. Store: message_store.put(Q || M.index, M)
  5. Broadcast REPLICATE(Q, M, replication_id) to all swarm peers
  6. Wait for 3 ACKs (quorum out of 5-10 peers)
  7. Return ACK to client

Node B receives REPLICATE(Q, M, replication_id) from peer A:
  1. Verify: peer A is swarm member in current epoch
  2. Verify: Q is assigned to this swarm
  3. Verify: message not already stored (idempotency via replication_id)
  4. Store: message_store.put(Q || M.index, M)
  5. Return ACK(replication_id) to A
```

### 4.4 File shard distribution protocol

When a client uploads a file:

```
Protocol: XFTP_SHARD_UPLOAD

Client splits file into chunks, each chunk into 16 Reed-Solomon shards:

For each shard S_i (i = 0..15):
  1. Client queries directory for available service nodes
  2. Client selects diverse nodes for shard placement:
     - Different ASN if possible
     - Different geographic regions if possible
     - Prefer high-reputation operators
  3. Client establishes onion route to selected node
  4. Client uploads shard via TLS within onion:
     UPLOAD(shard_id, shard_bytes, retention_hours)
  5. Service node stores shard, returns receipt:
     ACK(shard_id, signed_receipt)

Client records all shard locations and receipts in
file description. File description is sent to recipient
via SMP queue.
```

---

## 5. Audit protocol

### 5.1 Challenge distribution

Every hour, each service node receives approximately 10 random audit challenges from the network. Challenges test whether the node still holds the data it claims to have.

**Challenge source:** A rotating committee of 7 audit coordinator nodes (selected by VRF each epoch) generates challenges by:

```
1. Committee member samples a random subset of stored blocks
   from the network's metadata (which blocks should be where)
2. Committee member generates challenges:
   - For messages: "Compute HMAC-SHA256(your_operator_key, block_id)"
   - For shards: "Compute SHA-256 of bytes at offset X, length Y"
3. Committee aggregates challenges (threshold signature)
4. Challenges are gossiped through the BFT appchain
5. Each target node receives its challenges
```

### 5.2 Challenge response

Node receives a challenge:

```go
// Challenge format
type Challenge struct {
    ChallengeID  [32]byte
    NodeID       [32]byte
    BlockID      [32]byte
    ChallengeType uint8  // 0 = HMAC, 1 = partial hash, 2 = Merkle path
    ExtraData    []byte  // offset/length for partial hash, etc.
    Deadline     uint64  // unix timestamp
    CommitteeSig []byte  // threshold signature
}

func (n *Node) HandleChallenge(c Challenge) Response {
    // Verify challenge is validly signed
    if !verifyCommitteeSig(c) {
        return Response{Error: "invalid signature"}
    }

    // Verify challenge is for this node
    if c.NodeID != n.operatorPubKey {
        return Response{Error: "wrong node"}
    }

    // Respond based on challenge type
    switch c.ChallengeType {
    case ChallengeTypeHMAC:
        block := n.messageStore.Get(c.BlockID)
        if block == nil {
            return Response{Error: "block not found"}
        }
        hmac := computeHMAC(n.operatorPrivKey, c.BlockID)
        signature := ed25519.Sign(n.operatorPrivKey, hmac)
        return Response{HMAC: hmac, Signature: signature}

    case ChallengeTypePartialHash:
        // Similar, but returns SHA-256 of bytes at specified range
        ...
    }
}
```

### 5.3 Response verification and scoring

Audit coordinators collect responses and score each node:

```
Response scoring:
  Correct + on time: +1 reputation point
  Correct + late (>2 sec):   +0.5 reputation point
  Incorrect:   -10 reputation points
  Missing:  -20 reputation points

Node status transitions:
  Reputation > 100: Healthy (full rewards)
  Reputation 50-100: Warning (80% rewards)
  Reputation 20-50: Probation (50% rewards, more audits)
  Reputation 0-20: Deregistration pending (no new assignments)
  Reputation below 0: Disqualification (stake slashed 5%, held-back burned)

Reputation decay:
  Daily decay rate: -0.5 points (encourages consistent response)
  Maximum reputation: 500 (caps the advantage of long tenure)
```

### 5.4 Anti-gaming measures

**Why nodes can't fake responses:** Challenges require access to actual block content. HMAC challenges require the block bytes to compute. Partial hash challenges require specific byte ranges. Nodes that deleted data cannot answer correctly.

**Why operators can't run multiple instances on one machine:** The VRF-based audit load is roughly proportional to the number of registered nodes. An operator running N instances on one machine receives N × the audit load, and a single machine cannot typically keep up with concurrent audit responses. Timing-based anomaly detection flags suspicious patterns.

**Why large operators can't evade:** Audit challenges are weighted toward recently-inactive nodes. A node that responds to 100% of challenges gets few new ones; a node that misses challenges gets many more. This makes it hard to free-ride.

---

## 6. Smart contract design

### 6.1 Contract architecture

GoNode deploys five main smart contracts on Arbitrum One, plus auxiliary contracts:

```
GoCoin Token (ERC-20)
   ^
   | references
   |
+--+-------------------+-------------------+--------------------+
|                      |                   |                    |
| StakingContract      | RewardsContract   | SubscriptionContract
|                      |                   |                    |
| - deposit stake      | - emit monthly    | - receive EURC     |
| - withdraw stake     | - distribute      | - buy GoCoin       |
| - slash stake        | - held-back logic | - burn GoCoin      |
|                      |                   | - activate Pro     |
+----------------------+-------------------+--------------------+
                           ^
                           |
                       SlashingContract
                           |
                       - submit evidence
                       - slash stake
                       - burn held-back

                       GovernanceContract
                           |
                       - DAO parameter votes
                       - foundation multisig
                       - key burn at Phase 4
```

### 6.2 GoCoin ERC-20 contract

Standard OpenZeppelin ERC-20 with minor extensions:

```solidity
// SPDX-License-Identifier: AGPL-3.0
pragma solidity 0.8.24;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

contract GoCoin is ERC20, Ownable {
    uint256 public constant MAX_SUPPLY = 100_000_000 * 10**18;

    address public burnAddress = 0x000000000000000000000000000000000000dEaD;

    event TokensBurned(address indexed from, uint256 amount, bytes32 reason);

    constructor() ERC20("GoCoin", "GC") Ownable(msg.sender) {
        _mint(msg.sender, 30_000_000 * 10**18);
        // Remaining 70M minted to reward/treasury/team vesting contracts
    }

    function burn(uint256 amount, bytes32 reason) external {
        _transfer(msg.sender, burnAddress, amount);
        emit TokensBurned(msg.sender, amount, reason);
    }

    function burnFrom(address from, uint256 amount, bytes32 reason) external {
        _spendAllowance(from, msg.sender, amount);
        _transfer(from, burnAddress, amount);
        emit TokensBurned(from, amount, reason);
    }
}
```

### 6.3 Staking contract

```solidity
contract NodeStaking is Ownable {
    IERC20 public immutable goCoin;
    uint256 public constant STAKE_AMOUNT = 10_000 * 10**18;
    uint256 public constant EXIT_COOLDOWN = 48 hours;
    uint256 public constant HELDBACK_COOLDOWN = 270 days; // 9 months

    struct StakeInfo {
        uint256 amount;
        uint256 exitRequestedAt;
        bytes32 operatorPubKey;  // Ed25519 key, 32 bytes
        uint32 registeredEpoch;
        bool active;
    }

    mapping(address => StakeInfo) public stakes;

    event NodeRegistered(address indexed operator, bytes32 pubKey, uint32 epoch);
    event NodeExitRequested(address indexed operator, uint256 withdrawableAt);
    event NodeExited(address indexed operator);
    event StakeSlashed(address indexed operator, uint256 amount, bytes32 reason);

    function registerNode(bytes32 operatorPubKey) external {
        require(stakes[msg.sender].amount == 0, "already staked");
        goCoin.transferFrom(msg.sender, address(this), STAKE_AMOUNT);
        stakes[msg.sender] = StakeInfo({
            amount: STAKE_AMOUNT,
            exitRequestedAt: 0,
            operatorPubKey: operatorPubKey,
            registeredEpoch: getCurrentEpoch(),
            active: true
        });
        emit NodeRegistered(msg.sender, operatorPubKey, getCurrentEpoch());
    }

    function requestExit() external {
        require(stakes[msg.sender].active, "not active");
        stakes[msg.sender].exitRequestedAt = block.timestamp;
        stakes[msg.sender].active = false;
        emit NodeExitRequested(msg.sender, block.timestamp + EXIT_COOLDOWN);
    }

    function withdrawStake() external {
        StakeInfo storage info = stakes[msg.sender];
        require(info.exitRequestedAt > 0, "exit not requested");
        require(block.timestamp >= info.exitRequestedAt + EXIT_COOLDOWN, "cooldown active");
        uint256 amount = info.amount;
        delete stakes[msg.sender];
        goCoin.transfer(msg.sender, amount);
        emit NodeExited(msg.sender);
    }

    function slash(address operator, uint256 amount, bytes32 reason)
        external onlySlashingContract
    {
        require(stakes[operator].amount >= amount, "insufficient stake");
        stakes[operator].amount -= amount;
        GoCoin(address(goCoin)).burn(amount, reason);
        emit StakeSlashed(operator, amount, reason);
    }
}
```

### 6.4 Subscription contract (burn-and-mint core)

This is the critical contract that implements the burn-and-mint mechanism.

```solidity
contract GoNodeSubscription {
    IERC20 public immutable eurc;
    IERC20 public immutable goCoin;
    IUniswapV3Router public immutable uniswap;
    uint24 public constant POOL_FEE = 3000; // 0.3% pool

    uint256 public constant MONTHLY_PRICE_EURC = 5 * 10**6; // 5 EURC (6 decimals)
    uint256 public constant SUBSCRIPTION_DURATION = 30 days;

    mapping(address => uint256) public proExpires;
    uint256 public totalBurned;
    uint256 public totalSubscriptions;

    event ProActivated(address indexed user, uint256 expiresAt, uint256 goCoinBurned);

    function subscribe() external {
        // Pull EURC from user
        eurc.transferFrom(msg.sender, address(this), MONTHLY_PRICE_EURC);

        // Approve Uniswap to spend EURC
        eurc.approve(address(uniswap), MONTHLY_PRICE_EURC);

        // Buy GoCoin on Uniswap
        ISwapRouter.ExactInputSingleParams memory params = ISwapRouter.ExactInputSingleParams({
            tokenIn: address(eurc),
            tokenOut: address(goCoin),
            fee: POOL_FEE,
            recipient: address(this),
            deadline: block.timestamp + 300,
            amountIn: MONTHLY_PRICE_EURC,
            amountOutMinimum: 0,  // accept any amount (MEV protection via private mempool)
            sqrtPriceLimitX96: 0
        });
        uint256 goCoinBought = uniswap.exactInputSingle(params);

        // Burn all the GoCoin
        GoCoin(address(goCoin)).burn(goCoinBought, bytes32("SUB"));
        totalBurned += goCoinBought;
        totalSubscriptions += 1;

        // Activate or extend Pro subscription
        uint256 currentExpiry = proExpires[msg.sender];
        uint256 newExpiry = currentExpiry > block.timestamp
            ? currentExpiry + SUBSCRIPTION_DURATION
            : block.timestamp + SUBSCRIPTION_DURATION;
        proExpires[msg.sender] = newExpiry;

        emit ProActivated(msg.sender, newExpiry, goCoinBought);
    }

    function isProUser(address user) external view returns (bool) {
        return proExpires[user] > block.timestamp;
    }
}
```

### 6.5 Rewards contract

```solidity
contract NodeRewards {
    IERC20 public immutable goCoin;
    NodeStaking public immutable staking;

    // 40M GoCoin reward pool, emitted on halving curve
    uint256 public constant INITIAL_MONTHLY_EMISSION = 400_000 * 10**18;

    // Month -> emission amount (precomputed)
    mapping(uint32 => uint256) public monthlyEmissions;

    // Node -> month -> amount earned
    mapping(address => mapping(uint32 => uint256)) public earnings;

    // Node -> amount held back
    mapping(address => uint256) public heldBack;

    // Node -> month when next held-back release happens
    mapping(address => uint32) public nextHeldBackRelease;

    function distributeRewards(uint32 month, address[] calldata nodes, uint256[] calldata weights)
        external onlyRewardDistributor
    {
        require(nodes.length == weights.length, "length mismatch");
        uint256 totalWeight = 0;
        for (uint i = 0; i < weights.length; i++) totalWeight += weights[i];

        uint256 monthlyEmission = monthlyEmissions[month];
        for (uint i = 0; i < nodes.length; i++) {
            uint256 earned = (monthlyEmission * weights[i]) / totalWeight;
            earnings[nodes[i]][month] = earned;

            // Split between immediate payout and held-back
            uint256 monthsActive = getMonthsActive(nodes[i]);
            (uint256 payout, uint256 held) = splitPayout(earned, monthsActive);

            goCoin.transfer(nodes[i], payout);
            heldBack[nodes[i]] += held;
        }
    }

    function splitPayout(uint256 earned, uint256 monthsActive)
        internal pure returns (uint256 payout, uint256 held)
    {
        if (monthsActive < 4) { // months 1-3: 50/50
            return (earned / 2, earned / 2);
        } else if (monthsActive < 7) { // months 4-6: 75/25
            return (earned * 3 / 4, earned / 4);
        } else { // month 7+: 100/0
            return (earned, 0);
        }
    }

    function claimHeldBack() external {
        require(block.timestamp >= getMonthStart(nextHeldBackRelease[msg.sender]), "too early");
        uint256 amount = heldBack[msg.sender];
        heldBack[msg.sender] = 0;
        nextHeldBackRelease[msg.sender] = uint32(getCurrentMonth()) + 1;
        goCoin.transfer(msg.sender, amount);
    }
}
```

### 6.6 Slashing contract

```solidity
contract Slashing {
    NodeStaking public immutable staking;
    NodeRewards public immutable rewards;

    enum OffenseType {
        Equivocation,        // 5% slash
        CorruptedData,       // 5% slash
        Censorship,          // 10% slash
        MaliciousAudit       // 20% slash
    }

    event OffenseSubmitted(address indexed accuser, address indexed accused, OffenseType ot);
    event OffenseConfirmed(address indexed accused, OffenseType ot, uint256 slashed);

    // Evidence-based slashing with validator quorum confirmation
    function submitEvidence(
        address accused,
        OffenseType ot,
        bytes calldata evidence,
        bytes[] calldata validatorSigs
    ) external {
        // Verify evidence is cryptographically valid for the claim
        require(verifyEvidence(accused, ot, evidence), "invalid evidence");

        // Verify 2/3+ validator quorum signed off on this slashing
        require(validatorSigs.length >= getQuorumSize(), "insufficient quorum");
        require(verifyValidatorSigs(evidence, validatorSigs), "invalid sigs");

        // Slash amount
        uint256 stakeAmount = staking.getStakeAmount(accused);
        uint256 slashPercent;
        if (ot == OffenseType.Equivocation) slashPercent = 5;
        else if (ot == OffenseType.CorruptedData) slashPercent = 5;
        else if (ot == OffenseType.Censorship) slashPercent = 10;
        else slashPercent = 20;

        uint256 slashAmount = stakeAmount * slashPercent / 100;

        staking.slash(accused, slashAmount, bytes32("SLASH"));

        // Also burn all held-back rewards
        uint256 held = rewards.heldBackOf(accused);
        rewards.burnHeldBack(accused);

        emit OffenseConfirmed(accused, ot, slashAmount + held);
    }
}
```

### 6.7 Gas costs (Arbitrum One)

Typical operation costs at current Arbitrum gas prices:

| Operation | Estimated Gas | Cost at 0.1 gwei |
|:----------|:--------------|:-----------------|
| Register node (stake 10k GoCoin) | ~150,000 | ~$0.003 |
| Request exit | ~50,000 | ~$0.001 |
| Withdraw stake | ~60,000 | ~$0.001 |
| Subscribe to Pro (includes Uniswap swap + burn) | ~250,000 | ~$0.005 |
| Claim held-back rewards | ~70,000 | ~$0.002 |
| Submit slashing evidence (with sigs) | ~400,000 | ~$0.008 |
| Distribute monthly rewards (100 nodes) | ~3,000,000 | ~$0.06 |

These costs are negligible compared to the service value and do not affect unit economics.

---

## 7. Threat model

### 7.1 Trust assumptions

| Entity | Trust Level | Justification |
|:-------|:------------|:--------------|
| Any single service node | Untrusted | Could be malicious or compromised |
| Any single swarm | Partially trusted | Require 3 of 5-10 quorum for actions |
| Arbitrum One validators | Trusted (via Ethereum L1) | Ethereum's security model |
| Pulse BFT appchain | Trusted if 2/3+ honest | Standard BFT assumption |
| Foundation | Trusted initially, reduced over time | Admin keys burned at Phase 4 |
| GoKey hardware | Trusted if not physically compromised | Side-channel attacks require lab equipment |
| Client software | Trusted (user's device) | Not defensible if compromised |

### 7.2 Attack scenarios

#### 7.2.1 Malicious individual node

**Attack:** A single compromised service node attempts to read message content, inject false messages, drop messages, or serve corrupted data.

**Defences:**
- Messages are client-side encrypted with random keys; node cannot decrypt
- Onion routing means node sees only one hop of the route
- Replication across swarm means dropped messages still reach recipient
- Audit challenges detect corrupted data (wrong HMAC response)
- Swarm consensus rejects forged messages (signatures don't verify)

**Outcome:** Attacker sees only ciphertext metadata. Cannot decrypt, forge, or effectively disrupt service.

#### 7.2.2 Sybil attack: one operator, many nodes

**Attack:** A single operator registers many nodes to dominate a swarm and censor messages or disrupt service.

**Defences:**
- 10,000 GoCoin stake per node (economic cost)
- VRF-based deterministic swarm assignment (can't choose target swarm)
- Random swarm reshuffling every 24 hours (can't camp on target)
- Audit challenges detect multi-instance fraud (can't respond fast enough on one machine)
- Geographic and ASN diversity weighted in operator ranking
- Taking 33% of the network requires ~2.8M GoCoin (~$280k at $0.10/token, ~$2.8M at $1.00/token)

**Outcome:** Sybil attack is economically prohibitive and technically detectable.

#### 7.2.3 Eclipse attack on a specific user

**Attack:** Attacker surrounds a target user's swarm with malicious nodes to isolate them from the network.

**Defences:**
- VRF swarm assignment is unpredictable before epoch starts
- User does not control swarm assignment (deterministic from queue ID)
- User's onion routes go through random nodes not in the target swarm
- 3-hop onion means attacker needs to control multiple hops, not just the swarm
- Required attacker capability: N-1 of N swarm members plus 2 of 3 onion hops, computationally infeasible

**Outcome:** Eclipse attacks on specific users are impractical.

#### 7.2.4 Traffic analysis

**Attack:** Network observer attempts to correlate sender and recipient traffic patterns.

**Defences:**
- All packets are constant size (16 KB for messages, 400 KB for file shards)
- Random delays per hop (50-200ms)
- Onion encryption means packet contents differ at each hop
- Mix-net-like delays prevent direct timing correlation
- Cover traffic (optional, for high-threat users): continuous dummy messages

**Outcome:** Passive traffic analysis requires state-level resources and is still probabilistic, not certain.

#### 7.2.5 Slashing attack (false evidence)

**Attack:** Attacker submits false evidence of misbehaviour against an honest node to trigger slashing.

**Defences:**
- Evidence must be cryptographically valid (signatures, Merkle proofs)
- Evidence must be confirmed by 2/3 validator quorum
- Validators face their own slashing for submitting false confirmations
- Accused node has a grace period to submit counter-evidence
- Audit log on BFT appchain allows public verification

**Outcome:** False slashing requires colluding with 2/3+ of validators, which is a failure of the whole consensus layer.

#### 7.2.6 Smart contract exploit

**Attack:** Attacker exploits a bug in GoNode's smart contracts to steal funds or disrupt the economic layer.

**Defences:**
- OpenZeppelin battle-tested contract primitives
- Multiple independent security audits (pre-launch)
- Bug bounty program (post-launch)
- Timelocks on privileged operations (24h minimum)
- Emergency pause function (multisig, burned at Phase 4)
- Gradual deployment (testnet, then small mainnet, then full scale)

**Outcome:** Risk is non-zero but mitigated through standard DeFi security practices.

#### 7.2.7 Foundation compromise or capture

**Attack:** Foundation is compromised (keys stolen) or captured (regulatory pressure, insider threat).

**Defences:**
- Multisig governance (3 of 5) for Foundation operations
- Admin keys burned at Phase 4 (24+ months post-launch)
- After key burn, protocol is immutable - even Foundation cannot modify
- Swiss Stiftung structure has strong protection against hostile state action
- AGPL-3.0 code means anyone can fork if Foundation is corrupted

**Outcome:** Compromise during Phase 0-3 is significant risk (primary mitigation is operational security). Post-Phase 4, compromise has limited effect.

#### 7.2.8 Regulatory shutdown

**Attack:** A regulator orders Foundation to shut down the network, reveal user data, or install backdoors.

**Defences:**
- Zero-knowledge architecture: Foundation genuinely cannot reveal user data
- Swiss domicile: constitutional protection of financial privacy
- Decentralised node operators: Foundation cannot force node operators to comply
- Open-source code: network continues to operate even if Foundation is ordered offline
- Jurisdictional diversity: operators worldwide cannot all be ordered

**Outcome:** Partial operations might be restricted in specific jurisdictions, but the global network continues.

### 7.3 Security disclosure

Vulnerabilities should be reported to `security@gonode.dev` with GPG encryption. Acknowledgment within 48 hours, initial assessment within 7 days, fix and disclosure coordination within 90 days (standard industry practice).

Bug bounty program will be published before Phase 3 (TGE), with rewards scaled to severity:

| Severity | Reward (USD equivalent) |
|:---------|:------------------------|
| Critical (fund loss, key exposure, network-wide) | $10,000 - $250,000 |
| High (significant disruption, slashing bypass) | $2,500 - $25,000 |
| Medium (specific user impact, recovery possible) | $500 - $5,000 |
| Low (minor protocol deviation, self-limiting) | $100 - $1,000 |

---

## 8. Known limitations

| ID | Severity | Description | Mitigation / Status |
|:---|:---------|:------------|:-------------------|
| GN-SEC-01 | Low | 24-hour CRL propagation window for revoked GoUNITY certs | Accepted, configurable in critical deployments |
| GN-SEC-02 | Low | Cover traffic is optional, not mandatory | Deliberate - balances anonymity vs bandwidth costs |
| GN-SEC-03 | Medium | Phase 0-3 Foundation compromise could modify protocol | Mitigated by multisig and timelock; fully resolved at Phase 4 key burn |
| GN-SEC-04 | Low | Pulse BFT appchain has its own consensus assumptions | Checkpoints to Arbitrum daily limit blast radius |
| GN-SEC-05 | Medium | Quantum computers could break X25519 in Layer 6 onion | ML-KEM upgrade path planned for Phase 5 |
| GN-SEC-06 | Low | Uniswap pool manipulation could affect burn efficiency | Mitigated by pool depth, multiple routing, circuit breakers |
| GN-PERF-01 | Low | Onion routing adds latency (~200-500ms typical) | Acceptable for messaging; bypassed for real-time calls (future) |
| GN-PERF-02 | Medium | File retrieval requires 10 of 16 shards from diverse nodes | Parallelisable, typical latency 2-5 seconds for 1 GB file |
| GN-OP-01 | Medium | New operators may abandon after 1-3 months (Storj data) | Held-back payment structure aligns with abandonment data |
| GN-OP-02 | Low | Geographic concentration risk in early network | Weighted rewards incentivise diversity |

---

## 9. Deployment

### 9.1 Single-node deployment

Minimum viable setup for a home operator:

```bash
# Download pre-built binary (Phase 1+)
wget https://github.com/saschadaemgen/GoNode/releases/latest/gonode-linux-amd64
chmod +x gonode-linux-amd64

# Generate operator identity
./gonode-linux-amd64 keygen --output ~/.gonode/keystore

# Fund operator address with 10,000 GoCoin + gas
# (via exchange to the address shown by keygen)

# Configure
cat > ~/.gonode/config.yaml << EOF
arbitrum_rpc: https://arb1.arbitrum.io/rpc
storage_path: /var/lib/gonode
bandwidth_limit: unlimited
ports:
  peer: 7000
  client: 7001
EOF

# Register as node (stakes 10k GoCoin)
./gonode-linux-amd64 register

# Start service node
./gonode-linux-amd64 serve --config ~/.gonode/config.yaml

# Monitor earnings
./gonode-linux-amd64 earnings
```

### 9.2 Enterprise deployment

For enterprise SLA customers, GoNode can be deployed in a private cluster:

```yaml
# docker-compose.yml for enterprise deployment
services:
  gonode-1:
    image: gonode:latest
    environment:
      - NODE_ROLE=primary
      - CLUSTER_ID=enterprise-acme-001
    volumes:
      - ./data-1:/var/lib/gonode

  gonode-2:
    image: gonode:latest
    environment:
      - NODE_ROLE=secondary
      - CLUSTER_ID=enterprise-acme-001
    volumes:
      - ./data-2:/var/lib/gonode

  gonode-3:
    image: gonode:latest
    environment:
      - NODE_ROLE=secondary
      - CLUSTER_ID=enterprise-acme-001
    volumes:
      - ./data-3:/var/lib/gonode

  prometheus:
    image: prom/prometheus
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml

  grafana:
    image: grafana/grafana
    ports: ["3000:3000"]
```

Enterprise deployments do not require GoCoin staking - they are billed in fiat via SLA contracts. Private clusters communicate with the public GoNode network for interoperability but provide dedicated capacity and SLA guarantees to the enterprise customer.

### 9.3 Monitoring

GoNode exports Prometheus metrics on port 9100 by default:

```
gonode_messages_stored_total
gonode_messages_replicated_total
gonode_shards_stored_bytes
gonode_audit_challenges_received_total
gonode_audit_challenges_correct_total
gonode_audit_challenges_incorrect_total
gonode_reputation_score
gonode_earned_gocoin_current_month
gonode_held_back_gocoin_total
gonode_bandwidth_used_bytes
gonode_peer_connections_active
gonode_swarm_memberships_current
```

Example alert rules:

```yaml
- alert: GoNodeLowReputation
  expr: gonode_reputation_score < 50
  for: 1h
  annotations:
    summary: Node reputation degrading, potential disqualification risk

- alert: GoNodeAuditFailures
  expr: rate(gonode_audit_challenges_incorrect_total[1h]) > 0
  annotations:
    summary: Node failing audit challenges, data may be corrupted
```

---

## 10. System integration

### 10.1 Integration with SimpleGo hardware

```
SimpleGo T-Deck Plus (ESP32-S3)
  |
  | SMP protocol messages (via WiFi + TLS)
  v
GoNode service node (anywhere on network)
  |
  | Replicates across swarm
  v
GoNode swarm members (5-10 nodes globally)
  |
  | Stored as ciphertext
  v
Recipient fetches via their SimpleGo device
```

SimpleGo hardware uses GoNode transparently - it connects to the nearest GoNode service node as its SMP relay, replacing the current centralised SimpleX Chat relay infrastructure.

### 10.2 Integration with GoBot

```
GoBot (VPS)
  |
  | Routes group messages via GoNode
  v
GoNode swarms handle SMP queues for groups
  |
  | GoBot performs moderation actions (kick, ban)
  v
GoNode distributes moderation decisions to group members
```

GoBot uses GoNode as its relay infrastructure, replacing current dependence on SimpleX Chat's centralised relays.

### 10.3 Integration with GoKey

Operator identity binding (optional but recommended):

```
GoKey device (ESP32-S3)
  |
  | Holds operator Ed25519 key in eFuse
  |
  | WSS/mTLS connection
  v
GoNode service node
  |
  | Uses GoKey for all signing operations
  |
  | Key never leaves hardware
  v
Arbitrum One
  |
  | GoKey-signed stake deposit, reward claims
  v
Network
```

### 10.4 Integration with GoUNITY

GoUNITY provides user identity certificates that are independent of GoNode. GoNode only handles operator identity, not user identity.

```
User identity (GoUNITY):
  Ed25519 certificate for username "Alice"
  Used in GoBot-moderated groups, GoLab community channels
  GoNode has no awareness of this

Operator identity (GoNode):
  Ed25519 key for service node operator
  Used for staking, audits, rewards
  GoUNITY has no awareness of this

Shared infrastructure:
  GoUNITY's certificates are transmitted through GoNode swarms
  as ciphertext (same as any other message)
  GoNode cannot decrypt or inspect them
```

### 10.5 Integration with GoLab

GoLab uses GoNode swarms for channel fan-out:

```
GoLab user posts in a channel
  |
  | ActivityStreams message signed with GoUNITY cert
  v
Message enters GoBot
  |
  | GoBot fans out to all channel subscribers
  v
Each subscriber's SMP queue lives on a GoNode swarm
  |
  | GoNode swarms replicate across 5-10 nodes each
  v
Subscribers fetch and decrypt messages
```

At scale, GoLab communities with thousands of members depend on GoNode's swarm infrastructure for efficient fan-out.

---

## 11. Related components

| Component | Role | Documentation |
|:----------|:-----|:-------------|
| [SimpleGo](https://github.com/saschadaemgen/SimpleGo) | Hardware messenger, protocol foundation | [SimpleGo repo](https://github.com/saschadaemgen/SimpleGo) |
| [GoBot](https://github.com/saschadaemgen/GoBot) | Moderation layer using GoNode infrastructure | [Architecture](https://github.com/saschadaemgen/GoBot/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) |
| [GoKey](https://github.com/saschadaemgen/SimpleGo) | Hardware operator identity anchor | [Architecture](https://github.com/saschadaemgen/SimpleGo/blob/main/templates/gokey/docs/ARCHITECTURE_AND_SECURITY.md) |
| [GoUNITY](https://github.com/saschadaemgen/GoUNITY) | User identity certificates (separate from operator identity) | [Architecture](https://github.com/saschadaemgen/GoUNITY/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) |
| [GoLab](https://github.com/saschadaemgen/GoLab) | Community platform using GoNode for channel fan-out | [Architecture](https://github.com/saschadaemgen/GoLab/blob/main/docs/ARCHITECTURE_AND_SECURITY.md) |

---

*GoNode Architecture & Security v1 - April 2026*
*IT and More Systems, Recklinghausen, Germany*
