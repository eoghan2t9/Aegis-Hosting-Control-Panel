package svc

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// --- file API -------------------------------------------------------------------
//
// Every handler here mirrors the shape (request JSON, response JSON, status
// codes) of the equivalent /api/files/* endpoint in internal/api/handlers_files.go
// so behavior stays consistent between the panel's file manager and webftp's —
// they're both thin wrappers over the same *Files service.

func (w *WebFTP) apiList(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	entries, err := w.Files.List(w.sessionUser(sess), r.URL.Query().Get("path"))
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	writeWebFTPJSON(rw, entries)
}

func (w *WebFTP) apiDownload(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	abs, err := w.Files.Resolve(w.sessionUser(sess), r.URL.Query().Get("path"))
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(abs)+`"`)
	http.ServeFile(rw, r, abs)
}

func (w *WebFTP) apiUpload(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MiB, matches the panel's file manager
		writeWebFTPErrMsg(rw, http.StatusBadRequest, "upload too large: "+err.Error())
		return
	}
	destDir := r.FormValue("path")
	user := w.sessionUser(sess)
	var names []string
	for _, fh := range r.MultipartForm.File["files"] {
		src, err := fh.Open()
		if err != nil {
			writeWebFTPErr(rw, err)
			return
		}
		dest := strings.TrimSuffix(destDir, "/") + "/" + fh.Filename
		abs, err := w.Files.Resolve(user, dest)
		if err != nil {
			src.Close()
			writeWebFTPErr(rw, err)
			return
		}
		out, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			src.Close()
			writeWebFTPErr(rw, err)
			return
		}
		_, cerr := io.Copy(out, src)
		src.Close()
		out.Close()
		if cerr != nil {
			writeWebFTPErr(rw, cerr)
			return
		}
		names = append(names, fh.Filename)
	}
	w.audit(r, sess, "webftp.upload", destDir, strings.Join(names, ", "))
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpPathReq struct {
	Path string `json:"path"`
}

func (w *WebFTP) apiMkdir(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpPathReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Mkdir(w.sessionUser(sess), req.Path, 0o755); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.mkdir", req.Path, "")
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

func (w *WebFTP) apiDelete(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpPathReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Delete(w.sessionUser(sess), req.Path); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.delete", req.Path, "")
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpRenameReq struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (w *WebFTP) apiRename(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpRenameReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Rename(w.sessionUser(sess), req.From, req.To); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.rename", req.From+" → "+req.To, "")
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

func (w *WebFTP) apiRead(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	data, err := w.Files.Read(w.sessionUser(sess), r.URL.Query().Get("path"))
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = rw.Write(data)
}

type webftpWriteReq struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (w *WebFTP) apiWrite(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpWriteReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Write(w.sessionUser(sess), req.Path, []byte(req.Content), 0o644); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.write", req.Path, "")
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpChmodReq struct {
	Path      string `json:"path"`
	Mode      string `json:"mode"`
	Recursive bool   `json:"recursive"`
}

func (w *WebFTP) apiChmod(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpChmodReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Chmod(w.sessionUser(sess), req.Path, req.Mode, req.Recursive); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	detail := ""
	if req.Recursive {
		detail = "recursive"
	}
	w.audit(r, sess, "webftp.chmod", req.Path+" "+req.Mode, detail)
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpChownReq struct {
	Path      string `json:"path"`
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Recursive bool   `json:"recursive"`
}

func (w *WebFTP) apiChown(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpChownReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Chown(w.sessionUser(sess), req.Path, req.Owner, req.Group, req.Recursive); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.chown", req.Path, "owner="+req.Owner+" group="+req.Group)
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

func (w *WebFTP) apiSearch(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	hits, err := w.Files.Search(w.sessionUser(sess), r.URL.Query().Get("path"), r.URL.Query().Get("q"))
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	writeWebFTPJSON(rw, hits)
}

type webftpZipReq struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

func (w *WebFTP) apiZip(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpZipReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Zip(w.sessionUser(sess), req.Path, req.Name); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.zip", req.Path, req.Name)
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpUnzipReq struct {
	Path string `json:"path"`
	Dest string `json:"dest"`
}

func (w *WebFTP) apiUnzip(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpUnzipReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Unzip(w.sessionUser(sess), req.Path, req.Dest); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	w.audit(r, sess, "webftp.unzip", req.Path, req.Dest)
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

func (w *WebFTP) apiThumb(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	size := r.URL.Query().Get("size")
	if size == "" {
		size = "sm"
	}
	data, ok, err := w.Thumbs.Get(w.sessionUser(sess), r.URL.Query().Get("path"), size)
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	if !ok {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	rw.Header().Set("Content-Type", "image/jpeg")
	rw.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = rw.Write(data)
}

// --- small local JSON helpers (internal/api's are unavailable here — svc is
// a lower-level package that api imports, not the reverse) -------------------

func writeWebFTPJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(v)
}

func writeWebFTPErrMsg(rw http.ResponseWriter, status int, msg string) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(map[string]string{"error": msg})
}

func writeWebFTPErr(rw http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if err == ErrForbidden {
		status = http.StatusForbidden
	}
	writeWebFTPErrMsg(rw, status, err.Error())
}

func decodeWebFTPJSON(rw http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeWebFTPErrMsg(rw, http.StatusBadRequest, "bad request body")
		return false
	}
	return true
}
