package handler

import (
	"image"
	_ "image/gif" // register GIF decoder (still rendered as JPEG)
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"golang.org/x/image/draw"
)

const (
	imageMaxBytes = 5 * 1024 * 1024 // 5 MB
	imageMaxWidth = 1200
)

type ImageHandler struct {
	DB      *pgxpool.Pool
	RootDir string // typically "web/static"
}


// Upload accepts a multipart image, resizes to max 1200px width, saves as
// JPEG under {RootDir}/uploads/images/{uuid}.jpg, records it in the uploads
// table, and returns the public URL.
func (h *ImageHandler) Upload(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, imageMaxBytes)
	if err := r.ParseMultipartForm(imageMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "image too large (max 5 MB) or invalid form")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no file provided")
		return
	}
	defer file.Close()

	ct := header.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		writeError(w, http.StatusBadRequest, "file must be an image")
		return
	}
	// Restrict to the formats we actually want to serve.
	allowed := map[string]bool{
		"image/jpeg": true, "image/jpg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
	}
	if !allowed[ct] {
		writeError(w, http.StatusBadRequest, "unsupported image type")
		return
	}

	img, _, err := image.Decode(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not decode image")
		return
	}
	resized := resizeToMaxWidth(img, imageMaxWidth)

	dir := filepath.Join(h.RootDir, "uploads", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("image: mkdir", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	id := uuid.NewString()
	filename := id + ".jpg"
	target := filepath.Join(dir, filename)
	out, err := os.Create(target)
	if err != nil {
		slog.Error("image: create", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer out.Close()

	if err := jpeg.Encode(out, resized, &jpeg.Options{Quality: 85}); err != nil {
		slog.Error("image: encode", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	url := "/static/uploads/images/" + filename
	// Record the upload. Don't fail the request if this insert errors -
	// the image is already on disk and usable.
	if h.DB != nil {
		_, err := h.DB.Exec(r.Context(),
			`INSERT INTO uploads (user_id, filename, mime_type, size_bytes, url)
			 VALUES ($1, $2, $3, $4, $5)`,
			user.ID, filename, "image/jpeg", header.Size, url,
		)
		if err != nil {
			slog.Warn("image: uploads row insert failed", "error", err)
		}
	}

	slog.Info("image uploaded", "user", user.Username, "size", header.Size, "url", url)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":  url,
		"uuid": id,
	})
}

func resizeToMaxWidth(src image.Image, maxW int) image.Image {
	b := src.Bounds()
	w := b.Dx()
	if w <= maxW {
		return src
	}
	h := b.Dy()
	newW := maxW
	newH := int(float64(h) * float64(maxW) / float64(w))
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

