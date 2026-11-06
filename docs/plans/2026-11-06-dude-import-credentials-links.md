# Dude Credentials and Link Monitoring Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Import RouterOS credentials and SNMP profiles from The Dude, assign them automatically, and create SNMP-monitored links from Dude master-interface metadata.

**Architecture:** Extend the existing Dude preview/apply pipeline. The parser reads Nova credentials, SNMP profiles and link master fields. The preview handler encrypts secrets before persisting the JSON payload and strips ciphertext from API responses. The apply handler creates/reuses RouterOS and SNMP profiles, assigns them to imported devices, and writes `map_links` with `mastering_type='snmp'` when a master device and interface are available.

**Tech Stack:** Go 1.23, pgx, modernc.org/sqlite, React 18 + TypeScript, existing `secure` AES-GCM helper.

---

## Task 1: Fix Nova marker 0x11 decoding

**Files:**
- Modify: `backend/internal/dude/nova.go:83-88`
- Modify: `backend/internal/dude/dude_test.go:88-118` (test helpers)
- Test: `backend/internal/dude/dude_test.go`

**Step 1: Add a failing test**

Extend `novaTestField` with a `marker byte` field and make `novaTestBlob` default it to `0x10`:

```go
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
```

Add this test near the other Nova tests:

```go
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
```

**Step 2: Run the test and verify it fails**

Run:

```powershell
cd C:\Users\Alex\Desktop\NetworkMonitor\backend
go test ./internal/dude -run TestDecodeNovaMarker11 -v
```

Expected: FAIL because `readNovaValue` returns a one-byte value for marker `0x11` and misaligns the rest of the blob.

**Step 3: Fix the decoder**

In `backend/internal/dude/nova.go`, remove the unconditional marker `0x11` branch:

```go
func readNovaValue(data []byte, pos int, marker, tcode byte, tag uint16) (novaValue, int, error) {
	// Dude uses marker 0x11 for ordinary tcode values in continuation
	// fields (u32, strings, etc.). The tcode still determines the width.
	switch tcode {
	// ... existing switch unchanged ...
	}
}
```

Keep the `marker` parameter so callers do not change. The marker is validated by `decodeNova` before this function is called.

**Step 4: Run the test and verify it passes**

Run:

```powershell
go test ./internal/dude -run TestDecodeNovaMarker11 -v
```

Expected: PASS.

**Step 5: Run the full Dude test package**

```powershell
go test ./internal/dude -v
```

Expected: PASS.

**Step 6: Commit**

```powershell
git add backend/internal/dude/nova.go backend/internal/dude/dude_test.go
git commit -m "Fix Nova marker 0x11 decoding"
```

---

## Task 2: Extend the Dude model and parser

**Files:**
- Modify: `backend/internal/dude/dude.go` (types, legacy SQLite, XML)
- Modify: `backend/internal/dude/nova.go` (Nova parser)
- Test: `backend/internal/dude/dude_test.go`

**Step 1: Add model fields**

In `backend/internal/dude/dude.go`, extend `Device`:

```go
type Device struct {
	// ... existing fields ...
	Username            string `json:"username"`
	Password            string `json:"-"`
	PasswordEncrypted   string `json:"password_encrypted,omitempty"`
	HasPassword         bool   `json:"has_password"`
	SNMPProfileSourceID string `json:"snmp_profile_source_id"`
	SNMPProfileName     string `json:"snmp_profile_name"`
}
```

Add a profile type:

```go
type SNMPProfile struct {
	SourceID           string `json:"source_id"`
	Name               string `json:"name"`
	Version            string `json:"version"`
	Port               int    `json:"port"`
	TimeoutMS          int    `json:"timeout_ms"`
	Retries            int    `json:"retries"`
	Community          string `json:"-"`
	CommunityEncrypted string `json:"community_encrypted,omitempty"`
	HasCommunity       bool   `json:"has_community"`
}
```

Extend `Link`:

```go
type Link struct {
	// ... existing fields ...
	MasteringType  int    `json:"mastering_type"`
	MasterDevice   string `json:"master_device"`
	MasterInterface int   `json:"master_interface"`
}
```

Extend `Preview`:

```go
type Preview struct {
	// ... existing fields ...
	SNMPProfiles []SNMPProfile `json:"snmp_profiles"`
}
```

Update the package comment to remove "No credentials are imported".

**Step 2: Add failing parser tests**

In `dude_test.go`, extend `TestParseDudeNovaSQLite` with:
- an SNMP profile object (`0x3C68`, `0x3C69`, `0x3C6A`, `0x3C71`, `0x3C72`, `0x0010`);
- device fields `0x1F46` and `0x1F47` with marker `0x11`;
- device SNMP reference `0x1F4E`.

Add assertions:

```go
if len(p.SNMPProfiles) != 1 {
	t.Fatalf("expected one SNMP profile, got %+v", p.SNMPProfiles)
}
profile := p.SNMPProfiles[0]
if profile.Name != "v2-public" || profile.Version != "v2c" || profile.Port != 161 ||
	profile.TimeoutMS != 3000 || profile.Retries != 3 || profile.Community != "public" {
	t.Fatalf("unexpected SNMP profile: %+v", profile)
}
d := p.Devices[0]
if d.Username != "admin" || d.Password != "secret" || !d.HasPassword {
	t.Fatalf("unexpected device credentials: %+v", d)
}
if d.SNMPProfileSourceID != "60" || d.SNMPProfileName != "v2-public" {
	t.Fatalf("unexpected device SNMP reference: %+v", d)
}
```

Add link master fields to `TestParseDudeNovaSubmapsAndLinkLabels`:
`0x55F0 = 2`, `0x55F1 = 42`, `0x55F2 = 7`. Assert:

```go
if link.MasteringType != 2 || link.MasterDevice != "42" || link.MasterInterface != 7 {
	t.Fatalf("unexpected link mastering: %+v", link)
}
```

**Step 3: Run tests and verify they fail**

```powershell
go test ./internal/dude -run 'TestParseDudeNova(SQLite|SubmapsAndLinkLabels)' -v
```

Expected: FAIL because the fields are not parsed.

**Step 4: Implement Nova parsing**

In `nova.go`, add `snmpProfiles := map[uint32]SNMPProfile{}` in `parseNovaSQLite`.

Add a case in the object classification switch:

```go
case msg.hasRange(0x3c68, 0x3c72):
	if profile, ok := parseNovaSNMPProfile(id, msg); ok {
		snmpProfiles[id] = profile
	}
```

Add the helper:

```go
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
	if value, ok := msg.u32(0x3c71); ok && value > 0 && value <= 10 {
		retries = int(value)
	}
	community := msg.str(0x3c69)
	return SNMPProfile{
		SourceID: novaID(id), Name: name, Version: version,
		Port: port, TimeoutMS: timeout, Retries: retries,
		Community: community, HasCommunity: community != "",
	}, true
}
```

In the device loop, add:

```go
username := obj.msg.str(0x1f46)
password := obj.msg.str(0x1f47)
device.Username = username
device.Password = password
device.HasPassword = password != ""
if profileID, ok := obj.msg.u32(0x1f4e); ok && profileID != 0xffffffff {
	device.SNMPProfileSourceID = novaID(profileID)
	if profile, exists := snmpProfiles[profileID]; exists {
		device.SNMPProfileName = profile.Name
	}
}
```

After the maps loop, append the profiles to the preview:

```go
for _, profile := range snmpProfiles {
	p.SNMPProfiles = append(p.SNMPProfiles, profile)
}
sort.Slice(p.SNMPProfiles, func(i, j int) bool {
	return strings.ToLower(p.SNMPProfiles[i].Name) < strings.ToLower(p.SNMPProfiles[j].Name)
})
```

In the link loop, add:

```go
masteringType, _ := obj.msg.u32(0x55f0)
masterDevice, _ := obj.msg.u32(0x55f1)
masterInterface, _ := obj.msg.u32(0x55f2)
link.MasteringType = int(masteringType)
if masterDevice != 0xffffffff {
	link.MasterDevice = novaID(masterDevice)
}
if masterInterface != 0xffffffff {
	link.MasterInterface = int(masterInterface)
}
```

**Step 5: Implement legacy SQLite and XML parsing**

In `parseSQLite`:
- Add `snmpTable := findTable(tables, "snmp_profile", "snmp_profiles", "dude_snmp_profile", "snmp")`.
- Read it with `readTable` and append `SNMPProfile` entries:
  - `SourceID`: `text(row, "id", "uuid")` or `snmp-<n>`.
  - `Name`: `text(row, "name", "title")`.
  - `Version`: map `text(row, "version", "snmp_version")` (`1`/`v1`, `2`/`v2c`, `3`/`v3`); default `v2c`.
  - `Port`: `int(number(text(row, "port")))`, default 161.
  - `Community`: `text(row, "community", "community_string", "ro_community")`.
- In the device row, set `Username`, `Password`, `HasPassword`, `SNMPProfileSourceID`.
- After devices/profiles are loaded, set `SNMPProfileName` by matching source ID or name.
- In the link row, set `MasteringType`, `MasterDevice`, `MasterInterface` from common columns.

In `parseXML`:
- Device: `Username: nodeValue(node, "username", "user", "login")`, `Password: nodeValue(node, "password", "pass")`, and `SNMPProfileSourceID: nodeValue(node, "snmp_profile", "snmp_profile_id")`.
- Link: `MasteringType: int(number(nodeValue(...)))`, `MasterDevice`, `MasterInterface`.
- SNMP profiles from XML are optional; if a `<snmp_profile>` node is found, parse name/version/port/community.

**Step 6: Run tests**

```powershell
go test ./internal/dude -v
```

Expected: PASS.

**Step 7: Commit**

```powershell
git add backend/internal/dude/dude.go backend/internal/dude/nova.go backend/internal/dude/dude_test.go
git commit -m "Parse Dude credentials, SNMP profiles and link mastering"
```

---

## Task 3: Encrypt preview secrets and sanitize responses

**Files:**
- Create: `backend/internal/api/handlers_imports_secrets.go`
- Create: `backend/internal/api/handlers_imports_secrets_test.go`
- Modify: `backend/internal/api/handlers_imports.go` (`previewDudeImport`, `getDudeImport`)

**Step 1: Write failing tests**

Create `handlers_imports_secrets_test.go`:

```go
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func TestEncryptSanitizeDecryptDudeSecrets(t *testing.T) {
	secure.SetKey("test-key")
	preview := dude.Preview{
		Devices: []dude.Device{{Name: "R1", Username: "admin", Password: "secret", HasPassword: true}},
		SNMPProfiles: []dude.SNMPProfile{{Name: "public", Community: "public", HasCommunity: true}},
	}
	if err := encryptDudePreviewSecrets(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.Devices[0].Password != "" || preview.Devices[0].PasswordEncrypted == "" {
		t.Fatalf("device secret was not encrypted: %+v", preview.Devices[0])
	}
	if preview.SNMPProfiles[0].Community != "" || preview.SNMPProfiles[0].CommunityEncrypted == "" {
		t.Fatalf("community was not encrypted: %+v", preview.SNMPProfiles[0])
	}
	payload, _ := json.Marshal(preview)
	if strings.Contains(string(payload), "secret") || strings.Contains(string(payload), `"public"`) {
		t.Fatalf("payload leaked plaintext: %s", payload)
	}
	sanitizeDudePreview(&preview)
	if preview.Devices[0].PasswordEncrypted != "" || preview.SNMPProfiles[0].CommunityEncrypted != "" {
		t.Fatalf("ciphertext was not sanitized: %+v", preview)
	}
	if warnings := decryptDudePreviewSecrets(&preview); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if preview.Devices[0].Password != "secret" || preview.SNMPProfiles[0].Community != "public" {
		t.Fatalf("secrets were not restored: %+v", preview)
	}
}
```

**Step 2: Run test and verify it fails**

```powershell
go test ./internal/api -run TestEncryptSanitizeDecryptDudeSecrets -v
```

Expected: FAIL because the functions do not exist.

**Step 3: Implement the helpers**

Create `handlers_imports_secrets.go`:

```go
package api

import (
	"fmt"

	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func encryptDudePreviewSecrets(preview *dude.Preview) error {
	for i := range preview.Devices {
		d := &preview.Devices[i]
		if d.Password == "" {
			continue
		}
		enc, err := secure.Encrypt(d.Password)
		if err != nil {
			return fmt.Errorf("cifrar contraseña de %s: %w", d.Name, err)
		}
		d.PasswordEncrypted = enc
		d.HasPassword = true
		d.Password = ""
	}
	for i := range preview.SNMPProfiles {
		p := &preview.SNMPProfiles[i]
		if p.Community == "" {
			continue
		}
		enc, err := secure.Encrypt(p.Community)
		if err != nil {
			return fmt.Errorf("cifrar community de %s: %w", p.Name, err)
		}
		p.CommunityEncrypted = enc
		p.HasCommunity = true
		p.Community = ""
	}
	return nil
}

func sanitizeDudePreview(preview *dude.Preview) {
	for i := range preview.Devices {
		preview.Devices[i].Password = ""
		preview.Devices[i].PasswordEncrypted = ""
	}
	for i := range preview.SNMPProfiles {
		preview.SNMPProfiles[i].Community = ""
		preview.SNMPProfiles[i].CommunityEncrypted = ""
	}
}

func decryptDudePreviewSecrets(preview *dude.Preview) []string {
	var warnings []string
	for i := range preview.Devices {
		d := &preview.Devices[i]
		if d.PasswordEncrypted == "" {
			continue
		}
		plain, err := secure.Decrypt(d.PasswordEncrypted)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("No se pudo descifrar la credencial de %s.", d.Name))
			d.PasswordEncrypted = ""
			continue
		}
		d.Password = plain
		d.HasPassword = plain != ""
		d.PasswordEncrypted = ""
	}
	for i := range preview.SNMPProfiles {
		p := &preview.SNMPProfiles[i]
		if p.CommunityEncrypted == "" {
			continue
		}
		plain, err := secure.Decrypt(p.CommunityEncrypted)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("No se pudo descifrar la community SNMP de %s.", p.Name))
			p.CommunityEncrypted = ""
			continue
		}
		p.Community = plain
		p.HasCommunity = plain != ""
		p.CommunityEncrypted = ""
	}
	return warnings
}
```

**Step 4: Wire the preview handler**

In `previewDudeImport`:
- Remove the static warning about credentials not being imported.
- Append dynamic warnings based on counts.
- Call `encryptDudePreviewSecrets(&preview)` before `json.Marshal(preview)`.
- After marshaling the payload, call `sanitizeDudePreview(&preview)` before `writeJSON`.

Suggested warnings:

```go
credentialCount := 0
for _, d := range preview.Devices {
	if d.Username != "" {
		credentialCount++
	}
}
if credentialCount > 0 {
	preview.Warnings = append(preview.Warnings,
		fmt.Sprintf("Se importarán credenciales RouterOS de %d equipos.", credentialCount))
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
```

**Step 5: Wire `getDudeImport`**

Replace the `any` preview unmarshal with a sanitized `dude.Preview`:

```go
var preview dude.Preview
var previewObj any
if err := json.Unmarshal([]byte(payload), &preview); err == nil {
	sanitizeDudePreview(&preview)
	previewObj = preview
} else {
	_ = json.Unmarshal([]byte(payload), &previewObj)
}
```

Use `previewObj` in the response.

**Step 6: Run tests**

```powershell
go test ./internal/api -run TestEncryptSanitizeDecryptDudeSecrets -v
go test ./internal/api
```

Expected: PASS.

**Step 7: Commit**

```powershell
git add backend/internal/api/handlers_imports.go backend/internal/api/handlers_imports_secrets.go backend/internal/api/handlers_imports_secrets_test.go
git commit -m "Encrypt Dude import secrets in preview payloads"
```

---

## Task 4: Create and assign Dude SNMP profiles

**Files:**
- Create: `backend/internal/api/handlers_imports_snmp.go`
- Modify: `backend/internal/api/handlers_imports.go` (`dudeApplyRequest`, `applyDudeImport`)
- Test: `backend/internal/api/handlers_imports_snmp_test.go` (helper-level tests)

**Step 1: Add request flags**

In `dudeApplyRequest` add:

```go
ImportCredentials     *bool `json:"import_credentials"`
ImportSNMP            *bool `json:"import_snmp"`
	OverwriteCredentials  *bool `json:"overwrite_credentials"`
	AssignLinkMonitoring  *bool `json:"assign_link_monitoring"`
```

At the start of `applyDudeImport`, compute defaults:

```go
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
```

**Step 2: Write helper test**

Create `handlers_imports_snmp_test.go` with a test for the key/name helpers:

```go
func TestDudeSNMPProfileKey(t *testing.T) {
	p := dude.SNMPProfile{Name: " v2-public ", Version: "v2c", Port: 161, Community: "public"}
	if got := dudeSNMPProfileKey(p); got != "Dude · v2-public\x00v2c\x00161\x00public" {
		t.Fatalf("unexpected key: %q", got)
	}
}
```

Run it, expect failure, then implement the helper.

**Step 3: Implement profile creation/reuse**

Create `handlers_imports_snmp.go` with:

```go
func dudeSNMPProfileKey(p dude.SNMPProfile) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "SNMP"
	}
	return "Dude · " + name + "\x00" + p.Version + "\x00" + strconv.Itoa(p.Port) + "\x00" + p.Community
}

func ensureDudeSNMPProfiles(ctx context.Context, tx pgx.Tx, profiles []dude.SNMPProfile, needed map[string]bool) (map[string]string, int, int, []string, error) {
	// 1. Load existing profiles and decrypt communities.
	// 2. Build map[key]id and set of names.
	// 3. For each needed profile:
	//    - compute key; reuse if found.
	//    - choose unique name "Dude · <name>" with " (2)" suffix if needed.
	//    - skip v1/v2c with empty community and append a warning.
	//    - insert with secure.Encrypt(community).
	// 4. Return sourceID -> id, created, reused, warnings.
}
```

Implementation notes:
- Query: `SELECT id::text, name, version, port, COALESCE(community,'') FROM snmp_profiles`.
- `secure.Decrypt` the community for the key. Ignore decryption errors when building the comparison map.
- Truncate names to 100 characters.
- Insert:
  ```sql
  INSERT INTO snmp_profiles (name, version, port, timeout_ms, retries, community)
  VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text
  ```
- For v3 with empty community, still create the profile and add:
  `"Perfil SNMPv3 %q importado sin secretos; completa usuario/claves en Ajustes."`

**Step 4: Integrate into `applyDudeImport`**

Replace the current `snmpAssignments` build/validation block with this order:
1. Build `manualSNMP` from `req.SNMPProfiles`, only for selected devices.
2. Validate manual IDs exist:
   ```sql
   SELECT count(*) FROM snmp_profiles WHERE id = ANY($1::uuid[])
   ```
3. Build `neededProfiles` from selected devices where `manualSNMP[d.SourceID] == ""` and `d.SNMPProfileSourceID != ""`.
4. If `importSNMP`, call `ensureDudeSNMPProfiles`.
5. Build `snmpAssignments`:
   - start with `manualSNMP`;
   - for each selected device with no manual override, set `snmpAssignments[d.SourceID] = snmpProfileIDs[d.SNMPProfileSourceID]` if non-empty.
6. Keep the final validation query for all assigned IDs (they exist inside the transaction).

**Step 5: Assign in the device loop**

Replace the existing unconditional SNMP update with:

```go
if profileID := snmpAssignments[d.SourceID]; profileID != "" {
	manual := manualSNMP[d.SourceID] != ""
	if manual || overwriteCredentials {
		_, err = tx.Exec(ctx, `UPDATE devices SET snmp_profile_id=$2 WHERE id=$1`, deviceID, profileID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE devices SET snmp_profile_id=$2 WHERE id=$1 AND snmp_profile_id IS NULL`, deviceID, profileID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snmpAssigned++
}
```

**Step 6: Add result counters**

Track `snmpProfilesCreated`, `snmpProfilesReused`, and append helper warnings to `warnings`.

Result JSON:

```go
"snmp_profiles_created": snmpProfilesCreated,
"snmp_profiles_reused": snmpProfilesReused,
"snmp_assigned": snmpAssigned,
```

**Step 7: Run tests and build**

```powershell
go test ./internal/api -run TestDudeSNMPProfileKey -v
go test ./internal/api
go build ./...
```

Expected: PASS.

**Step 8: Commit**

```powershell
git add backend/internal/api/handlers_imports.go backend/internal/api/handlers_imports_snmp.go backend/internal/api/handlers_imports_snmp_test.go
git commit -m "Import and assign Dude SNMP profiles"
```

---

## Task 5: Create and assign Dude RouterOS credentials

**Files:**
- Create: `backend/internal/api/handlers_imports_credentials.go`
- Modify: `backend/internal/api/handlers_imports.go`
- Test: `backend/internal/api/handlers_imports_credentials_test.go`

**Step 1: Write helper tests**

Test `dudeCredentialKey` and unique-name generation:

```go
func TestDudeCredentialKey(t *testing.T) {
	if got := dudeCredentialKey("admin", "secret"); got != "admin\x00secret" {
		t.Fatalf("unexpected key: %q", got)
	}
}
```

Run, expect failure.

**Step 2: Implement credential helpers**

Create `handlers_imports_credentials.go`:

```go
type dudeCredentialDevice struct {
	SourceID   string
	DeviceID   string
	DeviceName string
	Username   string
	Password   string
}

func dudeCredentialKey(username, password string) string {
	return username + "\x00" + password
}

func uniqueDudeCredentialName(base string, used map[string]bool) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Dude"
	}
	if len(base) > 110 {
		base = base[:110]
	}
	name := base
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s (%d)", base, i)
	}
	return name
}

func ensureDudeRouterOSCredentials(ctx context.Context, tx pgx.Tx, devices []dudeCredentialDevice, overwrite bool, filename string) (int, int, int, int, []string, error) {
	// 1. Load existing routeros_credentials, decrypt passwords, build key->id and used names.
	// 2. Group devices by dudeCredentialKey.
	// 3. Reuse or insert one profile per group.
	// 4. Assign to devices with UPDATE ... WHERE routeros_credential_id IS NULL unless overwrite.
	// 5. Return created, reused, assigned, skipped, warnings, error.
}
```

Insert:

```sql
INSERT INTO routeros_credentials (name, username, password, port, use_tls, description)
VALUES ($1,$2,$3,8728,FALSE,$4) RETURNING id::text
```

Description: `"Importado desde The Dude (" + filename + ")"`.

Use `secure.Encrypt(password)`.

**Step 3: Integrate after the device loop**

After the map-element device loop and before links, build `credentialDevices` from `deviceIDs`:

```go
var credentialDevices []dudeCredentialDevice
if importCredentials {
	for _, d := range preview.Devices {
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
created, reused, assigned, skipped, credWarnings, err := ensureDudeRouterOSCredentials(
	ctx, tx, credentialDevices, overwriteCredentials, preview.Filename)
if err != nil {
	writeError(w, http.StatusInternalServerError, err.Error())
	return
}
warnings = append(warnings, credWarnings...)
```

Add result fields:

```go
"routeros_credentials_created": created,
"routeros_credentials_reused": reused,
"routeros_credentials_assigned": assigned,
"routeros_credentials_skipped": skipped,
```

**Step 4: Ensure decryption happens before device loop**

At the top of `applyDudeImport` after unmarshalling:

```go
warnings := decryptDudePreviewSecrets(&preview)
```

Move the `warnings` declaration earlier and remove the later `var warnings []string`.

**Step 5: Run tests**

```powershell
go test ./internal/api -run TestDudeCredentialKey -v
go test ./internal/api
go build ./...
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/api/handlers_imports.go backend/internal/api/handlers_imports_credentials.go backend/internal/api/handlers_imports_credentials_test.go
git commit -m "Import and assign Dude RouterOS credentials"
```

---

## Task 6: Assign SNMP monitoring to imported links

**Files:**
- Modify: `backend/internal/api/handlers_imports.go`
- Test: `backend/internal/api/handlers_imports_links_test.go`

**Step 1: Write a failing helper test**

Create `handlers_imports_links_test.go`:

```go
package api

import (
	"testing"

	"github.com/netmonitor/netmonitor/internal/dude"
)

func TestDudeLinkMonitoring(t *testing.T) {
	devices := map[string]string{"42": "device-uuid"}
	cases := []struct {
		name       string
		link       dude.Link
		enabled    bool
		mastering  string
		masterID   string
		ifIndex    int
	}{
		{"disabled", dude.Link{MasterDevice: "42", MasterInterface: 7}, false, "simple", "", 0},
		{"no interface", dude.Link{MasterDevice: "42"}, true, "simple", "", 0},
		{"missing device", dude.Link{MasterDevice: "99", MasterInterface: 7}, true, "simple", "", 0},
		{"valid", dude.Link{MasterDevice: "42", MasterInterface: 7}, true, "snmp", "device-uuid", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mastering, masterID, ifIndex := dudeLinkMonitoring(tc.link, devices, tc.enabled)
			if mastering != tc.mastering || masterID != tc.masterID || ifIndex != tc.ifIndex {
				t.Fatalf("got %s/%s/%d, want %s/%s/%d", mastering, masterID, ifIndex, tc.mastering, tc.masterID, tc.ifIndex)
			}
		})
	}
}
```

**Step 2: Run and verify failure**

```powershell
go test ./internal/api -run TestDudeLinkMonitoring -v
```

Expected: FAIL because `dudeLinkMonitoring` does not exist.

**Step 3: Implement the helper**

In `handlers_imports.go`:

```go
func dudeLinkMonitoring(link dude.Link, deviceIDs map[string]string, enabled bool) (mastering, masterDeviceID string, ifIndex int) {
	mastering = "simple"
	if !enabled || link.MasterInterface <= 0 || link.MasterDevice == "" {
		return
	}
	deviceID := deviceIDs[link.MasterDevice]
	if deviceID == "" {
		return
	}
	return "snmp", deviceID, link.MasterInterface
}
```

**Step 4: Use it in the link insert**

In the link loop:

```go
mastering, masterDeviceID, ifIndex := dudeLinkMonitoring(l, deviceIDs, assignLinkMonitoring)
var masterArg any
if masterDeviceID != "" {
	masterArg = masterDeviceID
}
var ifArg any
if ifIndex > 0 {
	ifArg = ifIndex
}
_, err := tx.Exec(ctx, `
	INSERT INTO map_links (map_id, source_element_id, target_element_id, source_handle,
	 target_handle, mastering_type, master_device_id, snmp_if_index, link_type, max_speed_bps, label_template)
	VALUES ($1,$2,$3,'center','center',$4,$5,$6,$7,$8,'')
`, mapID, aElem, bElem, mastering, masterArg, ifArg, l.LinkType, speed)
if err == nil {
	createdLinks++
	if mastering == "snmp" {
		monitoredLinks++
	}
}
```

Track `monitoredLinks` and add `"links_monitored": monitoredLinks` to the result. Add a warning for links with a master interface whose device was not imported:

```go
if assignLinkMonitoring && l.MasterInterface > 0 && l.MasterDevice != "" && deviceIDs[l.MasterDevice] == "" {
	warnings = append(warnings, "Algunos enlaces con interfaz maestra se crearon como simples porque su dispositivo no se importó.")
}
```

Avoid duplicate warnings by accumulating a count and adding one final warning.

**Step 5: Run tests**

```powershell
go test ./internal/api -run TestDudeLinkMonitoring -v
go test ./internal/api
go build ./...
```

Expected: PASS.

**Step 6: Commit**

```powershell
git add backend/internal/api/handlers_imports.go backend/internal/api/handlers_imports_links_test.go
git commit -m "Create Dude links with SNMP interface monitoring"
```

---

## Task 7: Frontend types, options and preview badges

**Files:**
- Modify: `frontend/src/types.ts:679-756`
- Modify: `frontend/src/pages/ImportsPage.tsx`
- Modify: `frontend/src/styles.css`

**Step 1: Update types**

Add:

```ts
export interface DudeSNMPProfile {
  source_id: string
  name: string
  version: string
  port: number
  timeout_ms: number
  retries: number
  has_community: boolean
}
```

Extend `DudeDevice`:

```ts
username: string
has_password: boolean
snmp_profile_source_id: string
snmp_profile_name: string
```

Extend `DudeLink`:

```ts
mastering_type: number
master_device: string
master_interface: number
```

Extend `DudePreview`:

```ts
snmp_profiles: DudeSNMPProfile[]
```

**Step 2: Add state and defaults**

In `ImportsPage.tsx`:

```ts
const [importCredentials, setImportCredentials] = useState(true)
const [importSNMP, setImportSNMP] = useState(true)
const [overwriteCredentials, setOverwriteCredentials] = useState(false)
const [assignLinkMonitoring, setAssignLinkMonitoring] = useState(true)
```

Reset these values in `upload` and `openHistory`.

Include in the apply payload:

```ts
import_credentials: importCredentials,
import_snmp: importSNMP,
overwrite_credentials: overwriteCredentials,
assign_link_monitoring: assignLinkMonitoring,
```

**Step 3: Add the options block**

Before the maps/devices columns:

```tsx
{!result && (
  <div className="import-options">
    <label><input type="checkbox" checked={importCredentials} onChange={e => setImportCredentials(e.target.checked)} /> Importar credenciales RouterOS</label>
    <label><input type="checkbox" checked={importSNMP} onChange={e => setImportSNMP(e.target.checked)} /> Importar perfiles SNMP</label>
    <label><input type="checkbox" checked={overwriteCredentials} onChange={e => setOverwriteCredentials(e.target.checked)} /> Sobrescribir credenciales existentes</label>
    <label><input type="checkbox" checked={assignLinkMonitoring} onChange={e => setAssignLinkMonitoring(e.target.checked)} /> Monitorizar enlaces con interfaz</label>
  </div>
)}
```

Add CSS:

```css
.import-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 8px;
  margin: 12px 0;
}
.import-options label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
```

**Step 4: Show device badges**

In the device picker row, append:

```tsx
{d.username && <span className="cred-pill"><KeyRound size={11} /> {d.username}</span>}
{d.snmp_profile_name && <span className="cred-pill">SNMP · {d.snmp_profile_name}</span>}
```

If the `cred-pill` class cannot be nested in the existing `<small>`, add a separate `<span className="device-import-badges">` after the small line.

**Step 5: Show link monitoring counts**

Compute:

```ts
const monitoredLinks = useMemo(
  () => preview?.links.filter(l => l.master_interface > 0 && l.master_device).length ?? 0,
  [preview],
)
```

In the create-links row:

```tsx
{createLinks && monitoredLinks > 0 && (
  <span className="muted small">Se asignará SNMP a {monitoredLinks} enlaces con interfaz maestra.</span>
)}
```

**Step 6: Update texts and result**

- Drop zone: replace "No se importan credenciales..." with "Se pueden importar credenciales RouterOS, perfiles SNMP y monitorización de enlaces."
- Warning box: replace "Las credenciales no se migran..." with a note about credentials being optional and encrypted.
- Result message: add `routeros_credentials_created`, `snmp_profiles_created`, `links_monitored`.
- SNMP section hint: "Los perfiles de Dude se importan automáticamente; usa este selector solo para sustituirlos."

**Step 7: Build**

```powershell
cd C:\Users\Alex\Desktop\NetworkMonitor\frontend
npm run build
```

Expected: PASS.

**Step 8: Commit**

```powershell
git add frontend/src/types.ts frontend/src/pages/ImportsPage.tsx frontend/src/styles.css
git commit -m "Show Dude credential and link monitoring options"
```

---

## Task 8: Update README

**Files:**
- Modify: `README.md:25`

**Step 1: Update the Dude bullet**

Replace the line with a description that mentions:
- RouterOS credentials imported and encrypted;
- SNMP profiles imported and assigned;
- links with master interface converted to SNMP monitoring;
- existing credentials are not overwritten by default.

**Step 2: Add a short subsection**

Add a subsection under the import section (or after the bullet) with the preview options and the SNMPv3 limitation.

**Step 3: Commit**

```bash
git add README.md
git commit -m "Document Dude credential and link import"
```

---

## Task 9: End-to-end verification with a synthetic Dude database

**Files:**
- No committed files required; use a temporary script/database.

**Step 1: Generate a synthetic `dude.db`**

Use a temporary Go test or Python script with the Nova blobs:
- map object;
- node object for device A;
- node object for device B;
- device A with `0x1F40`, `0x1F46` username, `0x1F47` password, `0x1F4E` SNMP profile ID;
- device B;
- SNMP profile object with name/version/community;
- link object with `0x55F0`, `0x55F1`, `0x55F2`, `0x55F4`, `0x55F5`.
- Use marker `0x11` for the continuation fields to validate the decoder fix.

**Step 2: Preview and apply through the local stack**

Use the local admin login and multipart upload:

```powershell
$envMap = @{}
Get-Content .env | ForEach-Object { if ($_ -match '^([^#=]+)=(.*)$') { $envMap[$matches[1]] = $matches[2] } }
$login = Invoke-RestMethod -Uri 'http://localhost:8080/api/v1/auth/login' -Method Post -ContentType 'application/json' -Body (@{username='admin';password=$envMap['ADMIN_PASSWORD']} | ConvertTo-Json)
$h = @{ Authorization = "Bearer $($login.token)" }
$preview = Invoke-RestMethod -Uri 'http://localhost:8080/api/v1/imports/dude/preview' -Method Post -Headers $h -Form @{ file = Get-Item C:\path\to\synthetic-dude.db }
$preview.data.preview
```

Apply:

```powershell
$body = @{
  map_ids = @($preview.data.preview.maps | ForEach-Object { $_.source_id })
  device_source_ids = @($preview.data.preview.devices | ForEach-Object { $_.source_id })
  import_credentials = $true
  import_snmp = $true
  overwrite_credentials = $false
  assign_link_monitoring = $true
  create_links = $true
} | ConvertTo-Json -Depth 5
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/imports/dude/$($preview.data.id)/apply" -Method Post -Headers $h -ContentType 'application/json' -Body $body
```

**Step 3: Verify the database**

```powershell
docker compose exec -T netmonitor-db psql -U netmonitor -d netmonitor -c "select name, username, length(password) from routeros_credentials where name like 'Dude · %';"
docker compose exec -T netmonitor-db psql -U netmonitor -d netmonitor -c "select name, version, port, length(community) from snmp_profiles where name like 'Dude · %';"
docker compose exec -T netmonitor-db psql -U netmonitor -d netmonitor -c "select mastering_type, master_device_id is not null as has_master, snmp_if_index from map_links order by created_at desc limit 5;"
```

Expected:
- one RouterOS credential with encrypted password;
- one SNMP profile with encrypted community;
- the imported device has `routeros_credential_id` and `snmp_profile_id`;
- the link has `mastering_type='snmp'`, a master device and `snmp_if_index=7`.

**Step 4: Verify the preview does not leak secrets**

```powershell
$preview.data.preview.devices | Select-Object name, username, has_password, password_encrypted
```

Expected: `password_encrypted` is empty in the API response; `has_password` is true.

**Step 5: Clean up the test import**

Delete the imported map/devices and the imported credential profiles, or reset the local test database. Do not commit the synthetic DB.

**Step 6: Run the full test suite**

```powershell
cd C:\Users\Alex\Desktop\NetworkMonitor\backend
go test ./...
go vet ./...
cd ..\frontend
npm run build
```

Expected: all pass.

**Step 7: Commit any remaining changes**

```bash
git status --short
git add -A
git commit -m "Verify Dude credentials and link monitoring import"
```

Only commit source changes; never commit the synthetic database.

---

## Final verification checklist

- [ ] `go test ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `npm run build` passes.
- [ ] Preview response contains no plaintext or ciphertext secrets.
- [ ] Applying creates RouterOS credentials and SNMP profiles.
- [ ] Existing credentials are preserved unless overwrite is enabled.
- [ ] Links with master device/interface are `snmp` and have `snmp_if_index`.
- [ ] Links without a valid master fall back to `simple` with a warning.
- [ ] README no longer says credentials are not imported.
- [ ] Synthetic database is deleted and not committed.
