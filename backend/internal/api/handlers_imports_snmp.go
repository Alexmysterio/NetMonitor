package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

const (
	dudeSNMPProfilePrefix  = "Dude · "
	dudeSNMPProfileNameMax = 100
)

type dudeSNMPExistingProfile struct {
	id        string
	name      string
	version   string
	port      int
	community string
	decoded   bool
}

func dudeSNMPProfileDisplayName(p dude.SNMPProfile) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "SNMP"
	}
	return name
}

func dudeSNMPProfileSourceSuffix(p dude.SNMPProfile) string {
	if id := strings.TrimSpace(p.SourceID); id != "" {
		return " [" + id + "]"
	}
	return ""
}

// dudeSNMPProfileName returns the deterministic base name for a profile. The
// Dude source ID is always part of the name so re-imports are idempotent even
// when two source profiles share the same display name.
func dudeSNMPProfileName(p dude.SNMPProfile) string {
	return dudeSNMPProfileTaggedName(p, "")
}

// dudeSNMPProfileHashedName is used when the base name is already taken by a
// profile with different settings. The hash only depends on version, port and
// community, so it is stable across imports.
func dudeSNMPProfileHashedName(p dude.SNMPProfile) string {
	sum := sha256.Sum256([]byte(dudeSNMPSettingsKey(p.Version, p.Port, p.Community)))
	return dudeSNMPProfileTaggedName(p, fmt.Sprintf("#%x", sum[:4]))
}

func dudeSNMPProfileTaggedName(p dude.SNMPProfile, tag string) string {
	display := []rune(dudeSNMPProfileDisplayName(p))
	suffix := ""
	if tag != "" {
		suffix = " " + tag
	}
	suffix += dudeSNMPProfileSourceSuffix(p)
	limit := dudeSNMPProfileNameMax - len([]rune(dudeSNMPProfilePrefix)) - len([]rune(suffix))
	if limit < 0 {
		limit = 0
	}
	if len(display) > limit {
		display = display[:limit]
	}
	name := dudeSNMPProfilePrefix + string(display) + suffix
	if runes := []rune(name); len(runes) > dudeSNMPProfileNameMax {
		name = string(runes[:dudeSNMPProfileNameMax])
	}
	return name
}

func dudeSNMPSettingsKey(version string, port int, community string) string {
	return version + "|" + strconv.Itoa(port) + "|" + community
}

func dudeSNMPProfileKey(p dude.SNMPProfile) string {
	return dudeSNMPProfileName(p) + "\x00" + p.Version + "\x00" + strconv.Itoa(p.Port) + "\x00" + p.Community
}

func dudeSNMPProfileMatches(p dude.SNMPProfile, existing dudeSNMPExistingProfile) bool {
	return existing.decoded &&
		p.Version == existing.version &&
		p.Port == existing.port &&
		p.Community == existing.community
}

func dudeSNMPProfileTargetName(p dude.SNMPProfile, existing map[string]dudeSNMPExistingProfile) (string, dudeSNMPExistingProfile, bool) {
	base := dudeSNMPProfileName(p)
	baseExisting, baseFound := existing[strings.ToLower(base)]
	if !baseFound {
		return base, dudeSNMPExistingProfile{}, false
	}
	if dudeSNMPProfileMatches(p, baseExisting) {
		return base, baseExisting, true
	}
	hashed := dudeSNMPProfileHashedName(p)
	hashedExisting, hashedFound := existing[strings.ToLower(hashed)]
	return hashed, hashedExisting, hashedFound
}

func dudeDeviceSelected(selected map[string]bool, sourceID string) bool {
	return len(selected) == 0 || selected[sourceID]
}

func dudeDeviceInSelectedMap(d dude.Device, selectedMaps map[string]bool) bool {
	return d.SourceMapID == "" || selectedMaps[d.SourceMapID]
}

// dudeDeviceSNMPProfileSourceID resolves the profile referenced by a device.
// It prefers the source ID, then falls back to the profile name for legacy
// previews that stored a name in the source-ID field.
func dudeDeviceSNMPProfileSourceID(d dude.Device, profiles []dude.SNMPProfile) string {
	if ref := strings.TrimSpace(d.SNMPProfileSourceID); ref != "" {
		for _, p := range profiles {
			if strings.EqualFold(p.SourceID, ref) {
				return p.SourceID
			}
		}
	}
	for _, ref := range []string{strings.TrimSpace(d.SNMPProfileName), strings.TrimSpace(d.SNMPProfileSourceID)} {
		if ref == "" {
			continue
		}
		for _, p := range profiles {
			if strings.EqualFold(p.Name, ref) {
				return p.SourceID
			}
		}
	}
	return ""
}

func dudeSNMPUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func loadDudeSNMPProfileByName(ctx context.Context, tx pgx.Tx, name string) (dudeSNMPExistingProfile, bool, error) {
	var profile dudeSNMPExistingProfile
	var community string
	err := tx.QueryRow(ctx, `
		SELECT id::text, name, version, port, COALESCE(community,'')
		FROM snmp_profiles WHERE name=$1
	`, name).Scan(&profile.id, &profile.name, &profile.version, &profile.port, &community)
	if errors.Is(err, pgx.ErrNoRows) {
		return dudeSNMPExistingProfile{}, false, nil
	}
	if err != nil {
		return dudeSNMPExistingProfile{}, false, err
	}
	if plain, err := secure.Decrypt(community); err == nil {
		profile.community = plain
		profile.decoded = true
	}
	return profile, true, nil
}

// insertDudeSNMPProfile wraps the insert in a savepoint so a concurrent unique
// violation can be handled without leaving the import transaction aborted.
func insertDudeSNMPProfile(ctx context.Context, tx pgx.Tx, name string, p dude.SNMPProfile, timeout, retries int, community string) (string, error) {
	const savepoint = "dude_snmp_profile_insert"
	if _, err := tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO snmp_profiles (name, version, port, timeout_ms, retries, community)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text
	`, name, p.Version, p.Port, timeout, retries, community).Scan(&id)
	if err != nil {
		if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rollbackErr != nil {
			return "", fmt.Errorf("%w; rollback savepoint: %v", err, rollbackErr)
		}
	}
	if _, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint); releaseErr != nil {
		return "", releaseErr
	}
	return id, err
}

// ensureDudeSNMPProfiles creates or reuses the SNMP profiles referenced by the
// selected Dude devices. It returns the source ID to profile ID mapping, the
// number of created and reused profiles, and any user-facing warnings.
func ensureDudeSNMPProfiles(ctx context.Context, tx pgx.Tx, profiles []dude.SNMPProfile, needed map[string]bool) (map[string]string, int, int, []string, error) {
	existing := map[string]dudeSNMPExistingProfile{}
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, version, port, COALESCE(community,'')
		FROM snmp_profiles
	`)
	if err != nil {
		return nil, 0, 0, nil, fmt.Errorf("consultar perfiles SNMP: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var profile dudeSNMPExistingProfile
		var community string
		if err := rows.Scan(&profile.id, &profile.name, &profile.version, &profile.port, &community); err != nil {
			return nil, 0, 0, nil, fmt.Errorf("leer perfiles SNMP: %w", err)
		}
		if plain, err := secure.Decrypt(community); err == nil {
			profile.community = plain
			profile.decoded = true
		}
		existing[strings.ToLower(profile.name)] = profile
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, nil, fmt.Errorf("leer perfiles SNMP: %w", err)
	}
	rows.Close()

	ids := map[string]string{}
	created, reused := 0, 0
	var warnings []string
	for _, p := range profiles {
		if !needed[p.SourceID] {
			continue
		}
		if (p.Version == "v1" || p.Version == "v2c") && p.Community == "" {
			warnings = append(warnings, fmt.Sprintf("Perfil SNMP %q sin community para %s; se omite.", dudeSNMPProfileName(p), p.Version))
			continue
		}

		name, current, found := dudeSNMPProfileTargetName(p, existing)
		if found {
			if dudeSNMPProfileMatches(p, current) {
				ids[p.SourceID] = current.id
				reused++
				continue
			}
			return nil, 0, 0, nil, fmt.Errorf("el perfil SNMP %q ya existe con otra configuración", name)
		}
		if p.Version == "v3" && p.Community == "" {
			warnings = append(warnings, fmt.Sprintf("Perfil SNMPv3 %q importado sin secretos; completa usuario/claves en Ajustes.", name))
		}

		community, err := secure.Encrypt(p.Community)
		if err != nil {
			return nil, 0, 0, nil, fmt.Errorf("cifrar community del perfil SNMP %q: %w", name, err)
		}
		timeout := p.TimeoutMS
		if timeout <= 0 {
			timeout = 1500
		}
		retries := p.Retries
		if retries < 0 {
			retries = 2
		}
		id, err := insertDudeSNMPProfile(ctx, tx, name, p, timeout, retries, community)
		if err != nil {
			if dudeSNMPUniqueViolation(err) {
				conflict, ok, loadErr := loadDudeSNMPProfileByName(ctx, tx, name)
				if loadErr != nil {
					return nil, 0, 0, nil, fmt.Errorf("releer el perfil SNMP %q: %w", name, loadErr)
				}
				if ok && dudeSNMPProfileMatches(p, conflict) {
					ids[p.SourceID] = conflict.id
					existing[strings.ToLower(conflict.name)] = conflict
					reused++
					continue
				}
				return nil, 0, 0, nil, fmt.Errorf("el perfil SNMP %q ya existe con otra configuración", name)
			}
			return nil, 0, 0, nil, fmt.Errorf("crear perfil SNMP %q: %w", name, err)
		}
		ids[p.SourceID] = id
		existing[strings.ToLower(name)] = dudeSNMPExistingProfile{
			id: id, name: name, version: p.Version, port: p.Port,
			community: p.Community, decoded: true,
		}
		created++
	}
	return ids, created, reused, warnings, nil
}
