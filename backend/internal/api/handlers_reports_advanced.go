package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/reports"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func (s *Server) reportService() reports.Service { return reports.Service{Pool: s.pool} }

func stringSetKeys(set map[string]bool) []string {
	out := []string{}
	for id := range set {
		out = append(out, id)
	}
	return out
}

func (s *Server) reportServiceFor(ctx context.Context, user auth.User) reports.Service {
	service := reports.Service{Pool: s.pool}
	if user.Role == "admin" {
		return service
	}
	if user.RestrictMaps {
		allowed, _ := s.allowedMapIDs(ctx, user)
		service.Scope.MapIDs = stringSetKeys(allowed)
		if service.Scope.MapIDs == nil {
			service.Scope.MapIDs = []string{}
		}
	}
	if user.RestrictGroups || user.RestrictDevices {
		allowed, _ := s.allowedDeviceIDs(ctx, user)
		service.Scope.DeviceIDs = stringSetKeys(allowed)
		if service.Scope.DeviceIDs == nil {
			service.Scope.DeviceIDs = []string{}
		}
	}
	return service
}

func (s *Server) reportGeneratorFor(ctx context.Context, user auth.User) reports.Generator {
	return reports.Generator{Service: s.reportServiceFor(ctx, user), Dir: s.cfg.ReportDir}
}

func (s *Server) reportGenerator() reports.Generator {
	return reports.Generator{Service: s.reportService(), Dir: s.cfg.ReportDir}
}

func reportQueryRange(r *http.Request) (time.Time, time.Time, error) {
	return reports.ParseRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
}

func (s *Server) reportOverview(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportQueryRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	summary, err := s.reportServiceFor(r.Context(), user).Summary(r.Context(), from, to, r.URL.Query().Get("map_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": summary})
}

func (s *Server) reportDevices(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportQueryRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.reportServiceFor(r.Context(), user).Devices(r.Context(), from, to, r.URL.Query().Get("map_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"from": from, "to": to, "devices": rows,
	}})
}

func (s *Server) reportInterfaces(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportQueryRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.reportServiceFor(r.Context(), user).Interfaces(r.Context(), from, to, r.URL.Query().Get("map_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"from": from, "to": to, "interfaces": rows,
	}})
}

func (s *Server) reportMonthly(w http.ResponseWriter, r *http.Request) {
	months := 6
	if raw := r.URL.Query().Get("months"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			months = n
		}
	}
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.reportServiceFor(r.Context(), user).Monthly(r.Context(), months, r.URL.Query().Get("map_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

func (s *Server) reportClients(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportQueryRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.reportServiceFor(r.Context(), user).Clients(r.Context(), from, to, strings.TrimSpace(r.URL.Query().Get("client_id")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"from": from, "to": to, "clients": rows,
	}})
}

func (s *Server) listSLAClients(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	allowedMaps, mapRestricted := s.allowedMapIDs(r.Context(), user)
	allowedDevices, deviceRestricted := s.allowedDeviceIDs(r.Context(), user)
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, description, device_ids, map_ids, tags, target_pct, created_at, updated_at
		FROM sla_clients ORDER BY name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, description string
		var devices, maps, tags []string
		var target float64
		var created, updated time.Time
		if rows.Scan(&id, &name, &description, &devices, &maps, &tags, &target, &created, &updated) == nil {
			if user.Role != "admin" {
				if mapRestricted {
					filtered := make([]string, 0, len(maps))
					for _, mapID := range maps {
						if allowedMaps[mapID] {
							filtered = append(filtered, mapID)
						}
					}
					maps = filtered
				}
				if deviceRestricted {
					filtered := make([]string, 0, len(devices))
					for _, deviceID := range devices {
						if allowedDevices[deviceID] {
							filtered = append(filtered, deviceID)
						}
					}
					devices = filtered
				}
				if (len(devices) == 0 && len(maps) == 0) && (len(tags) == 0) {
					continue
				}
			}
			out = append(out, map[string]any{"id": id, "name": name, "description": description,
				"device_ids": devices, "map_ids": maps, "tags": tags, "target_pct": target,
				"created_at": created, "updated_at": updated})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type slaClientRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	DeviceIDs   []string `json:"device_ids"`
	MapIDs      []string `json:"map_ids"`
	Tags        []string `json:"tags"`
	TargetPct   float64  `json:"target_pct"`
}

func normalizeClient(req *slaClientRequest) bool {
	if strings.TrimSpace(req.Name) == "" {
		return false
	}
	if req.TargetPct <= 0 || req.TargetPct > 100 {
		req.TargetPct = 99.5
	}
	if req.DeviceIDs == nil {
		req.DeviceIDs = []string{}
	}
	if req.MapIDs == nil {
		req.MapIDs = []string{}
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	return true
}

func (s *Server) createSLAClient(w http.ResponseWriter, r *http.Request) {
	var req slaClientRequest
	if err := decodeBody(r, &req); err != nil || !normalizeClient(&req) {
		writeError(w, http.StatusBadRequest, "name es obligatorio")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO sla_clients (name, description, device_ids, map_ids, tags, target_pct)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id
	`, strings.TrimSpace(req.Name), req.Description, req.DeviceIDs, req.MapIDs, req.Tags, req.TargetPct).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateSLAClient(w http.ResponseWriter, r *http.Request) {
	var req slaClientRequest
	if err := decodeBody(r, &req); err != nil || !normalizeClient(&req) {
		writeError(w, http.StatusBadRequest, "name es obligatorio")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE sla_clients SET name=$2, description=$3, device_ids=$4, map_ids=$5, tags=$6,
		       target_pct=$7, updated_at=NOW() WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), req.Description, req.DeviceIDs,
		req.MapIDs, req.Tags, req.TargetPct)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "cliente no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteSLAClient(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM sla_clients WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "cliente no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ─── Schedules ─────────────────────────────────────────────────────────────

type reportSchedule struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	ReportType        string     `json:"report_type"`
	Format            string     `json:"format"`
	Frequency         string     `json:"frequency"`
	HourUTC           int        `json:"hour_utc"`
	DayOfWeek         int        `json:"day_of_week"`
	DayOfMonth        int        `json:"day_of_month"`
	RangeDays         int        `json:"range_days"`
	MapID             string     `json:"map_id"`
	ClientID          string     `json:"client_id"`
	IncludeInterfaces bool       `json:"include_interfaces"`
	ExportExternal    bool       `json:"export_external"`
	IsEnabled         bool       `json:"is_enabled"`
	LastRunAt         *time.Time `json:"last_run_at"`
	NextRunAt         time.Time  `json:"next_run_at"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (s *Server) listReportSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, report_type, format, frequency, hour_utc, day_of_week, day_of_month,
		       range_days, COALESCE(map_id::text,''), COALESCE(client_id::text,''),
		       include_interfaces, export_external, is_enabled, last_run_at, next_run_at,
		       created_by, created_at, updated_at
		FROM report_schedules ORDER BY name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []reportSchedule{}
	for rows.Next() {
		var sched reportSchedule
		if rows.Scan(&sched.ID, &sched.Name, &sched.ReportType, &sched.Format, &sched.Frequency,
			&sched.HourUTC, &sched.DayOfWeek, &sched.DayOfMonth, &sched.RangeDays, &sched.MapID,
			&sched.ClientID, &sched.IncludeInterfaces, &sched.ExportExternal, &sched.IsEnabled,
			&sched.LastRunAt, &sched.NextRunAt, &sched.CreatedBy, &sched.CreatedAt, &sched.UpdatedAt) == nil {
			out = append(out, sched)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type reportScheduleRequest struct {
	Name              string `json:"name"`
	ReportType        string `json:"report_type"`
	Format            string `json:"format"`
	Frequency         string `json:"frequency"`
	HourUTC           int    `json:"hour_utc"`
	DayOfWeek         int    `json:"day_of_week"`
	DayOfMonth        int    `json:"day_of_month"`
	RangeDays         int    `json:"range_days"`
	MapID             string `json:"map_id"`
	ClientID          string `json:"client_id"`
	IncludeInterfaces *bool  `json:"include_interfaces"`
	ExportExternal    bool   `json:"export_external"`
	IsEnabled         *bool  `json:"is_enabled"`
}

func normalizeScheduleRequest(req *reportScheduleRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name es obligatorio")
	}
	switch req.ReportType {
	case "sla", "interfaces", "executive", "clients":
	default:
		req.ReportType = "sla"
	}
	if req.Format != "csv" {
		req.Format = "pdf"
	}
	switch req.Frequency {
	case "daily", "weekly", "monthly":
	default:
		req.Frequency = "weekly"
	}
	if req.HourUTC < 0 || req.HourUTC > 23 {
		req.HourUTC = 6
	}
	if req.DayOfWeek < 0 || req.DayOfWeek > 6 {
		req.DayOfWeek = 1
	}
	if req.DayOfMonth < 1 || req.DayOfMonth > 28 {
		req.DayOfMonth = 1
	}
	if req.RangeDays < 1 || req.RangeDays > 3650 {
		req.RangeDays = 30
	}
	return nil
}

func (s *Server) createReportSchedule(w http.ResponseWriter, r *http.Request) {
	var req reportScheduleRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := normalizeScheduleRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	include := true
	if req.IncludeInterfaces != nil {
		include = *req.IncludeInterfaces
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	next := nextScheduleTime(req.Frequency, req.HourUTC, req.DayOfWeek, req.DayOfMonth, time.Now().UTC())
	uid, _ := auth.UserFrom(r.Context())
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO report_schedules (name, report_type, format, frequency, hour_utc, day_of_week,
		 day_of_month, range_days, map_id, client_id, include_interfaces, export_external,
		 is_enabled, next_run_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,$13,$14,$15)
		RETURNING id
	`, strings.TrimSpace(req.Name), req.ReportType, req.Format, req.Frequency, req.HourUTC,
		req.DayOfWeek, req.DayOfMonth, req.RangeDays, req.MapID, req.ClientID, include,
		req.ExportExternal, enabled, next, uid.Username).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id, "next_run_at": next}})
}

func (s *Server) updateReportSchedule(w http.ResponseWriter, r *http.Request) {
	var req reportScheduleRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := normalizeScheduleRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	include := true
	if req.IncludeInterfaces != nil {
		include = *req.IncludeInterfaces
	}
	var enabled any
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	next := nextScheduleTime(req.Frequency, req.HourUTC, req.DayOfWeek, req.DayOfMonth, time.Now().UTC())
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE report_schedules SET name=$2, report_type=$3, format=$4, frequency=$5, hour_utc=$6,
		 day_of_week=$7, day_of_month=$8, range_days=$9, map_id=NULLIF($10,'')::uuid,
		 client_id=NULLIF($11,'')::uuid, include_interfaces=$12, export_external=$13,
		 is_enabled=COALESCE($14,is_enabled), next_run_at=$15, updated_at=NOW()
		WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), req.ReportType, req.Format, req.Frequency,
		req.HourUTC, req.DayOfWeek, req.DayOfMonth, req.RangeDays, req.MapID, req.ClientID,
		include, req.ExportExternal, enabled, next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "programación no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"next_run_at": next}})
}

func (s *Server) deleteReportSchedule(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM report_schedules WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "programación no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) runReportScheduleNow(w http.ResponseWriter, r *http.Request) {
	sched, err := s.loadReportSchedule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "programación no encontrada")
		return
	}
	uid, _ := auth.UserFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	run, err := s.executeReportSchedule(ctx, sched, uid.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": run})
}

func (s *Server) loadReportSchedule(ctx context.Context, id string) (reportSchedule, error) {
	var sched reportSchedule
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, report_type, format, frequency, hour_utc, day_of_week, day_of_month,
		       range_days, COALESCE(map_id::text,''), COALESCE(client_id::text,''),
		       include_interfaces, export_external, is_enabled, last_run_at, next_run_at,
		       created_by, created_at, updated_at
		FROM report_schedules WHERE id=$1
	`, id).Scan(&sched.ID, &sched.Name, &sched.ReportType, &sched.Format, &sched.Frequency,
		&sched.HourUTC, &sched.DayOfWeek, &sched.DayOfMonth, &sched.RangeDays, &sched.MapID,
		&sched.ClientID, &sched.IncludeInterfaces, &sched.ExportExternal, &sched.IsEnabled,
		&sched.LastRunAt, &sched.NextRunAt, &sched.CreatedBy, &sched.CreatedAt, &sched.UpdatedAt)
	return sched, err
}

func (s *Server) executeReportSchedule(ctx context.Context, sched reportSchedule, generatedBy string) (reports.Run, error) {
	to := time.Now().UTC()
	from := to.Add(-time.Duration(sched.RangeDays) * 24 * time.Hour)
	var storage *reports.StorageConfig
	if sched.ExportExternal {
		cfg := s.loadReportStorage(ctx)
		storage = &cfg
	}
	gen := s.reportGenerator()
	run, err := gen.Generate(ctx, reports.GenerateOptions{
		Type: sched.ReportType, Format: sched.Format, From: from, To: to,
		MapID: sched.MapID, ClientID: sched.ClientID, IncludeInterfaces: sched.IncludeInterfaces,
		GeneratedBy: generatedBy, ScheduleID: sched.ID, ExportExternal: sched.ExportExternal,
		Storage: storage,
	})
	if err != nil {
		return run, err
	}
	next := nextScheduleTime(sched.Frequency, sched.HourUTC, sched.DayOfWeek, sched.DayOfMonth, time.Now().UTC())
	_, _ = s.pool.Exec(ctx, `
		UPDATE report_schedules SET last_run_at=NOW(), next_run_at=$2, updated_at=NOW() WHERE id=$1
	`, sched.ID, next)
	return run, nil
}

func (s *Server) listReportRuns(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	filter := ""
	if u.Role != "admin" {
		filter = u.Username
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, COALESCE(schedule_id::text,''), report_type, format, status, period_from,
		       period_to, filename, path, size_bytes, external_url, error, generated_by,
		       created_at, finished_at
		FROM report_runs
		WHERE ($1='' OR generated_by=$1)
		ORDER BY created_at DESC LIMIT 200
	`, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []reports.Run{}
	for rows.Next() {
		var run reports.Run
		if rows.Scan(&run.ID, &run.ScheduleID, &run.ReportType, &run.Format, &run.Status,
			&run.PeriodFrom, &run.PeriodTo, &run.Filename, &run.Path, &run.SizeBytes,
			&run.ExternalURL, &run.Error, &run.GeneratedBy, &run.CreatedAt, &run.FinishedAt) == nil {
			run.Path = ""
			out = append(out, run)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) downloadReportRun(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var filename, filePath, format, generatedBy string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT filename, path, format, generated_by FROM report_runs WHERE id=$1
	`, chi.URLParam(r, "id")).Scan(&filename, &filePath, &format, &generatedBy); err != nil {
		writeError(w, http.StatusNotFound, "informe no encontrado")
		return
	}
	if u.Role != "admin" && generatedBy != u.Username {
		writeError(w, http.StatusForbidden, "sin acceso a este informe")
		return
	}
	absDir, err := filepath.Abs(s.cfg.ReportDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rel, err := filepath.Rel(absDir, absFile)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		writeError(w, http.StatusForbidden, "ruta de informe no permitida")
		return
	}
	if _, err := os.Stat(absFile); err != nil {
		writeError(w, http.StatusNotFound, "el archivo ya no está disponible")
		return
	}
	contentType := "application/pdf"
	if format == "csv" {
		contentType = "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
	http.ServeFile(w, r, absFile)
}

func (s *Server) generateReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type              string `json:"report_type"`
		Format            string `json:"format"`
		From              string `json:"from"`
		To                string `json:"to"`
		MapID             string `json:"map_id"`
		ClientID          string `json:"client_id"`
		IncludeInterfaces *bool  `json:"include_interfaces"`
		ExportExternal    bool   `json:"export_external"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	from, to, err := reports.ParseRange(req.From, req.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	include := true
	if req.IncludeInterfaces != nil {
		include = *req.IncludeInterfaces
	}
	var storage *reports.StorageConfig
	if req.ExportExternal {
		cfg := s.loadReportStorage(r.Context())
		storage = &cfg
	}
	uid, _ := auth.UserFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	run, err := s.reportGeneratorFor(ctx, uid).Generate(ctx, reports.GenerateOptions{
		Type: req.Type, Format: req.Format, From: from, To: to, MapID: req.MapID,
		ClientID: req.ClientID, IncludeInterfaces: include, GeneratedBy: uid.Username,
		ExportExternal: req.ExportExternal, Storage: storage,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": run})
}

// ─── External storage settings ─────────────────────────────────────────────

func (s *Server) loadReportStorage(ctx context.Context) reports.StorageConfig {
	var raw string
	_ = s.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='reports.storage'`).Scan(&raw)
	if raw == "" {
		return reports.StorageConfig{Type: "local", Path: s.cfg.ReportDir}
	}
	plain := secure.UnwrapJSON(raw)
	var cfg reports.StorageConfig
	if json.Unmarshal([]byte(plain), &cfg) != nil {
		return reports.StorageConfig{Type: "local", Path: s.cfg.ReportDir}
	}
	return cfg
}

func (s *Server) getReportStorage(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadReportStorage(r.Context())
	secretSet := cfg.SecretKey != ""
	cfg.SecretKey = ""
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"config": cfg, "has_secret": secretSet, "report_dir": s.cfg.ReportDir,
	}})
}

func (s *Server) setReportStorage(w http.ResponseWriter, r *http.Request) {
	var req reports.StorageConfig
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.SecretKey == "" {
		req.SecretKey = s.loadReportStorage(r.Context()).SecretKey
	}
	if req.Type == "" {
		req.Type = "local"
	}
	if req.Type == "local" && req.Path == "" {
		req.Path = s.cfg.ReportDir
	}
	if req.Type == "local" {
		base, _ := filepath.Abs(s.cfg.ReportDir)
		target, _ := filepath.Abs(req.Path)
		rel, err := filepath.Rel(base, target)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			writeError(w, http.StatusBadRequest, "la ruta local debe estar dentro de REPORT_DIR")
			return
		}
		req.Path = target
	}
	if req.Type == "s3" && (req.Bucket == "" || req.AccessKey == "" || req.SecretKey == "") {
		writeError(w, http.StatusBadRequest, "bucket, access_key y secret_key son obligatorios")
		return
	}
	raw, _ := json.Marshal(req)
	wrapped, err := secure.WrapJSON(string(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, err = s.pool.Exec(r.Context(), `
		INSERT INTO system_settings (key, value, updated_at) VALUES ('reports.storage',$1,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()
	`, wrapped)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) testReportStorage(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadReportStorage(r.Context())
	var req reports.StorageConfig
	if err := decodeBody(r, &req); err == nil && req.Type != "" {
		if req.SecretKey == "" {
			req.SecretKey = cfg.SecretKey
		}
		if req.Type == "local" {
			if req.Path == "" {
				req.Path = s.cfg.ReportDir
			}
			base, _ := filepath.Abs(s.cfg.ReportDir)
			target, _ := filepath.Abs(req.Path)
			rel, relErr := filepath.Rel(base, target)
			if relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				writeError(w, http.StatusBadRequest, "la ruta local debe estar dentro de REPORT_DIR")
				return
			}
			req.Path = target
		}
		cfg = req
	}
	if err := os.MkdirAll(s.cfg.ReportDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("netmonitor-storage-test-%d.txt", time.Now().Unix())
	tmp := filepath.Join(s.cfg.ReportDir, name)
	if err := os.WriteFile(tmp, []byte("NetMonitor storage test\n"), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp)
	url, err := cfg.Upload(r.Context(), tmp, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"url": url}})
}

// ─── Scheduler ─────────────────────────────────────────────────────────────

func (s *Server) startReportScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDueReports(ctx)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		s.cleanupReports(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanupReports(ctx)
			}
		}
	}()
}

func (s *Server) cleanupReports(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, path FROM report_runs WHERE created_at < NOW() - interval '90 days'
	`)
	if err != nil {
		return
	}
	type oldRun struct{ id, path string }
	var runs []oldRun
	for rows.Next() {
		var run oldRun
		if rows.Scan(&run.id, &run.path) == nil {
			runs = append(runs, run)
		}
	}
	rows.Close()
	for _, run := range runs {
		if run.path != "" {
			_ = os.Remove(run.path)
		}
		_, _ = s.pool.Exec(ctx, `DELETE FROM report_runs WHERE id=$1`, run.id)
	}
}

func (s *Server) runDueReports(ctx context.Context) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(982451654)`).Scan(&locked); err != nil || !locked {
		return
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM report_schedules
		WHERE is_enabled=TRUE AND next_run_at <= NOW()
		ORDER BY next_run_at LIMIT 3
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
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
	type claimed struct {
		id   string
		next time.Time
	}
	var work []claimed
	for _, id := range ids {
		sched, err := s.loadReportSchedule(ctx, id)
		if err != nil {
			continue
		}
		next := nextScheduleTime(sched.Frequency, sched.HourUTC, sched.DayOfWeek, sched.DayOfMonth, time.Now().UTC())
		if _, err := tx.Exec(ctx, `UPDATE report_schedules SET next_run_at=$2 WHERE id=$1`, id, next); err != nil {
			continue
		}
		work = append(work, claimed{id: id, next: next})
	}
	if err := tx.Commit(ctx); err != nil {
		return
	}
	for _, item := range work {
		sched, err := s.loadReportSchedule(ctx, item.id)
		if err != nil {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		_, err = s.executeReportSchedule(runCtx, sched, "scheduler")
		cancel()
		if err != nil {
			log.Printf("scheduled report %s: %v", item.id, err)
			_, _ = s.pool.Exec(ctx, `UPDATE report_schedules SET next_run_at=NOW()+interval '10 minutes' WHERE id=$1`, item.id)
		}
	}
}

func nextScheduleTime(frequency string, hour, dayOfWeek, dayOfMonth int, now time.Time) time.Time {
	hour = clampInt(hour, 0, 23)
	switch frequency {
	case "daily":
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	case "monthly":
		dom := clampInt(dayOfMonth, 1, 28)
		next := time.Date(now.Year(), now.Month(), dom, hour, 0, 0, 0, time.UTC)
		if !next.After(now) {
			next = next.AddDate(0, 1, 0)
			next = time.Date(next.Year(), next.Month(), dom, hour, 0, 0, 0, time.UTC)
		}
		return next
	default:
		dow := clampInt(dayOfWeek, 0, 6)
		delta := (dow - int(now.Weekday()) + 7) % 7
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC).AddDate(0, 0, delta)
		if !next.After(now) {
			next = next.AddDate(0, 0, 7)
		}
		return next
	}
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
