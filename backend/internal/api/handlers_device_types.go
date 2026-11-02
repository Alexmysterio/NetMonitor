package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listDeviceTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, label, icon, shape, color, description, is_builtin, COALESCE(probe_ids,'{}')
		FROM device_types ORDER BY is_builtin DESC, label
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, label, icon, shape, color, description string
		var builtin bool
		var probeIDs []string
		if rows.Scan(&id, &name, &label, &icon, &shape, &color, &description, &builtin, &probeIDs) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "label": label, "icon": icon,
				"shape": shape, "color": color, "description": description, "is_builtin": builtin, "probe_ids": probeIDs})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type deviceTypeRequest struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Icon        string   `json:"icon"`
	Shape       string   `json:"shape"`
	Color       string   `json:"color"`
	Description string   `json:"description"`
	ProbeIDs    []string `json:"probe_ids"`
}

func (s *Server) createDeviceType(w http.ResponseWriter, r *http.Request) {
	var req deviceTypeRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Label) == "" {
		writeError(w, http.StatusBadRequest, "name and label are required")
		return
	}
	if req.Shape == "" {
		req.Shape = "rounded_rect"
	}
	if req.Color == "" {
		req.Color = "#2563eb"
	}
	if req.ProbeIDs == nil {
		req.ProbeIDs = []string{}
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO device_types (name, label, icon, shape, color, description, probe_ids)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id
	`, slugify(req.Name), strings.TrimSpace(req.Label), req.Icon, req.Shape, req.Color, req.Description, req.ProbeIDs).Scan(&id)
	if err != nil {
		writeError(w, http.StatusConflict, "el tipo ya existe o no es válido")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateDeviceType(w http.ResponseWriter, r *http.Request) {
	var req deviceTypeRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Label) == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}
	if req.Shape == "" {
		req.Shape = "rounded_rect"
	}
	if req.Color == "" {
		req.Color = "#2563eb"
	}
	if req.ProbeIDs == nil {
		req.ProbeIDs = []string{}
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE device_types SET label=$2, icon=$3, shape=$4, color=$5, description=$6, probe_ids=$7
		WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Label), req.Icon, req.Shape, req.Color, req.Description, req.ProbeIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "tipo no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteDeviceType(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var builtin bool
	var name string
	if err := s.pool.QueryRow(r.Context(), `SELECT name, is_builtin FROM device_types WHERE id=$1`, id).Scan(&name, &builtin); err != nil {
		writeError(w, http.StatusNotFound, "tipo no encontrado")
		return
	}
	if builtin {
		writeError(w, http.StatusConflict, "los tipos built-in no se pueden eliminar")
		return
	}
	var used int
	_ = s.pool.QueryRow(r.Context(), `SELECT count(*) FROM devices WHERE device_type=$1`, name).Scan(&used)
	if used > 0 {
		writeError(w, http.StatusConflict, "el tipo está en uso por dispositivos")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM device_types WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
