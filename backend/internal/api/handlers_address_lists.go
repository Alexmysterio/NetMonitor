package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type addressListRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Kind        string `json:"kind"`
}

func (s *Server) listAddressLists(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT l.id, l.name, l.description, l.color, l.kind, l.created_at,
		       (SELECT count(*) FROM address_list_entries e WHERE e.list_id=l.id)
		FROM address_lists l ORDER BY l.name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, description, color, kind string
		var created any
		var count int
		if rows.Scan(&id, &name, &description, &color, &kind, &created, &count) == nil {
			out = append(out, map[string]any{
				"id": id, "name": name, "description": description, "color": color,
				"kind": kind, "created_at": created, "entry_count": count,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) getAddressList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var name, description, color, kind string
	err := s.pool.QueryRow(r.Context(), `SELECT name, description, color, kind FROM address_lists WHERE id=$1`, id).
		Scan(&name, &description, &color, &kind)
	if err != nil {
		writeError(w, http.StatusNotFound, "lista no encontrada")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, value::text, label, value::text
		FROM address_list_entries WHERE list_id=$1 ORDER BY value, label
	`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	entries := []map[string]any{}
	for rows.Next() {
		var entryID, hostValue, label, cidr string
		if rows.Scan(&entryID, &hostValue, &label, &cidr) == nil {
			entries = append(entries, map[string]any{"id": entryID, "value": hostValue, "cidr": cidr, "label": label})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": name, "description": description, "color": color, "kind": kind, "entries": entries,
	}})
}

func (s *Server) createAddressList(w http.ResponseWriter, r *http.Request) {
	var req addressListRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Color == "" {
		req.Color = "#0f766e"
	}
	if req.Kind != "block" {
		req.Kind = "allow"
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO address_lists (name, description, color, kind) VALUES ($1,$2,$3,$4) RETURNING id
	`, strings.TrimSpace(req.Name), req.Description, req.Color, req.Kind).Scan(&id)
	if err != nil {
		writeError(w, http.StatusConflict, "la lista ya existe o no es válida")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateAddressList(w http.ResponseWriter, r *http.Request) {
	var req addressListRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Color == "" {
		req.Color = "#0f766e"
	}
	if req.Kind != "block" {
		req.Kind = "allow"
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE address_lists SET name=$2, description=$3, color=$4, kind=$5, updated_at=NOW() WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), req.Description, req.Color, req.Kind)
	if err != nil {
		writeError(w, http.StatusConflict, "nombre de lista duplicado o inválido")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "lista no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteAddressList(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM address_lists WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "lista no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

type addressEntryRequest struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func normalizeAddressCIDR(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if _, ipnet, err := net.ParseCIDR(value); err == nil {
		return ipnet.String(), true
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return "", false
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String() + "/32", true
	}
	return ip.String() + "/128", true
}

func (s *Server) addAddressListEntry(w http.ResponseWriter, r *http.Request) {
	var req addressEntryRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	value, ok := normalizeAddressCIDR(req.Value)
	if !ok {
		writeError(w, http.StatusBadRequest, "dirección o CIDR inválido")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO address_list_entries (list_id, value, label) VALUES ($1,$2::cidr,$3)
		ON CONFLICT (list_id, value) DO UPDATE SET label=EXCLUDED.label
		RETURNING id
	`, chi.URLParam(r, "id"), value, strings.TrimSpace(req.Label)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.pool.Exec(r.Context(), `UPDATE address_lists SET updated_at=NOW() WHERE id=$1`, chi.URLParam(r, "id"))
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id, "value": value}})
}

func (s *Server) deleteAddressListEntry(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM address_list_entries WHERE id=$1 AND list_id=$2
	`, chi.URLParam(r, "entryID"), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "entrada no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) importAddressList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Entries []addressEntryRequest `json:"entries"`
		Text    string                `json:"text"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.Entries) == 0 && strings.TrimSpace(req.Text) != "" {
		for _, line := range strings.Split(req.Text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, ",", 2)
			entry := addressEntryRequest{Value: strings.TrimSpace(parts[0])}
			if len(parts) == 2 {
				entry.Label = strings.TrimSpace(parts[1])
			}
			req.Entries = append(req.Entries, entry)
		}
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "no hay entradas para importar")
		return
	}
	if len(req.Entries) > 10000 {
		writeError(w, http.StatusBadRequest, "máximo 10.000 entradas por importación")
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	imported := 0
	for _, entry := range req.Entries {
		value, ok := normalizeAddressCIDR(entry.Value)
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO address_list_entries (list_id, value, label) VALUES ($1,$2::cidr,$3)
			ON CONFLICT (list_id, value) DO UPDATE SET label=EXCLUDED.label
		`, chi.URLParam(r, "id"), value, strings.TrimSpace(entry.Label)); err == nil {
			imported++
		}
	}
	_, _ = tx.Exec(ctx, `UPDATE address_lists SET updated_at=NOW() WHERE id=$1`, chi.URLParam(r, "id"))
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"imported": imported}})
}

func (s *Server) exportAddressList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var name, description, kind string
	if err := s.pool.QueryRow(r.Context(), `SELECT name, description, kind FROM address_lists WHERE id=$1`, id).
		Scan(&name, &description, &kind); err != nil {
		writeError(w, http.StatusNotFound, "lista no encontrada")
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT value::text, label FROM address_list_entries WHERE list_id=$1 ORDER BY value`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	entries := []map[string]string{}
	for rows.Next() {
		var value, label string
		if rows.Scan(&value, &label) == nil {
			entries = append(entries, map[string]string{"value": value, "label": label})
		}
	}
	payload := map[string]any{"format": "netmonitor-address-list/v1", "name": name, "description": description, "kind": kind, "entries": entries}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"address-list.csv\"")
		_, _ = w.Write([]byte("value,label\n"))
		for _, e := range entries {
			// List values are validated CIDRs, so quoting only needs to escape commas in labels.
			_, _ = w.Write([]byte(e["value"] + ",\"" + strings.ReplaceAll(e["label"], `"`, `""`) + "\"\n"))
		}
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
