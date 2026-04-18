package handler

// Sprint 13 follow-up: AES-256-GCM encryption for every backup
// file written to /opt/backups. Keeps admins out of the "I found
// a plaintext dump in the filesystem and it contained every
// bcrypt hash on the platform" situation if the VPS disk is ever
// exfiltrated.
//
// File format on disk:
//
//     | 12-byte nonce | ciphertext | 16-byte GCM auth tag |
//
// The nonce is generated per file via crypto/rand and prepended
// so decrypt is self-contained - no sidecar state, no lookup
// table, just the file bytes + the key.
//
// The key comes from the GOLAB_BACKUP_KEY env var as a base64-
// encoded 32-byte value. If unset, NewBackupCrypto generates a
// fresh one and logs it EXACTLY ONCE at WARN level so the admin
// can copy it into docker-compose.yml. A missing key on the next
// restart means a new key gets generated and every previously
// encrypted backup becomes unrestorable - this is by design, not
// a bug: encryption without a persisted key is useless.
//
// Security invariants this file guarantees:
//
//   - Key is never persisted to disk, never stored in the DB,
//     never sent over the wire, never logged after startup.
//   - Every Encrypt call uses a fresh random nonce (GCM nonce
//     reuse with the same key breaks confidentiality + integrity).
//   - Decrypt uses GCM auth tag verification; a tampered blob
//     returns an error, never "best-effort" plaintext.
//   - Callers are responsible for zeroing plaintext buffers
//     before returning them to the GC.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
)

// BackupCrypto wraps an AES-256-GCM AEAD tied to a single key.
// Constructed once per process in main.go and shared (read-only)
// with the DBHandler.
type BackupCrypto struct {
	gcm cipher.AEAD
}

// NewBackupCrypto sets up AES-256-GCM using the base64-encoded key
// passed in. Empty string means "generate a fresh key and log it
// once". Any non-empty value that doesn't decode to exactly 32
// bytes is a hard error: we must not silently fall through to a
// different key than the admin configured.
func NewBackupCrypto(keyB64 string) (*BackupCrypto, error) {
	var key []byte
	if keyB64 == "" {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generating backup key: %w", err)
		}
		// One-shot visible log so the admin can copy it. This is
		// the ONLY place the raw key touches any output channel.
		// Don't change to Info - WARN makes it survive coarse log
		// filters and surfaces in docker logs without -v.
		encoded := base64.StdEncoding.EncodeToString(key)
		slog.Warn("============================================================")
		slog.Warn("GOLAB_BACKUP_KEY was empty - a new AES-256 key was generated.")
		slog.Warn("SAVE IT NOW. Without it every encrypted backup is unreadable.")
		slog.Warn("Add this line to docker-compose.yml (golab service env):")
		slog.Warn("GOLAB_BACKUP_KEY=" + encoded)
		slog.Warn("Restart the container after setting it, or every new start")
		slog.Warn("will generate a fresh key and orphan previous backups.")
		slog.Warn("============================================================")
	} else {
		decoded, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil {
			return nil, fmt.Errorf("GOLAB_BACKUP_KEY is not valid base64: %w", err)
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf(
				"GOLAB_BACKUP_KEY must decode to 32 bytes (AES-256), got %d",
				len(decoded))
		}
		key = decoded
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	// Zero out the local key slice. gcm retains its own copy
	// internally (stdlib's aesCipher.expandKey) so we can safely
	// clear ours.
	for i := range key {
		key[i] = 0
	}
	return &BackupCrypto{gcm: gcm}, nil
}

// NonceSize is exposed so callers can reason about the file
// layout without importing crypto/cipher.
func (c *BackupCrypto) NonceSize() int { return c.gcm.NonceSize() }

// Encrypt produces nonce||ciphertext||tag as a single blob
// suitable for writing straight to disk. Each call generates a
// fresh random nonce via crypto/rand.
func (c *BackupCrypto) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	// Seal with nil dst allocates a fresh ciphertext slice so the
	// nonce and ciphertext live in separate backing arrays; we
	// concatenate them explicitly for clarity over cleverness.
	ct := c.gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Decrypt inverts Encrypt. A tampered blob, a wrong key, or a
// truncated file all return a non-nil error - Decrypt NEVER
// returns partial plaintext.
func (c *BackupCrypto) Decrypt(blob []byte) ([]byte, error) {
	ns := c.gcm.NonceSize()
	if len(blob) < ns+c.gcm.Overhead() {
		return nil, errors.New("encrypted blob too short")
	}
	nonce := blob[:ns]
	ciphertext := blob[ns:]
	pt, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Don't expose GCM internals; treat every failure mode as
		// "authentication failed" at the caller boundary.
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return pt, nil
}

// ZeroBytes wipes a byte slice in place. Used on plaintext buffers
// (dump output, decrypted import body) before they drop out of
// scope so the GC doesn't hand them back to another allocation
// with recognisable SQL still in place.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
