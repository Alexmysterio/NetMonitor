package mib

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Object is a resolved MIB definition. The parser intentionally focuses on
// OBJECT-TYPE / OBJECT IDENTIFIER declarations (the ones useful in SNMP walks)
// rather than implementing the full SMI ASN.1 grammar.
type Object struct {
	OID         string
	Name        string
	Syntax      string
	Access      string
	Description string
}

type definition struct {
	name   string
	parent string
	subID  int
	body   string
	object bool
}

// Notification is a parsed SNMP NOTIFICATION-TYPE. The receiver uses it to
// turn a trap OID into a human-readable name and a list of expected varbinds.
type Notification struct {
	OID         string
	Name        string
	Description string
	Objects     []string
}

var (
	objectTypeRe   = regexp.MustCompile(`(?ms)^\s*([A-Za-z][A-Za-z0-9-]*)\s+OBJECT-TYPE\s+(.*?)\s*::=\s*\{\s*([A-Za-z][A-Za-z0-9-]*)\s+(\d+)\s*\}`)
	objectIDRe     = regexp.MustCompile(`(?ms)^\s*([A-Za-z][A-Za-z0-9-]*)\s+(?:OBJECT\s+IDENTIFIER|MODULE-IDENTITY|OBJECT-IDENTITY)\s+.*?::=\s*\{\s*([A-Za-z][A-Za-z0-9-]*)\s+(\d+)\s*\}`)
	macroOIDRe     = regexp.MustCompile(`(?ms)^\s*([A-Za-z][A-Za-z0-9-]*)\s+(?:OBJECT-GROUP|NOTIFICATION-GROUP|MODULE-COMPLIANCE|AGENT-CAPABILITIES|NOTIFICATION-TYPE)\s+.*?::=\s*\{\s*([A-Za-z][A-Za-z0-9-]*)\s+(\d+)\s*\}`)
	notificationRe = regexp.MustCompile(`(?ms)^\s*([A-Za-z][A-Za-z0-9-]*)\s+NOTIFICATION-TYPE\s+(.*?)\s*::=\s*\{\s*([A-Za-z][A-Za-z0-9-]*)\s+(\d+)\s*\}`)
	objectsRe      = regexp.MustCompile(`(?ms)OBJECTS\s*\{([^}]*)\}`)
	syntaxRe       = regexp.MustCompile(`(?ms)SYNTAX\s+(.+?)\s*(?:MAX-ACCESS|ACCESS|STATUS|DESCRIPTION|::=)`)
	accessRe       = regexp.MustCompile(`(?ms)(?:MAX-ACCESS|ACCESS)\s+([^\s]+)`)
	descriptionRe  = regexp.MustCompile(`(?ms)DESCRIPTION\s+"((?:[^"\\]|\\.)*)"`)
	commentRe      = regexp.MustCompile(`(?m)--.*$`)
)

var roots = map[string]string{
	"iso": "1", "org": "1.3", "dod": "1.3.6", "internet": "1.3.6.1",
	"directory": "1.3.6.1.1", "mgmt": "1.3.6.1.2", "mib-2": "1.3.6.1.2.1",
	"transmission": "1.3.6.1.2.1.10", "experimental": "1.3.6.1.3",
	"private": "1.3.6.1.4", "enterprises": "1.3.6.1.4.1",
	"security": "1.3.6.1.5", "snmpV2": "1.3.6.1.6",
}

// Parse resolves as many symbolic OIDs as possible. Unresolvable branches are
// silently skipped; a valid MIB usually resolves most objects once standard
// roots and OBJECT IDENTIFIER nodes are known.
func Parse(module, text string) ([]Object, error) {
	result, err := ParseMany(map[string]string{module: text})
	if err != nil {
		return nil, err
	}
	return result[module], nil
}

// ParseMany resolves a set of MIB modules together, so symbols imported from
// another module in the same batch can be used as OID parents.
func ParseMany(modules map[string]string) (map[string][]Object, error) {
	defsByModule, resolved := resolveDefinitions(modules)
	if len(resolved) == 0 {
		return nil, fmt.Errorf("no se han encontrado definiciones en los módulos indicados")
	}
	out := map[string][]Object{}
	total := 0
	for module, defs := range defsByModule {
		objects := buildObjects(defs, resolved)
		if len(objects) > 0 {
			out[module] = objects
			total += len(objects)
		}
	}
	if total == 0 {
		return nil, fmt.Errorf("no se pudo resolver ningún OID de los módulos indicados")
	}
	return out, nil
}

// ParseNotificationsMany extracts NOTIFICATION-TYPE declarations from a batch
// of modules. It shares the OID resolver with ParseMany so symbols imported
// from other modules resolve exactly like regular MIB objects.
func ParseNotificationsMany(modules map[string]string) map[string][]Notification {
	_, resolved := resolveDefinitions(modules)
	out := map[string][]Notification{}
	for module, text := range modules {
		clean := commentRe.ReplaceAllString(text, "")
		seen := map[string]bool{}
		for _, match := range notificationRe.FindAllStringSubmatch(clean, -1) {
			name := match[1]
			oid := resolved[name]
			if oid == "" || seen[oid] {
				continue
			}
			body := match[2]
			n := Notification{OID: oid, Name: name}
			if m := descriptionRe.FindStringSubmatch(body); len(m) > 1 {
				n.Description = strings.ReplaceAll(strings.ReplaceAll(m[1], `\"`, `"`), `\n`, " ")
			}
			if m := objectsRe.FindStringSubmatch(body); len(m) > 1 {
				for _, raw := range strings.Split(m[1], ",") {
					field := strings.TrimSpace(raw)
					if field != "" {
						n.Objects = append(n.Objects, field)
					}
				}
			}
			seen[oid] = true
			out[module] = append(out[module], n)
		}
		sort.Slice(out[module], func(i, j int) bool {
			if out[module][i].OID == out[module][j].OID {
				return out[module][i].Name < out[module][j].Name
			}
			return out[module][i].OID < out[module][j].OID
		})
		if len(out[module]) == 0 {
			delete(out, module)
		}
	}
	return out
}

func resolveDefinitions(modules map[string]string) (map[string][]definition, map[string]string) {
	defsByModule := map[string][]definition{}
	var all []definition
	for module, text := range modules {
		defs := collectDefinitions(text)
		defsByModule[module] = defs
		all = append(all, defs...)
	}
	resolved := map[string]string{}
	for k, v := range roots {
		resolved[k] = v
	}
	byName := map[string]definition{}
	for _, d := range all {
		if _, exists := byName[d.name]; !exists {
			byName[d.name] = d
		}
	}
	for pass := 0; pass < 24; pass++ {
		changed := false
		for name, d := range byName {
			if resolved[name] != "" {
				continue
			}
			if parentOID := resolved[d.parent]; parentOID != "" {
				resolved[name] = fmt.Sprintf("%s.%d", parentOID, d.subID)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return defsByModule, resolved
}

func collectDefinitions(text string) []definition {
	clean := commentRe.ReplaceAllString(text, "")
	var defs []definition
	for _, match := range objectTypeRe.FindAllStringSubmatch(clean, -1) {
		sub, _ := strconv.Atoi(match[4])
		defs = append(defs, definition{name: match[1], parent: match[3], subID: sub, body: match[2], object: true})
	}
	for _, match := range objectIDRe.FindAllStringSubmatch(clean, -1) {
		sub, _ := strconv.Atoi(match[3])
		defs = append(defs, definition{name: match[1], parent: match[2], subID: sub})
	}
	for _, match := range macroOIDRe.FindAllStringSubmatch(clean, -1) {
		sub, _ := strconv.Atoi(match[3])
		defs = append(defs, definition{name: match[1], parent: match[2], subID: sub})
	}
	return defs
}

func buildObjects(defs []definition, resolved map[string]string) []Object {
	objects := []Object{}
	seen := map[string]bool{}
	for _, d := range defs {
		oid := resolved[d.name]
		if oid == "" || seen[oid+"|"+d.name] {
			continue
		}
		obj := Object{OID: oid, Name: d.name}
		if d.object {
			if m := syntaxRe.FindStringSubmatch(d.body); len(m) > 1 {
				obj.Syntax = strings.TrimSpace(strings.Join(strings.Fields(m[1]), " "))
			}
			if m := accessRe.FindStringSubmatch(d.body); len(m) > 1 {
				obj.Access = strings.TrimSpace(m[1])
			}
			if m := descriptionRe.FindStringSubmatch(d.body); len(m) > 1 {
				obj.Description = strings.ReplaceAll(strings.ReplaceAll(m[1], `\"`, `"`), `\n`, " ")
			}
		}
		seen[oid+"|"+d.name] = true
		objects = append(objects, obj)
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].OID == objects[j].OID {
			return objects[i].Name < objects[j].Name
		}
		return objects[i].OID < objects[j].OID
	})
	return objects
}

// Imports returns module names referenced by an IMPORTS section.
func Imports(text string) []string {
	clean := commentRe.ReplaceAllString(text, "")
	start := strings.Index(clean, "IMPORTS")
	if start < 0 {
		return nil
	}
	end := strings.Index(clean[start:], ";")
	if end < 0 {
		return nil
	}
	section := clean[start : start+end]
	re := regexp.MustCompile(`FROM\s+([A-Za-z][A-Za-z0-9-]*)`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(section, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
