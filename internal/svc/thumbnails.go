package svc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/gif"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Thumbs generates and caches JPEG thumbnails for the file manager: static
// images via the Go stdlib decoders, video frames via ffmpeg, and PDF first
// pages via poppler's pdftoppm. Both external tools are optional — when
// missing, Get degrades to (nil, false, nil) so callers show a generic
// file-type glyph instead of an error.
type Thumbs struct {
	Cfg   *config.Config
	Files *Files
}

func NewThumbs(cfg *config.Config, files *Files) *Thumbs {
	return &Thumbs{Cfg: cfg, Files: files}
}

var thumbSizes = map[string]int{"sm": 160, "md": 320}

var (
	imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true}
	videoExts = map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true, ".m4v": true}
)

func thumbKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case imageExts[ext]:
		return "image"
	case videoExts[ext]:
		return "video"
	case ext == ".pdf":
		return "pdf"
	default:
		return ""
	}
}

// Get returns cached-or-generated JPEG thumbnail bytes for a user-relative
// path at the given size bucket ("sm" or "md", default "sm"). ok=false with
// a nil error means "not thumbnailable right now" — wrong file type, the
// generator tool isn't installed, or generation failed — callers must
// render a fallback glyph, not an error.
func (t *Thumbs) Get(user *store.User, rel, size string) ([]byte, bool, error) {
	px, ok := thumbSizes[size]
	if !ok {
		px = thumbSizes["sm"]
	}
	abs, err := t.Files.Resolve(user, rel)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return nil, false, nil
	}
	kind := thumbKind(abs)
	if kind == "" {
		return nil, false, nil
	}

	cacheDir := filepath.Join(t.Cfg.ThumbCacheDir, user.Username)
	key := thumbCacheKey(rel, size, info)
	cachePath := filepath.Join(cacheDir, key+".jpg")
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, true, nil
	}

	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return nil, false, nil
	}
	tmp := cachePath + ".tmp"
	var genErr error
	switch kind {
	case "image":
		genErr = generateImageThumb(abs, tmp, px)
	case "video":
		genErr = generateVideoThumb(abs, tmp, px)
	case "pdf":
		genErr = generatePDFThumb(abs, tmp, px)
	}
	if genErr != nil {
		os.Remove(tmp)
		slog.Warn("thumbnail generation failed", "path", rel, "kind", kind, "err", genErr)
		return nil, false, nil
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		os.Remove(tmp)
		return nil, false, nil
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false, nil
	}
	return data, true, nil
}

func thumbCacheKey(rel, size string, info os.FileInfo) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|%d", rel, size, info.ModTime().UnixNano(), info.Size())
	return hex.EncodeToString(h.Sum(nil))
}

// targetSize clamps the longer edge to maxPx, preserving aspect ratio, and
// never upscales a source already smaller than maxPx on both axes.
func targetSize(w, h, maxPx int) (int, int) {
	if w <= maxPx && h <= maxPx {
		return w, h
	}
	if w >= h {
		return maxPx, int(float64(h) * float64(maxPx) / float64(w))
	}
	return int(float64(w) * float64(maxPx) / float64(h)), maxPx
}

func generateImageThumb(src, dstTmp string, px int) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := img.Bounds()
	w, h := targetSize(b.Dx(), b.Dy(), px)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	out, err := os.Create(dstTmp)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, dst, &jpeg.Options{Quality: 82})
}

func generateVideoThumb(src, dstTmp string, px int) error {
	if !LookPath("ffmpeg") {
		return errors.New("ffmpeg not installed")
	}
	// -f image2 -c:v mjpeg: dstTmp has no recognizable extension (it's a
	// *.jpg.tmp staging path renamed into place on success), so ffmpeg can't
	// infer the output muxer/codec from the filename and errors out ("Unable
	// to find a suitable output format") without them being explicit.
	_, err := RunTimeout(20*time.Second, "ffmpeg", "-y", "-i", src,
		"-vf", fmt.Sprintf("thumbnail,scale=%d:-1", px), "-frames:v", "1",
		"-f", "image2", "-c:v", "mjpeg", dstTmp)
	return err
}

func generatePDFThumb(src, dstTmp string, px int) error {
	if !LookPath("pdftoppm") {
		return errors.New("pdftoppm not installed")
	}
	// The scratch dir must live next to dstTmp: os.Rename below can't cross
	// filesystems, and Cfg.ThumbCacheDir is commonly a separate mount (e.g.
	// its own Docker volume) from the system temp dir.
	tmpDir, err := os.MkdirTemp(filepath.Dir(dstTmp), "aegis-pdf-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	prefix := filepath.Join(tmpDir, "page")
	if _, err := RunTimeout(20*time.Second, "pdftoppm", "-jpeg", "-f", "1", "-l", "1",
		"-scale-to", strconv.Itoa(px), src, prefix); err != nil {
		return err
	}
	matches, err := filepath.Glob(prefix + "*.jpg")
	if err != nil || len(matches) == 0 {
		return errors.New("pdftoppm produced no output")
	}
	return os.Rename(matches[0], dstTmp)
}
