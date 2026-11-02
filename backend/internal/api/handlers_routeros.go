package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/routeros"
	"github.com/netmonitor/netmonitor/internal/secure"
)

type routerosCredential struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Username    string    `json:"username"`
	Port        int       `json:"port"`
	UseTLS      bool      `json:"use_tls"`
	InsecureTLS bool      `json:"insecure_tls"`
	Description string    `json:"description"`
	HasPassword bool      `json:"has_password"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Server) listRouterOSCredentials(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, username, port, use_tls, insecure_tls, description,
		       (password <> '') AS has_password, created_at, updated_at
		FROM routeros_credentials ORDER BY name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []routerosCredential{}
	for rows.Next() {
		var c routerosCredential
		if rows.Scan(&c.ID, &c.Name, &c.Username, &c.Port, &c.UseTLS, &c.InsecureTLS,
			&c.Description, &c.HasPassword, &c.CreatedAt, &c.UpdatedAt) == nil {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type routerosCredentialRequest struct {
	Name        string `json:"name"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Port        int    `json:"port"`
	UseTLS      bool   `json:"use_tls"`
	InsecureTLS bool   `json:"insecure_tls"`
	Description string `json:"description"`
}

func normalizeCredentialPort(port int, useTLS bool) int {
	if port > 0 {
		return port
	}
	if useTLS {
		return 8729
	}
	return 8728
}

func (s *Server) createRouterOSCredential(w http.ResponseWriter, r *http.Request) {
	var req routerosCredentialRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Username) == "" {
		writeError(w, http.StatusBadRequest, "name y username son obligatorios")
		return
	}
	enc, err := secure.Encrypt(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var id string
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO routeros_credentials (name, username, password, port, use_tls, insecure_tls, description)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id
	`, strings.TrimSpace(req.Name), strings.TrimSpace(req.Username), enc,
		normalizeCredentialPort(req.Port, req.UseTLS), req.UseTLS, req.InsecureTLS, req.Description).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateRouterOSCredential(w http.ResponseWriter, r *http.Request) {
	var req routerosCredentialRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Username) == "" {
		writeError(w, http.StatusBadRequest, "name y username son obligatorios")
		return
	}
	var password any
	if req.Password != "" {
		enc, err := secure.Encrypt(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		password = enc
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE routeros_credentials SET name=$2, username=$3,
		       password=COALESCE($4,password), port=$5, use_tls=$6, insecure_tls=$7,
		       description=$8, updated_at=NOW()
		WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), strings.TrimSpace(req.Username), password,
		normalizeCredentialPort(req.Port, req.UseTLS), req.UseTLS, req.InsecureTLS, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "credencial no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteRouterOSCredential(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM routeros_credentials WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "credencial no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listRouterOSInventory(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := s.pool.Query(r.Context(), `
		SELECT d.id, d.name, host(d.primary_ip), d.device_type, d.status,
		       COALESCE(d.routeros_version,''), COALESCE(d.hardware_model,''),
		       COALESCE(d.sys_name,''), COALESCE(d.vendor,''), COALESCE(d.routeros_credential_id::text,''),
		       COALESCE(rc.name,''), COALESCE(d.routeros_api_port,0),
		       d.routeros_last_sync_at, COALESCE(d.routeros_last_error,''),
		       d.routeros_credential_id IS NOT NULL
		FROM devices d LEFT JOIN routeros_credentials rc ON rc.id=d.routeros_credential_id
		WHERE ($1='' OR d.name ILIKE '%'||$1||'%' OR host(d.primary_ip) ILIKE '%'||$1||'%'
		       OR COALESCE(d.routeros_version,'') ILIKE '%'||$1||'%'
		       OR COALESCE(d.hardware_model,'') ILIKE '%'||$1||'%')
		ORDER BY d.name LIMIT 1000
	`, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	managed := 0
	for rows.Next() {
		var id, name, ip, typ, status, version, model, sysName, vendor, credentialID, credentialName, lastError string
		var apiPort int
		var lastSync *time.Time
		var isManaged bool
		if rows.Scan(&id, &name, &ip, &typ, &status, &version, &model, &sysName, &vendor,
			&credentialID, &credentialName, &apiPort, &lastSync, &lastError, &isManaged) != nil {
			continue
		}
		if isManaged {
			managed++
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "primary_ip": ip, "device_type": typ, "status": status,
			"routeros_version": version, "hardware_model": model, "sys_name": sysName, "vendor": vendor,
			"credential_id": credentialID, "credential_name": credentialName, "api_port": apiPort,
			"last_sync_at": lastSync, "last_error": lastError, "is_managed": isManaged,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"devices": out, "managed": managed, "total": len(out),
	}})
}

type routerosTarget struct {
	ID           string
	Name         string
	IP           string
	CredentialID string
	Username     string
	PasswordEnc  string
	Port         int
	UseTLS       bool
	InsecureTLS  bool
	APIPort      int
	Maps         string
	Tags         []string
	Managed      bool
}

func (s *Server) loadRouterOSTarget(ctx context.Context, deviceID string) (routerosTarget, error) {
	var t routerosTarget
	err := s.pool.QueryRow(ctx, `
		SELECT d.id::text, d.name, host(d.primary_ip), COALESCE(d.routeros_credential_id::text,''),
		       COALESCE(rc.username,''), COALESCE(rc.password,''), COALESCE(rc.port,8728),
		       COALESCE(rc.use_tls,false), COALESCE(rc.insecure_tls,false), COALESCE(d.routeros_api_port,0),
		       COALESCE((SELECT string_agg(m.name, ', ' ORDER BY m.name)
		                 FROM map_elements me JOIN maps m ON m.id=me.map_id WHERE me.device_id=d.id),''),
		       COALESCE(d.tags,'{}'), d.routeros_credential_id IS NOT NULL
		FROM devices d LEFT JOIN routeros_credentials rc ON rc.id=d.routeros_credential_id
		WHERE d.id=$1
	`, deviceID).Scan(&t.ID, &t.Name, &t.IP, &t.CredentialID, &t.Username, &t.PasswordEnc,
		&t.Port, &t.UseTLS, &t.InsecureTLS, &t.APIPort, &t.Maps, &t.Tags, &t.Managed)
	if err != nil {
		return t, fmt.Errorf("dispositivo no encontrado")
	}
	if t.APIPort > 0 {
		t.Port = t.APIPort
	}
	return t, nil
}

func (t routerosTarget) options() (routeros.Options, error) {
	password, err := secure.Decrypt(t.PasswordEnc)
	if err != nil {
		return routeros.Options{}, fmt.Errorf("no se pudo descifrar la credencial: %w", err)
	}
	return routeros.Options{
		Host: t.IP, Port: t.Port, Username: t.Username, Password: password,
		TLS: t.UseTLS, InsecureTLS: t.InsecureTLS, Timeout: 8 * time.Second,
	}, nil
}

func (s *Server) assignRouterOSCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID     string `json:"device_id"`
		CredentialID string `json:"credential_id"`
		APIPort      int    `json:"api_port"`
	}
	if err := decodeBody(r, &req); err != nil || req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id es obligatorio")
		return
	}
	var credential any
	if req.CredentialID != "" {
		var exists bool
		if s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM routeros_credentials WHERE id=$1)`, req.CredentialID).Scan(&exists) != nil || !exists {
			writeError(w, http.StatusBadRequest, "credencial no encontrada")
			return
		}
		credential = req.CredentialID
	}
	var port any
	if req.APIPort > 0 {
		port = req.APIPort
	}
	_, err := s.pool.Exec(r.Context(), `
		UPDATE devices SET routeros_credential_id=$2, routeros_api_port=$3, updated_at=NOW() WHERE id=$1
	`, req.DeviceID, credential, port)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) testRouterOS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CredentialID string `json:"credential_id"`
		DeviceID     string `json:"device_id"`
		Host         string `json:"host"`
		Port         int    `json:"port"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	host := strings.TrimSpace(req.Host)
	var username, passwordEnc string
	var port int
	var useTLS, insecure bool
	if req.DeviceID != "" {
		t, err := s.loadRouterOSTarget(r.Context(), req.DeviceID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		host, username, passwordEnc, port, useTLS, insecure = t.IP, t.Username, t.PasswordEnc, t.Port, t.UseTLS, t.InsecureTLS
	} else {
		if req.CredentialID == "" {
			writeError(w, http.StatusBadRequest, "credential_id o device_id es obligatorio")
			return
		}
		// Never send a stored credential to an arbitrary host. The host must
		// belong to a device that has this credential assigned.
		if host != "" {
			var allowed bool
			_ = s.pool.QueryRow(r.Context(), `
				SELECT EXISTS(SELECT 1 FROM devices WHERE routeros_credential_id=$1 AND host(primary_ip)=$2)
			`, req.CredentialID, host).Scan(&allowed)
			if !allowed {
				writeError(w, http.StatusForbidden, "el host no pertenece a ningún equipo con esta credencial")
				return
			}
		}
		err := s.pool.QueryRow(r.Context(), `
			SELECT username, password, port, use_tls, insecure_tls FROM routeros_credentials WHERE id=$1
		`, req.CredentialID).Scan(&username, &passwordEnc, &port, &useTLS, &insecure)
		if err != nil {
			writeError(w, http.StatusNotFound, "credencial no encontrada")
			return
		}
	}
	if host == "" {
		writeError(w, http.StatusBadRequest, "host es obligatorio")
		return
	}
	if req.Port > 0 {
		port = req.Port
	}
	password, err := secure.Decrypt(passwordEnc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo descifrar la credencial")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := routeros.Connect(ctx, routeros.Options{
		Host: host, Port: port, Username: username, Password: password,
		TLS: useTLS, InsecureTLS: insecure, Timeout: 10 * time.Second,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer client.Close()
	snap, err := client.Test()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": snap})
}

func (s *Server) refreshRouterOSInventory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceIDs []string `json:"device_ids"`
	}
	_ = decodeBody(r, &req)
	ids := req.DeviceIDs
	if len(ids) == 0 {
		rows, err := s.pool.Query(r.Context(), `
			SELECT id::text FROM devices WHERE routeros_credential_id IS NOT NULL ORDER BY name LIMIT 50
		`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
	}
	if len(ids) > 50 {
		writeError(w, http.StatusBadRequest, "máximo 50 dispositivos por lote")
		return
	}
	results := make([]map[string]any, len(ids))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			t, err := s.loadRouterOSTarget(r.Context(), id)
			if err != nil {
				results[i] = map[string]any{"device_id": id, "success": false, "error": err.Error()}
				return
			}
			if !t.Managed {
				results[i] = map[string]any{"device_id": id, "device_name": t.Name, "success": false, "error": "sin credencial RouterOS asignada"}
				return
			}
			opts, err := t.options()
			if err != nil {
				results[i] = map[string]any{"device_id": id, "device_name": t.Name, "success": false, "error": err.Error()}
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			client, err := routeros.Connect(ctx, opts)
			if err != nil {
				_, _ = s.pool.Exec(r.Context(), `UPDATE devices SET routeros_last_error=$2, updated_at=NOW() WHERE id=$1`, id, err.Error())
				results[i] = map[string]any{"device_id": id, "device_name": t.Name, "success": false, "error": err.Error()}
				return
			}
			defer client.Close()
			snap, err := client.CollectSnapshot()
			if err != nil {
				_, _ = s.pool.Exec(r.Context(), `UPDATE devices SET routeros_last_error=$2, updated_at=NOW() WHERE id=$1`, id, err.Error())
				results[i] = map[string]any{"device_id": id, "device_name": t.Name, "success": false, "error": err.Error()}
				return
			}
			_, err = s.pool.Exec(r.Context(), `
				UPDATE devices SET routeros_version=COALESCE(NULLIF($2,''),routeros_version),
				       hardware_model=COALESCE(NULLIF($3,''),hardware_model),
				       sys_name=COALESCE(NULLIF($4,''),sys_name),
				       routeros_last_sync_at=NOW(), routeros_last_error='', updated_at=NOW()
				WHERE id=$1
			`, id, snap.Version, snap.Model, snap.Identity)
			if err != nil {
				results[i] = map[string]any{"device_id": id, "device_name": t.Name, "success": false, "error": err.Error()}
				return
			}
			results[i] = map[string]any{"device_id": id, "device_name": t.Name, "success": true, "snapshot": snap}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": results})
}

// ─── Templates ─────────────────────────────────────────────────────────────

func (s *Server) listRouterOSTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, description, category, content, variables::text, is_builtin, created_by, updated_at
		FROM routeros_templates ORDER BY is_builtin DESC, name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, desc, category, content, vars, createdBy string
		var builtin bool
		var updated time.Time
		if rows.Scan(&id, &name, &desc, &category, &content, &vars, &builtin, &createdBy, &updated) == nil {
			var variables any
			_ = json.Unmarshal([]byte(vars), &variables)
			out = append(out, map[string]any{"id": id, "name": name, "description": desc,
				"category": category, "content": content, "variables": variables,
				"is_builtin": builtin, "created_by": createdBy, "updated_at": updated})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type routerosTemplateRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Content     string          `json:"content"`
	Variables   json.RawMessage `json:"variables"`
}

func (s *Server) createRouterOSTemplate(w http.ResponseWriter, r *http.Request) {
	var req routerosTemplateRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "name y content son obligatorios")
		return
	}
	if len(req.Content) > 64<<10 {
		writeError(w, http.StatusBadRequest, "el script supera 64 KB")
		return
	}
	if _, err := parseRouterOSTemplate(req.Content); err != nil {
		writeError(w, http.StatusBadRequest, "plantilla inválida: "+err.Error())
		return
	}
	if req.Category == "" {
		req.Category = "config"
	}
	vars := req.Variables
	if len(vars) == 0 {
		vars = json.RawMessage("[]")
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO routeros_templates (name, description, category, content, variables, created_by)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6) RETURNING id
	`, strings.TrimSpace(req.Name), req.Description, req.Category, req.Content, string(vars),
		username(r)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateRouterOSTemplate(w http.ResponseWriter, r *http.Request) {
	var req routerosTemplateRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "name y content son obligatorios")
		return
	}
	if len(req.Content) > 64<<10 {
		writeError(w, http.StatusBadRequest, "el script supera 64 KB")
		return
	}
	if _, err := parseRouterOSTemplate(req.Content); err != nil {
		writeError(w, http.StatusBadRequest, "plantilla inválida: "+err.Error())
		return
	}
	vars := req.Variables
	if len(vars) == 0 {
		vars = json.RawMessage("[]")
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE routeros_templates SET name=$2, description=$3, category=$4, content=$5,
		       variables=$6::jsonb, updated_at=NOW() WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), req.Description, req.Category, req.Content, string(vars))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "plantilla no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteRouterOSTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var builtin bool
	_ = s.pool.QueryRow(r.Context(), `SELECT is_builtin FROM routeros_templates WHERE id=$1`, id).Scan(&builtin)
	if builtin {
		writeError(w, http.StatusConflict, "las plantillas integradas no se pueden eliminar")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM routeros_templates WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "plantilla no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) previewRouterOSTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content    string         `json:"content"`
		TemplateID string         `json:"template_id"`
		DeviceID   string         `json:"device_id"`
		Variables  map[string]any `json:"variables"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	content := req.Content
	if req.TemplateID != "" {
		_ = s.pool.QueryRow(r.Context(), `SELECT content FROM routeros_templates WHERE id=$1`, req.TemplateID).Scan(&content)
	}
	if strings.TrimSpace(content) == "" {
		writeError(w, http.StatusBadRequest, "content o template_id es obligatorio")
		return
	}
	var t routerosTarget
	if req.DeviceID != "" {
		t, _ = s.loadRouterOSTarget(r.Context(), req.DeviceID)
	}
	if t.ID == "" {
		t = routerosTarget{ID: "00000000-0000-0000-0000-000000000000", Name: "equipo-demo", IP: "192.0.2.1"}
	}
	rendered, err := renderRouterOS(content, t, req.Variables, username(r), "preview")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"content": rendered, "device": t.Name, "variables": req.Variables,
	}})
}

// ─── Jobs ──────────────────────────────────────────────────────────────────

type routerosPlanItem struct {
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	IP          string `json:"primary_ip"`
	Credential  string `json:"credential"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
}

func (s *Server) listRouterOSJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT j.id, j.name, j.template_name, j.status, j.requires_approval,
		       cardinality(j.target_device_ids), j.created_by, j.approved_by, j.approved_at,
		       j.started_at, j.finished_at, j.created_at, j.stats::text,
		       count(t.id) FILTER (WHERE t.status='success'),
		       count(t.id) FILTER (WHERE t.status='failed')
		FROM routeros_jobs j LEFT JOIN routeros_job_targets t ON t.job_id=j.id
		GROUP BY j.id ORDER BY j.created_at DESC LIMIT 200
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, templateName, status, createdBy, approvedBy, stats string
		var requiresApproval bool
		var targets int
		var approvedAt, startedAt, finishedAt *time.Time
		var createdAt time.Time
		var success, failed int
		if rows.Scan(&id, &name, &templateName, &status, &requiresApproval, &targets, &createdBy,
			&approvedBy, &approvedAt, &startedAt, &finishedAt, &createdAt, &stats, &success, &failed) == nil {
			var statsObj any
			_ = json.Unmarshal([]byte(stats), &statsObj)
			out = append(out, map[string]any{"id": id, "name": name, "template_name": templateName,
				"status": status, "requires_approval": requiresApproval, "targets": targets,
				"created_by": createdBy, "approved_by": approvedBy, "approved_at": approvedAt,
				"started_at": startedAt, "finished_at": finishedAt, "created_at": createdAt,
				"stats": statsObj, "success": success, "failed": failed})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) getRouterOSJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var name, templateName, content, status, createdBy, approvedBy, notes, stats, plan string
	var requiresApproval bool
	var approvedAt, startedAt, finishedAt *time.Time
	var createdAt time.Time
	var targetIDs []string
	err := s.pool.QueryRow(r.Context(), `
		SELECT name, template_name, content, status, requires_approval, target_device_ids,
		       created_by, approved_by, approved_at, started_at, finished_at, notes,
		       stats::text, plan::text, created_at
		FROM routeros_jobs WHERE id=$1
	`, id).Scan(&name, &templateName, &content, &status, &requiresApproval, &targetIDs, &createdBy,
		&approvedBy, &approvedAt, &startedAt, &finishedAt, &notes, &stats, &plan, &createdAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "trabajo no encontrado")
		return
	}
	targets := []map[string]any{}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, device_id, device_name, status, output, error,
		       before_state::text, after_state::text, started_at, finished_at
		FROM routeros_job_targets WHERE job_id=$1 ORDER BY device_name
	`, id)
	if err == nil {
		for rows.Next() {
			var tid, deviceID, deviceName, tstatus, output, terror, before, after string
			var tstart, tfinish *time.Time
			if rows.Scan(&tid, &deviceID, &deviceName, &tstatus, &output, &terror, &before, &after,
				&tstart, &tfinish) == nil {
				var beforeObj, afterObj any
				_ = json.Unmarshal([]byte(before), &beforeObj)
				_ = json.Unmarshal([]byte(after), &afterObj)
				targets = append(targets, map[string]any{"id": tid, "device_id": deviceID,
					"device_name": deviceName, "status": tstatus, "output": output, "error": terror,
					"before_state": beforeObj, "after_state": afterObj,
					"started_at": tstart, "finished_at": tfinish})
			}
		}
		rows.Close()
	}
	var planObj, statsObj any
	_ = json.Unmarshal([]byte(plan), &planObj)
	_ = json.Unmarshal([]byte(stats), &statsObj)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": name, "template_name": templateName, "content": content, "status": status,
		"requires_approval": requiresApproval, "target_device_ids": targetIDs, "created_by": createdBy,
		"approved_by": approvedBy, "approved_at": approvedAt, "started_at": startedAt,
		"finished_at": finishedAt, "notes": notes, "stats": statsObj, "plan": planObj,
		"created_at": createdAt, "targets": len(targets), "target_rows": targets,
	}})
}

type routerosJobRequest struct {
	Name             string         `json:"name"`
	TemplateID       string         `json:"template_id"`
	Content          string         `json:"content"`
	Variables        map[string]any `json:"variables"`
	TargetDeviceIDs  []string       `json:"target_device_ids"`
	RequiresApproval *bool          `json:"requires_approval"`
	Notes            string         `json:"notes"`
}

func (s *Server) createRouterOSJob(w http.ResponseWriter, r *http.Request) {
	var req routerosJobRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || len(req.TargetDeviceIDs) == 0 {
		writeError(w, http.StatusBadRequest, "name y target_device_ids son obligatorios")
		return
	}
	if len(req.TargetDeviceIDs) > 50 {
		writeError(w, http.StatusBadRequest, "máximo 50 dispositivos por trabajo")
		return
	}
	uniqueTargets := make([]string, 0, len(req.TargetDeviceIDs))
	seenTargets := map[string]bool{}
	for _, id := range req.TargetDeviceIDs {
		if id != "" && !seenTargets[id] {
			seenTargets[id] = true
			uniqueTargets = append(uniqueTargets, id)
		}
	}
	req.TargetDeviceIDs = uniqueTargets
	if len(req.TargetDeviceIDs) == 0 {
		writeError(w, http.StatusBadRequest, "target_device_ids es obligatorio")
		return
	}
	if req.RequiresApproval != nil && !*req.RequiresApproval {
		writeError(w, http.StatusBadRequest, "todo despliegue RouterOS requiere aprobación")
		return
	}
	content := req.Content
	templateName := ""
	if req.TemplateID != "" {
		if err := s.pool.QueryRow(r.Context(), `SELECT content, name FROM routeros_templates WHERE id=$1`, req.TemplateID).Scan(&content, &templateName); err != nil {
			writeError(w, http.StatusBadRequest, "plantilla no encontrada")
			return
		}
	}
	if strings.TrimSpace(content) == "" {
		writeError(w, http.StatusBadRequest, "content o template_id es obligatorio")
		return
	}
	if len(content) > 64<<10 {
		writeError(w, http.StatusBadRequest, "el script supera 64 KB")
		return
	}
	if _, err := parseRouterOSTemplate(content); err != nil {
		writeError(w, http.StatusBadRequest, "plantilla inválida: "+err.Error())
		return
	}
	uid, _ := auth.UserFrom(r.Context())
	plan := make([]routerosPlanItem, 0, len(req.TargetDeviceIDs))
	var missing []string
	for _, id := range req.TargetDeviceIDs {
		t, err := s.loadRouterOSTarget(r.Context(), id)
		if err != nil {
			missing = append(missing, id)
			continue
		}
		if !t.Managed {
			missing = append(missing, t.Name)
			continue
		}
		rendered, err := renderRouterOS(content, t, req.Variables, uid.Username, req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s: %v", t.Name, err))
			return
		}
		sum := sha256.Sum256([]byte(rendered))
		plan = append(plan, routerosPlanItem{DeviceID: t.ID, DeviceName: t.Name, IP: t.IP,
			Credential: t.CredentialID, Content: rendered, ContentHash: hex.EncodeToString(sum[:])})
	}
	if len(missing) > 0 {
		writeError(w, http.StatusBadRequest, "sin credencial RouterOS asignada: "+strings.Join(missing, ", "))
		return
	}
	vars, _ := json.Marshal(req.Variables)
	planJSON, _ := json.Marshal(plan)
	sum := sha256.Sum256([]byte(content))
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO routeros_jobs (name, template_id, template_name, content, content_hash, variables,
		 target_device_ids, status, requires_approval, created_by, notes, plan)
		VALUES ($1,NULLIF($2,'')::uuid,$3,$4,$5,$6::jsonb,$7,'pending_approval',TRUE,$8,$9,$10::jsonb) RETURNING id
	`, strings.TrimSpace(req.Name), req.TemplateID, templateName, content, hex.EncodeToString(sum[:]),
		string(vars), req.TargetDeviceIDs, uid.Username, req.Notes, string(planJSON)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, item := range plan {
		if _, err := tx.Exec(ctx, `
			INSERT INTO routeros_job_targets (job_id, device_id, device_name) VALUES ($1,$2,$3)
			ON CONFLICT (job_id, device_id) DO NOTHING
		`, id, item.DeviceID, item.DeviceName); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{
		"id": id, "status": "pending_approval", "plan": plan,
	}})
}

func (s *Server) approveRouterOSJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	uid, _ := auth.UserFrom(r.Context())
	var createdBy string
	if err := s.pool.QueryRow(r.Context(), `SELECT created_by FROM routeros_jobs WHERE id=$1`, id).Scan(&createdBy); err != nil {
		writeError(w, http.StatusNotFound, "trabajo no encontrado")
		return
	}
	var adminCount int
	_ = s.pool.QueryRow(r.Context(), `SELECT count(*) FROM users WHERE role='admin'`).Scan(&adminCount)
	if adminCount > 1 && createdBy == uid.Username {
		writeError(w, http.StatusForbidden, "otro administrador debe aprobar este trabajo")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE routeros_jobs SET status='approved', approved_by=$2, approved_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND status='pending_approval'
	`, id, uid.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "el trabajo no está pendiente de aprobación")
		return
	}
	details, _ := json.Marshal(map[string]any{"action": "approve", "created_by": createdBy})
	_, _ = s.pool.Exec(r.Context(), `
		INSERT INTO audit_logs (user_id, username, method, path, status, resource_type, resource_id, details)
		VALUES (NULLIF($1,'')::uuid,$2,'APPROVE',$3,200,'routeros_job',$4,$5::jsonb)
	`, uid.ID, uid.Username, r.URL.Path, id, string(details))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) runRouterOSJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var status, planRaw string
	var targetIDs []string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT status, plan::text, target_device_ids FROM routeros_jobs WHERE id=$1
	`, id).Scan(&status, &planRaw, &targetIDs); err != nil {
		writeError(w, http.StatusNotFound, "trabajo no encontrado")
		return
	}
	if status != "approved" {
		writeError(w, http.StatusConflict, "el trabajo debe estar aprobado antes de ejecutarse")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE routeros_jobs SET status='running', started_at=NOW(), finished_at=NULL, updated_at=NOW()
		WHERE id=$1 AND status='approved'
	`, id)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "el trabajo ya está en ejecución")
		return
	}
	var plan []routerosPlanItem
	_ = json.Unmarshal([]byte(planRaw), &plan)
	if uid, ok := auth.UserFrom(r.Context()); ok {
		details, _ := json.Marshal(map[string]any{"targets": len(plan)})
		_, _ = s.pool.Exec(r.Context(), `
			INSERT INTO audit_logs (user_id, username, method, path, status, resource_type, resource_id, details)
			VALUES (NULLIF($1,'')::uuid,$2,'RUN',$3,202,'routeros_job',$4,$5::jsonb)
		`, uid.ID, uid.Username, r.URL.Path, id, string(details))
	}
	go s.executeRouterOSJob(context.Background(), id, plan)
	writeJSON(w, http.StatusAccepted, map[string]any{"success": true, "data": map[string]any{"status": "running"}})
}

func (s *Server) dryRunRouterOSJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var planRaw string
	if err := s.pool.QueryRow(r.Context(), `SELECT plan::text FROM routeros_jobs WHERE id=$1`, id).Scan(&planRaw); err != nil {
		writeError(w, http.StatusNotFound, "trabajo no encontrado")
		return
	}
	var plan []routerosPlanItem
	_ = json.Unmarshal([]byte(planRaw), &plan)
	if len(plan) > 50 {
		plan = plan[:50]
	}
	results := make([]map[string]any, len(plan))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, item := range plan {
		i, item := i, item
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			t, err := s.loadRouterOSTarget(r.Context(), item.DeviceID)
			if err != nil {
				results[i] = map[string]any{"device_id": item.DeviceID, "success": false, "error": err.Error()}
				return
			}
			opts, err := t.options()
			if err != nil {
				results[i] = map[string]any{"device_id": item.DeviceID, "device_name": item.DeviceName, "success": false, "error": err.Error()}
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			client, err := routeros.Connect(ctx, opts)
			if err != nil {
				results[i] = map[string]any{"device_id": item.DeviceID, "device_name": item.DeviceName, "success": false, "error": err.Error()}
				return
			}
			defer client.Close()
			snap, err := client.CollectSnapshot()
			if err != nil {
				results[i] = map[string]any{"device_id": item.DeviceID, "device_name": item.DeviceName, "success": false, "error": err.Error()}
				return
			}
			results[i] = map[string]any{"device_id": item.DeviceID, "device_name": item.DeviceName,
				"success": true, "snapshot": snap, "would_change": true, "script_bytes": len(item.Content)}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": results})
}

func (s *Server) cancelRouterOSJob(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE routeros_jobs SET status='cancelled', finished_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND status IN ('pending_approval','approved')
	`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "solo se pueden cancelar trabajos pendientes o aprobados")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteRouterOSJob(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM routeros_jobs WHERE id=$1 AND status IN ('completed','failed','cancelled')
	`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "solo se pueden borrar trabajos finalizados o cancelados")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) executeRouterOSJob(ctx context.Context, jobID string, plan []routerosPlanItem) {
	defer func() {
		if rec := recover(); rec != nil {
			_, _ = s.pool.Exec(context.Background(), `UPDATE routeros_jobs SET status='failed', finished_at=NOW() WHERE id=$1`, jobID)
		}
	}()
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, failed := 0, 0
	for _, item := range plan {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ok := s.executeRouterOSPlanItem(ctx, jobID, item)
			mu.Lock()
			if ok {
				success++
			} else {
				failed++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	final := "completed"
	if failed > 0 {
		final = "failed"
	}
	stats, _ := json.Marshal(map[string]any{"success": success, "failed": failed,
		"total": len(plan), "finished_at": time.Now().UTC()})
	_, _ = s.pool.Exec(context.Background(), `
		UPDATE routeros_jobs SET status=$2, finished_at=NOW(), stats=$3::jsonb, updated_at=NOW()
		WHERE id=$1 AND status='running'
	`, jobID, final, string(stats))
	details, _ := json.Marshal(map[string]any{"status": final, "success": success, "failed": failed, "total": len(plan)})
	auditStatus := 200
	if final == "failed" {
		auditStatus = 500
	}
	_, _ = s.pool.Exec(context.Background(), `
		INSERT INTO audit_logs (username, ip, method, path, status, resource_type, resource_id, details)
		VALUES ('system:routeros-job','','FINISH','routeros_job',$1,'routeros_job',$2,$3::jsonb)
	`, auditStatus, jobID, string(details))
}

func (s *Server) acquireRouterOSDeviceLock(ctx context.Context, deviceID, jobID string) bool {
	var locked string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO routeros_device_locks (device_id, job_id, expires_at)
		VALUES ($1,$2,NOW()+interval '5 minutes')
		ON CONFLICT (device_id) DO UPDATE
		SET job_id=EXCLUDED.job_id, expires_at=EXCLUDED.expires_at
		WHERE routeros_device_locks.expires_at < NOW() OR routeros_device_locks.job_id=EXCLUDED.job_id
		RETURNING device_id::text
	`, deviceID, jobID).Scan(&locked)
	return err == nil
}

func (s *Server) releaseRouterOSDeviceLock(deviceID, jobID string) {
	_, _ = s.pool.Exec(context.Background(),
		`DELETE FROM routeros_device_locks WHERE device_id=$1 AND job_id=$2`, deviceID, jobID)
}

func (s *Server) executeRouterOSPlanItem(ctx context.Context, jobID string, item routerosPlanItem) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !s.acquireRouterOSDeviceLock(ctx, item.DeviceID, jobID) {
		s.finishRouterOSTarget(ctx, jobID, item.DeviceID, "skipped", "", "otro despliegue está en curso en este equipo", nil, nil)
		return false
	}
	defer s.releaseRouterOSDeviceLock(item.DeviceID, jobID)
	t, err := s.loadRouterOSTarget(ctx, item.DeviceID)
	if err != nil {
		s.finishRouterOSTarget(ctx, jobID, item.DeviceID, "failed", "", err.Error(), nil, nil)
		return false
	}
	opts, err := t.options()
	if err != nil {
		s.finishRouterOSTarget(ctx, jobID, item.DeviceID, "failed", "", err.Error(), nil, nil)
		return false
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE routeros_job_targets SET status='running', started_at=NOW(), error=''
		WHERE job_id=$1 AND device_id=$2
	`, jobID, item.DeviceID)
	client, err := routeros.Connect(ctx, opts)
	if err != nil {
		s.finishRouterOSTarget(ctx, jobID, item.DeviceID, "failed", "", err.Error(), nil, nil)
		return false
	}
	defer client.Close()
	before, _ := client.CollectSnapshot()
	shortID := jobID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	output, err := client.RunScript(ctx, "netmonitor-"+shortID, item.Content, 90*time.Second)
	after, _ := client.CollectSnapshot()
	if err != nil {
		s.finishRouterOSTarget(ctx, jobID, item.DeviceID, "failed", output, err.Error(), before, after)
		return false
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE devices SET routeros_version=COALESCE(NULLIF($2,''),routeros_version),
		       hardware_model=COALESCE(NULLIF($3,''),hardware_model),
		       routeros_last_sync_at=NOW(), routeros_last_error='', updated_at=NOW()
		WHERE id=$1
	`, item.DeviceID, after.Version, after.Model)
	s.finishRouterOSTarget(ctx, jobID, item.DeviceID, "success", output, "", before, after)
	return true
}

func (s *Server) finishRouterOSTarget(ctx context.Context, jobID, deviceID, status, output, errMsg string, before, after any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if before == nil {
		b = []byte("{}")
	}
	if after == nil {
		a = []byte("{}")
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE routeros_job_targets SET status=$3, output=$4, error=$5,
		       before_state=$6::jsonb, after_state=$7::jsonb, finished_at=NOW()
		WHERE job_id=$1 AND device_id=$2
	`, jobID, deviceID, status, output, errMsg, string(b), string(a))
	if errMsg != "" {
		_, _ = s.pool.Exec(ctx, `UPDATE devices SET routeros_last_error=$2 WHERE id=$1`, deviceID, errMsg)
	}
}

// ─── Template rendering ────────────────────────────────────────────────────

func parseRouterOSTemplate(content string) (*template.Template, error) {
	return template.New("routeros").Option("missingkey=zero").Funcs(template.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"trim":  strings.TrimSpace,
		"join":  func(sep string, values []string) string { return strings.Join(values, sep) },
		"default": func(def any, value any) any {
			if value == nil || fmt.Sprint(value) == "" {
				return def
			}
			return value
		},
		"now": func() string { return time.Now().UTC().Format(time.RFC3339) },
	}).Parse(content)
}

func renderRouterOS(content string, target routerosTarget, vars map[string]any, user, jobName string) (string, error) {
	tpl, err := parseRouterOSTemplate(content)
	if err != nil {
		return "", fmt.Errorf("plantilla inválida: %w", err)
	}
	if vars == nil {
		vars = map[string]any{}
	}
	data := map[string]any{
		"Device": map[string]any{
			"ID": target.ID, "Name": target.Name, "IP": target.IP, "PrimaryIP": target.IP,
			"Tags": target.Tags, "Maps": target.Maps,
		},
		"Map":  map[string]any{"Name": target.Maps},
		"User": user,
		"Job":  jobName,
		"Now":  time.Now().UTC(),
		"Vars": vars,
	}
	var out strings.Builder
	if err := tpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	if strings.ContainsRune(out.String(), 0) {
		return "", fmt.Errorf("el script contiene bytes NUL")
	}
	return out.String(), nil
}

func username(r *http.Request) string {
	u, _ := auth.UserFrom(r.Context())
	return u.Username
}
