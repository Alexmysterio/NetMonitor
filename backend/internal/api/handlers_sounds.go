package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
)

func (s *Server) listNotificationSounds(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, filename, url, created_by, created_at
		FROM notification_sounds ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, filename, url, createdBy string
		var created time.Time
		if rows.Scan(&id, &name, &filename, &url, &createdBy, &created) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "filename": filename,
				"url": url, "created_by": createdBy, "created_at": created})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) uploadNotificationSound(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "archivo demasiado grande o formulario inválido")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "falta el archivo")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".mp3", ".wav", ".ogg", ".oga", ".webm", ".m4a":
	default:
		writeError(w, http.StatusBadRequest, "usa MP3, WAV, OGG, WebM o M4A")
		return
	}
	dir := os.Getenv("NOTIFICATION_SOUND_DIR")
	if dir == "" {
		dir = "/app/uploads/notification-sounds"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(header.Filename), ext)
	}
	filename := fmt.Sprintf("sound-%d%s", time.Now().UnixNano(), ext)
	dst, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dst.Close()
	url := "/notification-sounds/" + filename
	user, _ := auth.UserFrom(r.Context())
	var id string
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO notification_sounds (name, filename, url, created_by) VALUES ($1,$2,$3,$4) RETURNING id
	`, name, filename, url, user.Username).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": name, "filename": filename, "url": url,
	}})
}

func (s *Server) deleteNotificationSound(w http.ResponseWriter, r *http.Request) {
	var filename string
	if err := s.pool.QueryRow(r.Context(), `SELECT filename FROM notification_sounds WHERE id=$1`, chi.URLParam(r, "id")).Scan(&filename); err != nil {
		writeError(w, http.StatusNotFound, "sonido no encontrado")
		return
	}
	_, _ = s.pool.Exec(r.Context(), `DELETE FROM notification_sounds WHERE id=$1`, chi.URLParam(r, "id"))
	dir := os.Getenv("NOTIFICATION_SOUND_DIR")
	if dir == "" {
		dir = "/app/uploads/notification-sounds"
	}
	_ = os.Remove(filepath.Join(dir, filepath.Base(filename)))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) getNOCSettings(w http.ResponseWriter, r *http.Request) {
	values := map[string]string{}
	rows, err := s.pool.Query(r.Context(), `SELECT key, value FROM system_settings WHERE key IN ('noc_sound_url','noc_popup','noc_flash')`)
	if err == nil {
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				values[k] = v
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"sound_url": values["noc_sound_url"], "popup": values["noc_popup"] == "true",
		"flash": values["noc_flash"] == "true",
	}})
}

func (s *Server) setNOCSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SoundURL string `json:"sound_url"`
		Popup    bool   `json:"popup"`
		Flash    bool   `json:"flash"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.setSetting(r, "noc_sound_url", req.SoundURL)
	s.setSetting(r, "noc_popup", fmt.Sprintf("%t", req.Popup))
	s.setSetting(r, "noc_flash", fmt.Sprintf("%t", req.Flash))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) setSetting(r *http.Request, key, value string) {
	_, _ = s.pool.Exec(r.Context(), `
		INSERT INTO system_settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()
	`, key, value)
}
