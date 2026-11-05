package api

import "net/http"

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title": "NetMonitor API", "version": "0.1.0",
			"description": "API REST de NetMonitor. Autenticación Bearer JWT o X-API-Key.",
		},
		"servers": []map[string]string{{"url": "/api/v1"}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]string{"type": "http", "scheme": "bearer"},
				"apiKey":     map[string]string{"type": "apiKey", "in": "header", "name": "X-API-Key"},
			},
		},
		"security": []map[string]any{{"bearerAuth": []string{}}, {"apiKey": []string{}}},
		"paths": map[string]any{
			"/auth/login":                 map[string]any{"post": op("Login", "Autentica usuario y devuelve JWT")},
			"/health":                     map[string]any{"get": op("Health", "Estado del servicio")},
			"/devices":                    map[string]any{"get": op("Listar dispositivos", "Lista filtrada por permisos"), "post": op("Crear dispositivo", "Alta de dispositivo")},
			"/devices/bulk":               map[string]any{"post": op("Acciones masivas", "Perfil SNMP, etiquetas, monitorización, reconocimiento, sondeo o borrado")},
			"/devices/{id}":               map[string]any{"get": op("Obtener dispositivo", "Detalle"), "put": op("Editar dispositivo", "Actualiza"), "delete": op("Borrar dispositivo", "Elimina")},
			"/devices/{id}/snmp-info":     map[string]any{"get": op("Inventario SNMP", "Sistema, IPs, interfaces, ARP y rutas")},
			"/devices/{id}/interfaces":    map[string]any{"get": op("Interfaces SNMP", "Lista de interfaces para enlaces")},
			"/devices/{id}/ensure-probe":  map[string]any{"post": op("Ajustar sonda", "Crea la sonda ICMP/TCP/SNMP que corresponde al descubrimiento")},
			"/maps":                       map[string]any{"get": op("Listar mapas", "Mapas visibles"), "post": op("Crear mapa", "Nuevo mapa")},
			"/maps/{id}/canvas":           map[string]any{"get": op("Canvas", "Elementos y enlaces del mapa")},
			"/maps/{id}/layout":           map[string]any{"patch": op("Guardar layout", "Posiciones, tamaños y rotación")},
			"/alert-rules":                map[string]any{"get": op("Listar reglas", ""), "post": op("Crear regla", "")},
			"/notification-channels":      map[string]any{"get": op("Listar canales", ""), "post": op("Crear canal", "")},
			"/agents":                     map[string]any{"get": op("Listar agentes", ""), "post": op("Crear agente", "")},
			"/agent-releases":             map[string]any{"get": op("Listar releases", ""), "post": op("Publicar release", "")},
			"/agents/update":              map[string]any{"post": op("Despliegue por lotes", "Actualiza agentes desde un canal o release")},
			"/reports/sla":                map[string]any{"get": op("Informe SLA", "JSON o CSV con disponibilidad")},
			"/settings/key-status":        map[string]any{"get": op("Estado de clave", "Antigüedad y rotación")},
			"/settings/snmp-refresh":      map[string]any{"get": op("Refresco SNMP global", ""), "put": op("Guardar refresco SNMP global", "")},
			"/settings/holidays":          map[string]any{"get": op("Festivos globales", ""), "put": op("Guardar festivos", "")},
			"/device-groups":              map[string]any{"get": op("Listar grupos", ""), "post": op("Crear grupo", "")},
			"/routeros/inventory":         map[string]any{"get": op("Inventario RouterOS", "Versión, modelo, credencial y última sincronización")},
			"/routeros/inventory/refresh": map[string]any{"post": op("Sincronizar RouterOS", "Lectura de identidad, recurso, board y paquetes")},
			"/routeros/credentials":       map[string]any{"get": op("Credenciales RouterOS", ""), "post": op("Crear credencial RouterOS", "Cifrada en reposo")},
			"/routeros/templates":         map[string]any{"get": op("Plantillas RouterOS", ""), "post": op("Crear plantilla RouterOS", "")},
			"/routeros/templates/preview": map[string]any{"post": op("Previsualizar plantilla", "Renderiza variables por equipo")},
			"/routeros/jobs":              map[string]any{"get": op("Trabajos RouterOS", ""), "post": op("Crear trabajo RouterOS", "Genera plan y exige aprobación")},
			"/routeros/jobs/{id}/approve": map[string]any{"post": op("Aprobar trabajo", "Pasa a estado approved")},
			"/routeros/jobs/{id}/dry-run": map[string]any{"post": op("Dry-run RouterOS", "Comprueba conectividad y snapshot")},
			"/routeros/jobs/{id}/run":     map[string]any{"post": op("Ejecutar trabajo", "Despliegue masivo de scripts")},
			"/tools/winbox/ticket":        map[string]any{"post": op("Preparar Winbox", "Ticket de un solo uso para el puente local")},
			"/tools/winbox/bridge":        map[string]any{"get": op("Puente Winbox", "Descarga el instalador del puente para Windows")},
			"/tools/winbox/redeem":        map[string]any{"post": op("Canjear ticket Winbox", "Uso exclusivo del puente local")},
			"/traps":                      map[string]any{"get": op("Listar traps", "Feed forense decodificado")},
			"/traps/rules":                map[string]any{"get": op("Reglas de traps", ""), "post": op("Crear regla de trap", "")},
			"/traps/notifications":        map[string]any{"get": op("Notificaciones MIB", "NOTIFICATION-TYPE importados")},
			"/traps/test":                 map[string]any{"post": op("Probar trap", "Inyecta un trap sintético")},
			"/correlation/impact/{id}":    map[string]any{"get": op("Impacto de causa raíz", "Padres, L2 y equipos afectados")},
			"/imports/dude/preview":       map[string]any{"post": op("Preview The Dude", "SQLite o XML")},
			"/imports/dude/{id}/apply":    map[string]any{"post": op("Aplicar importación", "Crea mapas, dispositivos y enlaces")},
			"/reports/overview":           map[string]any{"get": op("Resumen de disponibilidad", "")},
			"/reports/interfaces":         map[string]any{"get": op("Disponibilidad por interfaz", "")},
			"/reports/monthly":            map[string]any{"get": op("Comparativa mensual", "")},
			"/reports/clients":            map[string]any{"get": op("SLA por cliente", "")},
			"/reports/schedules":          map[string]any{"get": op("Programaciones", ""), "post": op("Programar informe", "PDF/CSV diario, semanal o mensual")},
			"/reports/generate":           map[string]any{"post": op("Generar informe", "PDF/CSV en servidor")},
			"/reports/storage":            map[string]any{"get": op("Almacenamiento externo", ""), "put": op("Guardar almacenamiento externo", "")},
		},
	}
	writeJSON(w, http.StatusOK, spec)
}

func op(summary, description string) map[string]any {
	return map[string]any{
		"summary": summary, "description": description,
		"responses": map[string]any{"200": map[string]string{"description": "OK"}},
	}
}
