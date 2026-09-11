package svc

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

func newThumbsT(t *testing.T) (*Thumbs, *Files, *store.User) {
	t.Helper()
	f, u := newFilesT(t)
	cfg := config.Default()
	cfg.ThumbCacheDir = t.TempDir()
	f.Cfg = cfg
	th := NewThumbs(cfg, f)
	return th, f, u
}

func writeTestPNG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThumbsImageCacheMissThenHit(t *testing.T) {
	th, _, u := newThumbsT(t)
	src := filepath.Join(u.HomeDir, "img.png")
	writeTestPNG(t, src, color.RGBA{255, 0, 0, 255})

	data, ok, err := th.Get(u, "/img.png", "sm")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true for a thumbnailable image")
	}
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Errorf("expected JPEG magic bytes, got % x", data[:min(4, len(data))])
	}
	cacheDir := filepath.Join(th.Cfg.ThumbCacheDir, u.Username)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cache file after miss, got %d", len(entries))
	}

	data2, ok2, err := th.Get(u, "/img.png", "sm")
	if err != nil || !ok2 {
		t.Fatalf("second Get: ok=%v err=%v", ok2, err)
	}
	if !bytes.Equal(data, data2) {
		t.Error("cache hit returned different bytes than the miss")
	}
	entries2, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries2) != 1 {
		t.Fatalf("cache hit should not add files, got %d", len(entries2))
	}
}

func TestThumbsCacheInvalidatesOnSourceChange(t *testing.T) {
	th, _, u := newThumbsT(t)
	src := filepath.Join(u.HomeDir, "img.png")
	writeTestPNG(t, src, color.RGBA{255, 0, 0, 255})

	if _, _, err := th.Get(u, "/img.png", "sm"); err != nil {
		t.Fatal(err)
	}

	writeTestPNG(t, src, color.RGBA{0, 255, 0, 255})
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatal(err)
	}

	if _, _, err := th.Get(u, "/img.png", "sm"); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(th.Cfg.ThumbCacheDir, u.Username)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 cache files after source change (new key), got %d", len(entries))
	}
}

func TestThumbsUnthumbnailableReturnsOkFalse(t *testing.T) {
	th, f, u := newThumbsT(t)
	if err := f.Write(u, "/notes.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := th.Get(u, "/notes.txt", "sm")
	if err != nil {
		t.Fatalf("unthumbnailable file should not error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for a .txt file")
	}
}

func TestThumbsMissingFFmpegDegradesGracefully(t *testing.T) {
	t.Setenv("PATH", "")
	th, f, u := newThumbsT(t)
	if err := f.Write(u, "/clip.mp4", []byte("not a real video"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := th.Get(u, "/clip.mp4", "sm")
	if err != nil {
		t.Fatalf("missing ffmpeg should not error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when ffmpeg is unavailable")
	}
}

// These exercise the real external tools end to end (skipped when absent)
// rather than just the degradation path — this is what caught, on first
// live run, ffmpeg failing to infer an output format from an extensionless
// temp path, and pdftoppm's cross-device rename when the cache dir is a
// separate mount from the system temp dir.
func TestThumbsVideoGeneratesRealFrame(t *testing.T) {
	if !LookPath("ffmpeg") {
		t.Skip("ffmpeg not installed")
	}
	th, _, u := newThumbsT(t)
	src := filepath.Join(u.HomeDir, "clip.mp4")
	if _, err := RunTimeout(20*time.Second, "ffmpeg", "-y", "-f", "lavfi",
		"-i", "testsrc=size=64x64:duration=1:rate=5", "-pix_fmt", "yuv420p", src); err != nil {
		t.Fatalf("failed to generate test video fixture: %v", err)
	}
	data, ok, err := th.Get(u, "/clip.mp4", "sm")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true for a real video file with ffmpeg available")
	}
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Errorf("expected JPEG magic bytes, got % x", data[:min(4, len(data))])
	}
}

const minimalTestPDF = `%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 150]/Resources<<>>>>endobj
trailer<</Root 1 0 R>>
`

func TestThumbsPDFGeneratesRealPage(t *testing.T) {
	if !LookPath("pdftoppm") {
		t.Skip("pdftoppm not installed")
	}
	th, f, u := newThumbsT(t)
	if err := f.Write(u, "/doc.pdf", []byte(minimalTestPDF), 0o644); err != nil {
		t.Fatal(err)
	}
	data, ok, err := th.Get(u, "/doc.pdf", "sm")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true for a real PDF file with pdftoppm available")
	}
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Errorf("expected JPEG magic bytes, got % x", data[:min(4, len(data))])
	}
}

func TestThumbsMissingPdftoppmDegradesGracefully(t *testing.T) {
	t.Setenv("PATH", "")
	th, f, u := newThumbsT(t)
	if err := f.Write(u, "/doc.pdf", []byte("not a real pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := th.Get(u, "/doc.pdf", "sm")
	if err != nil {
		t.Fatalf("missing pdftoppm should not error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when pdftoppm is unavailable")
	}
}

