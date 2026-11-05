package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/secure"
)

var errNoToolCredential = errors.New("sin credencial")

type toolCredential struct {
	DeviceID    string
	DeviceName  string
	Address     string
	Kind        string
	Username    string
	Password    string
	Port        int
	UseTLS      bool
	InsecureTLS bool
	Path        string
	Notes       string
	Source      string
}

func validCredentialKind(kind string) bool {
	switch kind {
	case "winbox", "web", "ssh", "api", "other":
		return true
	default:
		return false
	}
}

func defaultToolPort(kind string, useTLS bool) int {
	switch kind {
	case "winbox":
		return 8291
	case "ssh":
		return 22
	case "web":
		if useTLS {
			return 443
		}
		return 80
	case "api":
		if useTLS {
			return 8729
		}
		return 8728
	default:
		return 0
	}
}

// resolveToolCredential prefers a per-device credential for the requested
// service and falls back to the RouterOS API credential already assigned to
// the device. The fallback keeps existing deployments working without forcing
// operators to duplicate credentials for Winbox, web and SSH.
func (s *Server) resolveToolCredential(ctx context.Context, deviceID, kind string) (toolCredential, error) {
	var c toolCredential
	c.DeviceID = deviceID
	c.Kind = kind
	if err := s.pool.QueryRow(ctx, `
		SELECT d.name, COALESCE(host(d.primary_ip),'')
		FROM devices d WHERE d.id=$1
	`, deviceID).Scan(&c.DeviceName, &c.Address); err != nil {
		return c, fmt.Errorf("dispositivo no encontrado")
	}
	if c.Address == "" {
		return c, fmt.Errorf("el dispositivo no tiene una IP principal")
	}

	var encrypted, path, notes string
	err := s.pool.QueryRow(ctx, `
		SELECT username, password, port, use_tls, insecure_tls, path, notes
		FROM device_credentials WHERE device_id=$1 AND kind=$2
	`, deviceID, kind).Scan(&c.Username, &encrypted, &c.Port, &c.UseTLS, &c.InsecureTLS, &path, &notes)
	if err == nil {
		password, derr := secure.Decrypt(encrypted)
		if derr != nil {
			return c, fmt.Errorf("no se pudo descifrar la credencial: %w", derr)
		}
		c.Password = password
		c.Path = path
		c.Notes = notes
		c.Source = "device"
		if c.Port <= 0 {
			c.Port = defaultToolPort(kind, c.UseTLS)
		}
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}

	var routerUsername, routerPassword string
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(rc.username,''), COALESCE(rc.password,'')
		FROM devices d
		LEFT JOIN routeros_credentials rc ON rc.id=d.routeros_credential_id
		WHERE d.id=$1
	`, deviceID).Scan(&routerUsername, &routerPassword)
	if err != nil || strings.TrimSpace(routerUsername) == "" {
		return c, errNoToolCredential
	}
	password, derr := secure.Decrypt(routerPassword)
	if derr != nil {
		return c, fmt.Errorf("no se pudo descifrar la credencial RouterOS: %w", derr)
	}
	c.Username = routerUsername
	c.Password = password
	c.Port = defaultToolPort(kind, false)
	c.Source = "routeros"
	return c, nil
}

func (s *Server) listDeviceCredentials(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	out := []map[string]any{}
	for _, kind := range []string{"winbox", "web", "ssh"} {
		c, err := s.resolveToolCredential(r.Context(), deviceID, kind)
		if errors.Is(err, errNoToolCredential) {
			out = append(out, map[string]any{
				"kind": kind, "source": "none", "username": "", "port": defaultToolPort(kind, false),
				"use_tls": false, "insecure_tls": false, "path": "", "notes": "", "has_password": false,
			})
			continue
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, map[string]any{
			"kind": c.Kind, "source": c.Source, "username": c.Username, "port": c.Port,
			"use_tls": c.UseTLS, "insecure_tls": c.InsecureTLS, "path": c.Path, "notes": c.Notes,
			"has_password": c.Password != "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type deviceCredentialRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
	Port          int    `json:"port"`
	UseTLS        bool   `json:"use_tls"`
	InsecureTLS   bool   `json:"insecure_tls"`
	Path          string `json:"path"`
	Notes         string `json:"notes"`
}

func (s *Server) upsertDeviceCredential(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	kind := strings.TrimSpace(chi.URLParam(r, "kind"))
	if !validCredentialKind(kind) {
		writeError(w, http.StatusBadRequest, "tipo de credencial no válido")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	var req deviceCredentialRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "cuerpo inválido")
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "puerto no válido")
		return
	}
	path := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(req.Path, "\r", ""), "\n", ""))
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	notes := strings.TrimSpace(req.Notes)
	username := strings.TrimSpace(req.Username)

	var existing string
	hasExisting := false
	err := s.pool.QueryRow(r.Context(), `
		SELECT password FROM device_credentials WHERE device_id=$1 AND kind=$2
	`, deviceID, kind).Scan(&existing)
	if err == nil {
		hasExisting = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var encrypted string
	switch {
	case req.ClearPassword:
		encrypted = ""
	case req.Password != "":
		encrypted, err = secure.Encrypt(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case hasExisting:
		encrypted = existing
	default:
		// When converting the RouterOS fallback into an explicit per-device
		// credential, keep the known password if the operator only changes the
		// username or port.
		fallback, ferr := s.resolveToolCredential(r.Context(), deviceID, kind)
		if ferr == nil {
			encrypted, err = secure.Encrypt(fallback.Password)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}

	_, err = s.pool.Exec(r.Context(), `
		INSERT INTO device_credentials (device_id, kind, username, password, port, use_tls,
			insecure_tls, path, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (device_id, kind) DO UPDATE SET
			username=EXCLUDED.username, password=EXCLUDED.password, port=EXCLUDED.port,
			use_tls=EXCLUDED.use_tls, insecure_tls=EXCLUDED.insecure_tls,
			path=EXCLUDED.path, notes=EXCLUDED.notes, updated_at=NOW()
	`, deviceID, kind, username, encrypted, req.Port, req.UseTLS, req.InsecureTLS, path, notes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteDeviceCredential(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM device_credentials WHERE device_id=$1 AND kind=$2`,
		deviceID, chi.URLParam(r, "kind"))
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

func (s *Server) revealDeviceCredential(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	c, err := s.resolveToolCredential(r.Context(), deviceID, chi.URLParam(r, "kind"))
	if err != nil {
		writeError(w, http.StatusNotFound, "credencial no disponible")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"username": c.Username, "password": c.Password, "source": c.Source,
		"port": c.Port, "use_tls": c.UseTLS, "path": c.Path,
	}})
}
