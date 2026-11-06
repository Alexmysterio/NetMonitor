package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/netmonitor/netmonitor/internal/secure"
)

const (
	dudeCredentialPrefix  = "Dude · "
	dudeCredentialNameMax = 100
)

type dudeCredentialDevice struct {
	SourceID   string
	DeviceID   string
	DeviceName string
	Username   string
	Password   string
}

type dudeCredentialGroup struct {
	Key      string
	Username string
	Password string
	Devices  []dudeCredentialDevice
}

func dudeCredentialKey(username, password string) string {
	return dudeCredentialSettingsKey(username, password)
}

func dudeCredentialSettingsKey(username, password string) string {
	return username + "\x00" + password
}

func dudeCredentialDeviceOrder(d dudeCredentialDevice) string {
	return strings.ToLower(strings.TrimSpace(d.DeviceName)) + "\x00" + d.SourceID
}

func dudeCredentialGroupOrder(g dudeCredentialGroup) string {
	return dudeCredentialDeviceOrder(dudeCredentialPrimaryDevice(g)) + "\x00" + g.Key
}

// groupDudeCredentials groups devices that share the same RouterOS username and
// password. Groups and devices are sorted deterministically so repeated imports
// produce the same profile names and assignment order.
func groupDudeCredentials(devices []dudeCredentialDevice) []dudeCredentialGroup {
	index := map[string]int{}
	groups := []dudeCredentialGroup{}
	for _, d := range devices {
		username := strings.TrimSpace(d.Username)
		if username == "" {
			continue
		}
		d.Username = username
		key := dudeCredentialKey(username, d.Password)
		if pos, ok := index[key]; ok {
			groups[pos].Devices = append(groups[pos].Devices, d)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, dudeCredentialGroup{
			Key:      key,
			Username: username,
			Password: d.Password,
			Devices:  []dudeCredentialDevice{d},
		})
	}
	for i := range groups {
		sort.SliceStable(groups[i].Devices, func(a, b int) bool {
			return dudeCredentialDeviceOrder(groups[i].Devices[a]) < dudeCredentialDeviceOrder(groups[i].Devices[b])
		})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return dudeCredentialGroupOrder(groups[i]) < dudeCredentialGroupOrder(groups[j])
	})
	return groups
}

func dudeCredentialPrimaryDevice(g dudeCredentialGroup) dudeCredentialDevice {
	if len(g.Devices) == 0 {
		return dudeCredentialDevice{}
	}
	return g.Devices[0]
}

func dudeCredentialDisplayName(d dudeCredentialDevice) string {
	name := strings.TrimSpace(d.DeviceName)
	if name == "" {
		name = "RouterOS"
	}
	return name
}

func dudeCredentialSourceSuffix(d dudeCredentialDevice) string {
	if id := strings.TrimSpace(d.SourceID); id != "" {
		return " [" + id + "]"
	}
	return ""
}

// dudeCredentialBaseName returns the deterministic base name for a credential
// group. The first device's Dude source ID is part of the name so re-imports
// are idempotent even when several devices share a display name.
func dudeCredentialBaseName(g dudeCredentialGroup) string {
	return dudeCredentialTaggedName(g, "")
}

// dudeCredentialHashedName is used when the base name is already taken by a
// profile with different credentials. The keyed fingerprint only depends on
// username and password, so it is stable across imports without exposing the
// password to offline verification.
func dudeCredentialHashedName(g dudeCredentialGroup) (string, error) {
	fingerprint, err := secure.Fingerprint(dudeCredentialSettingsKey(g.Username, g.Password))
	if err != nil {
		return "", fmt.Errorf("calcular huella de la credencial RouterOS de %q: %w", dudeCredentialDisplayName(dudeCredentialPrimaryDevice(g)), err)
	}
	return dudeCredentialTaggedName(g, "#"+fingerprint), nil
}

func dudeCredentialTaggedName(g dudeCredentialGroup, tag string) string {
	device := dudeCredentialPrimaryDevice(g)
	display := []rune(dudeCredentialDisplayName(device))
	suffix := ""
	if tag != "" {
		suffix = " " + tag
	}
	suffix += dudeCredentialSourceSuffix(device)
	limit := dudeCredentialNameMax - len([]rune(dudeCredentialPrefix)) - len([]rune(suffix))
	if limit < 0 {
		limit = 0
	}
	if len(display) > limit {
		display = display[:limit]
	}
	name := dudeCredentialPrefix + string(display) + suffix
	if runes := []rune(name); len(runes) > dudeCredentialNameMax {
		name = string(runes[:dudeCredentialNameMax])
	}
	return name
}

type dudeRouterOSExistingCredential struct {
	id       string
	name     string
	username string
	password string
	decoded  bool
}

func dudeRouterOSCredentialMatches(g dudeCredentialGroup, existing dudeRouterOSExistingCredential) bool {
	return existing.decoded &&
		g.Username == existing.username &&
		g.Password == existing.password
}

// dudeCredentialTargetName chooses between the base and hashed name for a
// group. It returns the existing profile when the chosen name is already in
// use, so the caller can reuse it or report a conflict.
func dudeCredentialTargetName(g dudeCredentialGroup, existing map[string]dudeRouterOSExistingCredential) (string, dudeRouterOSExistingCredential, bool, error) {
	base := dudeCredentialBaseName(g)
	baseExisting, baseFound := existing[strings.ToLower(base)]
	if !baseFound {
		return base, dudeRouterOSExistingCredential{}, false, nil
	}
	if dudeRouterOSCredentialMatches(g, baseExisting) {
		return base, baseExisting, true, nil
	}
	hashed, err := dudeCredentialHashedName(g)
	if err != nil {
		return "", dudeRouterOSExistingCredential{}, false, err
	}
	hashedExisting, hashedFound := existing[strings.ToLower(hashed)]
	return hashed, hashedExisting, hashedFound, nil
}

func dudeCredentialUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func loadDudeRouterOSCredentialByName(ctx context.Context, tx pgx.Tx, name string) (dudeRouterOSExistingCredential, bool, error) {
	var profile dudeRouterOSExistingCredential
	var encrypted string
	err := tx.QueryRow(ctx, `
		SELECT id::text, name, username, COALESCE(password,'')
		FROM routeros_credentials WHERE name=$1
	`, name).Scan(&profile.id, &profile.name, &profile.username, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return dudeRouterOSExistingCredential{}, false, nil
	}
	if err != nil {
		return dudeRouterOSExistingCredential{}, false, err
	}
	plain, err := secure.Decrypt(encrypted)
	if err != nil {
		return dudeRouterOSExistingCredential{}, false, fmt.Errorf("descifrar la contraseña del perfil RouterOS %q: %w", name, err)
	}
	profile.password = plain
	profile.decoded = true
	return profile, true, nil
}

// insertDudeRouterOSCredential wraps the insert in a savepoint so a concurrent
// unique violation can be handled without leaving the import transaction aborted.
func insertDudeRouterOSCredential(ctx context.Context, tx pgx.Tx, name, username, password, description string) (string, error) {
	const savepoint = "dude_routeros_credential_insert"
	if _, err := tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO routeros_credentials (name, username, password, port, use_tls, description)
		VALUES ($1,$2,$3,8728,FALSE,$4) RETURNING id::text
	`, name, username, password, description).Scan(&id)
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

// ensureDudeRouterOSCredentials creates or reuses the RouterOS credential
// profiles shared by the imported devices and assigns them inside the import
// transaction. It never logs or returns plaintext passwords.
func ensureDudeRouterOSCredentials(ctx context.Context, tx pgx.Tx, groups []dudeCredentialGroup, overwrite bool, filename string) (created, reused, assigned, skipped int, warnings []string, err error) {
	if len(groups) == 0 {
		return 0, 0, 0, 0, nil, nil
	}
	existingByName := map[string]dudeRouterOSExistingCredential{}
	existingByKey := map[string]string{}
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, username, COALESCE(password,'')
		FROM routeros_credentials
		ORDER BY lower(name), name, id
	`)
	if err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("consultar credenciales RouterOS: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var profile dudeRouterOSExistingCredential
		var encrypted string
		if err := rows.Scan(&profile.id, &profile.name, &profile.username, &encrypted); err != nil {
			return 0, 0, 0, 0, nil, fmt.Errorf("leer credenciales RouterOS: %w", err)
		}
		if plain, decErr := secure.Decrypt(encrypted); decErr == nil {
			profile.password = plain
			profile.decoded = true
			key := dudeCredentialKey(profile.username, plain)
			if _, ok := existingByKey[key]; !ok {
				existingByKey[key] = profile.id
			}
		}
		existingByName[strings.ToLower(profile.name)] = profile
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("leer credenciales RouterOS: %w", err)
	}
	rows.Close()

	description := "Importado desde The Dude (" + filename + ")"
	withoutPassword := 0
	for _, group := range groups {
		if len(group.Devices) == 0 {
			continue
		}
		if group.Password == "" {
			withoutPassword++
		}
		credentialID := existingByKey[group.Key]
		if credentialID != "" {
			reused++
		} else {
			name, current, found, targetErr := dudeCredentialTargetName(group, existingByName)
			if targetErr != nil {
				return 0, 0, 0, 0, nil, targetErr
			}
			if found {
				if !dudeRouterOSCredentialMatches(group, current) {
					return 0, 0, 0, 0, nil, fmt.Errorf("el perfil RouterOS %q ya existe con otras credenciales", name)
				}
				credentialID = current.id
				existingByKey[group.Key] = credentialID
				reused++
			} else {
				encrypted, encErr := secure.Encrypt(group.Password)
				if encErr != nil {
					return 0, 0, 0, 0, nil, fmt.Errorf("cifrar la contraseña del perfil RouterOS %q: %w", name, encErr)
				}
				id, insertErr := insertDudeRouterOSCredential(ctx, tx, name, group.Username, encrypted, description)
				if insertErr != nil {
					if !dudeCredentialUniqueViolation(insertErr) {
						return 0, 0, 0, 0, nil, fmt.Errorf("crear perfil RouterOS %q: %w", name, insertErr)
					}
					conflict, ok, loadErr := loadDudeRouterOSCredentialByName(ctx, tx, name)
					if loadErr != nil {
						return 0, 0, 0, 0, nil, fmt.Errorf("releer el perfil RouterOS %q: %w", name, loadErr)
					}
					if !ok || !dudeRouterOSCredentialMatches(group, conflict) {
						return 0, 0, 0, 0, nil, fmt.Errorf("el perfil RouterOS %q ya existe con otras credenciales", name)
					}
					credentialID = conflict.id
					existingByName[strings.ToLower(conflict.name)] = conflict
					existingByKey[group.Key] = credentialID
					reused++
				} else {
					credentialID = id
					existingByName[strings.ToLower(name)] = dudeRouterOSExistingCredential{
						id: id, name: name, username: group.Username,
						password: group.Password, decoded: true,
					}
					existingByKey[group.Key] = id
					created++
				}
			}
		}
		for _, device := range group.Devices {
			if device.DeviceID == "" {
				skipped++
				continue
			}
			var ct pgconn.CommandTag
			var updateErr error
			if overwrite {
				ct, updateErr = tx.Exec(ctx, `UPDATE devices SET routeros_credential_id=$2 WHERE id=$1`, device.DeviceID, credentialID)
			} else {
				ct, updateErr = tx.Exec(ctx, `UPDATE devices SET routeros_credential_id=$2 WHERE id=$1 AND routeros_credential_id IS NULL`, device.DeviceID, credentialID)
			}
			if updateErr != nil {
				return 0, 0, 0, 0, nil, fmt.Errorf("asignar la credencial RouterOS al dispositivo %q: %w", device.DeviceName, updateErr)
			}
			if ct.RowsAffected() > 0 {
				assigned++
			} else {
				skipped++
			}
		}
	}
	if withoutPassword > 0 {
		warnings = append(warnings, fmt.Sprintf("%d credenciales RouterOS de Dude no tienen contraseña; complétalas en el inventario.", withoutPassword))
	}
	return created, reused, assigned, skipped, warnings, nil
}
