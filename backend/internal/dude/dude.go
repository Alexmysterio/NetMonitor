// Package dude parses The Dude's SQLite database and exported XML documents
// into a neutral preview that can be reviewed before touching the inventory.
// No credentials are imported: passwords from The Dude are intentionally
// ignored, and the operator must assign encrypted SNMP/RouterOS credentials
// after migration.
package dude

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type Map struct {
	SourceID       string `json:"source_id"`
	Name           string `json:"name"`
	ParentSourceID string `json:"parent_source_id"`
	DeviceCount    int    `json:"device_count"`
}

type Device struct {
	SourceID    string   `json:"source_id"`
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	DNSName     string   `json:"dns_name"`
	MAC         string   `json:"mac_address"`
	Vendor      string   `json:"vendor"`
	Model       string   `json:"model"`
	DeviceType  string   `json:"device_type"`
	Notes       string   `json:"notes"`
	SourceMapID string   `json:"source_map_id"`
	MapName     string   `json:"map_name"`
	PosX        float64  `json:"pos_x"`
	PosY        float64  `json:"pos_y"`
	Width       float64  `json:"width"`
	Height      float64  `json:"height"`
	Tags        []string `json:"tags"`
}

type Link struct {
	SourceID    string `json:"source_id"`
	SourceMapID string `json:"source_map_id"`
	DeviceA     string `json:"device_a"`
	DeviceB     string `json:"device_b"`
	LinkType    string `json:"link_type"`
	SpeedBps    int64  `json:"max_speed_bps"`
}

type Decoration struct {
	SourceID string  `json:"source_id"`
	MapID    string  `json:"map_id"`
	Name     string  `json:"name"`
	PosX     float64 `json:"pos_x"`
	PosY     float64 `json:"pos_y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

type Preview struct {
	SourceKind  string       `json:"source_kind"`
	Filename    string       `json:"filename"`
	Maps        []Map        `json:"maps"`
	Devices     []Device     `json:"devices"`
	Decorations []Decoration `json:"decorations"`
	Links       []Link       `json:"links"`
	Warnings    []string     `json:"warnings"`
}

// MaxFileSize is the maximum The Dude export accepted for preview. The file is
// read into memory, so this also bounds the import memory footprint.
const MaxFileSize = 256 << 20

// Parse detects the file type and returns a normalized preview.
func Parse(ctx context.Context, filename string, data []byte) (Preview, error) {
	if len(data) == 0 {
		return Preview{}, fmt.Errorf("el archivo está vacío")
	}
	if len(data) > MaxFileSize {
		return Preview{}, fmt.Errorf("el archivo supera 256 MB")
	}
	if len(data) >= 16 && strings.HasPrefix(string(data[:16]), "SQLite format 3") {
		return parseSQLite(ctx, filename, data)
	}
	return parseXML(filename, data)
}

func parseSQLite(ctx context.Context, filename string, data []byte) (Preview, error) {
	tmp, err := os.CreateTemp("", "netmonitor-dude-*.db")
	if err != nil {
		return Preview{}, err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return Preview{}, err
	}
	if err := tmp.Close(); err != nil {
		return Preview{}, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return Preview{}, fmt.Errorf("abrir SQLite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return Preview{}, fmt.Errorf("SQLite no es legible: %w", err)
	}
	tables, err := sqliteTables(ctx, db)
	if err != nil {
		return Preview{}, err
	}
	// Modern Dude databases store devices, maps and links as Nova Message
	// blobs in the objs table instead of relational tables.
	if objsTable := findTable(tables, "objs"); objsTable != "" {
		if p, ok, err := parseNovaSQLite(ctx, filename, db, objsTable); err != nil {
			return Preview{}, err
		} else if ok {
			return p, nil
		}
	}
	p := Preview{SourceKind: "sqlite", Filename: filename, Maps: []Map{}, Devices: []Device{}, Decorations: []Decoration{}, Links: []Link{}}

	mapTable := findTable(tables, "map", "maps", "dude_map", "maps_map")
	deviceTable := findTable(tables, "device", "devices", "dude_device", "objects")
	objectTable := findTable(tables, "map_object", "map_objects", "mapobject", "object", "objects")
	linkTable := findTable(tables, "link", "links", "dude_link", "map_link")
	if deviceTable == "objects" && mapTable == "" {
		deviceTable = ""
	}
	if objectTable == deviceTable {
		objectTable = ""
	}
	if deviceTable == "" {
		return Preview{}, fmt.Errorf("no se encontró una tabla de dispositivos compatible en el SQLite")
	}

	mapRows, err := readTable(ctx, db, mapTable)
	if err == nil {
		for _, row := range mapRows {
			m := Map{
				SourceID:       text(row, "id", "map", "uuid"),
				Name:           text(row, "name", "title", "label"),
				ParentSourceID: text(row, "parent", "parent_id", "parentid"),
			}
			if m.SourceID == "" {
				m.SourceID = fmt.Sprintf("map-%d", len(p.Maps)+1)
			}
			if m.Name == "" {
				m.Name = "Mapa Dude " + m.SourceID
			}
			p.Maps = append(p.Maps, m)
		}
	}
	deviceRows, err := readTable(ctx, db, deviceTable)
	if err != nil {
		return Preview{}, err
	}
	deviceByID := map[string]int{}
	for _, row := range deviceRows {
		id := text(row, "id", "device", "uuid")
		name := text(row, "name", "title", "label", "host")
		address := text(row, "address", "ip", "ip_address", "host", "addresses")
		if address == "" {
			address = text(row, "dns", "dns_name")
		}
		d := Device{
			SourceID:   id,
			Name:       name,
			Address:    cleanAddress(address),
			DNSName:    text(row, "dns", "dns_name", "hostname"),
			MAC:        text(row, "mac", "mac_address", "macaddress"),
			Vendor:     text(row, "vendor", "manufacturer", "make"),
			Model:      text(row, "model", "hardware", "board"),
			DeviceType: normalizeType(text(row, "type", "device_type", "kind")),
			Notes:      text(row, "note", "notes", "comment", "description"),
			MapName:    text(row, "map", "map_name"),
		}
		if d.SourceID == "" {
			d.SourceID = fmt.Sprintf("device-%d", len(p.Devices)+1)
		}
		if name == "" {
			d.Name = d.Address
		}
		if d.Address == "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("El dispositivo %q no tiene dirección; se omitirá al aplicar.", d.Name))
		}
		deviceByID[id] = len(p.Devices)
		p.Devices = append(p.Devices, d)
	}

	if objectTable != "" {
		rows, err := readTable(ctx, db, objectTable)
		if err == nil {
			coords := map[string]struct {
				mapID, x, y, w, h string
			}{}
			for _, row := range rows {
				deviceID := text(row, "device", "device_id", "deviceid", "object", "object_id")
				mapID := text(row, "map", "map_id", "mapid")
				if deviceID == "" || mapID == "" {
					continue
				}
				coords[deviceID] = struct{ mapID, x, y, w, h string }{
					mapID: mapID, x: text(row, "x", "pos_x", "left"), y: text(row, "y", "pos_y", "top"),
					w: text(row, "width", "w"), h: text(row, "height", "h"),
				}
			}
			for i := range p.Devices {
				c, ok := coords[p.Devices[i].SourceID]
				if !ok {
					continue
				}
				p.Devices[i].SourceMapID = c.mapID
				p.Devices[i].MapName = mapName(p.Maps, c.mapID)
				p.Devices[i].PosX = number(c.x)
				p.Devices[i].PosY = number(c.y)
				p.Devices[i].Width = number(c.w)
				p.Devices[i].Height = number(c.h)
			}
		}
	}

	linkRows, err := readTable(ctx, db, linkTable)
	if err == nil {
		for _, row := range linkRows {
			a := text(row, "device1", "device1_id", "device_a", "src_device", "source", "from")
			b := text(row, "device2", "device2_id", "device_b", "dst_device", "target", "to")
			if a == "" || b == "" {
				continue
			}
			l := Link{
				SourceID:    text(row, "id", "uuid"),
				SourceMapID: text(row, "map", "map_id", "mapid"),
				DeviceA:     a, DeviceB: b,
				LinkType: normalizeLinkType(text(row, "type", "link_type")),
				SpeedBps: int64(number(text(row, "speed", "max_speed", "bandwidth"))),
			}
			if l.SourceID == "" {
				l.SourceID = fmt.Sprintf("link-%d", len(p.Links)+1)
			}
			if l.SpeedBps <= 0 {
				l.SpeedBps = 1000000000
			}
			p.Links = append(p.Links, l)
		}
	}

	finalizePreview(&p)
	return p, nil
}

func parseXML(filename string, data []byte) (Preview, error) {
	var root xmlNode
	if err := xml.Unmarshal(data, &root); err != nil {
		return Preview{}, fmt.Errorf("XML inválido: %w", err)
	}
	p := Preview{SourceKind: "xml", Filename: filename, Maps: []Map{}, Devices: []Device{}, Decorations: []Decoration{}, Links: []Link{}}
	seen := map[string]bool{}
	var walk func(node xmlNode, mapID string)
	walk = func(node xmlNode, mapID string) {
		local := strings.ToLower(node.XMLName.Local)
		switch local {
		case "map", "mapa", "submap":
			id := nodeValue(node, "id", "uuid", "source_id")
			if id == "" {
				id = fmt.Sprintf("map-%d", len(p.Maps)+1)
			}
			name := nodeValue(node, "name", "title", "label")
			if name == "" {
				name = "Mapa " + id
			}
			if !seen["map:"+id] {
				seen["map:"+id] = true
				p.Maps = append(p.Maps, Map{SourceID: id, Name: name,
					ParentSourceID: nodeValue(node, "parent", "parent_id", "parentid")})
			}
			mapID = id
		case "device", "host", "nodo":
			id := nodeValue(node, "id", "uuid", "source_id")
			if id == "" {
				id = fmt.Sprintf("device-%d", len(p.Devices)+1)
			}
			if seen["device:"+id] {
				break
			}
			seen["device:"+id] = true
			d := Device{
				SourceID: id, Name: nodeValue(node, "name", "title", "label", "hostname"),
				Address:     cleanAddress(nodeValue(node, "address", "ip", "ip_address", "host")),
				DNSName:     nodeValue(node, "dns", "dns_name", "hostname"),
				MAC:         nodeValue(node, "mac", "mac_address"),
				Vendor:      nodeValue(node, "vendor", "manufacturer"),
				Model:       nodeValue(node, "model", "hardware", "board"),
				DeviceType:  normalizeType(nodeValue(node, "type", "device_type", "kind")),
				Notes:       nodeValue(node, "note", "notes", "comment", "description"),
				SourceMapID: mapID, MapName: nodeValue(node, "map", "map_name"),
				PosX:   number(nodeValue(node, "x", "pos_x", "left")),
				PosY:   number(nodeValue(node, "y", "pos_y", "top")),
				Width:  number(nodeValue(node, "width", "w")),
				Height: number(nodeValue(node, "height", "h")),
			}
			if d.Name == "" {
				d.Name = d.Address
			}
			p.Devices = append(p.Devices, d)
		case "link", "enlace":
			id := nodeValue(node, "id", "uuid")
			if id == "" {
				id = fmt.Sprintf("link-%d", len(p.Links)+1)
			}
			a := nodeValue(node, "device1", "device_a", "source", "from", "src")
			b := nodeValue(node, "device2", "device_b", "target", "to", "dst")
			if a == "" || b == "" || seen["link:"+id] {
				break
			}
			seen["link:"+id] = true
			l := Link{SourceID: id, SourceMapID: mapID, DeviceA: a, DeviceB: b,
				LinkType: normalizeLinkType(nodeValue(node, "type", "link_type")),
				SpeedBps: int64(number(nodeValue(node, "speed", "max_speed", "bandwidth")))}
			if l.SpeedBps <= 0 {
				l.SpeedBps = 1000000000
			}
			p.Links = append(p.Links, l)
		}
		for _, child := range node.Nodes {
			walk(child, mapID)
		}
	}
	walk(root, "")
	if len(p.Maps) == 0 {
		p.Maps = append(p.Maps, Map{SourceID: "imported", Name: "Importado desde The Dude"})
	}
	for i := range p.Devices {
		if p.Devices[i].SourceMapID == "" {
			p.Devices[i].SourceMapID = p.Maps[0].SourceID
		}
	}
	for i := range p.Maps {
		for _, d := range p.Devices {
			if d.SourceMapID == p.Maps[i].SourceID {
				p.Maps[i].DeviceCount++
			}
		}
	}
	sort.Slice(p.Devices, func(i, j int) bool { return strings.ToLower(p.Devices[i].Name) < strings.ToLower(p.Devices[j].Name) })
	return p, nil
}

type xmlNode struct {
	XMLName xml.Name
	Attr    []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Nodes   []xmlNode  `xml:",any"`
}

func nodeValue(node xmlNode, keys ...string) string {
	for _, key := range keys {
		for _, attr := range node.Attr {
			if strings.EqualFold(attr.Name.Local, key) {
				return strings.TrimSpace(attr.Value)
			}
		}
		for _, child := range node.Nodes {
			if strings.EqualFold(child.XMLName.Local, key) {
				return strings.TrimSpace(child.Text)
			}
		}
	}
	return ""
}

func sqliteTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

func findTable(tables []string, candidates ...string) string {
	for _, candidate := range candidates {
		for _, table := range tables {
			if strings.EqualFold(table, candidate) {
				return table
			}
		}
	}
	return ""
}

func readTable(ctx context.Context, db *sql.DB, table string) ([]map[string]any, error) {
	if table == "" {
		return nil, fmt.Errorf("tabla no encontrada")
	}
	rows, err := db.QueryContext(ctx, `SELECT * FROM "`+strings.ReplaceAll(table, `"`, `""`)+`" LIMIT 20000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, col := range cols {
			row[strings.ToLower(col)] = values[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func text(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := row[key]; ok && value != nil {
			switch v := value.(type) {
			case []byte:
				return strings.TrimSpace(string(v))
			case string:
				return strings.TrimSpace(v)
			case int64:
				return strconv.FormatInt(v, 10)
			case float64:
				return strconv.FormatFloat(v, 'f', -1, 64)
			default:
				return strings.TrimSpace(fmt.Sprint(v))
			}
		}
	}
	return ""
}

func number(raw string) float64 {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if raw == "" {
		return 0
	}
	value, _ := strconv.ParseFloat(raw, 64)
	return value
}

func cleanAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.IndexAny(raw, ",; "); idx > 0 {
		raw = raw[:idx]
	}
	return strings.Trim(raw, "[]")
}

func normalizeType(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "router"), strings.Contains(lower, "mikrotik"):
		return "router"
	case strings.Contains(lower, "switch"), strings.Contains(lower, "bridge"):
		return "switch"
	case strings.Contains(lower, "firewall"):
		return "firewall"
	case strings.Contains(lower, "wireless"), strings.Contains(lower, "access"), lower == "ap":
		return "access_point"
	case strings.Contains(lower, "server"), strings.Contains(lower, "host"):
		return "server"
	case strings.Contains(lower, "printer"):
		return "printer"
	case strings.Contains(lower, "ups"):
		return "ups"
	default:
		return "generic"
	}
}

func normalizeLinkType(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "wireless"), strings.Contains(lower, "wifi"), strings.Contains(lower, "radio"):
		return "wireless"
	case strings.Contains(lower, "vpn"), strings.Contains(lower, "tunnel"):
		return "tunnel"
	default:
		return "ethernet"
	}
}

func mapName(maps []Map, id string) string {
	for _, m := range maps {
		if m.SourceID == id {
			return m.Name
		}
	}
	return ""
}
