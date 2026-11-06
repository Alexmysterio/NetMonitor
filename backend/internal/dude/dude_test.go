package dude

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestParseXML(t *testing.T) {
	xmlData := []byte(`<dude>
  <map id="m1" name="Sede">
    <device id="d1" name="Router" address="10.0.0.1" type="router" x="10" y="20" />
    <device id="d2" name="CPE" address="10.0.0.2" type="access_point" x="30" y="40" />
    <link id="l1" device1="d1" device2="d2" type="wireless" speed="100000000" />
  </map>
</dude>`)
	p, err := Parse(context.Background(), "dude.xml", xmlData)
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceKind != "xml" || len(p.Maps) != 1 || len(p.Devices) != 2 || len(p.Links) != 1 {
		t.Fatalf("unexpected preview: %+v", p)
	}
	if p.SNMPProfiles == nil {
		t.Fatal("SNMP profiles should be an empty slice, not nil")
	}
	if p.Devices[0].SourceMapID != "m1" || p.Devices[0].DeviceType != "access_point" {
		t.Fatalf("device map/type not parsed: %+v", p.Devices[0])
	}
	if p.Links[0].LinkType != "wireless" {
		t.Fatalf("link type not parsed: %+v", p.Links[0])
	}
}

func TestParseSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dude.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE map (id TEXT PRIMARY KEY, name TEXT, parent TEXT)`,
		`CREATE TABLE device (id TEXT PRIMARY KEY, name TEXT, address TEXT, type TEXT, vendor TEXT)`,
		`CREATE TABLE map_object (map TEXT, device TEXT, x REAL, y REAL)`,
		`CREATE TABLE link (id TEXT PRIMARY KEY, map TEXT, device1 TEXT, device2 TEXT, type TEXT, speed INTEGER)`,
		`INSERT INTO map VALUES ('m1','Sede','')`,
		`INSERT INTO device VALUES ('d1','Router','10.0.0.1','router','MikroTik')`,
		`INSERT INTO device VALUES ('d2','Switch','10.0.0.2','switch','MikroTik')`,
		`INSERT INTO map_object VALUES ('m1','d1',12,34)`,
		`INSERT INTO map_object VALUES ('m1','d2',56,78)`,
		`INSERT INTO link VALUES ('l1','m1','d1','d2','ethernet',1000000000)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(context.Background(), "dude.db", data)
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceKind != "sqlite" || len(p.Maps) != 1 || len(p.Devices) != 2 || len(p.Links) != 1 {
		t.Fatalf("unexpected preview: %+v", p)
	}
	if p.SNMPProfiles == nil {
		t.Fatal("SNMP profiles should be an empty slice, not nil")
	}
	for _, d := range p.Devices {
		if d.SourceID == "d1" && (d.PosX != 12 || d.PosY != 34 || d.SourceMapID != "m1") {
			t.Fatalf("coordinates not parsed: %+v", d)
		}
	}
}

func TestParseSQLiteCredentialsAndSNMP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dude.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE device (id TEXT PRIMARY KEY, name TEXT, address TEXT, type TEXT, username TEXT, password TEXT, snmp_profile TEXT)`,
		`CREATE TABLE snmp_profile (id TEXT PRIMARY KEY, name TEXT, version TEXT, port INTEGER, timeout_ms INTEGER, retries INTEGER, community TEXT)`,
		`CREATE TABLE link (id TEXT PRIMARY KEY, map TEXT, device1 TEXT, device2 TEXT, type TEXT, speed INTEGER, mastering_type INTEGER, master_device TEXT, master_interface INTEGER)`,
		`INSERT INTO device VALUES ('d1','Router','10.0.0.1','router',' admin ',' secret ','p1')`,
		`INSERT INTO device VALUES ('d2','Switch','10.0.0.2','switch','','','Shared')`,
		// p2 intentionally shares its name with p1's source ID to exercise
		// source-ID-first resolution. Its missing columns exercise defaults.
		`INSERT INTO snmp_profile VALUES ('p1','Shared','3',1161,2500,4,' public ')`,
		`INSERT INTO snmp_profile (id,name) VALUES ('p2','p1')`,
		`INSERT INTO link VALUES ('l1','map-1','d1','d2','ethernet',1000000000,2,'d1',7)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(context.Background(), "dude.db", data)
	if err != nil {
		t.Fatal(err)
	}
	if p.SNMPProfiles == nil || len(p.SNMPProfiles) != 2 {
		t.Fatalf("unexpected SNMP profiles: %+v", p.SNMPProfiles)
	}
	if p.SNMPProfiles[0].SourceID != "p2" || p.SNMPProfiles[1].SourceID != "p1" {
		t.Fatalf("SNMP profiles are not sorted by name: %+v", p.SNMPProfiles)
	}
	defaults := p.SNMPProfiles[0]
	if defaults.Name != "p1" || defaults.Version != "v2c" || defaults.Port != 161 ||
		defaults.TimeoutMS != 1500 || defaults.Retries != 2 || defaults.HasCommunity ||
		defaults.Community != "" {
		t.Fatalf("unexpected SNMP defaults: %+v", defaults)
	}
	custom := p.SNMPProfiles[1]
	if custom.Name != "Shared" || custom.Version != "v3" || custom.Port != 1161 ||
		custom.TimeoutMS != 2500 || custom.Retries != 4 || !custom.HasCommunity ||
		custom.Community != " public " {
		t.Fatalf("unexpected custom SNMP profile: %+v", custom)
	}
	devices := map[string]Device{}
	for _, d := range p.Devices {
		devices[d.SourceID] = d
	}
	router := devices["d1"]
	if router.Username != " admin " || router.Password != " secret " || !router.HasPassword {
		t.Fatalf("unexpected legacy credentials: %+v", router)
	}
	if router.SNMPProfileSourceID != "p1" || router.SNMPProfileName != "Shared" {
		t.Fatalf("unexpected source-ID SNMP resolution: %+v", router)
	}
	switchDevice := devices["d2"]
	if switchDevice.SNMPProfileSourceID != "p1" || switchDevice.SNMPProfileName != "Shared" {
		t.Fatalf("unexpected name SNMP resolution: %+v", switchDevice)
	}
	if len(p.Links) != 1 {
		t.Fatalf("unexpected links: %+v", p.Links)
	}
	link := p.Links[0]
	if link.MasteringType != 2 || link.MasterDevice != "d1" || link.MasterInterface != 7 {
		t.Fatalf("unexpected link mastering: %+v", link)
	}
}

func TestParseXMLCredentialsAndSNMP(t *testing.T) {
	xmlData := []byte(`<dude>
  <snmp_profile id="61" name="Alpha" version="1" />
  <snmp_profile id="60" name="v2-public" version="2" port="1161" timeout="3000" retries="0">
    <community> public </community>
  </snmp_profile>
  <device id="d1" name="Router" address="10.0.0.1">
    <username> admin </username>
    <password> secret </password>
    <snmp_profile id="60"/>
  </device>
  <device id="d2" name="Switch" address="10.0.0.2">
    <snmp_profile>v2-public</snmp_profile>
  </device>
  <link id="l1" device1="d1" device2="d2" mastering_type="2">
    <master_device>d1</master_device>
    <master_interface>7</master_interface>
  </link>
</dude>`)
	p, err := Parse(context.Background(), "dude.xml", xmlData)
	if err != nil {
		t.Fatal(err)
	}
	if p.SNMPProfiles == nil || len(p.SNMPProfiles) != 2 {
		t.Fatalf("bare nested reference created a profile: %+v", p.SNMPProfiles)
	}
	if p.SNMPProfiles[0].SourceID != "61" || p.SNMPProfiles[1].SourceID != "60" {
		t.Fatalf("SNMP profiles are not sorted by name: %+v", p.SNMPProfiles)
	}
	xmlDefaults := p.SNMPProfiles[0]
	if xmlDefaults.Name != "Alpha" || xmlDefaults.Version != "v1" || xmlDefaults.Port != 161 ||
		xmlDefaults.TimeoutMS != 1500 || xmlDefaults.Retries != 2 || xmlDefaults.HasCommunity {
		t.Fatalf("unexpected XML SNMP defaults: %+v", xmlDefaults)
	}
	profile := p.SNMPProfiles[1]
	if profile.SourceID != "60" || profile.Name != "v2-public" || profile.Version != "v2c" ||
		profile.Port != 1161 || profile.TimeoutMS != 3000 || profile.Retries != 0 ||
		profile.Community != " public " || !profile.HasCommunity {
		t.Fatalf("unexpected XML SNMP profile: %+v", profile)
	}
	if len(p.Devices) != 2 {
		t.Fatalf("unexpected devices: %+v", p.Devices)
	}
	d := p.Devices[0]
	if d.Username != " admin " || d.Password != " secret " || !d.HasPassword {
		t.Fatalf("unexpected XML credentials: %+v", d)
	}
	if d.SNMPProfileSourceID != "60" || d.SNMPProfileName != "v2-public" {
		t.Fatalf("nested SNMP reference not resolved: %+v", d)
	}
	switchDevice := p.Devices[1]
	if switchDevice.SNMPProfileSourceID != "60" || switchDevice.SNMPProfileName != "v2-public" {
		t.Fatalf("name SNMP reference not normalized: %+v", switchDevice)
	}
	if len(p.Links) != 1 {
		t.Fatalf("unexpected links: %+v", p.Links)
	}
	link := p.Links[0]
	if link.MasteringType != 2 || link.MasterDevice != "d1" || link.MasterInterface != 7 {
		t.Fatalf("unexpected XML link mastering: %+v", link)
	}
}

type novaTestField struct {
	tag    uint16
	marker byte
	tcode  byte
	value  []byte
}

func novaTestBlob(fields ...novaTestField) []byte {
	var buf bytes.Buffer
	buf.Write(novaMagic)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(fields)))
	for _, field := range fields {
		marker := field.marker
		if marker == 0 {
			marker = 0x10
		}
		_ = binary.Write(&buf, binary.LittleEndian, field.tag)
		buf.WriteByte(marker)
		buf.WriteByte(field.tcode)
		buf.Write(field.value)
	}
	return buf.Bytes()
}

func novaTestU32(value uint32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, value)
	return out
}

func novaTestString(value string) []byte {
	return append([]byte{byte(len(value))}, []byte(value)...)
}

func novaTestU32Array(values ...uint32) []byte {
	out := make([]byte, 2+4*len(values))
	binary.LittleEndian.PutUint16(out, uint16(len(values)))
	for i, value := range values {
		binary.LittleEndian.PutUint32(out[2+4*i:], value)
	}
	return out
}

func novaTestIPv4(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

func TestDecodeNovaMarker11(t *testing.T) {
	blob := novaTestBlob(
		novaTestField{tag: 0x3c72, marker: 0x11, tcode: 0x08, value: novaTestU32(3000)},
		novaTestField{tag: 0x3c69, marker: 0x11, tcode: 0x21, value: novaTestString("public")},
		novaTestField{tag: 0x0010, marker: 0xfe, tcode: 0x21, value: novaTestString("v2-public")},
	)
	msg, ok := decodeNova(blob)
	if !ok {
		t.Fatal("blob was not decoded")
	}
	if timeout, _ := msg.u32(0x3c72); timeout != 3000 {
		t.Fatalf("unexpected timeout: %d", timeout)
	}
	if community := msg.str(0x3c69); community != "public" {
		t.Fatalf("unexpected community: %q", community)
	}
	if name := msg.str(0x0010); name != "v2-public" {
		t.Fatalf("unexpected name: %q", name)
	}
}

func TestParseNovaSNMPProfileDefaultsAndZeroRetries(t *testing.T) {
	decode := func(fields ...novaTestField) *novaMessage {
		msg, ok := decodeNova(novaTestBlob(fields...))
		if !ok {
			t.Fatal("blob was not decoded")
		}
		return msg
	}
	zeroRetries := decode(
		novaTestField{tag: 0x3c68, tcode: 0x08, value: novaTestU32(1)},
		novaTestField{tag: 0x3c71, tcode: 0x08, value: novaTestU32(0)},
		novaTestField{tag: 0x3c72, tcode: 0x08, value: novaTestU32(0)},
	)
	profile, ok := parseNovaSNMPProfile(60, zeroRetries)
	if !ok || profile.Retries != 0 || profile.TimeoutMS != 1500 {
		t.Fatalf("unexpected zero retries/timeout handling: %+v, ok=%v", profile, ok)
	}
	absent := decode(novaTestField{tag: 0x3c68, tcode: 0x08, value: novaTestU32(1)})
	profile, ok = parseNovaSNMPProfile(61, absent)
	if !ok || profile.Retries != 2 || profile.TimeoutMS != 1500 || profile.Port != 161 {
		t.Fatalf("unexpected default handling: %+v, ok=%v", profile, ok)
	}
}

func TestParseDudeNovaSubmapsAndLinkLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dude.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE objs (id INTEGER PRIMARY KEY, obj BLOB)`); err != nil {
		t.Fatal(err)
	}
	newMap := func(id uint32, name string) []byte {
		return novaTestBlob(
			novaTestField{tag: 0x61a8, tcode: 0x08, value: novaTestU32(1)},
			novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(id)},
			novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString(name)},
		)
	}
	newNode := func(id, mapID, ref uint32) []byte {
		return novaTestBlob(
			novaTestField{tag: 0x5dc0, tcode: 0x08, value: novaTestU32(mapID)},
			novaTestField{tag: 0x5dc4, tcode: 0x08, value: novaTestU32(ref)},
			novaTestField{tag: 0x5dc5, tcode: 0x08, value: novaTestU32(100)},
			novaTestField{tag: 0x5dc6, tcode: 0x08, value: novaTestU32(200)},
			novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(id)},
			novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("")},
		)
	}
	newDevice := func(id uint32, name string, last byte) []byte {
		return novaTestBlob(
			novaTestField{tag: 0x1f40, tcode: 0x88, value: novaTestU32Array(novaTestIPv4(10, 0, 0, last))},
			novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(id)},
			novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString(name)},
		)
	}
	linkLabel := novaTestBlob(
		novaTestField{tag: 0x5dc0, tcode: 0x08, value: novaTestU32(20)},
		novaTestField{tag: 0x5dc4, tcode: 0x08, value: novaTestU32(0xffffffff)},
		novaTestField{tag: 0x5dc5, tcode: 0x08, value: novaTestU32(300)},
		novaTestField{tag: 0x5dc6, tcode: 0x08, value: novaTestU32(400)},
		novaTestField{tag: 0x5ddb, tcode: 0x08, value: novaTestU32(21)},
		novaTestField{tag: 0x5ddc, tcode: 0x08, value: novaTestU32(22)},
		novaTestField{tag: 0x5ddd, tcode: 0x08, value: novaTestU32(50)},
		novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(34)},
		novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("")},
	)
	linkBlob := novaTestBlob(
		novaTestField{tag: 0x55f0, tcode: 0x08, value: novaTestU32(2)},
		novaTestField{tag: 0x55f1, tcode: 0x08, value: novaTestU32(42)},
		novaTestField{tag: 0x55f2, tcode: 0x08, value: novaTestU32(7)},
		novaTestField{tag: 0x55f4, tcode: 0x08, value: novaTestU32(20)},
		novaTestField{tag: 0x55f5, tcode: 0x08, value: novaTestU32(34)},
		novaTestField{tag: 0x55f6, tcode: 0x08, value: novaTestU32(0xffffffff)},
		novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(50)},
	)
	objects := []struct {
		id   uint32
		blob []byte
	}{
		{10, newMap(10, "General")},
		{20, newMap(20, "Sitio A")},
		{30, newMap(30, "Sitio B")},
		{11, newNode(11, 10, 20)},
		{31, newNode(31, 10, 30)},
		{21, newNode(21, 20, 10)},
		{22, newNode(22, 20, 42)},
		{33, newNode(33, 30, 43)},
		{34, linkLabel},
		{42, newDevice(42, "Router", 42)},
		{43, newDevice(43, "Switch", 43)},
		{50, linkBlob},
	}
	for _, object := range objects {
		if _, err := db.Exec(`INSERT INTO objs (id, obj) VALUES (?,?)`, object.id, object.blob); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(context.Background(), "dude.db", data)
	if err != nil {
		t.Fatal(err)
	}
	parents := map[string]string{}
	for _, m := range p.Maps {
		parents[m.SourceID] = m.ParentSourceID
	}
	if parents["20"] != "10" || parents["30"] != "10" || parents["10"] != "" {
		t.Fatalf("unexpected hierarchy: %+v", parents)
	}
	if len(p.Devices) != 2 || len(p.Submaps) != 3 || len(p.Decorations) != 0 || len(p.Links) != 1 {
		t.Fatalf("unexpected preview: devices=%d submaps=%d decorations=%d links=%d", len(p.Devices), len(p.Submaps), len(p.Decorations), len(p.Links))
	}
	if p.SNMPProfiles == nil {
		t.Fatal("SNMP profiles should be an empty slice, not nil")
	}
	for _, submap := range p.Submaps {
		if submap.SourceID == "34" {
			t.Fatalf("link label imported as submap: %+v", submap)
		}
	}
	link := p.Links[0]
	if link.NodeA != "21" || link.NodeB != "22" || link.DeviceA != "" || link.DeviceB != "42" || link.SourceMapID != "20" {
		t.Fatalf("unexpected link endpoints: %+v", link)
	}
	if link.MasteringType != 2 || link.MasterDevice != "42" || link.MasterInterface != 7 {
		t.Fatalf("unexpected link mastering: %+v", link)
	}
}

func TestParseDudeNovaSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dude.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE objs (id INTEGER PRIMARY KEY, obj BLOB)`); err != nil {
		t.Fatal(err)
	}
	mapBlob := novaTestBlob(
		novaTestField{tag: 0x61a8, tcode: 0x08, value: novaTestU32(1)},
		novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(10)},
		novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("Sede")},
	)
	nodeBlob := novaTestBlob(
		novaTestField{tag: 0x5dc0, tcode: 0x08, value: novaTestU32(10)},
		novaTestField{tag: 0x5dc4, tcode: 0x08, value: novaTestU32(42)},
		novaTestField{tag: 0x5dc5, tcode: 0x08, value: novaTestU32(100)},
		novaTestField{tag: 0x5dc6, tcode: 0x08, value: novaTestU32(200)},
		novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(11)},
		novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("")},
	)
	deviceBlob := novaTestBlob(
		novaTestField{tag: 0x1f40, tcode: 0x88, value: novaTestU32Array(novaTestIPv4(10, 0, 0, 1))},
		novaTestField{tag: 0x1f46, marker: 0x11, tcode: 0x21, value: novaTestString(" admin ")},
		novaTestField{tag: 0x1f47, marker: 0x11, tcode: 0x21, value: novaTestString(" secret ")},
		novaTestField{tag: 0x1f4c, tcode: 0x08, value: novaTestU32(0xffffffff)},
		novaTestField{tag: 0x1f4e, tcode: 0x08, value: novaTestU32(60)},
		novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(42)},
		novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("Router")},
	)
	profileBlob := novaTestBlob(
		novaTestField{tag: 0x3c68, tcode: 0x08, value: novaTestU32(1)},
		novaTestField{tag: 0x3c69, marker: 0x11, tcode: 0x21, value: novaTestString(" public ")},
		novaTestField{tag: 0x3c6a, tcode: 0x08, value: novaTestU32(1161)},
		novaTestField{tag: 0x3c71, tcode: 0x08, value: novaTestU32(3)},
		novaTestField{tag: 0x3c72, marker: 0x11, tcode: 0x08, value: novaTestU32(3000)},
		novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("v2-public")},
	)
	if _, err := db.Exec(`INSERT INTO objs (id, obj) VALUES (?,?), (?,?), (?,?), (?,?)`,
		10, mapBlob, 11, nodeBlob, 42, deviceBlob, 60, profileBlob); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(context.Background(), "dude.db", data)
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceKind != "sqlite" || len(p.Maps) != 1 || len(p.Devices) != 1 {
		t.Fatalf("unexpected preview: %+v", p)
	}
	d := p.Devices[0]
	if d.Name != "Router" || d.Address != "10.0.0.1" || d.SourceMapID != "10" || d.PosX != 100 || d.PosY != 200 {
		t.Fatalf("unexpected Nova device: %+v", d)
	}
	if d.Username != " admin " || d.Password != " secret " || !d.HasPassword {
		t.Fatalf("unexpected device credentials: %+v", d)
	}
	if d.SNMPProfileSourceID != "60" || d.SNMPProfileName != "v2-public" {
		t.Fatalf("unexpected device SNMP reference: %+v", d)
	}
	if len(p.SNMPProfiles) != 1 {
		t.Fatalf("expected one SNMP profile, got %+v", p.SNMPProfiles)
	}
	profile := p.SNMPProfiles[0]
	if profile.SourceID != "60" || profile.Name != "v2-public" || profile.Version != "v2c" ||
		profile.Port != 1161 || profile.TimeoutMS != 3000 || profile.Retries != 3 ||
		profile.Community != " public " || !profile.HasCommunity {
		t.Fatalf("unexpected SNMP profile: %+v", profile)
	}
	if p.Maps[0].Name != "Sede" || p.Maps[0].DeviceCount != 1 {
		t.Fatalf("unexpected Nova map: %+v", p.Maps[0])
	}
}
