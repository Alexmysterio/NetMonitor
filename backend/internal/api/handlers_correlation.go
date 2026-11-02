package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/correlation"
)

func (s *Server) correlationRoot(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	resolution, err := correlation.Resolve(r.Context(), s.pool, deviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if resolution.HasRoot && !s.canAccessDevice(r.Context(), user, resolution.Root.ID) {
		resolution.HasRoot = false
		resolution.Root = correlation.Node{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": resolution})
}

func (s *Server) correlationImpact(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	rootID := deviceID
	resolution, err := correlation.Resolve(r.Context(), s.pool, deviceID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if resolution.HasRoot {
		rootID = resolution.Root.ID
	}
	impact, err := correlation.BuildImpact(r.Context(), s.pool, rootID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.canAccessDevice(r.Context(), user, impact.Root.ID) {
		writeError(w, http.StatusForbidden, "sin acceso a la causa raíz")
		return
	}
	if user.Role != "admin" && (user.RestrictMaps || user.RestrictGroups || user.RestrictDevices) {
		filtered := impact.Impacted[:0]
		for _, node := range impact.Impacted {
			if s.canAccessDevice(r.Context(), user, node.ID) {
				filtered = append(filtered, node)
			}
		}
		impact.Impacted = filtered
		chain := impact.Chain[:0]
		for _, node := range impact.Chain {
			if s.canAccessDevice(r.Context(), user, node.ID) {
				chain = append(chain, node)
			}
		}
		impact.Chain = chain
	}
	impact.Root.Via = ""
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"resolution": resolution, "impact": impact,
	}})
}

func (s *Server) recomputeCorrelation(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text FROM devices
		WHERE status IN ('down','unreachable')
		ORDER BY status='down' DESC, name LIMIT 2000
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	updated, roots := 0, 0
	for _, id := range ids {
		resolution, err := correlation.Resolve(r.Context(), s.pool, id)
		if err != nil {
			continue
		}
		if resolution.HasRoot && resolution.Root.ID != id {
			if correlation.Apply(r.Context(), s.pool, id, resolution.Root.ID, resolution.Depth) == nil {
				updated++
				roots++
			}
		} else {
			_ = correlation.Clear(r.Context(), s.pool, id)
			updated++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"devices": updated, "with_root_cause": roots,
	}})
}
