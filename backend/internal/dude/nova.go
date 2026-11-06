package dude

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The Dude stores most objects as Nova Message (nv::message) binary blobs in
// the objs table. The format is a small TLV stream:
//
//	magic[8] | field_count[u32] | fields...
//	field: tag[u16] marker[u8] tcode[u8] value...
//
// Markers 0x10/0xfe are standard values. Dude also uses 0x11 for ordinary
// tcode values in continuation fields, so the tcode determines the width.
// The decoder below is intentionally tolerant: it keeps every field it can
// read and stops at an unknown/truncated value.
var novaMagic = []byte{0x4d, 0x32, 0x01, 0x00, 0xff, 0x88, 0x01, 0x00}

type novaKind uint8

const (
	novaBool novaKind = iota
	novaU8
	novaU32
	novaU64
	novaStr
	novaBytes
	novaU32Array
	novaStringArray
	novaCompound
)

type novaValue struct {
	kind   novaKind
	boolV  bool
	numV   uint64
	strV   string
	bytesV []byte
	u32s   []uint32
	strs   []string
	fields []novaField
}

type novaField struct {
	tag uint16
	val novaValue
}

type novaMessage struct {
	fields []novaField
}

func decodeNova(data []byte) (*novaMessage, bool) {
	if len(data) < 12 || !bytes.Equal(data[:len(novaMagic)], novaMagic) {
		return nil, false
	}
	pos := len(novaMagic) + 4 // skip the declared field count
	msg := &novaMessage{}
	for pos+4 <= len(data) {
		tag := binary.LittleEndian.Uint16(data[pos:])
		marker := data[pos+2]
		tcode := data[pos+3]
		pos += 4
		if marker != 0x10 && marker != 0x11 && marker != 0xfe {
			break
		}
		val, next, err := readNovaValue(data, pos, marker, tcode, tag)
		if err != nil {
			break
		}
		msg.fields = append(msg.fields, novaField{tag: tag, val: val})
		pos = next
	}
	return msg, true
}

func readNovaValue(data []byte, pos int, marker, tcode byte, tag uint16) (novaValue, int, error) {
	// Dude uses marker 0x11 for ordinary tcode values in continuation fields,
	// so the tcode (not the marker) determines the value width.
	switch tcode {
	case 0x00:
		return novaValue{kind: novaBool, boolV: false}, pos, nil
	case 0x01:
		return novaValue{kind: novaBool, boolV: true}, pos, nil
	case 0x08:
		if pos+4 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		return novaValue{kind: novaU32, numV: uint64(binary.LittleEndian.Uint32(data[pos:]))}, pos + 4, nil
	case 0x09:
		if pos+1 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		return novaValue{kind: novaU8, numV: uint64(data[pos])}, pos + 1, nil
	case 0x10:
		if pos+8 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		lo := uint64(binary.LittleEndian.Uint32(data[pos:]))
		hi := uint64(binary.LittleEndian.Uint32(data[pos+4:]))
		return novaValue{kind: novaU64, numV: lo | hi<<32}, pos + 8, nil
	case 0x18:
		if pos+16 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		return novaValue{kind: novaBytes, bytesV: append([]byte(nil), data[pos:pos+16]...)}, pos + 16, nil
	case 0x20:
		if pos+4 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		return novaValue{kind: novaBytes, bytesV: append([]byte(nil), data[pos:pos+4]...)}, pos + 4, nil
	case 0x21:
		if pos >= len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		n := int(data[pos])
		pos++
		if pos+n > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: longitud de cadena inválida")
		}
		return novaValue{kind: novaStr, strV: string(data[pos : pos+n])}, pos + n, nil
	case 0x31:
		if pos >= len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		n := int(data[pos])
		pos++
		if pos+n > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: longitud de bytes inválida")
		}
		return novaValue{kind: novaBytes, bytesV: append([]byte(nil), data[pos:pos+n]...)}, pos + n, nil
	case 0x88:
		if pos+2 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		count := int(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2
		if pos+count*4 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: array truncado")
		}
		values := make([]uint32, 0, count)
		for i := 0; i < count; i++ {
			values = append(values, binary.LittleEndian.Uint32(data[pos:]))
			pos += 4
		}
		return novaValue{kind: novaU32Array, u32s: values}, pos, nil
	case 0xa0:
		if pos+2 > len(data) {
			return novaValue{}, pos, fmt.Errorf("nova: fin de datos")
		}
		count := int(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2
		if tag == 0x1f41 { // Device_DnsNames is a string array
			values := make([]string, 0, count)
			for i := 0; i < count; i++ {
				if pos+2 > len(data) {
					return novaValue{}, pos, fmt.Errorf("nova: string array truncado")
				}
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				if pos+n > len(data) {
					return novaValue{}, pos, fmt.Errorf("nova: string array truncado")
				}
				values = append(values, string(data[pos:pos+n]))
				pos += n
			}
			return novaValue{kind: novaStringArray, strs: values}, pos, nil
		}
		fields := make([]novaField, 0, count)
		for i := 0; i < count && pos+4 <= len(data); i++ {
			subTag := binary.LittleEndian.Uint16(data[pos:])
			subMarker := data[pos+2]
			subTcode := data[pos+3]
			pos += 4
			if subMarker != 0x10 && subMarker != 0x11 && subMarker != 0xfe {
				break
			}
			val, next, err := readNovaValue(data, pos, subMarker, subTcode, subTag)
			if err != nil {
				break
			}
			fields = append(fields, novaField{tag: subTag, val: val})
			pos = next
		}
		return novaValue{kind: novaCompound, fields: fields}, pos, nil
	default:
		return novaValue{}, pos, fmt.Errorf("nova: tcode 0x%02x desconocido", tcode)
	}
}

func (m *novaMessage) field(tag uint16) (novaValue, bool) {
	for i := len(m.fields) - 1; i >= 0; i-- {
		if m.fields[i].tag == tag {
			return m.fields[i].val, true
		}
	}
	return novaValue{}, false
}

func (m *novaMessage) u32(tag uint16) (uint32, bool) {
	value, ok := m.field(tag)
	if !ok {
		return 0, false
	}
	switch value.kind {
	case novaU8, novaU32, novaU64:
		return uint32(value.numV), true
	default:
		return 0, false
	}
}

func (m *novaMessage) u64(tag uint16) (uint64, bool) {
	value, ok := m.field(tag)
	if !ok {
		return 0, false
	}
	switch value.kind {
	case novaU8, novaU32, novaU64:
		return value.numV, true
	default:
		return 0, false
	}
}

func (m *novaMessage) str(tag uint16) string {
	return strings.TrimSpace(m.rawStr(tag))
}

func (m *novaMessage) rawStr(tag uint16) string {
	value, ok := m.field(tag)
	if !ok || value.kind != novaStr {
		return ""
	}
	return value.strV
}

func (m *novaMessage) u32s(tag uint16) []uint32 {
	value, ok := m.field(tag)
	if !ok || value.kind != novaU32Array {
		return nil
	}
	return value.u32s
}

func (m *novaMessage) strs(tag uint16) []string {
	value, ok := m.field(tag)
	if !ok || value.kind != novaStringArray {
		return nil
	}
	return value.strs
}

func (m *novaMessage) hasRange(lo, hi uint16) bool {
	for _, field := range m.fields {
		if field.tag >= lo && field.tag <= hi {
			return true
		}
	}
	return false
}

func ipv4FromU32(value uint32) string {
	if value == 0xffffffff {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}

func novaID(value uint32) string {
	if value == 0xffffffff {
		return ""
	}
	return strconv.FormatUint(uint64(value), 10)
}

func novaMAC(msg *novaMessage) string {
	value, ok := msg.field(0x1f44)
	if !ok || value.kind != novaBytes || len(value.bytesV) < 6 {
		return ""
	}
	raw := value.bytesV
	count := int(binary.LittleEndian.Uint16(raw[4:6]))
	end := 6 + count
	if end > len(raw) {
		end = len(raw)
	}
	for pos := 6; pos+6 <= end; pos += 6 {
		mac := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
			raw[pos], raw[pos+1], raw[pos+2], raw[pos+3], raw[pos+4], raw[pos+5])
		if mac != "00:00:00:00:00:00" && mac != "ff:ff:ff:ff:ff:ff" {
			return mac
		}
	}
	return ""
}

func parseNovaSNMPProfile(id uint32, msg *novaMessage) (SNMPProfile, bool) {
	rawVersion, ok := msg.u32(0x3c68)
	if !ok || rawVersion == 0xffffffff {
		return SNMPProfile{}, false
	}
	version := "v3"
	switch rawVersion {
	case 0:
		version = "v1"
	case 1:
		version = "v2c"
	case 2:
		version = "v3"
	}
	name := msg.str(0x0010)
	if name == "" {
		name = fmt.Sprintf("SNMP %d", id)
	}
	port := 161
	if value, ok := msg.u32(0x3c6a); ok && value > 0 && value <= 65535 {
		port = int(value)
	}
	timeout := 1500
	if value, ok := msg.u32(0x3c72); ok && value > 0 && value <= 60000 {
		timeout = int(value)
	}
	retries := 2
	if value, ok := msg.u32(0x3c71); ok && value <= 10 {
		retries = int(value)
	}
	community := msg.rawStr(0x3c69)
	return SNMPProfile{
		SourceID: novaID(id), Name: name, Version: version,
		Port: port, TimeoutMS: timeout, Retries: retries,
		Community: community, HasCommunity: community != "",
	}, true
}

func isNovaDevice(id uint32, msg *novaMessage) bool {
	if !msg.hasRange(0x1f40, 0x1f5a) {
		return false
	}
	if self, ok := msg.u32(0x0001); ok && self != id {
		return false
	}
	_, hasIP := msg.field(0x1f40)
	_, hasLookup := msg.field(0x1f42)
	_, hasName := msg.field(0x0010)
	return hasIP || hasLookup || hasName
}

type novaObject struct {
	id  uint32
	msg *novaMessage
}

type novaNode struct {
	mapID    uint32
	deviceID uint32
	x, y     float64
}

// parseNovaSQLite reads The Dude's modern objs table. It returns ok=false when
// the table exists but does not contain any decodable Nova object, so the
// caller can fall back to the legacy relational parser.
func parseNovaSQLite(ctx context.Context, filename string, db *sql.DB, objsTable string) (Preview, bool, error) {
	query := `SELECT id, obj FROM "` + strings.ReplaceAll(objsTable, `"`, `""`) + `" ORDER BY id LIMIT 200000`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return Preview{}, false, err
	}
	defer rows.Close()

	var devices []novaObject
	var maps []novaObject
	var nodes []novaObject
	var links []novaObject
	deviceTypes := map[uint32]string{}
	linkTypes := map[uint32]string{}
	snmpProfiles := map[uint32]SNMPProfile{}
	decoded, invalid := 0, 0
	for rows.Next() {
		var rawID int64
		var blob []byte
		if err := rows.Scan(&rawID, &blob); err != nil {
			continue
		}
		msg, ok := decodeNova(blob)
		if !ok || msg == nil {
			invalid++
			continue
		}
		decoded++
		id := uint32(rawID)
		obj := novaObject{id: id, msg: msg}
		switch {
		case isNovaDevice(id, msg):
			devices = append(devices, obj)
		case msg.hasRange(0x61a8, 0x61fa):
			maps = append(maps, obj)
		case msg.hasRange(0x5dc0, 0x5ddf):
			nodes = append(nodes, obj)
		case msg.hasRange(0x55f0, 0x55f9):
			links = append(links, obj)
		case msg.hasRange(0x2710, 0x271f):
			if name := msg.str(0x0010); name != "" {
				deviceTypes[id] = name
			}
		case msg.hasRange(0x59d8, 0x59db):
			if name := msg.str(0x0010); name != "" {
				linkTypes[id] = name
			}
		case msg.hasRange(0x3c68, 0x3c72):
			if profile, ok := parseNovaSNMPProfile(id, msg); ok {
				snmpProfiles[id] = profile
			}
		}
	}
	if err := rows.Err(); err != nil {
		return Preview{}, false, err
	}
	if decoded == 0 {
		if invalid > 0 {
			return Preview{}, false, fmt.Errorf("la tabla objs contiene datos, pero ningún objeto Nova se pudo decodificar")
		}
		return Preview{}, false, nil
	}

	p := Preview{SourceKind: "sqlite", Filename: filename, Maps: []Map{}, Devices: []Device{}, Submaps: []Submap{}, Decorations: []Decoration{}, Links: []Link{}, SNMPProfiles: []SNMPProfile{}}
	for _, obj := range maps {
		name := obj.msg.str(0x0010)
		if name == "" {
			name = fmt.Sprintf("Mapa Dude %d", obj.id)
		}
		p.Maps = append(p.Maps, Map{SourceID: novaID(obj.id), Name: name})
	}

	mapSet := map[uint32]bool{}
	mapOrder := make([]uint32, 0, len(maps))
	for _, obj := range maps {
		mapSet[obj.id] = true
		mapOrder = append(mapOrder, obj.id)
	}
	deviceSet := map[uint32]bool{}
	for _, obj := range devices {
		deviceSet[obj.id] = true
	}

	type deviceMapKey struct {
		deviceID uint32
		mapID    uint32
	}
	deviceNode := map[deviceMapKey]uint32{}
	firstDeviceNode := map[uint32]uint32{}
	nodeByID := map[uint32]novaNode{}
	submapByNode := map[uint32]uint32{}
	linkLabels := map[uint32][2]uint32{}
	for _, obj := range nodes {
		mapID, okMap := obj.msg.u32(0x5dc0)
		if !okMap || mapID == 0xffffffff || !mapSet[mapID] {
			continue
		}
		deviceID, okDevice := obj.msg.u32(0x5dc4)
		if !okDevice {
			deviceID = 0xffffffff
		}
		x, _ := obj.msg.u32(0x5dc5)
		y, _ := obj.msg.u32(0x5dc6)
		nodeByID[obj.id] = novaNode{mapID: mapID, deviceID: deviceID, x: float64(x), y: float64(y)}
		switch {
		case deviceSet[deviceID]:
			key := deviceMapKey{deviceID: deviceID, mapID: mapID}
			if _, exists := deviceNode[key]; !exists {
				deviceNode[key] = obj.id
			}
			if _, exists := firstDeviceNode[deviceID]; !exists {
				firstDeviceNode[deviceID] = obj.id
			}
		case mapSet[deviceID] && deviceID != mapID:
			submapByNode[obj.id] = deviceID
		default:
			// Link labels carry the two real endpoint nodes plus the link ID.
			if linkID, ok := obj.msg.u32(0x5ddd); ok && linkID != 0xffffffff {
				aNode, okA := obj.msg.u32(0x5ddb)
				bNode, okB := obj.msg.u32(0x5ddc)
				if okA && okB && aNode != 0xffffffff && bNode != 0xffffffff {
					linkLabels[obj.id] = [2]uint32{aNode, bNode}
				}
			}
		}
	}

	for _, obj := range devices {
		var address string
		for _, value := range obj.msg.u32s(0x1f40) {
			if ip := ipv4FromU32(value); ip != "" {
				address = ip
				break
			}
		}
		dnsNames := obj.msg.strs(0x1f41)
		dns := ""
		if len(dnsNames) > 0 {
			dns = strings.TrimSpace(dnsNames[0])
		}
		name := obj.msg.str(0x0010)
		if address == "" {
			address = dns
		}
		if address == "" {
			address = name
		}
		if name == "" {
			name = address
		}
		deviceType := ""
		if typeID, ok := obj.msg.u32(0x1f4c); ok && typeID != 0xffffffff {
			deviceType = deviceTypes[typeID]
		}
		var notes []string
		for _, tag := range []uint16{0x1f58, 0x1f59, 0x1f5a} {
			if text := obj.msg.str(tag); text != "" {
				notes = append(notes, text)
			}
		}
		username := obj.msg.rawStr(0x1f46)
		password := obj.msg.rawStr(0x1f47)
		device := Device{
			SourceID:    novaID(obj.id),
			Name:        name,
			Address:     cleanAddress(address),
			DNSName:     dns,
			MAC:         novaMAC(obj.msg),
			DeviceType:  normalizeType(deviceType),
			Notes:       strings.Join(notes, " · "),
			Username:    username,
			Password:    password,
			HasPassword: password != "",
		}
		if profileID, ok := obj.msg.u32(0x1f4e); ok && profileID != 0xffffffff {
			device.SNMPProfileSourceID = novaID(profileID)
			if profile, exists := snmpProfiles[profileID]; exists {
				device.SNMPProfileName = profile.Name
			}
		}
		if nodeID, ok := firstDeviceNode[obj.id]; ok {
			node := nodeByID[nodeID]
			device.NodeID = novaID(nodeID)
			device.SourceMapID = novaID(node.mapID)
			device.MapName = mapName(p.Maps, device.SourceMapID)
			device.PosX, device.PosY = node.x, node.y
		}
		if device.Address == "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("El dispositivo %q no tiene dirección; se omitirá al aplicar.", device.Name))
		}
		p.Devices = append(p.Devices, device)
	}
	for _, profile := range snmpProfiles {
		p.SNMPProfiles = append(p.SNMPProfiles, profile)
	}
	sortSNMPProfiles(p.SNMPProfiles)

	// Build the map hierarchy from the submap nodes. The Dude usually adds a
	// node back to the parent map inside every child map, so a plain "referenced
	// map is a child" rule creates cycles. Pick the map with no incoming
	// references as the root; if every map is referenced, prefer the map that
	// contains the most distinct submaps.
	adjacency := map[uint32]map[uint32]bool{}
	for nodeID, target := range submapByNode {
		source := nodeByID[nodeID].mapID
		if source == target {
			continue
		}
		if adjacency[source] == nil {
			adjacency[source] = map[uint32]bool{}
		}
		adjacency[source][target] = true
	}
	incoming := map[uint32]bool{}
	for _, targets := range adjacency {
		for target := range targets {
			incoming[target] = true
		}
	}
	childrenOf := func(mapID uint32) []uint32 {
		set := adjacency[mapID]
		if len(set) == 0 {
			return nil
		}
		out := make([]uint32, 0, len(set))
		for _, candidate := range mapOrder {
			if set[candidate] {
				out = append(out, candidate)
			}
		}
		return out
	}
	parentOf := map[uint32]uint32{}
	visitedMaps := map[uint32]bool{}
	assignTree := func(root uint32) {
		if visitedMaps[root] {
			return
		}
		visitedMaps[root] = true
		queue := []uint32{root}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, child := range childrenOf(current) {
				if visitedMaps[child] {
					continue
				}
				visitedMaps[child] = true
				parentOf[child] = current
				queue = append(queue, child)
			}
		}
	}
	var roots []uint32
	for _, id := range mapOrder {
		if !incoming[id] {
			roots = append(roots, id)
		}
	}
	if len(roots) == 0 && len(mapOrder) > 0 {
		best, bestDegree := mapOrder[0], -1
		for _, id := range mapOrder {
			if degree := len(adjacency[id]); degree > bestDegree {
				best, bestDegree = id, degree
			}
		}
		roots = append(roots, best)
	}
	for _, root := range roots {
		assignTree(root)
	}
	for _, id := range mapOrder {
		assignTree(id)
	}
	for i := range p.Maps {
		raw := strings.TrimPrefix(p.Maps[i].SourceID, "dude-")
		id64, _ := strconv.ParseUint(raw, 10, 32)
		if parent, ok := parentOf[uint32(id64)]; ok {
			p.Maps[i].ParentSourceID = novaID(parent)
		}
	}

	for _, obj := range nodes {
		target, ok := submapByNode[obj.id]
		if !ok {
			continue
		}
		node := nodeByID[obj.id]
		name := mapName(p.Maps, novaID(target))
		if name == "" {
			name = obj.msg.str(0x0010)
		}
		name = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(name, "\r\n", "\n"), "\r", "\n"))
		if name == "" {
			name = "Submapa Dude"
		}
		if len(name) > 240 {
			name = name[:240]
		}
		p.Submaps = append(p.Submaps, Submap{
			SourceID: novaID(obj.id), MapID: novaID(node.mapID), TargetSourceID: novaID(target),
			Name: name, PosX: node.x, PosY: node.y, Width: 190, Height: 110,
		})
	}

	decorationPositions := map[string]int{}
	for _, obj := range nodes {
		node, ok := nodeByID[obj.id]
		if !ok {
			continue
		}
		if deviceSet[node.deviceID] {
			continue // placement of a real device
		}
		if _, ok := submapByNode[obj.id]; ok {
			continue // handled as a submap element
		}
		if _, ok := linkLabels[obj.id]; ok {
			continue // the link is drawn by NetMonitor; no text label needed
		}
		name := obj.msg.str(0x0010)
		if name == "" && node.deviceID != 0xffffffff {
			name = mapName(p.Maps, novaID(node.deviceID))
		}
		name = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(name, "\r\n", "\n"), "\r", "\n"))
		if name == "" {
			name = "Elemento Dude"
		}
		if len(name) > 240 {
			name = name[:240]
		}
		// Dude can place several annotations at the exact same point. Offset
		// duplicates slightly so they remain readable in NetMonitor.
		posKey := fmt.Sprintf("%d:%.0f:%.0f", node.mapID, node.x, node.y)
		offset := float64(decorationPositions[posKey] * 22)
		decorationPositions[posKey]++
		p.Decorations = append(p.Decorations, Decoration{
			SourceID: novaID(obj.id), MapID: novaID(node.mapID), Name: name,
			PosX: node.x, PosY: node.y + offset, Width: 180, Height: 60,
		})
	}

	for _, obj := range links {
		aSide, okA := obj.msg.u32(0x55f1)
		bSide, okB := obj.msg.u32(0x55f5)
		if !okA || !okB || aSide == 0xffffffff || bSide == 0xffffffff {
			continue
		}
		mapID, _ := obj.msg.u32(0x55f4)
		var aNode, bNode uint32
		if label, ok := linkLabels[bSide]; ok {
			aNode, bNode = label[0], label[1]
		} else if bInfo, ok := nodeByID[bSide]; ok {
			bNode = bSide
			if _, ok := nodeByID[aSide]; ok {
				aNode = aSide
			} else {
				aNode = deviceNode[deviceMapKey{deviceID: aSide, mapID: bInfo.mapID}]
				if aNode == 0 {
					aNode = firstDeviceNode[aSide]
				}
			}
		} else {
			continue
		}
		aInfo, aFound := nodeByID[aNode]
		bInfo, bFound := nodeByID[bNode]
		if !aFound || !bFound {
			continue
		}
		if mapID == 0xffffffff || !mapSet[mapID] || aInfo.mapID != bInfo.mapID {
			mapID = aInfo.mapID
		}
		deviceA, deviceB := "", ""
		if deviceSet[aInfo.deviceID] {
			deviceA = novaID(aInfo.deviceID)
		}
		if deviceSet[bInfo.deviceID] {
			deviceB = novaID(bInfo.deviceID)
		}
		if deviceA == "" && deviceB == "" {
			continue
		}
		linkTypeID, _ := obj.msg.u32(0x55f6)
		speed, _ := obj.msg.u64(0x55f3)
		link := Link{
			SourceID:    novaID(obj.id),
			SourceMapID: novaID(mapID),
			NodeA:       novaID(aNode),
			NodeB:       novaID(bNode),
			DeviceA:     deviceA,
			DeviceB:     deviceB,
			LinkType:    normalizeLinkType(linkTypes[linkTypeID]),
			SpeedBps:    int64(speed),
		}
		masteringType, _ := obj.msg.u32(0x55f0)
		masterDevice, hasMasterDevice := obj.msg.u32(0x55f1)
		masterInterface, hasMasterInterface := obj.msg.u32(0x55f2)
		if masteringType != 0xffffffff {
			link.MasteringType = int(masteringType)
		}
		if hasMasterDevice && masterDevice != 0xffffffff {
			link.MasterDevice = novaID(masterDevice)
		}
		if hasMasterInterface && masterInterface != 0xffffffff {
			link.MasterInterface = int(masterInterface)
		}
		if link.SpeedBps <= 0 {
			link.SpeedBps = 1000000000
		}
		p.Links = append(p.Links, link)
	}

	if len(p.Devices) == 0 {
		p.Warnings = append(p.Warnings, "La tabla objs no contiene dispositivos de The Dude; revisa que la base no esté vacía.")
	}
	finalizePreview(&p)
	return p, true, nil
}

// finalizePreview fills the default map, assigns unmapped devices, counts
// devices per map and sorts the preview by device name.
func finalizePreview(p *Preview) {
	if len(p.Maps) == 0 {
		p.Maps = append(p.Maps, Map{SourceID: "imported", Name: "Importado desde The Dude"})
	}
	for i := range p.Devices {
		if p.Devices[i].SourceMapID != "" {
			continue
		}
		for _, m := range p.Maps {
			if strings.EqualFold(m.Name, p.Devices[i].MapName) {
				p.Devices[i].SourceMapID = m.SourceID
				break
			}
		}
		if p.Devices[i].SourceMapID == "" {
			p.Devices[i].SourceMapID = p.Maps[0].SourceID
		}
	}
	for i := range p.Maps {
		p.Maps[i].DeviceCount = 0
	}
	for _, d := range p.Devices {
		for i := range p.Maps {
			if d.SourceMapID == p.Maps[i].SourceID {
				p.Maps[i].DeviceCount++
				break
			}
		}
	}
	sort.Slice(p.Devices, func(i, j int) bool {
		return strings.ToLower(p.Devices[i].Name) < strings.ToLower(p.Devices[j].Name)
	})
	if len(p.Links) > 0 {
		knownDevices := map[string]bool{}
		for _, device := range p.Devices {
			knownDevices[device.SourceID] = true
		}
		knownElements := map[string]bool{}
		for _, decoration := range p.Decorations {
			knownElements[decoration.SourceID] = true
		}
		for _, submap := range p.Submaps {
			knownElements[submap.SourceID] = true
		}
		orphanLinks := 0
		for _, link := range p.Links {
			aOK := knownDevices[link.DeviceA] || knownElements[link.NodeA] || knownElements[link.DeviceA]
			bOK := knownDevices[link.DeviceB] || knownElements[link.NodeB] || knownElements[link.DeviceB]
			if !aOK || !bOK {
				orphanLinks++
			}
		}
		if orphanLinks > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%d enlaces no se podrán crear porque uno de sus extremos no es un dispositivo ni un elemento importable.", orphanLinks))
		}
	}
}
