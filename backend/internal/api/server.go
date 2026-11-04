package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netmonitor/netmonitor/internal/agents"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/config"
	"github.com/netmonitor/netmonitor/internal/state"
	"github.com/netmonitor/netmonitor/internal/ws"
)

type Server struct {
	pool      *pgxpool.Pool
	store     *state.Store
	hub       *ws.Hub
	agentMgr  *agents.Manager
	cfg       config.Config
	notifyAck func(context.Context, string)
}

func New(pool *pgxpool.Pool, store *state.Store, hub *ws.Hub, agentMgr *agents.Manager, cfg config.Config) *Server {
	return &Server{pool: pool, store: store, hub: hub, agentMgr: agentMgr, cfg: cfg}
}

func (s *Server) SetAckNotifier(fn func(context.Context, string)) { s.notifyAck = fn }

func (s *Server) AgentWSHandler() http.Handler {
	return http.HandlerFunc(s.handleAgentWS)
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)
	r.Use(selectiveTimeout)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-API-Key"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/api/v1/health", s.health)
	r.Get("/api/v1/openapi.json", s.openapi)
	r.Post("/api/v1/auth/login", s.login)
	if !s.cfg.AgentTLSEnabled {
		r.Get("/api/v1/agent/ws", s.handleAgentWS)
	}
	r.Post("/api/v1/telegram/webhook/{id}", s.telegramWebhook)
	r.Get("/ws", s.handleWS)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Use(s.auditMiddleware)
		r.Use(s.writeGuard)
		r.Use(s.accessGuard)
		r.Get("/auth/me", s.me)
		r.Get("/dashboard/summary", s.dashboardSummary)

		r.Get("/maps", s.listMaps)
		r.Post("/maps", s.createMap)
		r.Post("/maps/import", s.importMap)
		r.Get("/maps/{id}", s.getMap)
		r.Patch("/maps/{id}", s.updateMap)
		r.Put("/maps/{id}", s.updateMap)
		r.Delete("/maps/{id}", s.deleteMap)
		r.Get("/maps/{id}/canvas", s.getCanvas)
		r.Patch("/maps/{id}/layout", s.saveLayout)
		r.Post("/maps/{id}/elements", s.createElement)
		r.Post("/maps/{id}/background", s.uploadMapBackground)
		r.Patch("/maps/{id}/elements/{elemID}", s.updateElement)
		r.Delete("/maps/{id}/elements/{elemID}", s.deleteElement)
		r.Post("/maps/{id}/links", s.createLink)
		r.Patch("/maps/{id}/links/{linkID}", s.updateLink)
		r.Delete("/maps/{id}/links/{linkID}", s.deleteLink)
		r.Get("/maps/{id}/export", s.exportMap)

		r.Get("/networks", s.listNetworks)
		r.Post("/networks", s.createNetwork)
		r.Put("/networks/{id}", s.updateNetwork)
		r.Delete("/networks/{id}", s.deleteNetwork)
		r.Post("/networks/{id}/scan", s.scanNetworkNow)
		r.Get("/networks/{id}/runs", s.listNetworkRuns)
		r.Get("/networks/{id}/runs/{runID}", s.getNetworkRun)
		r.Post("/networks/{id}/apply", s.applyNetworkRun)

		r.Get("/address-lists", s.listAddressLists)
		r.Post("/address-lists", s.createAddressList)
		r.Get("/address-lists/{id}", s.getAddressList)
		r.Put("/address-lists/{id}", s.updateAddressList)
		r.Delete("/address-lists/{id}", s.deleteAddressList)
		r.Post("/address-lists/{id}/entries", s.addAddressListEntry)
		r.Delete("/address-lists/{id}/entries/{entryID}", s.deleteAddressListEntry)
		r.Post("/address-lists/{id}/import", s.importAddressList)
		r.Get("/address-lists/{id}/export", s.exportAddressList)

		r.Get("/panels", s.listPanels)
		r.Post("/panels", s.createPanel)
		r.Put("/panels/{id}", s.updatePanel)
		r.Delete("/panels/{id}", s.deletePanel)

		r.Get("/metrics/catalog", s.metricsCatalog)
		r.Get("/metrics/compare", s.metricsCompare)
		r.Get("/notification-sounds", s.listNotificationSounds)
		r.Post("/notification-sounds", s.uploadNotificationSound)
		r.Delete("/notification-sounds/{id}", s.deleteNotificationSound)
		r.Get("/settings/noc", s.getNOCSettings)
		r.Put("/settings/noc", s.setNOCSettings)
		r.Post("/mibs/label", s.mibObjectToLabel)

		r.Get("/devices", s.listDevices)
		r.Post("/devices", s.createDevice)
		r.Get("/devices/{id}", s.getDevice)
		r.Put("/devices/{id}", s.updateDevice)
		r.Delete("/devices/{id}", s.deleteDevice)
		r.Post("/devices/{id}/acknowledge", s.acknowledgeDevice)
		r.Delete("/devices/{id}/acknowledge", s.unacknowledgeDevice)
		r.Post("/devices/{id}/poll-now", s.pollNow)
		r.Post("/devices/{id}/maintenance", s.setMaintenance)
		r.Get("/devices/{id}/interfaces", s.deviceInterfaces)
		r.Get("/devices/{id}/snmp-info", s.deviceSNMPInfo)
		r.Get("/devices/{id}/services", s.listServices)
		r.Post("/devices/{id}/services", s.addService)
		r.Post("/devices/{id}/ensure-probe", s.ensureDeviceProbe)
		r.Delete("/devices/{id}/services/{serviceID}", s.deleteService)

		r.Get("/device-types", s.listDeviceTypes)
		r.Post("/device-types", s.createDeviceType)
		r.Put("/device-types/{id}", s.updateDeviceType)
		r.Delete("/device-types/{id}", s.deleteDeviceType)
		r.Get("/probes", s.listProbes)
		r.Post("/probes", s.createProbe)
		r.Put("/probes/{id}", s.updateProbe)
		r.Delete("/probes/{id}", s.deleteProbe)
		r.Get("/functions", s.listFunctions)
		r.Post("/functions", s.createFunction)
		r.Get("/snmp-profiles", s.listSNMPProfiles)
		r.With(s.adminOnly).Post("/snmp-profiles", s.createSNMPProfile)
		r.With(s.adminOnly).Put("/snmp-profiles/{id}", s.updateSNMPProfile)
		r.With(s.adminOnly).Delete("/snmp-profiles/{id}", s.deleteSNMPProfile)
		r.Get("/mibs", s.listMIBs)
		r.Post("/mibs/import", s.importMIB)
		r.Post("/mibs/import-batch", s.importMIBBatch)
		r.Delete("/mibs/{module}", s.deleteMIB)
		r.Get("/mibs/objects", s.searchMIBObjects)

		r.Group(func(r chi.Router) {
			r.Use(s.adminOnly)
			r.Get("/users", s.listUsers)
			r.Post("/users", s.createUser)
			r.Put("/users/{id}", s.updateUser)
			r.Delete("/users/{id}", s.deleteUser)
			r.Get("/users/{id}/maps", s.getUserMaps)
			r.Put("/users/{id}/maps", s.setUserMaps)
			r.Get("/users/{id}/groups", s.getUserGroups)
			r.Put("/users/{id}/groups", s.setUserGroups)
			r.Get("/users/{id}/devices", s.getUserDevices)
			r.Put("/users/{id}/devices", s.setUserDevices)
			r.Get("/device-groups", s.listDeviceGroups)
			r.Post("/device-groups", s.createDeviceGroup)
			r.Put("/device-groups/{id}", s.updateDeviceGroup)
			r.Delete("/device-groups/{id}", s.deleteDeviceGroup)
			r.Get("/audit", s.listAuditLogs)
			r.Get("/api-keys", s.listAPIKeys)
			r.Post("/api-keys", s.createAPIKey)
			r.Put("/api-keys/{id}", s.updateAPIKey)
			r.Delete("/api-keys/{id}", s.deleteAPIKey)
			r.Post("/agents", s.createAgent)
			r.Put("/agents/{id}", s.updateAgent)
			r.Post("/agents/{id}/update", s.updateAgentBinary)
			r.Get("/agent-releases", s.listAgentReleases)
			r.Post("/agent-releases", s.createAgentRelease)
			r.Delete("/agent-releases/{id}", s.deleteAgentRelease)
			r.Post("/agents/update", s.batchUpdateAgents)
			r.Delete("/agents/{id}", s.deleteAgent)

			// Fase 3 · Gestión RouterOS (solo administradores).
			r.Get("/routeros/inventory", s.listRouterOSInventory)
			r.Post("/routeros/inventory/refresh", s.refreshRouterOSInventory)
			r.Get("/routeros/credentials", s.listRouterOSCredentials)
			r.Post("/routeros/credentials", s.createRouterOSCredential)
			r.Put("/routeros/credentials/{id}", s.updateRouterOSCredential)
			r.Delete("/routeros/credentials/{id}", s.deleteRouterOSCredential)
			r.Post("/routeros/assign", s.assignRouterOSCredential)
			r.Post("/routeros/test", s.testRouterOS)
			r.Get("/routeros/templates", s.listRouterOSTemplates)
			r.Post("/routeros/templates", s.createRouterOSTemplate)
			r.Put("/routeros/templates/{id}", s.updateRouterOSTemplate)
			r.Delete("/routeros/templates/{id}", s.deleteRouterOSTemplate)
			r.Post("/routeros/templates/preview", s.previewRouterOSTemplate)
			r.Get("/routeros/jobs", s.listRouterOSJobs)
			r.Post("/routeros/jobs", s.createRouterOSJob)
			r.Get("/routeros/jobs/{id}", s.getRouterOSJob)
			r.Post("/routeros/jobs/{id}/approve", s.approveRouterOSJob)
			r.Post("/routeros/jobs/{id}/dry-run", s.dryRunRouterOSJob)
			r.Post("/routeros/jobs/{id}/run", s.runRouterOSJob)
			r.Post("/routeros/jobs/{id}/cancel", s.cancelRouterOSJob)
			r.Delete("/routeros/jobs/{id}", s.deleteRouterOSJob)
		})
		r.Get("/agents", s.listAgents)

		r.Get("/settings/key-status", s.keyStatus)
		r.Get("/settings/holidays", s.getGlobalHolidays)
		r.Put("/settings/holidays", s.setGlobalHolidays)
		r.Get("/settings/snmp-refresh", s.getSNMPRefreshSettings)
		r.Put("/settings/snmp-refresh", s.setSNMPRefreshSettings)
		r.Get("/maintenance-windows", s.listMaintenanceWindows)
		r.Post("/maintenance-windows", s.createMaintenanceWindow)
		r.Delete("/maintenance-windows/{id}", s.deleteMaintenanceWindow)
		r.With(s.adminOnly).Get("/reports/sla", s.slaReport)
		r.Get("/reports/overview", s.reportOverview)
		r.Get("/reports/devices", s.reportDevices)
		r.Get("/reports/interfaces", s.reportInterfaces)
		r.Get("/reports/monthly", s.reportMonthly)
		r.Get("/reports/clients", s.reportClients)
		r.Get("/reports/sla-clients", s.listSLAClients)
		r.With(s.adminOnly).Post("/reports/sla-clients", s.createSLAClient)
		r.With(s.adminOnly).Put("/reports/sla-clients/{id}", s.updateSLAClient)
		r.With(s.adminOnly).Delete("/reports/sla-clients/{id}", s.deleteSLAClient)
		r.With(s.adminOnly).Get("/reports/schedules", s.listReportSchedules)
		r.With(s.adminOnly).Post("/reports/schedules", s.createReportSchedule)
		r.With(s.adminOnly).Put("/reports/schedules/{id}", s.updateReportSchedule)
		r.With(s.adminOnly).Delete("/reports/schedules/{id}", s.deleteReportSchedule)
		r.With(s.adminOnly).Post("/reports/schedules/{id}/run", s.runReportScheduleNow)
		r.Get("/reports/runs", s.listReportRuns)
		r.Get("/reports/runs/{id}/download", s.downloadReportRun)
		r.Post("/reports/generate", s.generateReport)
		r.With(s.adminOnly).Get("/reports/storage", s.getReportStorage)
		r.With(s.adminOnly).Put("/reports/storage", s.setReportStorage)
		r.With(s.adminOnly).Post("/reports/storage/test", s.testReportStorage)
		r.Get("/outages", s.listOutages)
		r.Get("/outages/active", s.listActiveOutages)
		r.Post("/outages/{id}/acknowledge", s.acknowledgeOutage)

		r.Get("/events", s.listEvents)
		r.Get("/traps", s.listTraps)
		r.Get("/traps/rules", s.listTrapRules)
		r.Get("/traps/notifications", s.listMIBNotifications)
		r.Get("/traps/settings", s.getTrapSettings)
		r.With(s.adminOnly).Put("/traps/settings", s.setTrapSettings)
		r.With(s.adminOnly).Post("/traps/test", s.testTrap)
		r.Get("/traps/{id}", s.getTrap)
		r.With(s.adminOnly).Post("/traps/rules", s.createTrapRule)
		r.With(s.adminOnly).Put("/traps/rules/{id}", s.updateTrapRule)
		r.With(s.adminOnly).Delete("/traps/rules/{id}", s.deleteTrapRule)
		r.Get("/correlation/root/{id}", s.correlationRoot)
		r.Get("/correlation/impact/{id}", s.correlationImpact)
		r.With(s.adminOnly).Post("/correlation/recompute", s.recomputeCorrelation)
		r.With(s.adminOnly).Get("/imports/dude", s.listDudeImports)
		r.With(s.adminOnly).Post("/imports/dude/preview", s.previewDudeImport)
		r.With(s.adminOnly).Get("/imports/dude/{id}", s.getDudeImport)
		r.With(s.adminOnly).Post("/imports/dude/{id}/apply", s.applyDudeImport)
		r.Get("/metrics/service/{id}", s.serviceMetrics)
		r.Get("/metrics/link/{id}", s.linkMetrics)
		r.Get("/metrics/device/{id}", s.deviceMetrics)

		r.Get("/digest-rules", s.listDigestRules)
		r.Post("/digest-rules", s.createDigestRule)
		r.Put("/digest-rules/{id}", s.updateDigestRule)
		r.Delete("/digest-rules/{id}", s.deleteDigestRule)
		r.Get("/alert-rules", s.listAlertRules)
		r.Post("/alert-rules", s.createAlertRule)
		r.Put("/alert-rules/{id}", s.updateAlertRule)
		r.Delete("/alert-rules/{id}", s.deleteAlertRule)
		r.Get("/notification-channels", s.listChannels)
		r.With(s.adminOnly).Post("/notification-channels", s.createChannel)
		r.With(s.adminOnly).Put("/notification-channels/{id}", s.updateChannel)
		r.With(s.adminOnly).Delete("/notification-channels/{id}", s.deleteChannel)
		r.With(s.adminOnly).Post("/notification-channels/{id}/test", s.testChannel)
		r.With(s.adminOnly).Post("/notification-channels/{id}/telegram-webhook", s.setupTelegramWebhook)

		r.Post("/tools/snmp-walk", s.snmpWalk)
		r.Post("/tools/evaluate-dsl", s.evaluateDSL)
		r.Post("/tools/ping", s.pingTool)
		r.Post("/tools/dns", s.dnsTool)
		r.Post("/tools/tcp", s.tcpTool)
		r.Post("/tools/http", s.httpTool)
		r.Post("/tools/traceroute", s.tracerouteTool)
		r.Post("/discovery/scan", s.discoveryScan)
		r.Post("/discovery/links", s.discoverLinks)
	})
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := "ok"
	code := http.StatusOK
	if err := s.pool.Ping(ctx); err != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "time": time.Now().UTC()})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if apiKey == "" {
		apiKey = r.URL.Query().Get("api_key")
	}
	if apiKey != "" {
		u, ok := s.apiKeyUser(r.Context(), apiKey)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		s.hub.ServeHTTP(w, r, s.wsAccess(r.Context(), u))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	claims, err := auth.Parse(s.cfg.JWTSecret, token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	u, ok := s.refreshUser(r.Context(), claims.UserID, claims.Version)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or expired session")
		return
	}
	s.hub.ServeHTTP(w, r, s.wsAccess(r.Context(), u))
}

func (s *Server) refreshUser(ctx context.Context, userID string, version int) (auth.User, bool) {
	if userID == "" {
		return auth.User{}, false
	}
	var u auth.User
	var tokenVersion int
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, COALESCE(display_name,''), role, restrict_maps, restrict_groups, restrict_devices,
		       COALESCE(allowed_probe_types,'{}'), token_version
		FROM users WHERE id=$1
	`, userID).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.RestrictMaps, &u.RestrictGroups,
		&u.RestrictDevices, &u.AllowedProbeTypes, &tokenVersion)
	if err != nil || tokenVersion != version {
		return auth.User{}, false
	}
	return u, true
}

func (s *Server) wsAccess(ctx context.Context, u auth.User) ws.Access {
	access := ws.Access{Username: u.Username}
	access.Maps, access.RestrictMaps = s.allowedMapIDs(ctx, u)
	access.Devices, access.RestrictDevices = s.allowedDeviceIDs(ctx, u)
	return access
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
			u, ok := s.apiKeyUser(r.Context(), apiKey)
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
			return
		}
		raw := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := auth.Parse(s.cfg.JWTSecret, strings.TrimSpace(raw[7:]))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		u, ok := s.refreshUser(r.Context(), claims.UserID, claims.Version)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"success": false, "error": message})
}

func decodeBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}
