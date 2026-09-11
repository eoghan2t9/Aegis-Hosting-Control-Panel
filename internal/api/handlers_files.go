package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"aegis/internal/store"
)

// fileUser returns the user whose files are being accessed. Admins can pass
// ?user= to browse another account (support feature).
func (s *Server) fileUser(r *http.Request) (*store.User, error) {
	actor := userFrom(r)
	target := actor
	if u := r.URL.Query().Get("user"); u != "" && actor.Role == store.RoleAdmin {
		t, err := s.Store.GetUserByUsername(r.Context(), u)
		if err != nil {
			return nil, err
		}
		target = t
	}
	return target, nil
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	path := r.URL.Query().Get("path")
	entries, err := s.Files.List(u, path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	path := r.URL.Query().Get("path")
	data, err := s.Files.Read(u, path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

type fileOpReq struct {
	Path string `json:"path"`
}

func (s *Server) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	mode, _ := strconv.ParseUint(req.Mode, 8, 32)
	if err := s.Files.Write(u, req.Path, []byte(req.Content), os.FileMode(mode)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.write", req.Path, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	mode, _ := strconv.ParseUint(req.Mode, 8, 32)
	if err := s.Files.Mkdir(u, req.Path, os.FileMode(mode)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.mkdir", req.Path, "")
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Files.Rename(u, req.From, req.To); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.rename", req.From+" → "+req.To, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req fileOpReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Files.Delete(u, req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.delete", req.Path, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesChmod(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Files.Chmod(u, req.Path, req.Mode, req.Recursive); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	detail := ""
	if req.Recursive {
		detail = "recursive"
	}
	s.audit(r, "file.chmod", req.Path+" "+req.Mode, detail)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesChown(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path      string `json:"path"`
		Owner     string `json:"owner"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Files.Chown(u, req.Path, req.Owner, req.Group, req.Recursive); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.chown", req.Path, "owner="+req.Owner+" group="+req.Group+" recursive="+strconv.FormatBool(req.Recursive))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesThumb(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	size := r.URL.Query().Get("size")
	if size == "" {
		size = "sm"
	}
	data, ok, err := s.Thumbs.Get(u, r.URL.Query().Get("path"), size)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

func (s *Server) handleFilesSearch(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path  string `json:"path"`
		Query string `json:"query"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	hits, err := s.Files.Search(u, req.Path, req.Query)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

func (s *Server) handleFilesZip(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Files.Zip(u, req.Path, req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.zip", req.Path, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesUnzip(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Path string `json:"path"`
		Dest string `json:"dest"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Files.Unzip(u, req.Path, req.Dest); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "file.unzip", req.Path, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	path := r.URL.Query().Get("path")
	abs, err := s.Files.Resolve(u, path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if info.IsDir() {
		writeErr(w, http.StatusBadRequest, "cannot download a directory; zip it first")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(abs))
	http.ServeFile(w, r, abs)
}

func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	u, err := s.fileUser(r)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MiB
		writeErr(w, http.StatusBadRequest, "upload too large: "+err.Error())
		return
	}
	destDir := r.FormValue("path")
	files := r.MultipartForm.File["files"]
	var names []string
	for _, fh := range files {
		src, err := fh.Open()
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		dest := strings.TrimSuffix(destDir, "/") + "/" + fh.Filename
		abs, err := s.Files.Resolve(u, dest)
		if err != nil {
			src.Close()
			writeErr(w, http.StatusForbidden, err.Error())
			return
		}
		out, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			src.Close()
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			src.Close()
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		out.Close()
		src.Close()
		names = append(names, fh.Filename)
	}
	s.audit(r, "file.upload", strings.Join(names, ","), "to="+destDir)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "files": names})
}
