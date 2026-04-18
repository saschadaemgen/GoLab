package handler

// Sprint 13 admin database management. Seven endpoints:
//
//   POST   /api/admin/db/backup                       - pg_dump, encrypted .sql.enc written to /opt/backups/
//   GET    /api/admin/db/export                       - pg_dump, streamed as plaintext .sql download
//   POST   /api/admin/db/import                       - multipart upload (.sql or .sql.enc), auto pre-import backup, psql
//   GET    /api/admin/db/backups                      - list existing backups (both .sql legacy and .sql.enc)
//   GET    /api/admin/db/backups/{filename}/download  - download one saved backup (decrypts .sql.enc on-the-fly)
//   DELETE /api/admin/db/backups/{filename}           - delete one saved backup
//
// All endpoints require admin (RequireAdmin middleware in the router).
// Import additionally requires Owner (power_level == 100, id == 1).
//
// Encryption (follow-up to the initial Sprint 13 commits):
//   - Every new backup is written as golab-*.sql.enc containing
//     12-byte random nonce + AES-256-GCM ciphertext + 16-byte tag.
//   - The key lives in GOLAB_BACKUP_KEY env var (base64, 32 bytes).
//   - Unencrypted legacy .sql files written before this feature
//     landed are still listed + downloadable + deletable; they
//     just carry a warning icon in the UI. Import accepts both.
//   - Plaintext never touches disk during backup: pg_dump streams
//     into a memory buffer which is immediately encrypted and
//     atomically written to the final .sql.enc file (0o600).
//   - After every successful new backup, cleanupOld() trims the
//     backup dir to the 20 most recent files.
//
// Security notes:
//   - pg_dump / psql invoked via exec.Command with separate args.
//     No user input flows into the arg list.
//   - PGPASSWORD passed via process env, not command line.
//   - Backup file names generated server-side from a timestamp.
//   - isSafeBackupName gates the {filename} URL param against
//     path traversal / reserved characters / weird suffixes.
//   - Plaintext buffers (pg_dump output, decrypted import body)
//     are zeroed via ZeroBytes before dropping out of scope.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/config"
)

// maxBackups is the upper bound on files kept in BackupDir. After
// every successful new backup the oldest files above this count
// are deleted. Pre-import backups are counted together with manual
// backups - one shared retention pool.
const maxBackups = 20

// DBHandler runs pg_dump / psql against the Postgres container for
// manual backup, export and import from the admin dashboard. Crypto
// is required and must be non-nil when the handler is constructed.
type DBHandler struct {
	Cfg       *config.Config
	Crypto    *BackupCrypto
	BackupDir string // default "/opt/backups"
}

// backupFile describes one backup entry returned by ListBackups.
// Encrypted tells the UI whether to show a lock icon (true,
// .sql.enc) or a warning icon (false, legacy .sql plaintext).
type backupFile struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Encrypted bool      `json:"encrypted"`
}

// backupDir returns the configured backup directory, defaulting to
// /opt/backups so the mount in docker-compose.yml is picked up
// without extra configuration.
func (h *DBHandler) backupDir() string {
	if h.BackupDir != "" {
		return h.BackupDir
	}
	return "/opt/backups"
}

// pgEnv builds the environment used by pg_dump / psql. Only
// PGPASSWORD is added to avoid leaking secrets on the command line.
// PATH is preserved so the binaries are found.
func (h *DBHandler) pgEnv() []string {
	env := os.Environ()
	env = append(env, "PGPASSWORD="+h.Cfg.DB.Password)
	return env
}

// ListBackups returns every golab-*.sql and golab-*.sql.enc file in
// BackupDir, newest first, capped at the 50 most recent entries.
func (h *DBHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	files, err := h.scanBackups()
	if err != nil {
		slog.Error("list backups", "error", err)
		writeError(w, http.StatusInternalServerError, "could not list backups")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": files})
}

func (h *DBHandler) scanBackups() ([]backupFile, error) {
	dir := h.backupDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []backupFile{}, nil
		}
		return nil, err
	}
	var out []backupFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "golab-") {
			continue
		}
		// Accept both the new .sql.enc format and the pre-encryption
		// .sql legacy files. The .sql.enc check comes first so the
		// boolean below is right.
		encrypted := strings.HasSuffix(name, ".sql.enc")
		plaintext := strings.HasSuffix(name, ".sql")
		if !encrypted && !plaintext {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, backupFile{
			Name:      name,
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
			Encrypted: encrypted,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

// Backup creates a new encrypted pg_dump file in BackupDir named
// golab-manual-YYYYMMDD-HHMMSS.sql.enc. Owner and every admin can
// use this, since it's non-destructive.
func (h *DBHandler) Backup(w http.ResponseWriter, r *http.Request) {
	name := fmt.Sprintf("golab-manual-%s.sql.enc", time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(h.backupDir(), name)

	if err := os.MkdirAll(h.backupDir(), 0o750); err != nil {
		slog.Error("backup: mkdir", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create backup directory")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := h.pgDumpEncrypted(ctx, path); err != nil {
		slog.Error("backup: pg_dump encrypted", "error", err)
		// Remove a half-written file so the listing stays clean.
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		slog.Error("backup: stat", "error", err)
		writeError(w, http.StatusInternalServerError, "backup created but could not stat file")
		return
	}

	h.cleanupOld()

	writeJSON(w, http.StatusOK, backupFile{
		Name:      name,
		Size:      info.Size(),
		CreatedAt: info.ModTime(),
		Encrypted: true,
	})
}

// Export runs pg_dump and streams the SQL directly to the client as
// a plaintext file download. Nothing is persisted server-side, so
// this endpoint does NOT go through the encrypted-at-rest pipeline.
// It's intended for "give me a dump I can take to another host",
// where encrypting with a key the destination doesn't have would
// defeat the point.
//
// Headers must be set BEFORE any byte of the dump body is written,
// otherwise WriteHeader implicitly commits status 200 with text/html
// defaults and the browser opens the dump inline instead of saving
// it.
func (h *DBHandler) Export(w http.ResponseWriter, r *http.Request) {
	filename := fmt.Sprintf("golab-export-%s.sql", time.Now().UTC().Format("2006-01-02"))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pg_dump",
		"--clean", "--if-exists", "--no-owner", "--no-privileges",
		"-h", h.Cfg.DB.Host,
		"-p", strconv.Itoa(h.Cfg.DB.Port),
		"-U", h.Cfg.DB.User,
		"-d", h.Cfg.DB.Name,
	)
	cmd.Env = h.pgEnv()
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Error("export: stdout pipe", "error", err)
		writeError(w, http.StatusInternalServerError, "could not open pg_dump pipe")
		return
	}
	if err := cmd.Start(); err != nil {
		slog.Error("export: pg_dump start", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start pg_dump")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, stdout); err != nil {
		slog.Error("export: stream", "error", err)
	}
	if err := cmd.Wait(); err != nil {
		slog.Error("export: pg_dump wait", "error", err)
	}
}

// pgDumpEncrypted runs pg_dump, encrypts the output in memory, and
// writes the ciphertext atomically to path with 0o600 permissions.
// The plaintext dump NEVER touches disk: pg_dump stdout goes into
// a bytes.Buffer, the buffer is encrypted, the plaintext is zeroed,
// and the encrypted blob is written via tmp-and-rename.
func (h *DBHandler) pgDumpEncrypted(ctx context.Context, path string) error {
	if h.Crypto == nil {
		return fmt.Errorf("backup crypto not configured")
	}

	cmd := exec.CommandContext(ctx, "pg_dump",
		"--clean", "--if-exists", "--no-owner", "--no-privileges",
		"-h", h.Cfg.DB.Host,
		"-p", strconv.Itoa(h.Cfg.DB.Port),
		"-U", h.Cfg.DB.User,
		"-d", h.Cfg.DB.Name,
	)
	cmd.Env = h.pgEnv()
	cmd.Stderr = os.Stderr

	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		// Scrub any partial output that may already be in the buffer.
		ZeroBytes(buf.Bytes())
		return fmt.Errorf("pg_dump: %w", err)
	}

	plaintext := buf.Bytes()
	ciphertext, err := h.Crypto.Encrypt(plaintext)
	// Wipe plaintext immediately regardless of encryption result.
	ZeroBytes(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	// Atomic write: .tmp then rename. 0o600 so only the app user
	// can read the ciphertext (belt and suspenders - the key is
	// separate, but reducing the file's reach is free security).
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, ciphertext, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	// Re-chmod defensively: some filesystems preserve mode on
	// rename, others don't. Explicit 0o600 is the intent.
	if err := os.Chmod(path, 0o600); err != nil {
		slog.Warn("pg_dump encrypted: chmod", "path", path, "error", err)
	}
	return nil
}

// cleanupOld keeps the backup directory at no more than maxBackups
// files (both .sql and .sql.enc count toward the cap). The oldest
// files above the cap are removed. Errors are logged but do not
// surface to the user - cleanup is best-effort.
func (h *DBHandler) cleanupOld() {
	files, err := h.scanBackups()
	if err != nil || len(files) <= maxBackups {
		return
	}
	// scanBackups sorts newest-first, so the tail of the slice
	// holds the oldest entries.
	for _, f := range files[maxBackups:] {
		path := filepath.Join(h.backupDir(), f.Name)
		if err := os.Remove(path); err != nil {
			slog.Warn("cleanup: remove old backup", "name", f.Name, "error", err)
			continue
		}
		slog.Info("cleanup: removed old backup",
			"name", f.Name, "size", f.Size, "age", time.Since(f.CreatedAt).Round(time.Second))
	}
}

// Import accepts a multipart upload (.sql or .sql.enc), verifies
// it, creates a pre-import backup, then replays the SQL via psql.
// Owner-only (id == 1). Plaintext of the upload never hits disk -
// everything flows through memory and into psql via stdin.
func (h *DBHandler) Import(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFromContext(r.Context())
	if actor == nil || actor.ID != 1 {
		writeError(w, http.StatusForbidden, "only the platform owner (id=1) can import databases")
		return
	}

	const maxUpload = 100 << 20
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	// Accept .sql (legacy plaintext) and .sql.enc (Sprint 13+).
	lowerName := strings.ToLower(header.Filename)
	isEnc := strings.HasSuffix(lowerName, ".sql.enc") || strings.HasSuffix(lowerName, ".enc")
	isSQL := strings.HasSuffix(lowerName, ".sql")
	if !isEnc && !isSQL {
		writeError(w, http.StatusBadRequest, "only .sql or .sql.enc files are accepted")
		return
	}

	if err := os.MkdirAll(h.backupDir(), 0o750); err != nil {
		slog.Error("import: mkdir backups", "error", err)
		writeError(w, http.StatusInternalServerError, "could not prepare backup directory")
		return
	}

	// Step 1: read the upload fully into memory, cap-enforced. The
	// ParseMultipartForm cap above limits this; io.LimitReader is
	// a second defence in case the header was malformed.
	uploaded, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		slog.Error("import: read upload", "error", err)
		writeError(w, http.StatusInternalServerError, "could not buffer upload")
		return
	}
	if len(uploaded) > maxUpload {
		ZeroBytes(uploaded)
		writeError(w, http.StatusBadRequest, "upload exceeds 100 MiB")
		return
	}
	// Make sure the upload bytes are scrubbed even if the decrypt
	// path replaces `plaintext` with a new slice.
	defer ZeroBytes(uploaded)

	// Step 2: decrypt if encrypted, else use as-is. Tampered or
	// wrong-key .enc files get rejected here BEFORE we touch the
	// live DB.
	var plaintext []byte
	if isEnc {
		if h.Crypto == nil {
			writeError(w, http.StatusInternalServerError, "backup crypto not configured")
			return
		}
		pt, err := h.Crypto.Decrypt(uploaded)
		if err != nil {
			slog.Error("import: decrypt upload", "error", err)
			writeError(w, http.StatusBadRequest, "decryption failed - wrong key or tampered file")
			return
		}
		plaintext = pt
		defer ZeroBytes(plaintext)
	} else {
		plaintext = uploaded
	}

	// Step 3: auto-backup BEFORE touching the live DB. If this step
	// fails we abort and tell the admin - never import without a
	// known-good safety net.
	ts := time.Now().UTC().Format("20060102-150405")
	preBackupName := fmt.Sprintf("golab-pre-import-%s.sql.enc", ts)
	preBackupPath := filepath.Join(h.backupDir(), preBackupName)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := h.pgDumpEncrypted(ctx, preBackupPath); err != nil {
		slog.Error("import: pre-backup", "error", err)
		_ = os.Remove(preBackupPath)
		writeError(w, http.StatusInternalServerError,
			"pre-import backup failed, import aborted: "+err.Error())
		return
	}

	// Step 4: replay the plaintext SQL via psql, piped in on stdin.
	// --set ON_ERROR_STOP=1 aborts the whole file on the first
	// error, avoiding partial imports.
	cmd := exec.CommandContext(ctx, "psql",
		"--set", "ON_ERROR_STOP=1",
		"-h", h.Cfg.DB.Host,
		"-p", strconv.Itoa(h.Cfg.DB.Port),
		"-U", h.Cfg.DB.User,
		"-d", h.Cfg.DB.Name,
	)
	cmd.Env = h.pgEnv()
	cmd.Stdin = bytes.NewReader(plaintext)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	slog.Warn("admin database import started",
		"actor_id", actor.ID, "pre_backup", preBackupName,
		"upload_bytes", header.Size, "encrypted_upload", isEnc)

	if err := cmd.Run(); err != nil {
		slog.Error("import: psql", "error", err, "stderr", stderr.String())
		writeError(w, http.StatusInternalServerError,
			"psql failed: "+firstLine(stderr.String())+
				" (pre-import backup saved as "+preBackupName+")")
		return
	}

	slog.Warn("admin database import completed",
		"actor_id", actor.ID, "pre_backup", preBackupName)

	// Cleanup runs AFTER success so a failed import leaves the
	// pre-backup and previous set intact for manual recovery.
	h.cleanupOld()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "imported",
		"pre_backup": preBackupName,
	})
}

// firstLine trims the potentially noisy multi-line psql stderr to
// the first non-empty line so error responses stay readable.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "unknown error"
}

// isSafeBackupName is the path-traversal gate for DownloadBackup /
// DeleteBackup. Allows only filenames that:
//   - start with the "golab-" prefix,
//   - end with ".sql" (legacy plaintext) or ".sql.enc" (encrypted),
//   - contain no path separators ('/' or '\\') or ".." segments,
//   - contain no NUL bytes,
//   - contain no Windows reserved / shell-meta characters.
//
// The frontend only ever sends names that came from ListBackups,
// but the server NEVER trusts that - this check is the whole point.
func isSafeBackupName(name string) bool {
	if name == "" {
		return false
	}
	if !strings.HasPrefix(name, "golab-") {
		return false
	}
	if !strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".sql.enc") {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsAny(name, ":*?\"<>|") {
		return false
	}
	if filepath.Base(name) != name {
		return false
	}
	return true
}

// DownloadBackup streams a saved backup back to the client as an
// attachment. For .sql.enc files the ciphertext is decrypted in
// memory and the ".enc" suffix is stripped from the download
// filename so the browser saves a ready-to-import .sql file. For
// legacy .sql files the bytes are streamed through unchanged.
func (h *DBHandler) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "filename")
	if !isSafeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	dir := h.backupDir()
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(filepath.Join(dir, name))
	if filepath.Dir(cleanPath) != cleanDir {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		slog.Error("download backup: stat", "error", err, "name", name)
		writeError(w, http.StatusInternalServerError, "could not read backup")
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "not a file")
		return
	}
	if !info.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "not a regular file")
		return
	}

	// Load the whole file into memory. For the encrypted path we
	// need the full ciphertext before we can emit any plaintext
	// bytes (GCM auth tag check). For the legacy path we buffer
	// too, for consistency with Content-Length + cleaner error
	// handling. Backups are bounded by maxUpload elsewhere.
	blob, err := os.ReadFile(cleanPath)
	if err != nil {
		slog.Error("download backup: read", "error", err, "name", name)
		writeError(w, http.StatusInternalServerError, "could not open backup")
		return
	}

	var payload []byte
	dlName := name
	if strings.HasSuffix(name, ".sql.enc") {
		if h.Crypto == nil {
			writeError(w, http.StatusInternalServerError, "backup crypto not configured")
			return
		}
		pt, err := h.Crypto.Decrypt(blob)
		// Always zero the ciphertext slice; we don't need it again.
		ZeroBytes(blob)
		if err != nil {
			slog.Error("download backup: decrypt", "error", err, "name", name)
			writeError(w, http.StatusInternalServerError, "decryption failed - wrong key or tampered file")
			return
		}
		payload = pt
		// Serve as golab-xxx.sql (the usable form) not .sql.enc.
		dlName = strings.TrimSuffix(name, ".enc")
	} else {
		payload = blob
	}
	// Scrub plaintext once it's been flushed to the socket.
	defer ZeroBytes(payload)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+dlName+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(payload); err != nil {
		slog.Error("download backup: write", "error", err, "name", name)
	}
}

// DeleteBackup removes a single saved backup file from BackupDir.
// Works on both .sql and .sql.enc entries. Same path-traversal
// gate as DownloadBackup; Warn-level log with actor id so deletes
// can be reconstructed from container logs.
func (h *DBHandler) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "filename")
	if !isSafeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	dir := h.backupDir()
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(filepath.Join(dir, name))
	if filepath.Dir(cleanPath) != cleanDir {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		slog.Error("delete backup: stat", "error", err, "name", name)
		writeError(w, http.StatusInternalServerError, "could not read backup")
		return
	}
	if !info.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "not a regular file")
		return
	}

	actor := auth.UserFromContext(r.Context())
	if err := os.Remove(cleanPath); err != nil {
		slog.Error("delete backup: remove", "error", err, "name", name)
		writeError(w, http.StatusInternalServerError, "could not delete backup")
		return
	}
	var actorID int64
	if actor != nil {
		actorID = actor.ID
	}
	slog.Warn("admin backup deleted",
		"actor_id", actorID, "name", name, "size", info.Size())

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"name":   name,
	})
}
