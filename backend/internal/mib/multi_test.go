package mib

import "testing"

func TestParseManyCrossModule(t *testing.T) {
	base := `
TEST-MIB DEFINITIONS ::= BEGIN
rootNode OBJECT IDENTIFIER ::= { enterprises 99999 }
END`
	child := `
TEST-CHILD-MIB DEFINITIONS ::= BEGIN
IMPORTS rootNode FROM TEST-MIB;
childValue OBJECT-TYPE
    SYNTAX Integer32
    MAX-ACCESS read-only
    STATUS current
    DESCRIPTION "child"
    ::= { rootNode 1 }
END`
	out, err := ParseMany(map[string]string{"TEST-MIB": base, "TEST-CHILD-MIB": child})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["TEST-CHILD-MIB"]) != 1 || out["TEST-CHILD-MIB"][0].OID != "1.3.6.1.4.1.99999.1" {
		t.Fatalf("unexpected objects: %+v", out)
	}
	if len(Imports(child)) != 1 || Imports(child)[0] != "TEST-MIB" {
		t.Fatalf("imports = %v", Imports(child))
	}
}
