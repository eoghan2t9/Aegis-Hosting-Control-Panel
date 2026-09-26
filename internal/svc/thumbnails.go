package svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

	// sandbox prepares where and as whom an external tool runs. nil means
	// accountSandbox. Unexported so only tests in this package can swap it.
	sandbox func(account string) (*toolSandbox, error)
}

// toolSandbox is a private scratch directory plus a way to run an external
// tool inside it. ffmpeg and poppler parse files a customer uploaded, and both
// have a long history of parser bugs and local-file-disclosure tricks
// (crafted playlists that pull in file:// URLs), so they must never run with
// the panel's root privileges.
type toolSandbox struct {
	Dir   string
	Run   func(timeout time.Duration, name string, args ...string) error
	Close func()
}

// accountSandbox runs tools as the customer's own uid/gid, in a scratch
// directory only they can enter, with a minimal environment (the panel's own
// environment carries the database admin passwords).
func accountSandbox(account string) (*toolSandbox, error) {
	uid, gid, groups, err := terminalIdentity(account)
	if err != nil {
		return nil, fmt.Errorf("no system account for %q: %w", account, err)
	}
	if uid == 0 || gid == 0 {
		return nil, errors.New("refusing to run a media tool as root")
	}
	dir, err := os.MkdirTemp("", "aegis-thumb-")
	if err != nil {
		return nil, err
	}
	if err := os.Chown(dir, int(uid), int(gid)); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	return &toolSandbox{
		Dir:   dir,
		Close: func() { os.RemoveAll(dir) },
		Run: func(timeout time.Duration, name string, args ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dir, "LANG=C.UTF-8"}
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: groups}}
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}, nil
}

// maxThumbBytes caps what is accepted back from a tool run as the customer.
const maxThumbBytes = 8 << 20

// takeToolOutput reads a file a customer-privileged tool produced. The tool ran
// as the customer, so the path is not trusted: it must be a plain regular file
// (never a symlink to something root can read) that starts with JPEG magic.
func takeToolOutput(path string) ([]byte, error) {
	fh, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > maxThumbBytes {
		return nil, errors.New("tool produced no usable output")
	}
	data, err := io.ReadAll(io.LimitReader(fh, maxThumbBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		return nil, errors.New("tool output is not a JPEG")
	}
	return data, nil
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
	// Opened as the account: the kernel, not just Resolve's path check, decides
	// whether this customer may read the file.
	fh, info, err := t.Files.OpenRead(user, rel)
	if err != nil {
		return nil, false, nil
	}
	defer fh.Close()
	if info.IsDir() {
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
		genErr = encodeImageThumb(fh, tmp, px)
	case "video":
		genErr = t.generateVideoThumb(user.Username, abs, tmp, px)
	case "pdf":
		genErr = t.generatePDFThumb(user.Username, abs, tmp, px)
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

// maxThumbPixels bounds the decoded size of an image we are willing to
// thumbnail: a tiny PNG can declare gigapixel dimensions and would otherwise be
// decompressed into memory inside the panel process.
const maxThumbPixels = 100_000_000

// encodeImageThumb decodes an already-opened image (opened as the customer) and
// writes its JPEG thumbnail to dstTmp.
func encodeImageThumb(f io.ReadSeeker, dstTmp string, px int) error {
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxThumbPixels {
		return errors.New("image is too large to thumbnail")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
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

func (t *Thumbs) newSandbox(account string) (*toolSandbox, error) {
	if t.sandbox != nil {
		return t.sandbox(account)
	}
	return accountSandbox(account)
}

func (t *Thumbs) generateVideoThumb(account, src, dstTmp string, px int) error {
	if !LookPath("ffmpeg") {
		return errors.New("ffmpeg not installed")
	}
	sb, err := t.newSandbox(account)
	if err != nil {
		return err
	}
	defer sb.Close()
	out := filepath.Join(sb.Dir, "thumb.jpg")
	// -f image2 -c:v mjpeg: dstTmp has no recognizable extension (it's a
	// *.jpg.tmp staging path renamed into place on success), so ffmpeg can't
	// infer the output muxer/codec from the filename and errors out ("Unable
	// to find a suitable output format") without them being explicit.
	// -protocol_whitelist file: the input is a local file; refuse the network and
	// every other protocol a crafted playlist could reference. -nostdin keeps
	// ffmpeg from waiting on a terminal.
	if err := sb.Run(20*time.Second, "ffmpeg", "-nostdin", "-y", "-protocol_whitelist", "file", "-i", src,
		"-vf", fmt.Sprintf("thumbnail,scale=%d:-1", px), "-frames:v", "1",
		"-f", "image2", "-c:v", "mjpeg", out); err != nil {
		return err
	}
	data, err := takeToolOutput(out)
	if err != nil {
		return err
	}
	return os.WriteFile(dstTmp, data, 0o600)
}

func (t *Thumbs) generatePDFThumb(account, src, dstTmp string, px int) error {
	if !LookPath("pdftoppm") {
		return errors.New("pdftoppm not installed")
	}
	// The tool runs as the customer in a private scratch dir, and only a plain
	// JPEG file from it is copied into the cache (see takeToolOutput).
	sb, err := t.newSandbox(account)
	if err != nil {
		return err
	}
	defer sb.Close()
	prefix := filepath.Join(sb.Dir, "page")
	if err := sb.Run(20*time.Second, "pdftoppm", "-jpeg", "-f", "1", "-l", "1",
		"-scale-to", strconv.Itoa(px), src, prefix); err != nil {
		return err
	}
	matches, err := filepath.Glob(prefix + "*.jpg")
	if err != nil || len(matches) == 0 {
		return errors.New("pdftoppm produced no output")
	}
	data, err := takeToolOutput(matches[0])
	if err != nil {
		return err
	}
	return os.WriteFile(dstTmp, data, 0o600)
}
