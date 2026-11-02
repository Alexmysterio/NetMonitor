package dude

import (
	"context"
	"database/sql"
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
