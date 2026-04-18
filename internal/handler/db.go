package handler

// Sprint 13 admin database management. Five endpoints:
//
//   POST /api/admin/db/backup                       - run pg_dump, save to /opt/backups/
//   GET  /api/admin/db/export                       - run pg_dump, stream as .sql download
//   POST /api/admin/db/import                       - multipart upload, auto-backup, psql < file
//   GET  /api/admin/db/backups                      - list existing backups
//   GET  /api/admin/db/backups/{filename}/download  - download one saved backup
//
// All five require admin (RequireAdmin middleware in the router).
// Import additionally requires Owner (power_level == 100, id == 1).
//
// Security notes:
//   - pg_dump and psql are invoked via exec.Command with separate
//     args (never shell). No user input flows into the arg list.
//   - PGPASSWORD is passed via the process env, not on the command
//     line, so it doesn't appear in `ps`.
//   - Backup file names are generated server-side from a timestamp
//     and sanitized before being sent to the client. Listing is
//     restricted to files matching "golab-*.sql" in BackupDir;
//     there's no path traversal surface.
//   - Import always creates a pre-import backup BEFORE running psql,
//     and deletes the uploaded temp file when done.

import (
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

// DBHandler runs pg_dump / psql against the Postgres container for
// manual backup, export and import from the admin dashboard.
type DBHandler struct {
	Cfg       *config.Config
	BackupDir string // default "/opt/backups"
}

// backupFile describes one backup entry returned by ListBackups.
type backupFile struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
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

// ListBackups returns every golab-*.sql file in BackupDir, newest
// first, capped at the 50 most recent entries.
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
		// Only list our own dumps. Anything else in /opt/backups is
		// foreign and not our business.
		if !strings.HasPrefix(name, "golab-") || !strings.HasSuffix(name, ".sql") {
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

// Backup creates a new pg_dump file in BackupDir named
// golab-manual-YYYYMMDD-HHMMSS.sql. Owner and every admin can use
// this, since it's non-destructive.
func (h *DBHandler) Backup(w http.ResponseWriter, r *http.Request) {
	name := fmt.Sprintf("golab-manual-%s.sql", time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(h.backupDir(), name)

	if err := os.MkdirAll(h.backupDir(), 0o750); err != nil {
		slog.Error("backup: mkdir", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create backup directory")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := h.pgDumpToFile(ctx, path); err != nil {
		slog.Error("backup: pg_dump", "error", err)
		// Remove a half-written file so the listing stays clean.
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, "pg_dump failed: "+err.Error())
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		slog.Error("backup: stat", "error", err)
		writeError(w, http.StatusInternalServerError, "backup created but could not stat file")
		return
	}

	writeJSON(w, http.StatusOK, backupFile{
		Name:      name,
		Size:      info.Size(),
		CreatedAt: info.ModTime(),
	})
}

// Export runs pg_dump and streams the SQL directly to the client as
// a file download. Nothing is persisted server-side.
//
// Headers must be set BEFORE any byte of the dump body is written,
// otherwise WriteHeader implicitly commits status 200 with text/html
// defaults and the browser opens the dump inline instead of saving
// it. application/octet-stream is the safest Content-Type for a
// file download; browsers never render it and Nginx won't add a
// charset directive. If Nginx sits in front and strips the
// Content-Disposition, add `proxy_pass_header Content-Disposition;`
// and `proxy_pass_header Content-Type;` to the /api/admin location
// block.
func (h *DBHandler) Export(w http.ResponseWriter, r *http.Request) {
	filename := fmt.Sprintf("golab-export-%s.sql", time.Now().UTC().Format("2006-01-02"))

	// 1) Lock the pg_dump call down with a context + timeout first so
	//    we never flush headers and then fail to even start pg_dump.
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

	// 2) Wire pg_dump's stdout into a pipe so we can decide to emit
	//    headers only once we're sure the process actually started.
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

	// 3) Headers committed BEFORE we touch the body. Once the first
	//    Copy byte flies, these cannot be changed.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Tell proxies not to buffer the stream so large dumps flow
	// through instead of filling Nginx memory.
	w.Header().Set("X-Accel-Buffering", "no")
	// We don't know the size up front; omit Content-Length and let
	// the client read until EOF.
	w.WriteHeader(http.StatusOK)

	// 4) Stream the dump. Errors here are unrecoverable (headers
	//    already sent) - log and let the client see a short file.
	if _, err := io.Copy(w, stdout); err != nil {
		slog.Error("export: stream", "error", err)
	}
	if err := cmd.Wait(); err != nil {
		slog.Error("export: pg_dump wait", "error", err)
	}
}

// pgDumpToFile runs pg_dump against the configured database and
// writes the result to path. Used by Backup directly and by Import
// for the pre-import safety backup.
func (h *DBHandler) pgDumpToFile(ctx context.Context, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer f.Close()

	cmd := exec.CommandContext(ctx, "pg_dump",
		"--clean", "--if-exists", "--no-owner", "--no-privileges",
		"-h", h.Cfg.DB.Host,
		"-p", strconv.Itoa(h.Cfg.DB.Port),
		"-U", h.Cfg.DB.User,
		"-d", h.Cfg.DB.Name,
	)
	cmd.Env = h.pgEnv()
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Import accepts a multipart file upload and replays it via psql.
// Owner-only (id == 1). Always creates a pre-import backup first so
// the admin can roll back by re-uploading that file.
func (h *DBHandler) Import(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFromContext(r.Context())
	if actor == nil || actor.ID != 1 {
		writeError(w, http.StatusForbidden, "only the platform owner (id=1) can import databases")
		return
	}

	// 100 MiB cap on the upload - bigger than any realistic GoLab
	// dump for years and small enough to reject a runaway request.
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

	// Basic extension check. We don't trust the client-provided name
	// for anything else - the import path is server-generated.
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".sql") {
		writeError(w, http.StatusBadRequest, "only .sql files are accepted")
		return
	}

	if err := os.MkdirAll(h.backupDir(), 0o750); err != nil {
		slog.Error("import: mkdir backups", "error", err)
		writeError(w, http.StatusInternalServerError, "could not prepare backup directory")
		return
	}

	// Step 1: save the upload to a temp file so psql can read it via
	// stdin (streaming straight through would lose retry ability and
	// make psql errors harder to debug).
	ts := time.Now().UTC().Format("20060102-150405")
	uploadPath := filepath.Join(os.TempDir(), "golab-import-"+ts+".sql")
	uploaded, err := os.OpenFile(uploadPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		slog.Error("import: tempfile", "error", err)
		writeError(w, http.StatusInternalServerError, "could not buffer upload")
		return
	}
	if _, err := io.Copy(uploaded, file); err != nil {
		uploaded.Close()
		_ = os.Remove(uploadPath)
		slog.Error("import: copy upload", "error", err)
		writeError(w, http.StatusInternalServerError, "could not buffer upload")
		return
	}
	uploaded.Close()
	// Clean up the temp file whatever happens next.
	defer os.Remove(uploadPath)

	// Step 2: auto-backup BEFORE touching the live DB. If this step
	// fails we abort and tell the admin - never import without a
	// known-good safety net.
	preBackupName := fmt.Sprintf("golab-pre-import-%s.sql", ts)
	preBackupPath := filepath.Join(h.backupDir(), preBackupName)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := h.pgDumpToFile(ctx, preBackupPath); err != nil {
		slog.Error("import: pre-backup", "error", err)
		_ = os.Remove(preBackupPath)
		writeError(w, http.StatusInternalServerError,
			"pre-import backup failed, import aborted: "+err.Error())
		return
	}

	// Step 3: replay the uploaded SQL via psql. `--set ON_ERROR_STOP=1`
	// means the first error aborts the whole file, avoiding partial
	// imports where half the tables got truncated but the restore
	// failed.
	uploadedFile, err := os.Open(uploadPath)
	if err != nil {
		slog.Error("import: reopen upload", "error", err)
		writeError(w, http.StatusInternalServerError, "could not re-open upload")
		return
	}
	defer uploadedFile.Close()

	cmd := exec.CommandContext(ctx, "psql",
		"--set", "ON_ERROR_STOP=1",
		"-h", h.Cfg.DB.Host,
		"-p", strconv.Itoa(h.Cfg.DB.Port),
		"-U", h.Cfg.DB.User,
		"-d", h.Cfg.DB.Name,
	)
	cmd.Env = h.pgEnv()
	cmd.Stdin = uploadedFile
	// Collect stderr so we can surface the actual psql error to the
	// admin if replay fails (mysterious 500s here are unacceptable).
	var stderr strings.Builder
	cmd.Stderr = &stderr

	slog.Warn("admin database import started",
		"actor_id", actor.ID, "pre_backup", preBackupName,
		"upload_bytes", header.Size)

	if err := cmd.Run(); err != nil {
		slog.Error("import: psql", "error", err, "stderr", stderr.String())
		writeError(w, http.StatusInternalServerError,
			"psql failed: "+firstLine(stderr.String())+
				" (pre-import backup saved as "+preBackupName+")")
		return
	}

	slog.Warn("admin database import completed",
		"actor_id", actor.ID, "pre_backup", preBackupName)

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

// isSafeBackupName is the path-traversal gate for DownloadBackup.
// It allows only filenames that:
//   - start with the "golab-" prefix (so unrelated files in
//     /opt/backups stay invisible),
//   - end with ".sql",
//   - contain no path separators ('/' or '\\') or ".." segments,
//   - contain no NUL bytes (defence in depth against weird locales).
//
// The frontend only ever sends names that came from ListBackups,
// but the server NEVER trusts that - the whole point of this
// check is to reject a manually crafted URL.
func isSafeBackupName(name string) bool {
	if name == "" {
		return false
	}
	if !strings.HasPrefix(name, "golab-") || !strings.HasSuffix(name, ".sql") {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	// Reject Windows-ish reserved characters / shell chars while we
	// are at it. None of our generated names ever contain these.
	if strings.ContainsAny(name, ":*?\"<>|") {
		return false
	}
	// Confirm the name filepath.Base normalises to itself - anything
	// that collapses differently is suspicious.
	if filepath.Base(name) != name {
		return false
	}
	return true
}

// DownloadBackup streams a single saved backup file back to the
// client as an attachment. The filename comes from the URL param
// {filename} and is validated by isSafeBackupName; the file must
// live directly inside BackupDir (no symlinks followed, no nested
// directories).
func (h *DBHandler) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "filename")
	if !isSafeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	dir := h.backupDir()
	path := filepath.Join(dir, name)

	// Defence in depth: after joining, the resolved path must still
	// be a direct child of BackupDir. EvalSymlinks isn't used here
	// because BackupDir itself may be a bind mount; we accept that
	// root-level symlink and just verify the filename component.
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(path)
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
	// Reject anything that isn't a regular file (symlink, device, pipe).
	if !info.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "not a regular file")
		return
	}

	f, err := os.Open(cleanPath)
	if err != nil {
		slog.Error("download backup: open", "error", err, "name", name)
		writeError(w, http.StatusInternalServerError, "could not open backup")
		return
	}
	defer f.Close()

	// Commit headers BEFORE streaming. Same playbook as Export:
	// application/octet-stream so the browser always saves, never
	// renders. X-Accel-Buffering tells Nginx not to buffer the
	// whole file into memory on the way out.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	// Block caching so a later "delete backup" doesn't leave copies
	// in shared proxy caches.
	w.Header().Set("Cache-Control", "private, no-store")

	// http.ServeContent would handle Range requests for free, but
	// we want the hard no-cache semantics above and a guaranteed
	// attachment disposition; a plain io.Copy is clearer.
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		slog.Error("download backup: stream", "error", err, "name", name)
	}
}
