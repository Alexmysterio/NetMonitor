package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
)

type panelRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Widgets     any    `json:"widgets"`
	IsShared    bool   `json:"is_shared"`
	IsNOC       bool   `json:"is_noc"`
}

func validatePanelWidgets(raw any) (string, error) {
	if raw == nil {
		raw = []any{}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "[]", err
	}
	var widgets []map[string]any
	if err := json.Unmarshal(data, &widgets); err != nil {
		return "[]", err
	}
	if len(widgets) > 40 {
		return "[]", errTooManyWidgets
	}
	for _, w := range widgets {
		kind, _ := w["kind"].(string)
		switch kind {
		case "chart", "table", "map", "counter", "text":
		default:
			return "[]", errInvalidWidget
		}
	}
	return string(data), nil
}

var (
	errTooManyWidgets = panelError("máximo 40 widgets por panel")
	errInvalidWidget  = panelError("tipo de widget inválido")
)

type panelError string

func (e panelError) Error() string { return string(e) }

func (s *Server) listPanels(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	admin := strings.EqualFold(user.Role, "admin")
	rows, err := s.pool.Query(r.Context(), `
		SELECT p.id, COALESCE(p.user_id::text,''), COALESCE(u.username,''), p.name, p.description,
		       p.widgets::text, p.is_shared, p.is_noc, p.created_at, p.updated_at
		FROM dashboard_panels p
		LEFT JOIN users u ON u.id=p.user_id
		WHERE $1 OR p.user_id=$2::uuid OR p.is_shared=TRUE
		ORDER BY p.is_noc DESC, p.name
	`, admin, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, userID, username, name, description, widgets string
		var shared, noc bool
		var created, updated time.Time
		if rows.Scan(&id, &userID, &username, &name, &description, &widgets, &shared, &noc, &created, &updated) == nil {
			var parsed any
			_ = json.Unmarshal([]byte(widgets), &parsed)
			out = append(out, map[string]any{
				"id": id, "user_id": userID, "owner": username, "name": name,
				"description": description, "widgets": parsed, "is_shared": shared,
				"is_noc": noc, "created_at": created, "updated_at": updated,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createPanel(w http.ResponseWriter, r *http.Request) {
	var req panelRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	widgets, err := validatePanelWidgets(req.Widgets)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	var id string
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO dashboard_panels (user_id, name, description, widgets, is_shared, is_noc)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6) RETURNING id
	`, user.ID, strings.TrimSpace(req.Name), req.Description, widgets, req.IsShared, req.IsNOC).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updatePanel(w http.ResponseWriter, r *http.Request) {
	var req panelRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	widgets, err := validatePanelWidgets(req.Widgets)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	admin := strings.EqualFold(user.Role, "admin")
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE dashboard_panels SET name=$3, description=$4, widgets=$5::jsonb,
		       is_shared=$6, is_noc=$7, updated_at=NOW()
		WHERE id=$1 AND ($2 OR user_id=$8::uuid)
	`, chi.URLParam(r, "id"), admin, strings.TrimSpace(req.Name), req.Description, widgets,
		req.IsShared, req.IsNOC, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "panel no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deletePanel(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	admin := strings.EqualFold(user.Role, "admin")
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM dashboard_panels WHERE id=$1 AND ($2 OR user_id=$3::uuid)
	`, chi.URLParam(r, "id"), admin, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "panel no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
