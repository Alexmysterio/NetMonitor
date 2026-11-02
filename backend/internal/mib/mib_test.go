package mib

import "testing"

const sample = `
TEST-MIB DEFINITIONS ::= BEGIN
IMPORTS
    enterprises FROM RFC1155-SMI;

testModule OBJECT IDENTIFIER ::= { enterprises 99999 }

testTable OBJECT-TYPE
    SYNTAX SEQUENCE OF TestEntry
    MAX-ACCESS not-accessible
    STATUS current
    DESCRIPTION "A table."
    ::= { testModule 1 }

testEntry OBJECT-TYPE
    SYNTAX TestEntry
    MAX-ACCESS not-accessible
    STATUS current
    DESCRIPTION "Entry."
    ::= { testTable 1 }

testName OBJECT-TYPE
    SYNTAX DisplayString
    MAX-ACCESS read-only
    STATUS current
    DESCRIPTION "A test name."
    ::= { testEntry 1 }

testValue OBJECT-TYPE
    SYNTAX INTEGER
    MAX-ACCESS read-only
    STATUS current
    DESCRIPTION "A test value."
    ::= { testEntry 2 }
END
`

func TestParse(t *testing.T) {
	objects, err := Parse("TEST-MIB", sample)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Object{}
	for _, o := range objects {
		got[o.Name] = o
	}
	if got["testName"].OID != "1.3.6.1.4.1.99999.1.1.1" {
		t.Fatalf("testName OID = %q", got["testName"].OID)
	}
	if got["testValue"].OID != "1.3.6.1.4.1.99999.1.1.2" {
		t.Fatalf("testValue OID = %q", got["testValue"].OID)
	}
	if got["testName"].Syntax != "DisplayString" || got["testName"].Access != "read-only" {
		t.Fatalf("metadata not parsed: %+v", got["testName"])
	}
}

func TestParseNotifications(t *testing.T) {
	text := `
TEST-MIB DEFINITIONS ::= BEGIN
IMPORTS
    enterprises FROM RFC1155-SMI
    NOTIFICATION-TYPE, OBJECT-TYPE, Integer32 FROM SNMPv2-SMI;
testModule OBJECT IDENTIFIER ::= { enterprises 99999 }
testValue OBJECT-TYPE
    SYNTAX Integer32
    MAX-ACCESS read-only
    STATUS current
    DESCRIPTION "A value."
    ::= { testModule 2 }
testNotification NOTIFICATION-TYPE
    OBJECTS { testValue }
    STATUS current
    DESCRIPTION "Trap de prueba"
    ::= { testModule 1 }
END
`
	all := ParseNotificationsMany(map[string]string{"TEST-MIB": text})
	list := all["TEST-MIB"]
	if len(list) != 1 {
		t.Fatalf("expected one notification, got %+v", list)
	}
	if list[0].Name != "testNotification" || list[0].OID != "1.3.6.1.4.1.99999.1" {
		t.Fatalf("notification not resolved: %+v", list[0])
	}
	if len(list[0].Objects) != 1 || list[0].Objects[0] != "testValue" {
		t.Fatalf("objects not parsed: %+v", list[0].Objects)
	}
	if list[0].Description != "Trap de prueba" {
		t.Fatalf("description not parsed: %q", list[0].Description)
	}
}
