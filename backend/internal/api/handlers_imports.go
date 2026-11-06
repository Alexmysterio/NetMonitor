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
	"github.com/jackc/pgx/v5/pgconn"
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
	routerCredentialDevices := 0
	for _, d := range preview.Devices {
		if d.Username != "" {
			routerCredentialDevices++
		}
	}
	if routerCredentialDevices > 0 {
		preview.Warnings = append(preview.Warnings,
			fmt.Sprintf("Se importarán credenciales RouterOS de %d equipos.", routerCredentialDevices))
	}
	if len(preview.SNMPProfiles) > 0 {
		preview.Warnings = append(preview.Warnings,
			fmt.Sprintf("Se detectaron %d perfiles SNMP de The Dude.", len(preview.SNMPProfiles)))
	}
	monitoredLinks := 0
	for _, l := range preview.Links {
		if l.MasterInterface > 0 && l.MasterDevice != "" {
			monitoredLinks++
		}
	}
	if monitoredLinks > 0 {
		preview.Warnings = append(preview.Warnings,
			fmt.Sprintf("Se asignará monitorización SNMP a %d enlaces.", monitoredLinks))
	}
	if err := encryptDudePreviewSecrets(&preview); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload, _ := json.Marshal(preview)
	sanitizeDudePreview(&preview)
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
	var preview dude.Preview
	if err := json.Unmarshal([]byte(payload), &preview); err != nil {
		writeError(w, http.StatusInternalServerError, "el preview guardado está dañado")
		return
	}
	sanitizeDudePreview(&preview)
	var warningsObj, resultObj any
	_ = json.Unmarshal([]byte(warnings), &warningsObj)
	_ = json.Unmarshal([]byte(result), &resultObj)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "filename": filename, "source_kind": kind, "status": status,
		"preview": preview, "warnings": warningsObj, "result": resultObj,
		"created_at": created, "applied_at": applied,
	}})
}

type dudeApplyRequest struct {
	MapIDs               *[]string         `json:"map_ids"`
	DeviceSourceIDs      *[]string         `json:"device_source_ids"`
	SNMPProfiles         map[string]string `json:"snmp_profiles"`
	ProbeIDs             []string          `json:"probe_ids"`
	DeviceTypes          map[string]string `json:"device_types"`
	ParentMapID          string            `json:"parent_map_id"`
	CreateLinks          *bool             `json:"create_links"`
	Prefix               string            `json:"name_prefix"`
	ImportCredentials    *bool             `json:"import_credentials"`
	ImportSNMP           *bool             `json:"import_snmp"`
	OverwriteCredentials *bool             `json:"overwrite_credentials"`
	AssignLinkMonitoring *bool             `json:"assign_link_monitoring"`
}

func (s *Server) applyDudeImport(w http.ResponseWriter, r *http.Request) {
	var req dudeApplyRequest
	_ = decodeBody(r, &req)
	importCredentials := true
	if req.ImportCredentials != nil {
		importCredentials = *req.ImportCredentials
	}
	importSNMP := true
	if req.ImportSNMP != nil {
		importSNMP = *req.ImportSNMP
	}
	overwriteCredentials := false
	if req.OverwriteCredentials != nil {
		overwriteCredentials = *req.OverwriteCredentials
	}
	assignLinkMonitoring := true
	if req.AssignLinkMonitoring != nil {
		assignLinkMonitoring = *req.AssignLinkMonitoring
	}
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
	warnings := decryptDudePreviewSecrets(&preview)
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
	devicesBySourceID := map[string]dude.Device{}
	for _, d := range preview.Devices {
		devicesBySourceID[d.SourceID] = d
	}
	manualSNMP := map[string]string{}
	var manualProfileIDs []string
	seenManualProfiles := map[string]bool{}
	for sourceID, profileID := range req.SNMPProfiles {
		profileID = strings.TrimSpace(profileID)
		if profileID == "" || !dudeDeviceSelected(selectedDevices, sourceID) {
			continue
		}
		if d, ok := devicesBySourceID[sourceID]; ok && !dudeDeviceInSelectedMap(d, selectedMaps) {
			continue
		}
		manualSNMP[sourceID] = profileID
		if !seenManualProfiles[profileID] {
			seenManualProfiles[profileID] = true
			manualProfileIDs = append(manualProfileIDs, profileID)
		}
	}
	if len(manualProfileIDs) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM snmp_profiles WHERE id = ANY($1::uuid[])`, manualProfileIDs).Scan(&count); err != nil {
			writeError(w, http.StatusBadRequest, "perfil SNMP no válido")
			return
		}
		if count != len(manualProfileIDs) {
			writeError(w, http.StatusBadRequest, "uno o más perfiles SNMP ya no existen")
			return
		}
	}
	neededProfiles := map[string]bool{}
	missingProfileRefs := 0
	for _, d := range preview.Devices {
		if !dudeDeviceSelected(selectedDevices, d.SourceID) || !dudeDeviceInSelectedMap(d, selectedMaps) {
			continue
		}
		if manualSNMP[d.SourceID] != "" {
			continue
		}
		if strings.TrimSpace(d.SNMPProfileSourceID) == "" && strings.TrimSpace(d.SNMPProfileName) == "" {
			continue
		}
		sourceID := dudeDeviceSNMPProfileSourceID(d, preview.SNMPProfiles)
		if sourceID == "" {
			missingProfileRefs++
			continue
		}
		neededProfiles[sourceID] = true
	}
	if missingProfileRefs > 0 {
		warnings = append(warnings, fmt.Sprintf("%d equipos referencian perfiles SNMP que no están en el preview.", missingProfileRefs))
	}
	snmpProfileIDs := map[string]string{}
	snmpProfilesCreated, snmpProfilesReused := 0, 0
	if importSNMP && len(neededProfiles) > 0 {
		ids, created, reused, snmpWarnings, err := ensureDudeSNMPProfiles(ctx, tx, preview.SNMPProfiles, neededProfiles)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		snmpProfileIDs = ids
		snmpProfilesCreated = created
		snmpProfilesReused = reused
		warnings = append(warnings, snmpWarnings...)
	}
	snmpAssignments := map[string]string{}
	for sourceID, profileID := range manualSNMP {
		snmpAssignments[sourceID] = profileID
	}
	for _, d := range preview.Devices {
		if !dudeDeviceSelected(selectedDevices, d.SourceID) || !dudeDeviceInSelectedMap(d, selectedMaps) {
			continue
		}
		if manualSNMP[d.SourceID] != "" {
			continue
		}
		if profileID := snmpProfileIDs[dudeDeviceSNMPProfileSourceID(d, preview.SNMPProfiles)]; profileID != "" {
			snmpAssignments[d.SourceID] = profileID
		}
	}
	var assignedProfileIDs []string
	seenAssignedProfiles := map[string]bool{}
	for _, profileID := range snmpAssignments {
		if !seenAssignedProfiles[profileID] {
			seenAssignedProfiles[profileID] = true
			assignedProfileIDs = append(assignedProfileIDs, profileID)
		}
	}
	if len(assignedProfileIDs) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM snmp_profiles WHERE id = ANY($1::uuid[])`, assignedProfileIDs).Scan(&count); err != nil {
			writeError(w, http.StatusBadRequest, "perfil SNMP no válido")
			return
		}
		if count != len(assignedProfileIDs) {
			writeError(w, http.StatusBadRequest, "uno o más perfiles SNMP ya no existen")
			return
		}
	}
	probeIDs := uniqueStrings(req.ProbeIDs)
	if len(probeIDs) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM probes WHERE id = ANY($1::uuid[])`, probeIDs).Scan(&count); err != nil {
			writeError(w, http.StatusBadRequest, "sonda no válida")
			return
		}
		if count != len(probeIDs) {
			writeError(w, http.StatusBadRequest, "una o más sondas ya no existen")
			return
		}
	}
	typeAssignments := map[string]string{}
	var typeNames []string
	seenTypes := map[string]bool{}
	for sourceID, typeName := range req.DeviceTypes {
		typeName = strings.TrimSpace(typeName)
		if typeName == "" || !selectedDevices[sourceID] {
			continue
		}
		typeAssignments[sourceID] = typeName
		if !seenTypes[typeName] {
			seenTypes[typeName] = true
			typeNames = append(typeNames, typeName)
		}
	}
	if len(typeNames) > 0 {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM device_types WHERE name = ANY($1::text[])`, typeNames).Scan(&count); err != nil {
			writeError(w, http.StatusBadRequest, "tipo de dispositivo no válido")
			return
		}
		if count != len(typeNames) {
			writeError(w, http.StatusBadRequest, "uno o más tipos de dispositivo ya no existen")
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

	createdDevices, reusedDevices, snmpAssigned, snmpSkipped, probesAdded, deviceTypesAssigned := 0, 0, 0, 0, 0, 0
	createdSubmaps := 0
	deviceIDs := map[string]string{}
	nodeElements := map[string]string{}
	skipped := 0
	order := 0
	for _, d := range preview.Devices {
		if !dudeDeviceSelected(selectedDevices, d.SourceID) {
			continue
		}
		if !dudeDeviceInSelectedMap(d, selectedMaps) {
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
		deviceType := d.DeviceType
		overrideType := typeAssignments[d.SourceID]
		if overrideType != "" {
			deviceType = overrideType
		}
		if deviceType == "" {
			deviceType = "generic"
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
			`, d.Name, ip, d.DNSName, strings.ToLower(d.MAC), d.Vendor, d.Model, deviceType, notes, string(custom)).Scan(&deviceID)
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
		if overrideType != "" {
			if _, err := tx.Exec(ctx, `UPDATE devices SET device_type=$2 WHERE id=$1`, deviceID, overrideType); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			deviceTypesAssigned++
		}
		if profileID := snmpAssignments[d.SourceID]; profileID != "" {
			manual := manualSNMP[d.SourceID] != ""
			var ct pgconn.CommandTag
			var err error
			if manual || overwriteCredentials {
				ct, err = tx.Exec(ctx, `UPDATE devices SET snmp_profile_id=$2 WHERE id=$1`, deviceID, profileID)
			} else {
				ct, err = tx.Exec(ctx, `UPDATE devices SET snmp_profile_id=$2 WHERE id=$1 AND snmp_profile_id IS NULL`, deviceID, profileID)
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if ct.RowsAffected() > 0 {
				snmpAssigned++
			} else {
				snmpSkipped++
			}
		}
		if len(probeIDs) > 0 {
			ct, err := tx.Exec(ctx, `
				INSERT INTO device_services (device_id, probe_id, custom_name,
					interval_sec, timeout_ms, down_retries, up_retries)
				SELECT $1, p.id, p.name, p.default_interval_sec, p.default_timeout_ms,
				       p.default_down_retries, p.default_up_retries
				FROM probes p
				WHERE p.id = ANY($2::uuid[])
				  AND NOT EXISTS (
				    SELECT 1 FROM device_services ds WHERE ds.device_id=$1 AND ds.probe_id=p.id
				  )
			`, deviceID, probeIDs)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			probesAdded += int(ct.RowsAffected())
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
		var elemID string
		err = tx.QueryRow(ctx, `
			INSERT INTO map_elements (map_id, element_type, device_id, pos_x, pos_y, width, height,
			 shape, icon_name, label_template)
			SELECT $1,'device',$2,$3,$4,$5,$6,$7,$8,m.default_device_label
			FROM maps m WHERE m.id=$1
			AND NOT EXISTS (SELECT 1 FROM map_elements WHERE map_id=$1 AND device_id=$2)
			RETURNING id
		`, targetMap, deviceID, x, y, width, height, shape, icon).Scan(&elemID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				SELECT id::text FROM map_elements
				WHERE map_id=$1 AND element_type='device' AND device_id=$2 LIMIT 1
			`, targetMap, deviceID).Scan(&elemID)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if d.NodeID != "" {
			nodeElements[d.NodeID] = elemID
		}
	}

	sourceParent := map[string]string{}
	for _, m := range preview.Maps {
		if m.ParentSourceID != "" {
			sourceParent[m.SourceID] = m.ParentSourceID
		}
	}
	for _, submap := range preview.Submaps {
		parentMapID := mapIDs[submap.MapID]
		targetMapID := mapIDs[submap.TargetSourceID]
		if parentMapID == "" || targetMapID == "" || parentMapID == targetMapID {
			continue
		}
		styleMap := map[string]any{"dude_source_id": submap.SourceID, "system": "dude_submap"}
		if sourceParent[submap.MapID] == submap.TargetSourceID {
			// The Dude places a link back to the parent map inside every child.
			// Mark it as the standard back element instead of duplicating it.
			styleMap["system"] = "back"
		}
		style, _ := json.Marshal(styleMap)
		width, height := submap.Width, submap.Height
		if width <= 0 {
			width = 190
		}
		if height <= 0 {
			height = 110
		}
		name := strings.TrimSpace(submap.Name)
		if name == "" {
			name = "Submapa Dude"
		}
		var elemID string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM map_elements
			WHERE map_id=$1 AND custom_style->>'dude_source_id'=$2 LIMIT 1
		`, parentMapID, submap.SourceID).Scan(&elemID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				INSERT INTO map_elements (map_id, element_type, target_submap_id, pos_x, pos_y, width, height,
				 shape, icon_name, show_icon, label_template, z_index, custom_style)
				VALUES ($1,'submap',$2,$3,$4,$5,$6,'rounded_rect','map',TRUE,$7,10,$8::jsonb)
				RETURNING id
			`, parentMapID, targetMapID, submap.PosX, submap.PosY, width, height, name, string(style)).Scan(&elemID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			createdSubmaps++
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		nodeElements[submap.SourceID] = elemID
	}
	for sourceID, parentSourceID := range sourceParent {
		childID := mapIDs[sourceID]
		parentID := mapIDs[parentSourceID]
		if childID == "" || parentID == "" {
			continue
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO map_elements (map_id, element_type, target_submap_id, pos_x, pos_y, width, height,
			 shape, icon_name, show_icon, label_template, z_index, custom_style)
			SELECT $1,'submap',$2,40,40,150,150,'circle','building',TRUE,'Volver [Submap.Name]',10,'{"system":"back"}'::jsonb
			WHERE NOT EXISTS (
				SELECT 1 FROM map_elements
				WHERE map_id=$1 AND element_type='submap' AND target_submap_id=$2
			)
		`, childID, parentID)
	}

	createdDecorations := 0
	decorationElements := map[string]string{}
	for _, decoration := range preview.Decorations {
		mapID := mapIDs[decoration.MapID]
		if mapID == "" {
			continue
		}
		name := strings.TrimSpace(decoration.Name)
		if name == "" {
			name = "Elemento Dude"
		}
		width, height := decoration.Width, decoration.Height
		if width <= 0 {
			width = 180
		}
		if height <= 0 {
			height = 60
		}
		style, _ := json.Marshal(map[string]any{"dude_source_id": decoration.SourceID, "system": "dude_decoration"})
		var elemID string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM map_elements
			WHERE map_id=$1 AND custom_style->>'dude_source_id'=$2 LIMIT 1
		`, mapID, decoration.SourceID).Scan(&elemID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				INSERT INTO map_elements (map_id, element_type, pos_x, pos_y, width, height,
					label_template, custom_style)
				VALUES ($1,'text_label',$2,$3,$4,$5,$6,$7::jsonb)
				RETURNING id
			`, mapID, decoration.PosX, decoration.PosY, width, height, name, string(style)).Scan(&elemID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			createdDecorations++
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		decorationElements[decoration.SourceID] = elemID
	}

	var credentialDevices []dudeCredentialDevice
	if importCredentials {
		for _, d := range preview.Devices {
			if !dudeDeviceSelected(selectedDevices, d.SourceID) {
				continue
			}
			deviceID := deviceIDs[d.SourceID]
			if deviceID == "" || strings.TrimSpace(d.Username) == "" {
				continue
			}
			credentialDevices = append(credentialDevices, dudeCredentialDevice{
				SourceID: d.SourceID, DeviceID: deviceID, DeviceName: d.Name,
				Username: d.Username, Password: d.Password,
			})
		}
	}
	createdCredentials, reusedCredentials, assignedCredentials, skippedCredentials := 0, 0, 0, 0
	if groups := groupDudeCredentials(credentialDevices); len(groups) > 0 {
		var credentialWarnings []string
		createdCredentials, reusedCredentials, assignedCredentials, skippedCredentials, credentialWarnings, err =
			ensureDudeRouterOSCredentials(ctx, tx, groups, overwriteCredentials, preview.Filename)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		warnings = append(warnings, credentialWarnings...)
	}

	createdLinks, skippedLinks, monitoredLinks, missingLinkMasters := 0, 0, 0, 0
	if createLinks {
		// Reuse only links between map elements that belong to this import.
		seenLinks := map[string]bool{}
		linkOrder := order
		for _, l := range preview.Links {
			mapID := mapIDs[l.SourceMapID]
			if mapID == "" {
				skippedLinks++
				continue
			}
			aElem := nodeElements[l.NodeA]
			if aElem == "" {
				aElem = decorationElements[l.NodeA]
			}
			if aElem == "" && l.DeviceA != "" {
				if aSource := deviceIDs[l.DeviceA]; aSource != "" {
					var err error
					aElem, err = ensureDudeDeviceElement(ctx, tx, mapID, aSource, linkOrder)
					if err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
					linkOrder++
				}
			}
			if aElem == "" {
				skippedLinks++
				continue
			}
			bElem := nodeElements[l.NodeB]
			if bElem == "" {
				bElem = decorationElements[l.NodeB]
			}
			if bElem == "" && l.DeviceB != "" {
				if bSource := deviceIDs[l.DeviceB]; bSource != "" {
					var err error
					bElem, err = ensureDudeDeviceElement(ctx, tx, mapID, bSource, linkOrder)
					if err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
					linkOrder++
				}
			}
			if bElem == "" || aElem == bElem {
				skippedLinks++
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
			mastering, masterDeviceID, ifIndex := dudeLinkMonitoring(l, deviceIDs, assignLinkMonitoring)
			var masterArg any
			if masterDeviceID != "" {
				masterArg = masterDeviceID
			}
			var ifArg any
			if ifIndex > 0 {
				ifArg = ifIndex
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO map_links (map_id, source_element_id, target_element_id, source_handle,
				 target_handle, mastering_type, master_device_id, snmp_if_index, link_type, max_speed_bps, label_template)
				VALUES ($1,$2,$3,'center','center',$4,$5,$6,$7,$8,'')
			`, mapID, aElem, bElem, mastering, masterArg, ifArg, l.LinkType, speed); err == nil {
				createdLinks++
				if mastering == "snmp" {
					monitoredLinks++
				}
				if assignLinkMonitoring && l.MasterInterface > 0 && l.MasterDevice != "" && deviceIDs[l.MasterDevice] == "" {
					missingLinkMasters++
				}
			}
		}
	}
	if missingLinkMasters > 0 {
		warnings = append(warnings, fmt.Sprintf("%d enlaces tenían interfaz maestra pero su dispositivo no se importó; se crearon como enlaces simples.", missingLinkMasters))
	}
	if skippedLinks > 0 {
		warnings = append(warnings, fmt.Sprintf("%d enlaces no se pudieron crear porque uno de sus extremos no es un dispositivo ni un elemento decorativo importable.", skippedLinks))
	}
	result, _ := json.Marshal(map[string]any{
		"created_maps": createdMaps, "created_devices": createdDevices, "reused_devices": reusedDevices,
		"created_decorations": createdDecorations, "created_submaps": createdSubmaps,
		"created_links": createdLinks, "skipped_links": skippedLinks, "links_monitored": monitoredLinks,
		"snmp_profiles_created": snmpProfilesCreated, "snmp_profiles_reused": snmpProfilesReused,
		"snmp_assigned": snmpAssigned, "snmp_skipped": snmpSkipped,
		"routeros_credentials_created": createdCredentials, "routeros_credentials_reused": reusedCredentials,
		"routeros_credentials_assigned": assignedCredentials, "routeros_credentials_skipped": skippedCredentials,
		"probes_added": probesAdded, "device_types_assigned": deviceTypesAssigned,
		"skipped_devices": skipped, "warnings": warnings,
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
		"created_links": createdLinks, "skipped_links": skippedLinks, "links_monitored": monitoredLinks,
		"created_decorations": createdDecorations, "created_submaps": createdSubmaps,
		"snmp_profiles_created": snmpProfilesCreated, "snmp_profiles_reused": snmpProfilesReused,
		"snmp_assigned": snmpAssigned, "snmp_skipped": snmpSkipped,
		"routeros_credentials_created": createdCredentials, "routeros_credentials_reused": reusedCredentials,
		"routeros_credentials_assigned": assignedCredentials, "routeros_credentials_skipped": skippedCredentials,
		"probes_added": probesAdded, "device_types_assigned": deviceTypesAssigned,
		"skipped_devices": skipped, "warnings": warnings,
		"map_ids": mapIDs, "by": uid.Username,
	}})
}

func dudeLinkMonitoring(link dude.Link, deviceIDs map[string]string, enabled bool) (mastering, masterDeviceID string, ifIndex int) {
	mastering = "simple"
	if !enabled || link.MasterInterface <= 0 || int64(link.MasterInterface) >= 0xffffffff || link.MasterDevice == "" {
		return
	}
	deviceID := deviceIDs[link.MasterDevice]
	if deviceID == "" {
		return
	}
	return "snmp", deviceID, link.MasterInterface
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

// ensureDudeDeviceElement returns the device element on a map. If the device
// is on another map (Dude allows the same device on several maps), it creates
// the missing element so topology links on this map can be imported too.
func ensureDudeDeviceElement(ctx context.Context, tx pgx.Tx, mapID, deviceID string, index int) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM map_elements WHERE map_id=$1 AND device_id=$2 LIMIT 1
	`, mapID, deviceID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	x := float64(80 + (index%8)*190)
	y := float64(80 + (index/8)*140)
	err = tx.QueryRow(ctx, `
		INSERT INTO map_elements (map_id, element_type, device_id, pos_x, pos_y, width, height,
			shape, icon_name, label_template)
		VALUES ($1,'device',$2,$3,$4,180,88,'rounded_rect','server',$5)
		RETURNING id
	`, mapID, deviceID, x, y, "[Device.Name]\n[Device.FirstAddress]").Scan(&id)
	return id, err
}
