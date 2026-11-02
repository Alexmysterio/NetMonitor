package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/dsl"
)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "map"
	}
	return s
}

func statusText(s string) string {
	switch s {
	case "up":
		return "Operativo"
	case "warning":
		return "Advertencia"
	case "down":
		return "Caído"
	case "unreachable":
		return "Inalcanzable"
	case "acknowledged":
		return "Reconocido"
	case "disabled":
		return "Deshabilitado"
	default:
		return "Desconocido"
	}
}

func worstStatus(statuses []string) string {
	rank := map[string]int{"down": 6, "unreachable": 5, "warning": 4, "unknown": 3, "disabled": 2, "up": 1}
	best := "unknown"
	for _, s := range statuses {
		if rank[s] > rank[best] {
			best = s
		}
	}
	return best
}

type mapCounts struct {
	Up          int `json:"up"`
	Warning     int `json:"warning"`
	Down        int `json:"down"`
	Unreachable int `json:"unreachable"`
	Disabled    int `json:"disabled"`
	Unknown     int `json:"unknown"`
}

func (c mapCounts) Status() string {
	switch {
	case c.Down > 0:
		return "down"
	case c.Unreachable > 0:
		return "unreachable"
	case c.Warning > 0:
		return "warning"
	case c.Up > 0 && c.Unknown == 0:
		return "up"
	default:
		return "unknown"
	}
}

func (s *Server) listMaps(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	allowedDevicesSet, restrictedGroups := s.allowedDeviceIDs(r.Context(), user)
	var allowedDevices []string
	if restrictedGroups {
		for id := range allowedDevicesSet {
			allowedDevices = append(allowedDevices, id)
		}
	}
	rows, err := s.pool.Query(r.Context(), `
		WITH RECURSIVE tree AS (
			SELECT id, id AS root_id FROM maps
			UNION ALL
			SELECT child.id, tree.root_id FROM maps child JOIN tree ON child.parent_map_id=tree.id
		)
		SELECT m.id, COALESCE(m.parent_map_id::text,''), m.name, m.slug, m.description,
		       m.grid_enabled, m.grid_size, m.snap_to_grid, m.background_color,
		       COALESCE(c.up,0), COALESCE(c.warning,0), COALESCE(c.down,0),
		       COALESCE(c.unreachable,0), COALESCE(c.disabled,0), COALESCE(c.unknown,0)
		FROM maps m
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE status='up') AS up,
			       count(*) FILTER (WHERE status='warning') AS warning,
			       count(*) FILTER (WHERE status IN ('down','acknowledged')) AS down,
			       count(*) FILTER (WHERE status='unreachable') AS unreachable,
			       count(*) FILTER (WHERE status='disabled') AS disabled,
			       count(*) FILTER (WHERE status='unknown') AS unknown
			FROM (
				SELECT DISTINCT d.id, d.status
				FROM map_elements me JOIN devices d ON d.id=me.device_id
				WHERE me.map_id IN (SELECT id FROM tree WHERE root_id=m.id)
				  AND (NOT $1 OR d.id = ANY($2::uuid[]))
			) unique_devices
		) c ON TRUE
		ORDER BY m.created_at, m.name
	`, restrictedGroups, allowedDevices)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	allowed, restricted := s.allowedMapIDs(r.Context(), user)
	out := []map[string]any{}
	for rows.Next() {
		var id, parent, name, slug, desc, bg string
		var grid bool
		var gridSize int
		var snap bool
		var c mapCounts
		if err := rows.Scan(&id, &parent, &name, &slug, &desc, &grid, &gridSize, &snap, &bg,
			&c.Up, &c.Warning, &c.Down, &c.Unreachable, &c.Disabled, &c.Unknown); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if restricted && !allowed[id] {
			continue
		}
		out = append(out, map[string]any{
			"id": id, "parent_map_id": parent, "name": name, "slug": slug, "description": desc,
			"grid_enabled": grid, "grid_size": gridSize, "snap_to_grid": snap, "background_color": bg,
			"counts": c, "status": c.Status(), "status_text": statusText(c.Status()),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type createMapRequest struct {
	Name            string `json:"name"`
	ParentMapID     string `json:"parent_map_id"`
	Description     string `json:"description"`
	BackgroundColor string `json:"background_color"`
	GridEnabled     *bool  `json:"grid_enabled"`
	DefaultLabel    string `json:"default_device_label"`
}

func (s *Server) createMap(w http.ResponseWriter, r *http.Request) {
	var req createMapRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	slug := slugify(req.Name)
	var parent any
	if req.ParentMapID != "" {
		parent = req.ParentMapID
	}
	var id, finalSlug string
	for i := 0; i < 50; i++ {
		try := slug
		if i > 0 {
			try = fmt.Sprintf("%s-%d", slug, i)
		}
		err := s.pool.QueryRow(r.Context(), `
			INSERT INTO maps (name, slug, parent_map_id, description, background_color)
			VALUES ($1,$2,$3,$4,COALESCE(NULLIF($5,''),'#FFFFFF'))
			RETURNING id, slug
		`, req.Name, try, parent, req.Description, req.BackgroundColor).Scan(&id, &finalSlug)
		if err == nil {
			if user, ok := auth.UserFrom(r.Context()); ok && user.RestrictMaps && user.Role != "admin" {
				_, _ = s.pool.Exec(r.Context(), `INSERT INTO map_permissions (user_id,map_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, user.ID, id)
			}
			if parent != nil {
				if parentID, ok := parent.(string); ok {
					s.ensureBackElement(r.Context(), id, parentID)
				}
			}
			writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id, "slug": finalSlug, "name": req.Name}})
			return
		}
		if !strings.Contains(err.Error(), "duplicate") {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeError(w, http.StatusConflict, "could not allocate a unique slug")
}

func (s *Server) getMap(w http.ResponseWriter, r *http.Request) {
	var id, parent, name, slug, desc, bg, label, shape, align, bgImage, pollAgent string
	var bgOpacity float64
	var pollInterval, pollRetries, pollNotify int
	var grid, snap bool
	var size int
	var viewport []byte
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, COALESCE(parent_map_id::text,''), name, slug, description, grid_enabled, grid_size,
		       snap_to_grid, background_color, default_device_label, default_device_shape, label_align,
		       background_image, background_opacity, viewport,
		       COALESCE(poll_interval_sec,0), COALESCE(poll_down_retries,0), COALESCE(poll_agent_id::text,''),
		       COALESCE(poll_notifications_enabled::int,-1)
		FROM maps WHERE id=$1
	`, chi.URLParam(r, "id")).Scan(&id, &parent, &name, &slug, &desc, &grid, &size, &snap, &bg, &label, &shape, &align, &bgImage, &bgOpacity, &viewport,
		&pollInterval, &pollRetries, &pollAgent, &pollNotify)
	if err != nil {
		writeError(w, http.StatusNotFound, "map not found")
		return
	}
	var vp any
	_ = json.Unmarshal(viewport, &vp)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "parent_map_id": parent, "name": name, "slug": slug, "description": desc,
		"grid_enabled": grid, "grid_size": size, "snap_to_grid": snap, "background_color": bg,
		"default_device_label": label, "default_device_shape": shape, "label_align": align,
		"background_image": bgImage, "background_opacity": bgOpacity, "viewport": vp,
		"poll_interval_sec": pollInterval, "poll_down_retries": pollRetries,
		"poll_agent_id": pollAgent, "poll_notifications": pollNotify,
	}})
}

type updateMapRequest struct {
	Name              *string  `json:"name"`
	Description       *string  `json:"description"`
	GridEnabled       *bool    `json:"grid_enabled"`
	GridSize          *int     `json:"grid_size"`
	SnapToGrid        *bool    `json:"snap_to_grid"`
	BackgroundColor   *string  `json:"background_color"`
	DefaultLabel      *string  `json:"default_device_label"`
	DefaultShape      *string  `json:"default_device_shape"`
	LabelAlign        *string  `json:"label_align"`
	BackgroundImage   *string  `json:"background_image"`
	BackgroundOpacity *float64 `json:"background_opacity"`
	PollInterval      *int     `json:"poll_interval_sec"`
	PollRetries       *int     `json:"poll_down_retries"`
	PollAgentID       *string  `json:"poll_agent_id"`
	PollNotifications *int     `json:"poll_notifications"`
	Viewport          any      `json:"viewport"`
}

func (s *Server) updateMap(w http.ResponseWriter, r *http.Request) {
	var req updateMapRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.LabelAlign != nil {
		switch *req.LabelAlign {
		case "left", "center", "right":
		default:
			writeError(w, http.StatusBadRequest, "label_align debe ser left, center o right")
			return
		}
	}
	vp, _ := json.Marshal(req.Viewport)
	_, err := s.pool.Exec(r.Context(), `
		UPDATE maps SET
		 name=COALESCE($2,name), description=COALESCE($3,description),
		 grid_enabled=COALESCE($4,grid_enabled), grid_size=COALESCE($5,grid_size),
		 snap_to_grid=COALESCE($6,snap_to_grid), background_color=COALESCE($7,background_color),
		 default_device_label=COALESCE($8,default_device_label),
		 default_device_shape=COALESCE($10,default_device_shape),
		 label_align=COALESCE($11,label_align),
		 background_image=COALESCE($12,background_image),
		 background_opacity=COALESCE($13,background_opacity),
		 poll_interval_sec=CASE WHEN $14::int IN (0,-1) THEN NULL WHEN $14::int > 0 THEN $14 ELSE poll_interval_sec END,
		 poll_down_retries=CASE WHEN $15::int IN (0,-1) THEN NULL WHEN $15::int > 0 THEN $15 ELSE poll_down_retries END,
		 poll_agent_id=CASE WHEN $16::text IS NULL THEN poll_agent_id WHEN $16::text='' THEN NULL ELSE $16::uuid END,
		 poll_notifications_enabled=CASE WHEN $17::int = -1 THEN NULL WHEN $17::int = 0 THEN FALSE WHEN $17::int = 1 THEN TRUE ELSE poll_notifications_enabled END,
		 viewport=CASE WHEN $9::text='null' THEN viewport ELSE $9::jsonb END,
		 updated_at=NOW()
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Name, req.Description, req.GridEnabled, req.GridSize,
		req.SnapToGrid, req.BackgroundColor, req.DefaultLabel, string(vp), req.DefaultShape, req.LabelAlign,
		req.BackgroundImage, req.BackgroundOpacity, req.PollInterval, req.PollRetries, req.PollAgentID, req.PollNotifications)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) uploadMapBackground(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "imagen demasiado grande o formulario inválido")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "falta el archivo")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		writeError(w, http.StatusBadRequest, "usa PNG, JPG, JPEG o WebP")
		return
	}
	dir := os.Getenv("MAP_BACKGROUND_DIR")
	if dir == "" {
		dir = "/app/uploads/map-backgrounds"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("map-%s-%d%s", slugify(chi.URLParam(r, "id")), time.Now().UnixNano(), ext)
	dst, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dst.Close()
	url := "/map-backgrounds/" + name
	tag, err := s.pool.Exec(r.Context(), `UPDATE maps SET background_image=$2, updated_at=NOW() WHERE id=$1`, chi.URLParam(r, "id"), url)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "mapa no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"background_image": url}})
}

func (s *Server) deleteMap(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `DELETE FROM maps WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

type layoutNode struct {
	ID       string  `json:"id"`
	PosX     float64 `json:"pos_x"`
	PosY     float64 `json:"pos_y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	Rotation float64 `json:"rotation"`
	ZoneID   string  `json:"zone_id"`
	ZIndex   int     `json:"z_index"`
}

type layoutRequest struct {
	Nodes    []layoutNode `json:"nodes"`
	Viewport any          `json:"viewport"`
}

func (s *Server) saveLayout(w http.ResponseWriter, r *http.Request) {
	var req layoutRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	for _, n := range req.Nodes {
		_, err := tx.Exec(ctx, `
			UPDATE map_elements SET pos_x=$2, pos_y=$3, width=$4, height=$5,
			 rotation=$6, z_index=$7, zone_id=NULLIF($8,'')::uuid, updated_at=NOW()
			WHERE id=$1 AND map_id=$9
		`, n.ID, n.PosX, n.PosY, n.Width, n.Height, n.Rotation, n.ZIndex, n.ZoneID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.Viewport != nil {
		vp, _ := json.Marshal(req.Viewport)
		_, _ = tx.Exec(ctx, `UPDATE maps SET viewport=$2, updated_at=NOW() WHERE id=$1`, chi.URLParam(r, "id"), string(vp))
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "saved": len(req.Nodes)})
}

// ─── Canvas ─────────────────────────────────────────────────────────────────

type labelVars struct {
	DeviceName   string
	DeviceIP     string
	DeviceRTT    string
	DeviceLoss   string
	DeviceStatus string
	DeviceCustom map[string]any
	SubmapName   string
	SubmapStatus string
	Counts       mapCounts
}

func (s *Server) getCanvas(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mapID := chi.URLParam(r, "id")
	var name, parent, desc, bg, defaultLabel, defaultShape, labelAlign, bgImage, pollAgent string
	var bgOpacity float64
	var pollInterval, pollRetries, pollNotify int
	var grid, snap bool
	var gridSize int
	var viewport []byte
	err := s.pool.QueryRow(ctx, `
		SELECT name, COALESCE(parent_map_id::text,''), description, grid_enabled, grid_size,
		       snap_to_grid, background_color, default_device_label, default_device_shape, label_align,
		       background_image, background_opacity, viewport,
		       COALESCE(poll_interval_sec,0), COALESCE(poll_down_retries,0),
		       COALESCE(poll_agent_id::text,''), COALESCE(poll_notifications_enabled::int,-1)
		FROM maps WHERE id=$1
	`, mapID).Scan(&name, &parent, &desc, &grid, &gridSize, &snap, &bg, &defaultLabel, &defaultShape, &labelAlign, &bgImage, &bgOpacity, &viewport,
		&pollInterval, &pollRetries, &pollAgent, &pollNotify)
	if err != nil {
		writeError(w, http.StatusNotFound, "map not found")
		return
	}
	var vp any
	_ = json.Unmarshal(viewport, &vp)

	user, _ := auth.UserFrom(ctx)
	allowedSet, restrictedGroups := s.allowedDeviceIDs(ctx, user)
	var allowedDevices []string
	if restrictedGroups {
		for id := range allowedSet {
			allowedDevices = append(allowedDevices, id)
		}
	}
	funcs := s.functionMap(ctx)
	summary := s.computeMapSummary(ctx, mapID, allowedDevices, restrictedGroups)
	elements := s.loadElements(ctx, mapID, funcs, allowedDevices, restrictedGroups)
	links := visibleLinks(s.loadLinks(ctx, mapID), elements)
	breadcrumbs := s.breadcrumbs(ctx, mapID)
	if user.Role != "admin" && user.RestrictMaps {
		allowedMaps, _ := s.allowedMapIDs(ctx, user)
		filtered := breadcrumbs[:0]
		for _, crumb := range breadcrumbs {
			if allowedMaps[crumb["id"]] {
				filtered = append(filtered, crumb)
			}
		}
		breadcrumbs = filtered
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"map": map[string]any{
			"id": mapID, "name": name, "parent_map_id": parent, "description": desc,
			"grid_enabled": grid, "grid_size": gridSize, "snap_to_grid": snap,
			"background_color": bg, "default_device_label": defaultLabel,
			"default_device_shape": defaultShape, "label_align": labelAlign,
			"background_image": bgImage, "background_opacity": bgOpacity, "viewport": vp,
			"poll_interval_sec": pollInterval, "poll_down_retries": pollRetries,
			"poll_agent_id": pollAgent, "poll_notifications": pollNotify,
		},
		"elements": elements, "links": links,
		"summary": map[string]any{
			"up": summary.Up, "warning": summary.Warning, "down": summary.Down,
			"unreachable": summary.Unreachable, "disabled": summary.Disabled,
			"unknown": summary.Unknown, "status": summary.Status(),
		},
		"breadcrumbs": breadcrumbs,
	}})
}

func (s *Server) functionMap(ctx context.Context) map[string]string {
	rows, err := s.pool.Query(ctx, `SELECT name, expression_code FROM functions`)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, c string
		if rows.Scan(&n, &c) == nil {
			out[n] = c
		}
	}
	return out
}

func (s *Server) computeMapSummary(ctx context.Context, mapID string, allowedDevices []string, restricted bool) mapCounts {
	var c mapCounts
	_ = s.pool.QueryRow(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id, id AS root_id FROM maps WHERE id=$1
			UNION ALL
			SELECT child.id, tree.root_id FROM maps child JOIN tree ON child.parent_map_id=tree.id
		)
		SELECT count(*) FILTER (WHERE status='up'),
		       count(*) FILTER (WHERE status='warning'),
		       count(*) FILTER (WHERE status IN ('down','acknowledged')),
		       count(*) FILTER (WHERE status='unreachable'),
		       count(*) FILTER (WHERE status='disabled'),
		       count(*) FILTER (WHERE status='unknown')
		FROM (
			SELECT DISTINCT d.id, d.status
			FROM map_elements me JOIN devices d ON d.id=me.device_id
			WHERE me.map_id IN (SELECT id FROM tree)
			  AND (NOT $2 OR d.id = ANY($3::uuid[]))
		) unique_devices
	`, mapID, restricted, allowedDevices).Scan(&c.Up, &c.Warning, &c.Down, &c.Unreachable, &c.Disabled, &c.Unknown)
	return c
}

func (s *Server) loadElements(ctx context.Context, mapID string, funcs map[string]string, allowedDevices []string, restricted bool) []map[string]any {
	rows, err := s.pool.Query(ctx, `
		SELECT me.id, me.element_type, COALESCE(me.device_id::text,''), COALESCE(me.target_submap_id::text,''),
		       COALESCE(me.network_cidr::text,''),
		       me.pos_x, me.pos_y, me.width, me.height, me.rotation, COALESCE(me.zone_id::text,''), me.shape, me.icon_name, me.show_icon,
		       me.label_template, me.label_position, me.z_index, me.is_locked,
		       COALESCE(me.custom_style::text,'{}'),
		       COALESCE(d.name,''), COALESCE(host(d.primary_ip),''),
		       COALESCE(ARRAY(SELECT host(x) FROM unnest(d.secondary_ips) x),'{}'), COALESCE(d.status,''),
		       COALESCE(d.status_reason,''), COALESCE(d.is_acknowledged,false),
		       COALESCE(d.root_cause_device_id::text,''), COALESCE(rc.name,''),
		       COALESCE(d.tags,'{}'), COALESCE(d.custom_fields::text,'{}'),
		       COALESCE(tm.name,''), COALESCE(tm.id::text,''),
		       COALESCE(c.up,0), COALESCE(c.warning,0), COALESCE(c.down,0),
		       COALESCE(c.unreachable,0), COALESCE(c.disabled,0), COALESCE(c.unknown,0),
		       COALESCE(n.up,0), COALESCE(n.warning,0), COALESCE(n.down,0),
		       COALESCE(n.unreachable,0), COALESCE(n.disabled,0), COALESCE(n.unknown,0)
		FROM map_elements me
		LEFT JOIN devices d ON d.id=me.device_id
		LEFT JOIN devices rc ON rc.id=d.root_cause_device_id
		LEFT JOIN maps tm ON tm.id=me.target_submap_id
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE d2.status='up') AS up,
			       count(*) FILTER (WHERE d2.status='warning') AS warning,
			       count(*) FILTER (WHERE d2.status IN ('down','acknowledged')) AS down,
			       count(*) FILTER (WHERE d2.status='unreachable') AS unreachable,
			       count(*) FILTER (WHERE d2.status='disabled') AS disabled,
			       count(*) FILTER (WHERE d2.status='unknown') AS unknown
			FROM map_elements me2 JOIN devices d2 ON d2.id=me2.device_id
			WHERE me2.map_id=me.target_submap_id
			  AND (NOT $2 OR d2.id = ANY($3::uuid[]))
		) c ON TRUE
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE d3.status='up') AS up,
			       count(*) FILTER (WHERE d3.status='warning') AS warning,
			       count(*) FILTER (WHERE d3.status IN ('down','acknowledged')) AS down,
			       count(*) FILTER (WHERE d3.status='unreachable') AS unreachable,
			       count(*) FILTER (WHERE d3.status='disabled') AS disabled,
			       count(*) FILTER (WHERE d3.status='unknown') AS unknown
			FROM devices d3
			WHERE me.network_cidr IS NOT NULL AND d3.primary_ip << me.network_cidr
			  AND (NOT $2 OR d3.id = ANY($3::uuid[]))
		) n ON TRUE
		WHERE me.map_id=$1
		  AND (NOT $2 OR me.device_id IS NULL OR me.device_id = ANY($3::uuid[]))
		ORDER BY me.z_index, me.created_at
	`, mapID, restricted, allowedDevices)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, typ, deviceID, targetID, networkCIDR, zoneID, shape, icon, label, labelPos, style string
		var posX, posY, width, height, rotation float64
		var show, locked bool
		var zIndex int
		var devName, devIP, devStatus, devReason, submapName, submapID string
		var rootCauseID, rootCauseName string
		var ack bool
		var tags, devSecondary []string
		var customJSON string
		var c, nc mapCounts
		if err := rows.Scan(&id, &typ, &deviceID, &targetID, &networkCIDR, &posX, &posY, &width, &height, &rotation, &zoneID,
			&shape, &icon, &show, &label, &labelPos, &zIndex, &locked, &style,
			&devName, &devIP, &devSecondary, &devStatus, &devReason, &ack, &rootCauseID, &rootCauseName,
			&tags, &customJSON,
			&submapName, &submapID,
			&c.Up, &c.Warning, &c.Down, &c.Unreachable, &c.Disabled, &c.Unknown,
			&nc.Up, &nc.Warning, &nc.Down, &nc.Unreachable, &nc.Disabled, &nc.Unknown); err != nil {
			continue
		}
		var custom map[string]any
		_ = json.Unmarshal([]byte(customJSON), &custom)
		if custom == nil {
			custom = map[string]any{}
		}
		status := devStatus
		if typ == "submap" {
			status = c.Status()
		}
		if typ == "network" {
			status = nc.Status()
		}
		rendered := s.renderElementLabel(ctx, label, devName, devIP, devSecondary, devStatus, deviceID, submapName, networkCIDR, status, c, nc, custom, funcs)
		var devObj any
		if deviceID != "" {
			st := s.store.GetDevice(ctx, deviceID)
			rtt, _ := strconv.ParseFloat(st["rtt_ms"], 64)
			loss, _ := strconv.ParseFloat(st["packet_loss"], 64)
			devObj = map[string]any{
				"id": deviceID, "name": devName, "primary_ip": devIP, "secondary_ips": devSecondary, "status": devStatus,
				"status_reason": devReason, "is_acknowledged": ack, "tags": tags,
				"custom_fields": custom, "rtt_ms": rtt, "packet_loss_pct": loss,
				"root_cause_device_id": rootCauseID, "root_cause": rootCauseName,
				"services_summary": s.serviceSummary(ctx, deviceID),
			}
		}
		var submapObj any
		if targetID != "" {
			submapObj = map[string]any{"id": targetID, "name": submapName, "status": c.Status(),
				"counts": c, "status_text": statusText(c.Status())}
		}
		var networkObj any
		if typ == "network" {
			networkObj = map[string]any{"cidr": networkCIDR, "status": nc.Status(),
				"counts": nc, "status_text": statusText(nc.Status())}
		}
		var elementStyle any
		_ = json.Unmarshal([]byte(style), &elementStyle)
		out = append(out, map[string]any{
			"id": id, "element_type": typ, "device_id": deviceID, "target_submap_id": targetID,
			"network_cidr": networkCIDR,
			"pos_x":        posX, "pos_y": posY, "width": width, "height": height, "rotation": rotation, "zone_id": zoneID,
			"shape": shape, "icon_name": icon, "show_icon": show, "label_template": label,
			"label_position": labelPos, "z_index": zIndex, "is_locked": locked,
			"custom_style": elementStyle, "status": status, "status_text": statusText(status),
			"rendered_label": rendered, "device": devObj, "submap": submapObj, "network": networkObj,
		})
	}
	return out
}

func (s *Server) renderElementLabel(ctx context.Context, template, devName, devIP string, devSecondary []string, devStatus, deviceID, submapName, networkCIDR, status string, c, nc mapCounts, custom map[string]any, funcs map[string]string) string {
	if template == "" {
		template = "[Device.Name]\n[Device.FirstAddress]"
	}
	allAddresses := devIP
	if len(devSecondary) > 0 {
		allAddresses = strings.Join(append([]string{devIP}, devSecondary...), ", ")
	}
	vars := map[string]string{
		"Device.Name": devName, "Device.FirstAddress": devIP,
		"Device.SecondaryAddresses": strings.Join(devSecondary, ", "), "Device.Addresses": allAddresses,
		"Device.Status": devStatus, "Device.StatusText": statusText(devStatus),
		"Device.RTT": "—", "Device.PacketLoss": "0%", "Device.Uptime": "—", "Device.ServicesDown": "0",
		"Submap.Name": submapName, "Submap.Status": status, "Submap.StatusText": statusText(status),
		"Submap.DevicesUp": strconv.Itoa(c.Up), "Submap.DevicesWarning": strconv.Itoa(c.Warning),
		"Submap.DevicesDown": strconv.Itoa(c.Down), "Submap.DevicesUnreachable": strconv.Itoa(c.Unreachable),
		"Submap.Summary": fmt.Sprintf("%d up · %d warn · %d down", c.Up, c.Warning, c.Down),
		"Network.CIDR":   networkCIDR, "Network.Status": status, "Network.StatusText": statusText(status),
		"Network.DevicesUp": strconv.Itoa(nc.Up), "Network.DevicesWarning": strconv.Itoa(nc.Warning),
		"Network.DevicesDown": strconv.Itoa(nc.Down), "Network.DevicesUnreachable": strconv.Itoa(nc.Unreachable),
		"Network.DevicesTotal": strconv.Itoa(nc.Up + nc.Warning + nc.Down + nc.Unreachable + nc.Disabled + nc.Unknown),
		"Network.Summary":      fmt.Sprintf("%d up · %d warn · %d down", nc.Up, nc.Warning, nc.Down),
	}
	for k, v := range custom {
		vars["Device.Custom."+k] = fmt.Sprint(v)
	}
	if deviceID != "" {
		st := s.store.GetDevice(ctx, deviceID)
		if st["rtt_ms"] != "" {
			vars["Device.RTT"] = st["rtt_ms"]
		}
		if st["packet_loss"] != "" {
			vars["Device.PacketLoss"] = st["packet_loss"] + "%"
		}
		if up, ok := vars["Device.Uptime"]; ok && up == "" {
			vars["Device.Uptime"] = "—"
		}
	}
	oidMap := map[string]any{}
	cols := map[string][]float64{}
	rates := map[string]float64{}
	if deviceID != "" {
		oidMap = s.store.GetOIDs(ctx, deviceID)
		cols = s.store.GetColumns(ctx, deviceID)
		rates = s.store.GetRates(ctx, deviceID)
	}
	dslCtx := &dsl.Context{OIDs: oidMap, Columns: cols, Rates: rates, Vars: vars}
	res := dsl.RenderLabel(template, dslCtx, funcs)
	if res == "" {
		res = devName + "\n" + devIP
	}
	return res
}

func (s *Server) serviceSummary(ctx context.Context, deviceID string) map[string]int {
	out := map[string]int{"up": 0, "warning": 0, "down": 0}
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM device_services WHERE device_id=$1 AND is_enabled=TRUE GROUP BY status`, deviceID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			out[st] = n
		}
	}
	return out
}

func visibleLinks(links, elements []map[string]any) []map[string]any {
	visible := map[string]bool{}
	for _, e := range elements {
		if id, ok := e["id"].(string); ok {
			visible[id] = true
		}
	}
	out := make([]map[string]any, 0, len(links))
	for _, l := range links {
		src, _ := l["source_element_id"].(string)
		tgt, _ := l["target_element_id"].(string)
		if visible[src] && visible[tgt] {
			out = append(out, l)
		}
	}
	return out
}

func (s *Server) loadLinks(ctx context.Context, mapID string) []map[string]any {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.source_element_id, l.target_element_id, COALESCE(l.source_handle,''), COALESCE(l.target_handle,''),
		       l.mastering_type, COALESCE(l.master_device_id::text,''), COALESCE(l.snmp_if_index,0),
		       COALESCE(l.snmp_if_name,''), l.link_type, l.max_speed_bps, l.warning_utilization_pct,
		       l.critical_utilization_pct, l.routing_style, l.thickness, l.dashed,
		       COALESCE(l.label_template,''), l.status, l.color_by_traffic,
		       COALESCE(lm.rx_bps,0), COALESCE(lm.tx_bps,0), COALESCE(lm.oper_status,1)
		FROM map_links l
		LEFT JOIN LATERAL (
			SELECT rx_bps, tx_bps, oper_status FROM link_metrics WHERE link_id=l.id ORDER BY time DESC LIMIT 1
		) lm ON TRUE
		WHERE l.map_id=$1
	`, mapID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, src, tgt, sh, th, mastering, masterID, ifName, linkType, routing, label, status string
		var deviceID string
		var ifIndex int
		var maxSpeed int64
		var warnPct, critPct, thickness float64
		var dashed, colorByTraffic bool
		var rx, tx int64
		var oper int
		if err := rows.Scan(&id, &src, &tgt, &sh, &th, &mastering, &masterID, &ifIndex, &ifName,
			&linkType, &maxSpeed, &warnPct, &critPct, &routing, &thickness, &dashed, &label, &status, &colorByTraffic,
			&rx, &tx, &oper); err != nil {
			continue
		}
		deviceID = masterID
		st := s.store.GetLink(ctx, id)
		util := 0.0
		if maxSpeed > 0 {
			util = float64(max(rx, tx)) / float64(maxSpeed) * 100
		}
		if st["rx_bps"] != "" {
			rx, _ = strconv.ParseInt(st["rx_bps"], 10, 64)
		}
		if st["tx_bps"] != "" {
			tx, _ = strconv.ParseInt(st["tx_bps"], 10, 64)
		}
		if st["utilization_pct"] != "" {
			util, _ = strconv.ParseFloat(st["utilization_pct"], 64)
		}
		if st["status"] != "" {
			status = st["status"]
		}
		level := "normal"
		if status == "down" {
			level = "down"
		} else if util >= critPct {
			level = "critical"
		} else if util >= warnPct {
			level = "warning"
		}
		lctx := &dsl.Context{OIDs: map[string]any{}, Columns: map[string][]float64{}, Rates: map[string]float64{}, Vars: map[string]string{
			"Link.IfName": ifName, "Link.TxRate": formatBps(float64(tx)), "Link.RxRate": formatBps(float64(rx)),
			"Link.UtilizationPct": fmt.Sprintf("%.0f", util), "Link.Speed": formatBps(float64(maxSpeed)),
			"Link.Status": status, "Link.StatusText": statusText(status),
		}}
		rendered := ""
		if label != "" {
			rendered = dsl.RenderLabel(label, lctx, nil)
		} else {
			rendered = fmt.Sprintf("⬆ %s · ⬇ %s", formatBps(float64(tx)), formatBps(float64(rx)))
		}
		out = append(out, map[string]any{
			"id": id, "map_id": mapID, "source_element_id": src, "target_element_id": tgt,
			"source_handle": sh, "target_handle": th, "mastering_type": mastering,
			"master_device_id": deviceID, "snmp_if_index": ifIndex, "snmp_if_name": ifName,
			"link_type": linkType, "max_speed_bps": maxSpeed, "warning_utilization_pct": warnPct,
			"critical_utilization_pct": critPct, "routing_style": routing, "thickness": thickness,
			"dashed": dashed, "label_template": label, "status": status, "color_by_traffic": colorByTraffic,
			"rx_bps": rx, "tx_bps": tx, "utilization_pct": util, "utilization_level": level,
			"rendered_label": rendered, "oper_status": oper,
		})
	}
	return out
}

func (s *Server) breadcrumbs(ctx context.Context, mapID string) []map[string]string {
	var out []map[string]string
	current := mapID
	for i := 0; i < 12 && current != ""; i++ {
		var id, name, parent string
		if err := s.pool.QueryRow(ctx, `SELECT id, name, COALESCE(parent_map_id::text,'') FROM maps WHERE id=$1`, current).Scan(&id, &name, &parent); err != nil {
			break
		}
		out = append([]map[string]string{{"id": id, "name": name}}, out...)
		current = parent
	}
	return out
}

// ─── Elements / Links CRUD ──────────────────────────────────────────────────

type createElementRequest struct {
	ElementType   string  `json:"element_type"`
	DeviceID      string  `json:"device_id"`
	SubmapID      string  `json:"target_submap_id"`
	NetworkCIDR   string  `json:"network_cidr"`
	PosX          float64 `json:"pos_x"`
	PosY          float64 `json:"pos_y"`
	Width         float64 `json:"width"`
	Height        float64 `json:"height"`
	Rotation      float64 `json:"rotation"`
	ZoneID        string  `json:"zone_id"`
	Shape         string  `json:"shape"`
	Icon          string  `json:"icon_name"`
	ShowIcon      *bool   `json:"show_icon"`
	Label         string  `json:"label_template"`
	LabelPosition string  `json:"label_position"`
	ZIndex        int     `json:"z_index"`
	CustomStyle   any     `json:"custom_style"`
}

func (s *Server) createElement(w http.ResponseWriter, r *http.Request) {
	var req createElementRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.ElementType == "" {
		req.ElementType = "device"
	}
	if req.ElementType == "device" && req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id is required")
		return
	}
	if req.ElementType == "submap" && req.SubmapID == "" {
		writeError(w, http.StatusBadRequest, "target_submap_id is required")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if req.ElementType == "device" {
		if !s.canAccessDevice(r.Context(), user, req.DeviceID) {
			writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
			return
		}
		var duplicate bool
		if s.pool.QueryRow(r.Context(), `
			SELECT EXISTS(SELECT 1 FROM map_elements WHERE map_id=$1 AND element_type='device' AND device_id=$2)
		`, chi.URLParam(r, "id"), req.DeviceID).Scan(&duplicate) == nil && duplicate {
			writeError(w, http.StatusConflict, "el dispositivo ya está en este mapa")
			return
		}
	}
	if req.ElementType == "submap" && !s.canAccessMap(r.Context(), user, req.SubmapID) {
		writeError(w, http.StatusForbidden, "sin acceso al submapa")
		return
	}
	if req.ElementType == "network" {
		req.NetworkCIDR = strings.TrimSpace(req.NetworkCIDR)
		if req.NetworkCIDR == "" {
			writeError(w, http.StatusBadRequest, "network_cidr is required")
			return
		}
		_, ipnet, err := net.ParseCIDR(req.NetworkCIDR)
		if err != nil {
			writeError(w, http.StatusBadRequest, "network_cidr debe ser un CIDR válido, por ejemplo 192.0.2.0/24")
			return
		}
		req.NetworkCIDR = ipnet.String()
	}
	if req.ElementType == "device" {
		var defShape, defLabel string
		_ = s.pool.QueryRow(r.Context(), `SELECT default_device_shape, default_device_label FROM maps WHERE id=$1`, chi.URLParam(r, "id")).Scan(&defShape, &defLabel)
		if req.Shape == "" && defShape != "" {
			req.Shape = defShape
		}
		if req.Label == "" && defLabel != "" {
			req.Label = defLabel
		}
	}
	if req.Width <= 0 {
		req.Width = 180
	}
	if req.Height <= 0 {
		req.Height = 90
	}
	if req.Shape == "" {
		if req.ElementType == "device" {
			req.Shape = "circle"
		} else {
			req.Shape = "rounded_rect"
		}
	}
	if req.Icon == "" {
		if req.ElementType == "network" {
			req.Icon = "network"
		} else {
			req.Icon = "server"
		}
	}
	if req.LabelPosition == "" {
		req.LabelPosition = "inside"
	}
	if req.ZIndex == 0 {
		switch req.ElementType {
		case "zone":
			req.ZIndex = 0
		case "text_label":
			req.ZIndex = 20
		default:
			req.ZIndex = 10
		}
	}
	if req.Label == "" {
		switch req.ElementType {
		case "submap":
			req.Label = "Submapa\n[Submap.Summary]"
		case "network":
			req.Label = "[Network.CIDR]\n[Network.Summary]"
		case "zone":
			req.Label = "Zona"
		case "text_label":
			req.Label = "Texto"
		default:
			req.Label = "[Device.Name]\n[Device.FirstAddress]"
		}
	}
	if req.ElementType == "network" && req.Width <= 0 {
		req.Width = 230
	}
	if req.ElementType == "network" && req.Height <= 0 {
		req.Height = 116
	}
	showIcon := true
	if req.ShowIcon != nil {
		showIcon = *req.ShowIcon
	} else if req.ElementType == "zone" || req.ElementType == "text_label" {
		showIcon = false
	}
	if req.CustomStyle == nil {
		req.CustomStyle = map[string]any{}
	}
	style, _ := json.Marshal(req.CustomStyle)
	var deviceID, targetID any
	if req.DeviceID != "" {
		deviceID = req.DeviceID
	}
	if req.SubmapID != "" {
		targetID = req.SubmapID
	}
	var zoneID any
	if req.ZoneID != "" {
		zoneID = req.ZoneID
	} else if req.ElementType != "zone" {
		var zid string
		_ = s.pool.QueryRow(r.Context(), `
			SELECT id::text FROM map_elements
			WHERE map_id=$1 AND element_type='zone'
			  AND $2 >= pos_x AND $2 <= pos_x + width
			  AND $3 >= pos_y AND $3 <= pos_y + height
			ORDER BY z_index DESC LIMIT 1
		`, chi.URLParam(r, "id"), req.PosX+req.Width/2, req.PosY+req.Height/2).Scan(&zid)
		if zid != "" {
			zoneID = zid
		}
	}
	var networkCIDR any
	if req.NetworkCIDR != "" {
		networkCIDR = req.NetworkCIDR
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO map_elements (map_id, element_type, device_id, target_submap_id, network_cidr, pos_x, pos_y,
		  width, height, rotation, zone_id, shape, icon_name, show_icon, label_template, label_position, z_index, custom_style)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::jsonb) RETURNING id
	`, chi.URLParam(r, "id"), req.ElementType, deviceID, targetID, networkCIDR, req.PosX, req.PosY,
		req.Width, req.Height, req.Rotation, zoneID, req.Shape, req.Icon, showIcon, req.Label, req.LabelPosition, req.ZIndex, string(style)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ElementType == "submap" && req.SubmapID != "" && req.SubmapID != chi.URLParam(r, "id") {
		parentID := chi.URLParam(r, "id")
		_, _ = s.pool.Exec(r.Context(), `UPDATE maps SET parent_map_id=COALESCE(parent_map_id,$2) WHERE id=$1`, req.SubmapID, parentID)
		s.ensureBackElement(r.Context(), req.SubmapID, parentID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) ensureBackElement(ctx context.Context, childMapID, parentMapID string) {
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO map_elements (map_id, element_type, target_submap_id, pos_x, pos_y, width, height,
		  shape, icon_name, show_icon, label_template, z_index, custom_style)
		SELECT $1,'submap',$2,40,40,150,150,'circle','building',TRUE,'Volver [Submap.Name]',10,'{"system":"back"}'
		WHERE NOT EXISTS (
		  SELECT 1 FROM map_elements WHERE map_id=$1 AND element_type='submap' AND custom_style->>'system'='back'
		)
	`, childMapID, parentMapID)
}

type updateElementRequest struct {
	PosX        *float64 `json:"pos_x"`
	PosY        *float64 `json:"pos_y"`
	NetworkCIDR *string  `json:"network_cidr"`
	Width       *float64 `json:"width"`
	Height      *float64 `json:"height"`
	Rotation    *float64 `json:"rotation"`
	ZoneID      *string  `json:"zone_id"`
	Shape       *string  `json:"shape"`
	Icon        *string  `json:"icon_name"`
	Label       *string  `json:"label_template"`
	LabelPos    *string  `json:"label_position"`
	ShowIcon    *bool    `json:"show_icon"`
	ZIndex      *int     `json:"z_index"`
	Locked      *bool    `json:"is_locked"`
	CustomStyle any      `json:"custom_style"`
}

func (s *Server) updateElement(w http.ResponseWriter, r *http.Request) {
	var req updateElementRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.NetworkCIDR != nil && strings.TrimSpace(*req.NetworkCIDR) != "" {
		_, ipnet, err := net.ParseCIDR(strings.TrimSpace(*req.NetworkCIDR))
		if err != nil {
			writeError(w, http.StatusBadRequest, "network_cidr debe ser un CIDR válido")
			return
		}
		normalized := ipnet.String()
		req.NetworkCIDR = &normalized
	}
	style, _ := json.Marshal(req.CustomStyle)
	_, err := s.pool.Exec(r.Context(), `
		UPDATE map_elements SET
		  pos_x=COALESCE($3,pos_x), pos_y=COALESCE($4,pos_y), width=COALESCE($5,width), height=COALESCE($6,height),
		  shape=COALESCE($7,shape), icon_name=COALESCE($8,icon_name), label_template=COALESCE($9,label_template),
		  label_position=COALESCE($10,label_position), show_icon=COALESCE($11,show_icon), z_index=COALESCE($12,z_index),
		  is_locked=COALESCE($13,is_locked), rotation=COALESCE($15,rotation),
		  zone_id=CASE WHEN $16::text IS NULL THEN zone_id ELSE NULLIF($16,'')::uuid END,
		  custom_style=CASE WHEN $14::text='null' THEN custom_style ELSE $14::jsonb END,
		  network_cidr=CASE WHEN $17::text IS NULL THEN network_cidr ELSE NULLIF($17,'')::cidr END,
		  updated_at=NOW()
		WHERE id=$1 AND map_id=$2
	`, chi.URLParam(r, "elemID"), chi.URLParam(r, "id"), req.PosX, req.PosY, req.Width, req.Height,
		req.Shape, req.Icon, req.Label, req.LabelPos, req.ShowIcon, req.ZIndex, req.Locked, string(style), req.Rotation, req.ZoneID, req.NetworkCIDR)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteElement(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `DELETE FROM map_elements WHERE id=$1 AND map_id=$2`,
		chi.URLParam(r, "elemID"), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

type createLinkRequest struct {
	SourceElementID string  `json:"source_element_id"`
	TargetElementID string  `json:"target_element_id"`
	SourceHandle    string  `json:"source_handle"`
	TargetHandle    string  `json:"target_handle"`
	MasteringType   string  `json:"mastering_type"`
	MasterDeviceID  string  `json:"master_device_id"`
	SNMPIfIndex     int     `json:"snmp_if_index"`
	SNMPIfName      string  `json:"snmp_if_name"`
	LinkType        string  `json:"link_type"`
	MaxSpeedBps     int64   `json:"max_speed_bps"`
	Thickness       float64 `json:"thickness"`
	Dashed          bool    `json:"dashed"`
	Label           string  `json:"label_template"`
	RoutingStyle    string  `json:"routing_style"`
	ColorByTraffic  *bool   `json:"color_by_traffic"`
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	var req createLinkRequest
	if err := decodeBody(r, &req); err != nil || req.SourceElementID == "" || req.TargetElementID == "" {
		writeError(w, http.StatusBadRequest, "source and target are required")
		return
	}
	if req.MaxSpeedBps <= 0 {
		req.MaxSpeedBps = 1000000000
	}
	if req.Thickness <= 0 {
		req.Thickness = 2.5
	}
	if req.LinkType == "" {
		req.LinkType = "ethernet"
	}
	if req.RoutingStyle == "" {
		req.RoutingStyle = "straight"
	}
	if req.MasteringType == "" {
		req.MasteringType = "simple"
	}
	if req.SourceHandle == "" {
		req.SourceHandle = "center"
	}
	if req.TargetHandle == "" {
		req.TargetHandle = "center"
	}
	user, _ := auth.UserFrom(r.Context())
	var sourceDevice, targetDevice string
	if err := s.pool.QueryRow(r.Context(), `SELECT COALESCE(device_id::text,'') FROM map_elements WHERE id=$1 AND map_id=$2`, req.SourceElementID, chi.URLParam(r, "id")).Scan(&sourceDevice); err != nil {
		writeError(w, http.StatusBadRequest, "el elemento origen no pertenece a este mapa")
		return
	}
	if err := s.pool.QueryRow(r.Context(), `SELECT COALESCE(device_id::text,'') FROM map_elements WHERE id=$1 AND map_id=$2`, req.TargetElementID, chi.URLParam(r, "id")).Scan(&targetDevice); err != nil {
		writeError(w, http.StatusBadRequest, "el elemento destino no pertenece a este mapa")
		return
	}
	if (sourceDevice != "" && !s.canAccessDevice(r.Context(), user, sourceDevice)) ||
		(targetDevice != "" && !s.canAccessDevice(r.Context(), user, targetDevice)) ||
		(req.MasterDeviceID != "" && !s.canAccessDevice(r.Context(), user, req.MasterDeviceID)) {
		writeError(w, http.StatusForbidden, "sin acceso a uno de los dispositivos del enlace")
		return
	}
	colorByTraffic := true
	if req.ColorByTraffic != nil {
		colorByTraffic = *req.ColorByTraffic
	}
	var master any
	if req.MasterDeviceID != "" {
		master = req.MasterDeviceID
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO map_links (map_id, source_element_id, target_element_id, source_handle, target_handle,
		  mastering_type, master_device_id, snmp_if_index, snmp_if_name, link_type, max_speed_bps,
		  thickness, dashed, label_template, routing_style, color_by_traffic)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id
	`, chi.URLParam(r, "id"), req.SourceElementID, req.TargetElementID, req.SourceHandle, req.TargetHandle,
		req.MasteringType, master, req.SNMPIfIndex, req.SNMPIfName, req.LinkType, req.MaxSpeedBps,
		req.Thickness, req.Dashed, req.Label, req.RoutingStyle, colorByTraffic).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

type updateLinkRequest struct {
	MasteringType  *string  `json:"mastering_type"`
	MasterDevice   *string  `json:"master_device_id"`
	SNMPIfIndex    *int     `json:"snmp_if_index"`
	SNMPIfName     *string  `json:"snmp_if_name"`
	LinkType       *string  `json:"link_type"`
	MaxSpeedBps    *int64   `json:"max_speed_bps"`
	WarnPct        *float64 `json:"warning_utilization_pct"`
	CritPct        *float64 `json:"critical_utilization_pct"`
	Thickness      *float64 `json:"thickness"`
	Dashed         *bool    `json:"dashed"`
	Label          *string  `json:"label_template"`
	RoutingStyle   *string  `json:"routing_style"`
	Status         *string  `json:"status"`
	ColorByTraffic *bool    `json:"color_by_traffic"`
}

func (s *Server) updateLink(w http.ResponseWriter, r *http.Request) {
	var req updateLinkRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.MasterDevice != nil && *req.MasterDevice != "" {
		user, _ := auth.UserFrom(r.Context())
		if !s.canAccessDevice(r.Context(), user, *req.MasterDevice) {
			writeError(w, http.StatusForbidden, "sin acceso al dispositivo maestro")
			return
		}
	}
	_, err := s.pool.Exec(r.Context(), `
		UPDATE map_links SET
		 mastering_type=COALESCE($3,mastering_type), master_device_id=COALESCE($4,master_device_id),
		 snmp_if_index=COALESCE($5,snmp_if_index), snmp_if_name=COALESCE($6,snmp_if_name),
		 link_type=COALESCE($7,link_type), max_speed_bps=COALESCE($8,max_speed_bps),
		 warning_utilization_pct=COALESCE($9,warning_utilization_pct),
		 critical_utilization_pct=COALESCE($10,critical_utilization_pct),
		 thickness=COALESCE($11,thickness), dashed=COALESCE($12,dashed),
		 label_template=COALESCE($13,label_template), routing_style=COALESCE($14,routing_style),
		 status=COALESCE($15,status), color_by_traffic=COALESCE($16,color_by_traffic), updated_at=NOW()
		WHERE id=$1 AND map_id=$2
	`, chi.URLParam(r, "linkID"), chi.URLParam(r, "id"), req.MasteringType, req.MasterDevice,
		req.SNMPIfIndex, req.SNMPIfName, req.LinkType, req.MaxSpeedBps, req.WarnPct, req.CritPct,
		req.Thickness, req.Dashed, req.Label, req.RoutingStyle, req.Status, req.ColorByTraffic)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `DELETE FROM map_links WHERE id=$1 AND map_id=$2`,
		chi.URLParam(r, "linkID"), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) exportMap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mapID := chi.URLParam(r, "id")
	var name, parent, desc, bg, defaultLabel string
	var grid, snap bool
	var gridSize int
	_ = s.pool.QueryRow(ctx, `
		SELECT name, COALESCE(parent_map_id::text,''), description, grid_enabled, grid_size,
		       snap_to_grid, background_color, default_device_label
		FROM maps WHERE id=$1
	`, mapID).Scan(&name, &parent, &desc, &grid, &gridSize, &snap, &bg, &defaultLabel)
	user, _ := auth.UserFrom(ctx)
	allowedSet, restrictedGroups := s.allowedDeviceIDs(ctx, user)
	var allowedDevices []string
	if restrictedGroups {
		for id := range allowedSet {
			allowedDevices = append(allowedDevices, id)
		}
	}
	funcs := s.functionMap(ctx)
	elements := s.loadElements(ctx, mapID, funcs, allowedDevices, restrictedGroups)
	links := visibleLinks(s.loadLinks(ctx, mapID), elements)
	writeJSON(w, http.StatusOK, map[string]any{
		"format": "netmonitor-map/v1", "exported_at": time.Now().UTC(),
		"map_id": mapID,
		"map": map[string]any{
			"name": name, "parent_map_id": parent, "description": desc,
			"grid_enabled": grid, "grid_size": gridSize, "snap_to_grid": snap,
			"background_color": bg, "default_device_label": defaultLabel,
		},
		"elements": elements, "links": links,
	})
}

type importedElement struct {
	ID            string  `json:"id"`
	ElementType   string  `json:"element_type"`
	DeviceID      string  `json:"device_id"`
	TargetSubmap  string  `json:"target_submap_id"`
	NetworkCIDR   string  `json:"network_cidr"`
	PosX          float64 `json:"pos_x"`
	PosY          float64 `json:"pos_y"`
	Width         float64 `json:"width"`
	Height        float64 `json:"height"`
	Shape         string  `json:"shape"`
	Icon          string  `json:"icon_name"`
	ShowIcon      bool    `json:"show_icon"`
	Label         string  `json:"label_template"`
	LabelPosition string  `json:"label_position"`
	ZIndex        int     `json:"z_index"`
	CustomStyle   any     `json:"custom_style"`
}

type importedLink struct {
	SourceElement string  `json:"source_element_id"`
	TargetElement string  `json:"target_element_id"`
	SourceHandle  string  `json:"source_handle"`
	TargetHandle  string  `json:"target_handle"`
	MasteringType string  `json:"mastering_type"`
	MasterDevice  string  `json:"master_device_id"`
	SNMPIfIndex   int     `json:"snmp_if_index"`
	SNMPIfName    string  `json:"snmp_if_name"`
	LinkType      string  `json:"link_type"`
	MaxSpeed      int64   `json:"max_speed_bps"`
	WarnPct       float64 `json:"warning_utilization_pct"`
	CritPct       float64 `json:"critical_utilization_pct"`
	RoutingStyle  string  `json:"routing_style"`
	Thickness     float64 `json:"thickness"`
	Dashed        bool    `json:"dashed"`
	Label         string  `json:"label_template"`
}

func (s *Server) importMap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string          `json:"name"`
		ParentMapID string          `json:"parent_map_id"`
		Data        json.RawMessage `json:"data"`
	}
	if err := decodeBody(r, &req); err != nil || len(req.Data) == 0 {
		writeError(w, http.StatusBadRequest, "data is required")
		return
	}
	var payload struct {
		Map      map[string]any    `json:"map"`
		Elements []importedElement `json:"elements"`
		Links    []importedLink    `json:"links"`
	}
	if err := json.Unmarshal(req.Data, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid map JSON")
		return
	}
	if req.Name == "" {
		req.Name = stringValue(payload.Map, "name")
		if req.Name == "" {
			req.Name = "Mapa importado"
		}
	}
	var parent any
	if req.ParentMapID != "" {
		var exists bool
		if s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM maps WHERE id=$1)`, req.ParentMapID).Scan(&exists) == nil && exists {
			parent = req.ParentMapID
		}
	}
	slug := slugify(req.Name)
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	var mapID, finalSlug string
	for i := 0; i < 50; i++ {
		try := slug
		if i > 0 {
			try = fmt.Sprintf("%s-%d", slug, i)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO maps (name, slug, parent_map_id, description, grid_enabled, grid_size,
			 snap_to_grid, background_color, default_device_label)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, slug
		`, req.Name, try, parent, stringValue(payload.Map, "description"),
			boolValue(payload.Map, "grid_enabled", true), intValue(payload.Map, "grid_size", 16),
			boolValue(payload.Map, "snap_to_grid", true), stringDefault(stringValue(payload.Map, "background_color"), "#FFFFFF"),
			stringDefault(stringValue(payload.Map, "default_device_label"), "[Device.Name]\n[Device.FirstAddress]")).Scan(&mapID, &finalSlug)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "duplicate") {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if mapID == "" {
		writeError(w, http.StatusConflict, "could not allocate slug")
		return
	}
	idMap := map[string]string{}
	for _, el := range payload.Elements {
		var device, submap any
		if el.DeviceID != "" {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1)`, el.DeviceID).Scan(&exists)
			if exists {
				device = el.DeviceID
			}
		}
		if el.TargetSubmap != "" {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM maps WHERE id=$1)`, el.TargetSubmap).Scan(&exists)
			if exists {
				submap = el.TargetSubmap
			}
		}
		if el.ElementType == "device" && device == nil {
			continue
		}
		if el.ElementType == "submap" && submap == nil {
			continue
		}
		if el.ElementType == "network" {
			if _, _, err := net.ParseCIDR(strings.TrimSpace(el.NetworkCIDR)); err != nil {
				continue
			}
		}
		if el.Width <= 0 {
			el.Width = 190
		}
		if el.Height <= 0 {
			el.Height = 96
		}
		if el.Shape == "" {
			el.Shape = "rounded_rect"
		}
		if el.CustomStyle == nil {
			el.CustomStyle = map[string]any{}
		}
		styleJSON, _ := json.Marshal(el.CustomStyle)
		var networkCIDR any
		if strings.TrimSpace(el.NetworkCIDR) != "" {
			if _, ipnet, err := net.ParseCIDR(strings.TrimSpace(el.NetworkCIDR)); err == nil {
				networkCIDR = ipnet.String()
			}
		}
		defaultLabel := "[Device.Name]\n[Device.FirstAddress]"
		if el.ElementType == "network" {
			defaultLabel = "[Network.CIDR]\n[Network.Summary]"
		}
		var newID string
		err := tx.QueryRow(ctx, `
			INSERT INTO map_elements (map_id, element_type, device_id, target_submap_id, network_cidr, pos_x, pos_y,
			 width, height, shape, icon_name, show_icon, label_template, label_position, z_index, custom_style)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb) RETURNING id
		`, mapID, stringDefault(el.ElementType, "device"), device, submap, networkCIDR, el.PosX, el.PosY,
			el.Width, el.Height, el.Shape, stringDefault(el.Icon, "server"), el.ShowIcon,
			stringDefault(el.Label, defaultLabel), stringDefault(el.LabelPosition, "inside"), el.ZIndex, string(styleJSON)).Scan(&newID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if el.ID != "" {
			idMap[el.ID] = newID
		}
	}
	for _, ln := range payload.Links {
		src := idMap[ln.SourceElement]
		tgt := idMap[ln.TargetElement]
		if src == "" || tgt == "" {
			continue
		}
		if ln.MaxSpeed <= 0 {
			ln.MaxSpeed = 1000000000
		}
		if ln.Thickness <= 0 {
			ln.Thickness = 2.5
		}
		var master any
		if ln.MasterDevice != "" {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1)`, ln.MasterDevice).Scan(&exists)
			if exists {
				master = ln.MasterDevice
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO map_links (map_id, source_element_id, target_element_id, source_handle, target_handle,
			 mastering_type, master_device_id, snmp_if_index, snmp_if_name, link_type, max_speed_bps,
			 warning_utilization_pct, critical_utilization_pct, routing_style, thickness, dashed, label_template)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		`, mapID, src, tgt, ln.SourceHandle, ln.TargetHandle, stringDefault(ln.MasteringType, "simple"),
			master, ln.SNMPIfIndex, ln.SNMPIfName, stringDefault(ln.LinkType, "ethernet"),
			ln.MaxSpeed, floatDefault(ln.WarnPct, 75), floatDefault(ln.CritPct, 90),
			stringDefault(ln.RoutingStyle, "straight"), ln.Thickness, ln.Dashed, ln.Label)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if user, ok := auth.UserFrom(r.Context()); ok && user.RestrictMaps && user.Role != "admin" {
		_, _ = s.pool.Exec(r.Context(), `INSERT INTO map_permissions (user_id,map_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, user.ID, mapID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": mapID, "slug": finalSlug, "name": req.Name}})
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func stringDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func boolValue(m map[string]any, key string, fallback bool) bool {
	if m == nil {
		return fallback
	}
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return fallback
}

func intValue(m map[string]any, key string, fallback int) int {
	if m == nil {
		return fallback
	}
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return fallback
}

func floatDefault(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	return v
}

func formatBps(v float64) string {
	if v < 0 {
		v = 0
	}
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}
