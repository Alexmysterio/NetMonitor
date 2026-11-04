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
	for _, d := range p.Devices {
		if d.SourceID == "d1" && (d.PosX != 12 || d.PosY != 34 || d.SourceMapID != "m1") {
			t.Fatalf("coordinates not parsed: %+v", d)
		}
	}
}

type novaTestField struct {
	tag   uint16
	tcode byte
	value []byte
}

func novaTestBlob(fields ...novaTestField) []byte {
	var buf bytes.Buffer
	buf.Write(novaMagic)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(fields)))
	for _, field := range fields {
		_ = binary.Write(&buf, binary.LittleEndian, field.tag)
		buf.WriteByte(0x10)
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
		novaTestField{tag: 0x1f4c, tcode: 0x08, value: novaTestU32(0xffffffff)},
		novaTestField{tag: 0x0001, tcode: 0x08, value: novaTestU32(42)},
		novaTestField{tag: 0x0010, tcode: 0x21, value: novaTestString("Router")},
	)
	if _, err := db.Exec(`INSERT INTO objs (id, obj) VALUES (?,?), (?,?), (?,?)`,
		10, mapBlob, 11, nodeBlob, 42, deviceBlob); err != nil {
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
	if p.Maps[0].Name != "Sede" || p.Maps[0].DeviceCount != 1 {
		t.Fatalf("unexpected Nova map: %+v", p.Maps[0])
	}
}
