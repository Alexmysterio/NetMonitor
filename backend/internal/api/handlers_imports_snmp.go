package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/netmonitor/netmonitor/internal/dude"
	"github.com/netmonitor/netmonitor/internal/secure"
)

const dudeSNMPProfileNameMax = 100

func dudeSNMPProfileKey(p dude.SNMPProfile) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "SNMP"
	}
	return "Dude · " + name + "\x00" + p.Version + "\x00" + strconv.Itoa(p.Port) + "\x00" + p.Community
}

// dudeSNMPExistingKey builds the same comparison key for a profile that is
// already stored. Imported profiles keep the "Dude · " prefix in their name,
// while dudeSNMPProfileKey expects the original (unprefixed) Dude name.
func dudeSNMPExistingKey(name, version string, port int, community string) string {
	name = strings.TrimPrefix(name, "Dude · ")
	return dudeSNMPProfileKey(dude.SNMPProfile{
		Name: name, Version: version, Port: port, Community: community,
	})
}

func uniqueDudeSNMPName(base string, used map[string]bool) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Dude · SNMP"
	}
	if runes := []rune(base); len(runes) > dudeSNMPProfileNameMax {
		base = string(runes[:dudeSNMPProfileNameMax])
	}
	name := base
	for i := 2; used[strings.ToLower(name)]; i++ {
		suffix := fmt.Sprintf(" (%d)", i)
		prefix := base
		limit := dudeSNMPProfileNameMax - len([]rune(suffix))
		if runes := []rune(prefix); len(runes) > limit {
			prefix = string(runes[:limit])
		}
		name = prefix + suffix
	}
	return name
}

// ensureDudeSNMPProfiles creates or reuses the SNMP profiles referenced by the
// selected Dude devices. It returns the source ID to profile ID mapping, the
// number of created and reused profiles, and any user-facing warnings.
func ensureDudeSNMPProfiles(ctx context.Context, tx pgx.Tx, profiles []dude.SNMPProfile, needed map[string]bool) (map[string]string, int, int, []string, error) {
	byKey := map[string]string{}
	usedNames := map[string]bool{}
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, version, port, COALESCE(community,'')
		FROM snmp_profiles
	`)
	if err != nil {
		return nil, 0, 0, nil, err
	}
	for rows.Next() {
		var id, name, version, community string
		var port int
		if err := rows.Scan(&id, &name, &version, &port, &community); err != nil {
			rows.Close()
			return nil, 0, 0, nil, err
		}
		usedNames[strings.ToLower(name)] = true
		plain, err := secure.Decrypt(community)
		if err != nil {
			continue
		}
		byKey[dudeSNMPExistingKey(name, version, port, plain)] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, 0, nil, err
	}

	ids := map[string]string{}
	created, reused := 0, 0
	var warnings []string
	for _, p := range profiles {
		if !needed[p.SourceID] {
			continue
		}
		key := dudeSNMPProfileKey(p)
		if id := byKey[key]; id != "" {
			ids[p.SourceID] = id
			reused++
			continue
		}
		base := "Dude · " + strings.TrimSpace(p.Name)
		if strings.TrimSpace(p.Name) == "" {
			base = "Dude · SNMP"
		}
		name := uniqueDudeSNMPName(base, usedNames)
		if (p.Version == "v1" || p.Version == "v2c") && p.Community == "" {
			warnings = append(warnings, fmt.Sprintf("Perfil SNMP %q sin community para %s; se omite.", name, p.Version))
			continue
		}
		if p.Version == "v3" && p.Community == "" {
			warnings = append(warnings, fmt.Sprintf("Perfil SNMPv3 %q importado sin secretos; completa usuario/claves en Ajustes.", name))
		}
		community, err := secure.Encrypt(p.Community)
		if err != nil {
			return nil, 0, 0, nil, err
		}
		timeout := p.TimeoutMS
		if timeout <= 0 {
			timeout = 1500
		}
		retries := p.Retries
		if retries < 0 {
			retries = 2
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO snmp_profiles (name, version, port, timeout_ms, retries, community)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text
		`, name, p.Version, p.Port, timeout, retries, community).Scan(&id); err != nil {
			return nil, 0, 0, nil, err
		}
		ids[p.SourceID] = id
		byKey[key] = id
		usedNames[strings.ToLower(name)] = true
		created++
	}
	return ids, created, reused, warnings, nil
}
