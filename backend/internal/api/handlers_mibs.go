package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/mib"
)

func (s *Server) importMIB(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Module  string `json:"module"`
		Content string `json:"content"`
	}
	if err := decodeBody(r, &req); err != nil || req.Module == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "module and content are required")
		return
	}
	if len(req.Content) > 4<<20 {
		writeError(w, http.StatusBadRequest, "el archivo MIB supera 4 MB")
		return
	}
	objects, err := mib.Parse(req.Module, req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	notifications := mib.ParseNotificationsMany(map[string]string{req.Module: req.Content})[req.Module]
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM mib_objects WHERE module=$1`, req.Module)
	for _, o := range objects {
		if _, err := tx.Exec(ctx, `
			INSERT INTO mib_objects (module, oid, name, syntax, access, description)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (module, oid) DO UPDATE
			SET name=EXCLUDED.name, syntax=EXCLUDED.syntax, access=EXCLUDED.access,
			    description=EXCLUDED.description, imported_at=NOW()
		`, req.Module, o.OID, o.Name, o.Syntax, o.Access, o.Description); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	_, _ = tx.Exec(ctx, `DELETE FROM mib_notifications WHERE module=$1`, req.Module)
	for _, n := range notifications {
		if _, err := tx.Exec(ctx, `
			INSERT INTO mib_notifications (module, oid, name, description, objects)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (module, oid) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description,
			    objects=EXCLUDED.objects, imported_at=NOW()
		`, req.Module, n.OID, n.Name, n.Description, n.Objects); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{
		"module": req.Module, "objects": len(objects), "notifications": len(notifications),
	}})
}

func (s *Server) importMIBBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Modules []struct {
			Module  string `json:"module"`
			Content string `json:"content"`
		} `json:"modules"`
	}
	if err := decodeBody(r, &req); err != nil || len(req.Modules) == 0 {
		writeError(w, http.StatusBadRequest, "modules are required")
		return
	}
	files := map[string]string{}
	deps := map[string][]string{}
	for _, m := range req.Modules {
		if m.Module == "" || m.Content == "" || len(m.Content) > 4<<20 {
			writeError(w, http.StatusBadRequest, "módulo o contenido inválido")
			return
		}
		files[m.Module] = m.Content
		deps[m.Module] = mib.Imports(m.Content)
	}
	parsed, err := mib.ParseMany(files)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	notifications := mib.ParseNotificationsMany(files)
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	counts := map[string]int{}
	notificationCounts := map[string]int{}
	for module, objects := range parsed {
		_, _ = tx.Exec(ctx, `DELETE FROM mib_objects WHERE module=$1`, module)
		for _, o := range objects {
			if _, err := tx.Exec(ctx, `
				INSERT INTO mib_objects (module, oid, name, syntax, access, description)
				VALUES ($1,$2,$3,$4,$5,$6)
				ON CONFLICT (module, oid) DO UPDATE
				SET name=EXCLUDED.name, syntax=EXCLUDED.syntax, access=EXCLUDED.access,
				    description=EXCLUDED.description, imported_at=NOW()
			`, module, o.OID, o.Name, o.Syntax, o.Access, o.Description); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		counts[module] = len(objects)
	}
	for module := range files {
		_, _ = tx.Exec(ctx, `DELETE FROM mib_notifications WHERE module=$1`, module)
	}
	for module, list := range notifications {
		for _, n := range list {
			if _, err := tx.Exec(ctx, `
				INSERT INTO mib_notifications (module, oid, name, description, objects)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (module, oid) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description,
				    objects=EXCLUDED.objects, imported_at=NOW()
			`, module, n.OID, n.Name, n.Description, n.Objects); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		notificationCounts[module] = len(list)
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{
		"modules": counts, "notifications": notificationCounts, "dependencies": deps,
	}})
}

func (s *Server) listMIBs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT module, count(*), max(imported_at)
		FROM mib_objects GROUP BY module ORDER BY module
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var module string
		var count int
		var imported *time.Time
		if rows.Scan(&module, &count, &imported) == nil {
			out = append(out, map[string]any{"module": module, "objects": count, "imported_at": imported})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) deleteMIB(w http.ResponseWriter, r *http.Request) {
	module := chi.URLParam(r, "module")
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM mib_objects WHERE module=$1`, module)
	_, _ = tx.Exec(ctx, `DELETE FROM mib_notifications WHERE module=$1`, module)
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listMIBNotifications(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	module := strings.TrimSpace(r.URL.Query().Get("module"))
	rows, err := s.pool.Query(r.Context(), `
		SELECT module, oid, name, COALESCE(description,''), objects
		FROM mib_notifications
		WHERE ($1='' OR name ILIKE '%'||$1||'%' OR oid LIKE $1||'%')
		  AND ($2='' OR module=$2)
		ORDER BY module, oid LIMIT 2000
	`, q, module)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var mod, oid, name, desc string
		var objects []string
		if rows.Scan(&mod, &oid, &name, &desc, &objects) == nil {
			out = append(out, map[string]any{"module": mod, "oid": oid, "name": name,
				"description": desc, "objects": objects})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) searchMIBObjects(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	module := strings.TrimSpace(r.URL.Query().Get("module"))
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 3000 {
			limit = n
		}
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT module, oid, name, syntax, access, COALESCE(description,'')
		FROM mib_objects
		WHERE ($1='' OR name ILIKE '%'||$1||'%' OR oid LIKE $1||'%')
		  AND ($2='' OR module=$2)
		ORDER BY oid LIMIT $3
	`, q, module, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var mod, oid, name, syntax, access, description string
		if rows.Scan(&mod, &oid, &name, &syntax, &access, &description) == nil {
			out = append(out, map[string]any{"module": mod, "oid": oid, "name": name, "syntax": syntax, "access": access, "description": description})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) mibObjectToLabel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OID       string `json:"oid"`
		Name      string `json:"name"`
		MapID     string `json:"map_id"`
		ElementID string `json:"element_id"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.OID) == "" || req.MapID == "" || req.ElementID == "" {
		writeError(w, http.StatusBadRequest, "oid, map_id y element_id son obligatorios")
		return
	}
	var deviceID string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT COALESCE(device_id::text,'') FROM map_elements WHERE id=$1 AND map_id=$2
	`, req.ElementID, req.MapID).Scan(&deviceID); err != nil || deviceID == "" {
		writeError(w, http.StatusBadRequest, "el elemento no existe o no es un dispositivo")
		return
	}
	name := mibFunctionName(req.Name)
	if name == "" {
		name = "mib_" + strings.ReplaceAll(req.OID, ".", "_")
	}
	base := name
	expr := `oid("` + strings.TrimSuffix(req.OID, ".0") + `.0")`
	for i := 0; i < 30; i++ {
		if i > 0 {
			name = fmt.Sprintf("%s_%d", base, i+1)
		}
		var existingExpr string
		err := s.pool.QueryRow(r.Context(), `SELECT expression_code FROM functions WHERE name=$1`, name).Scan(&existingExpr)
		if err != nil {
			_, err = s.pool.Exec(r.Context(), `
				INSERT INTO functions (name, description, expression_code, unit)
				VALUES ($1,$2,$3,'')
			`, name, "Creada desde MIB "+req.OID, expr)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			break
		}
		if existingExpr == expr {
			break
		}
		if i == 29 {
			writeError(w, http.StatusConflict, "no se pudo asignar un nombre de función")
			return
		}
	}
	_, err := s.pool.Exec(r.Context(), `
		UPDATE map_elements SET label_template =
		  CASE WHEN label_template ILIKE '%[' || $2 || '()]%' THEN label_template
		       ELSE label_template || E'\n[' || $2 || '()]' END,
		  updated_at=NOW()
		WHERE id=$1 AND map_id=$3
	`, req.ElementID, name, req.MapID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"function": name, "expression": expr, "device_id": deviceID,
	}})
}

func mibFunctionName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "mib_" + out
	}
	return out
}

func (s *Server) mibName(ctx context.Context, oid string) string {
	var name string
	_ = s.pool.QueryRow(ctx, `
		SELECT name FROM mib_objects
		WHERE $1 = oid OR $1 LIKE oid || '.%'
		ORDER BY length(oid) DESC LIMIT 1
	`, oid).Scan(&name)
	return name
}
