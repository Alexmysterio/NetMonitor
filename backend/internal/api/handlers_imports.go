package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/dude"
)

func (s *Server) previewDudeImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, dude.MaxFileSize+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "sube un archivo SQLite (.db) o XML de hasta 256 MB")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "falta el archivo")
		return
	}
	defer file.Close()
	if header != nil && header.Size > dude.MaxFileSize {
		writeError(w, http.StatusBadRequest, "el archivo supera 256 MB")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, dude.MaxFileSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filename := "dude-import"
	if header != nil && header.Filename != "" {
		filename = header.Filename
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	preview, err := dude.Parse(ctx, filename, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	preview.Warnings = append(preview.Warnings,
		"Las credenciales (usuario/contraseña/SNMP) de The Dude no se importan por seguridad; asígnalas después desde Inventario o RouterOS.")
	payload, _ := json.Marshal(preview)
	warnings, _ := json.Marshal(preview.Warnings)
	uid, _ := auth.UserFrom(r.Context())
	var id string
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO dude_imports (filename, source_kind, status, device_count, map_count, link_count,
		 payload, warnings, created_by)
		VALUES ($1,$2,'preview',$3,$4,$5,$6::jsonb,$7::jsonb,$8) RETURNING id
	`, filename, preview.SourceKind, len(preview.Devices), len(preview.Maps), len(preview.Links),
		string(payload), string(warnings), uid.Username).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{
		"id": id, "preview": preview,
	}})
}

func (s *Server) listDudeImports(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, filename, source_kind, status, device_count, map_count, link_count,
		       created_by, created_at, applied_at, result::text
		FROM dude_imports ORDER BY created_at DESC LIMIT 100
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, filename, kind, status, createdBy, result string
		var devices, maps, links int
		var created time.Time
		var applied *time.Time
		if rows.Scan(&id, &filename, &kind, &status, &devices, &maps, &links, &createdBy, &created, &applied, &result) == nil {
			var resultObj any
			_ = json.Unmarshal([]byte(result), &resultObj)
			out = append(out, map[string]any{"id": id, "filename": filename, "source_kind": kind,
				"status": status, "device_count": devices, "map_count": maps, "link_count": links,
				"created_by": createdBy, "created_at": created, "applied_at": applied, "result": resultObj})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) getDudeImport(w http.ResponseWriter, r *http.Request) {
	var id, filename, kind, status, payload, warnings, result string
	var created time.Time
	var applied *time.Time
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, filename, source_kind, status, payload::text, warnings::text, result::text, created_at, applied_at
		FROM dude_imports WHERE id=$1
	`, chi.URLParam(r, "id")).Scan(&id, &filename, &kind, &status, &payload, &warnings, &result, &created, &applied)
	if err != nil {
		writeError(w, http.StatusNotFound, "importación no encontrada")
		return
	}
	var preview, warningsObj, resultObj any
	_ = json.Unmarshal([]byte(payload), &preview)
	_ = json.Unmarshal([]byte(warnings), &warningsObj)
	_ = json.Unmarshal([]byte(result), &resultObj)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "filename": filename, "source_kind": kind, "status": status,
		"preview": preview, "warnings": warningsObj, "result": resultObj,
		"created_at": created, "applied_at": applied,
	}})
}

type dudeApplyRequest struct {
	MapIDs          *[]string         `json:"map_ids"`
	DeviceSourceIDs *[]string         `json:"device_source_ids"`
	SNMPProfiles    map[string]string `json:"snmp_profiles"`
	ParentMapID     string            `json:"parent_map_id"`
	CreateLinks     *bool             `json:"create_links"`
	Prefix          string            `json:"name_prefix"`
}

func (s *Server) applyDudeImport(w http.ResponseWriter, r *http.Request) {
	var req dudeApplyRequest
	_ = decodeBody(r, &req)
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	var status, payload string
	if err := tx.QueryRow(ctx, `SELECT status, payload::text FROM dude_imports WHERE id=$1 FOR UPDATE`,
		chi.URLParam(r, "id")).Scan(&status, &payload); err != nil {
		writeError(w, http.StatusNotFound, "importación no encontrada")
		return
	}
	if status != "preview" {
		writeError(w, http.StatusConflict, "esta importación ya se aplicó")
		return
	}
	var preview dude.Preview
	if err := json.Unmarshal([]byte(payload), &preview); err != nil {
		writeError(w, http.StatusInternalServerError, "el preview guardado está dañado")
		return
	}
	uid, _ := auth.UserFrom(ctx)
	createLinks := true
	if req.CreateLinks != nil {
		createLinks = *req.CreateLinks
	}
	selectedMaps := map[string]bool{}
	if req.MapIDs != nil {
		for _, id := range *req.MapIDs {
			selectedMaps[id] = true
		}
	} else {
		for _, m := range preview.Maps {
			selectedMaps[m.SourceID] = true
		}
	}
	selectedDevices := map[string]bool{}
	if req.DeviceSourceIDs != nil {
		for _, id := range *req.DeviceSourceIDs {
			selectedDevices[id] = true
		}
	} else {
		for _, d := range preview.Devices {
			selectedDevices[d.SourceID] = true
		}
	}
	snmpAssignments := map[string]string{}
	var profileIDs []string
	seenProfiles := map[string]bool{}
	for sourceID, profileID := range req.SNMPProfiles {
		profileID = strings.TrimSpace(profileID)
		if profileID == "" || !selectedDevices[sourceID] {
			continue
		}
		snmpAssignments[sourceID] = profileID
		if !seenProfiles[profileID] {
			seenProfiles[profileID] = true
			profileIDs = append(profileIDs, profileID)
		}
	}
	if len(profileIDs) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM snmp_profiles WHERE id = ANY($1::uuid[])`, profileIDs).Scan(&count); err != nil {
			writeError(w, http.StatusBadRequest, "perfil SNMP no válido")
			return
		}
		if count != len(profileIDs) {
			writeError(w, http.StatusBadRequest, "uno o más perfiles SNMP ya no existen")
			return
		}
	}
	prefix := strings.TrimSpace(req.Prefix)
	var parent any
	if req.ParentMapID != "" {
		var exists bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM maps WHERE id=$1)`, req.ParentMapID).Scan(&exists) == nil && exists {
			parent = req.ParentMapID
		}
	}
	mapIDs := map[string]string{}
	createdMaps := 0
	// Create parent maps before children, regardless of source ordering.
	pending := append([]dude.Map(nil), preview.Maps...)
	for pass := 0; pass < len(preview.Maps)+2 && len(pending) > 0; pass++ {
		var next []dude.Map
		for _, m := range pending {
			if !selectedMaps[m.SourceID] {
				continue
			}
			if m.ParentSourceID != "" && selectedMaps[m.ParentSourceID] && mapIDs[m.ParentSourceID] == "" {
				next = append(next, m)
				continue
			}
			mapParent := parent
			if m.ParentSourceID != "" && mapIDs[m.ParentSourceID] != "" {
				mapParent = mapIDs[m.ParentSourceID]
			}
			name := prefix + m.Name
			if name == "" {
				name = "Mapa Dude"
			}
			slug := slugify(name)
			var mapID, finalSlug string
			ok := false
			for i := 0; i < 60; i++ {
				try := slug
				if i > 0 {
					try = fmt.Sprintf("%s-%d", slug, i)
				}
				err := tx.QueryRow(ctx, `
					INSERT INTO maps (name, slug, parent_map_id, description, grid_enabled, grid_size,
					 snap_to_grid, background_color, default_device_label)
					VALUES ($1,$2,$3,$4,TRUE,16,TRUE,'#FFFFFF','[Device.Name]\n[Device.FirstAddress]')
					RETURNING id, slug
				`, name, try, mapParent, "Importado desde The Dude").Scan(&mapID, &finalSlug)
				if err == nil {
					_ = finalSlug
					mapIDs[m.SourceID] = mapID
					createdMaps++
					ok = true
					break
				}
				if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			if !ok {
				writeError(w, http.StatusConflict, "no se pudo generar un slug para el mapa "+name)
				return
			}
		}
		pending = next
	}
	if len(mapIDs) == 0 && len(selectedMaps) > 0 {
		// The Dude sometimes has devices without a map record. Create a landing map.
		var mapID string
		name := prefix + "Importado desde The Dude"
		if err := tx.QueryRow(ctx, `
			INSERT INTO maps (name, slug, parent_map_id, description, default_device_label)
			VALUES ($1,$2,$3,'Importado desde The Dude','[Device.Name]\n[Device.FirstAddress]')
			RETURNING id
		`, name, slugify(name), parent).Scan(&mapID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		mapIDs["imported"] = mapID
		createdMaps = 1
	}

	createdDevices, reusedDevices, snmpAssigned := 0, 0, 0
	deviceIDs := map[string]string{}
	skipped := 0
	var warnings []string
	order := 0
	for _, d := range preview.Devices {
		if len(selectedDevices) > 0 && !selectedDevices[d.SourceID] {
			continue
		}
		if d.SourceMapID != "" && !selectedMaps[d.SourceMapID] {
			continue
		}
		targetMap := mapIDs[d.SourceMapID]
		if targetMap == "" {
			targetMap = mapIDs["imported"]
		}
		if targetMap == "" {
			continue
		}
		ip := resolveDudeAddress(d.Address)
		if ip == "" {
			skipped++
			warnings = append(warnings, fmt.Sprintf("%s: dirección no válida (%s)", d.Name, d.Address))
			continue
		}
		var deviceID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM devices WHERE primary_ip=$1::inet LIMIT 1`, ip).Scan(&deviceID)
		if err == nil {
			reusedDevices++
		} else if errors.Is(err, pgx.ErrNoRows) {
			notes := d.Notes
			if strings.TrimSpace(notes) != "" {
				notes += "\n"
			}
			notes += "Importado desde The Dude (" + preview.Filename + ")"
			custom, _ := json.Marshal(map[string]any{"dude_source_id": d.SourceID, "dude_model": d.Model})
			err = tx.QueryRow(ctx, `
				INSERT INTO devices (name, primary_ip, dns_name, mac_address, vendor, hardware_model,
				 device_type, notes, custom_fields, status)
				VALUES ($1,$2::inet,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,$9::jsonb,'unknown')
				RETURNING id
			`, d.Name, ip, d.DNSName, strings.ToLower(d.MAC), d.Vendor, d.Model, d.DeviceType, notes, string(custom)).Scan(&deviceID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			createdDevices++
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		deviceIDs[d.SourceID] = deviceID
		if profileID := snmpAssignments[d.SourceID]; profileID != "" {
			if _, err := tx.Exec(ctx, `UPDATE devices SET snmp_profile_id=$2 WHERE id=$1`, deviceID, profileID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			snmpAssigned++
		}

		x, y := d.PosX, d.PosY
		if x == 0 && y == 0 {
			x = float64(80 + (order%8)*190)
			y = float64(80 + (order/8)*140)
		}
		order++
		width, height := d.Width, d.Height
		if width <= 0 {
			width = 180
		}
		if height <= 0 {
			height = 88
		}
		shape, icon := dudeShape(ctx, tx, d.DeviceType)
		_, _ = tx.Exec(ctx, `
			INSERT INTO map_elements (map_id, element_type, device_id, pos_x, pos_y, width, height,
			 shape, icon_name, label_template)
			SELECT $1,'device',$2,$3,$4,$5,$6,$7,$8,m.default_device_label
			FROM maps m WHERE m.id=$1
			AND NOT EXISTS (SELECT 1 FROM map_elements WHERE map_id=$1 AND device_id=$2)
		`, targetMap, deviceID, x, y, width, height, shape, icon)
	}

	createdLinks := 0
	if createLinks {
		// Reuse only links between map elements that belong to this import.
		seenLinks := map[string]bool{}
		for _, l := range preview.Links {
			mapID := mapIDs[l.SourceMapID]
			if mapID == "" {
				continue
			}
			aSource := deviceIDs[l.DeviceA]
			bSource := deviceIDs[l.DeviceB]
			if aSource == "" || bSource == "" {
				continue
			}
			var aElem, bElem string
			if tx.QueryRow(ctx, `SELECT id::text FROM map_elements WHERE map_id=$1 AND device_id=$2 LIMIT 1`, mapID, aSource).Scan(&aElem) != nil {
				continue
			}
			if tx.QueryRow(ctx, `SELECT id::text FROM map_elements WHERE map_id=$1 AND device_id=$2 LIMIT 1`, mapID, bSource).Scan(&bElem) != nil {
				continue
			}
			key := mapID + ":" + aElem + ":" + bElem
			reverse := mapID + ":" + bElem + ":" + aElem
			if seenLinks[key] || seenLinks[reverse] {
				continue
			}
			seenLinks[key] = true
			speed := l.SpeedBps
			if speed <= 0 {
				speed = 1000000000
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO map_links (map_id, source_element_id, target_element_id, source_handle,
				 target_handle, link_type, max_speed_bps, label_template)
				VALUES ($1,$2,$3,'center','center',$4,$5,'')
			`, mapID, aElem, bElem, l.LinkType, speed); err == nil {
				createdLinks++
			}
		}
	}
	result, _ := json.Marshal(map[string]any{
		"created_maps": createdMaps, "created_devices": createdDevices, "reused_devices": reusedDevices,
		"created_links": createdLinks, "snmp_assigned": snmpAssigned, "skipped_devices": skipped, "warnings": warnings,
		"map_ids": mapIDs,
	})
	_, err = tx.Exec(ctx, `
		UPDATE dude_imports SET status='applied', result=$2::jsonb, applied_at=NOW() WHERE id=$1
	`, chi.URLParam(r, "id"), string(result))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"created_maps": createdMaps, "created_devices": createdDevices, "reused_devices": reusedDevices,
		"created_links": createdLinks, "snmp_assigned": snmpAssigned, "skipped_devices": skipped, "warnings": warnings,
		"map_ids": mapIDs, "by": uid.Username,
	}})
}

func resolveDudeAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.IndexAny(raw, ",; "); idx > 0 {
		raw = raw[:idx]
	}
	raw = strings.Trim(raw, "[]")
	if net.ParseIP(raw) != nil {
		return raw
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupHost(ctx, raw)
	if err == nil && len(ips) > 0 {
		return ips[0]
	}
	return ""
}

func dudeShape(ctx context.Context, tx pgx.Tx, deviceType string) (string, string) {
	var shape, icon string
	if err := tx.QueryRow(ctx, `SELECT shape, icon FROM device_types WHERE name=$1`, deviceType).Scan(&shape, &icon); err != nil {
		return "circle", "server"
	}
	if shape == "" {
		shape = "circle"
	}
	if icon == "" {
		icon = "server"
	}
	return shape, icon
}
