package handler

import (
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"golang.org/x/image/draw"
)

const (
	avatarSize       = 256
	avatarMaxBytes   = 5 * 1024 * 1024 // 5 MB upload limit
	avatarsDirSuffix = "uploads/avatars"
)

// AvatarHandler manages profile picture upload + deletion.
type AvatarHandler struct {
	Users   *model.UserStore
	RootDir string // e.g. "web/static"
}

// Upload accepts a multipart image, resizes to 256x256 JPEG, saves to
// {RootDir}/uploads/avatars/{user_id}.jpg, and updates the user's avatar_url.
func (h *AvatarHandler) Upload(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Limit the whole request size.
	r.Body = http.MaxBytesReader(w, r.Body, avatarMaxBytes)
	if err := r.ParseMultipartForm(avatarMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "image too large or invalid form")
		return
	}

	file, header, err := r.FormFile("avatar")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no file provided")
		return
	}
	defer file.Close()

	// Content-type sniff from the first 512 bytes is cheap, but image.Decode
	// covers validation too. Keep the header check for faster rejection.
	ct := header.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		writeError(w, http.StatusBadRequest, "file must be an image")
		return
	}

	img, _, err := image.Decode(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not decode image")
		return
	}

	resized := resizeAndCenterCrop(img, avatarSize)

	// Ensure directory exists.
	dir := filepath.Join(h.RootDir, avatarsDirSuffix)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("avatar: mkdir", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Save as JPEG (smaller than PNG, good enough for avatars).
	target := filepath.Join(dir, fmt.Sprintf("%d.jpg", user.ID))
	out, err := os.Create(target)
	if err != nil {
		slog.Error("avatar: create file", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer out.Close()

	if err := jpeg.Encode(out, resized, &jpeg.Options{Quality: 88}); err != nil {
		slog.Error("avatar: encode", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Public URL with cache-busting query string.
	publicURL := fmt.Sprintf("/static/uploads/avatars/%d.jpg?v=%d", user.ID, header.Size)
	if err := h.Users.UpdateProfile(r.Context(), user.ID, user.DisplayName, user.Bio, publicURL); err != nil {
		slog.Error("avatar: update profile", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"avatar_url": publicURL,
	})
}

// Remove deletes the avatar file and clears avatar_url.
func (h *AvatarHandler) Remove(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	target := filepath.Join(h.RootDir, avatarsDirSuffix, fmt.Sprintf("%d.jpg", user.ID))
	_ = os.Remove(target)

	if err := h.Users.UpdateProfile(r.Context(), user.ID, user.DisplayName, user.Bio, ""); err != nil {
		slog.Error("avatar: clear url", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// resizeAndCenterCrop scales the image so the shorter side fits `size`, then
// center-crops to a square. Uses catmull-rom for quality on downscales.
func resizeAndCenterCrop(src image.Image, size int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// Scale so the shorter side equals size
	var scaledW, scaledH int
	if w < h {
		scaledW = size
		scaledH = int(float64(h) * float64(size) / float64(w))
	} else {
		scaledH = size
		scaledW = int(float64(w) * float64(size) / float64(h))
	}

	scaled := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Over, nil)

	// Center crop to size x size
	offsetX := (scaledW - size) / 2
	offsetY := (scaledH - size) / 2
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(out, out.Bounds(), scaled, image.Point{X: offsetX, Y: offsetY}, draw.Src)
	return out
}

