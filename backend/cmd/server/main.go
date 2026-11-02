package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/netmonitor/netmonitor/internal/agents"
	"github.com/netmonitor/netmonitor/internal/api"
	"github.com/netmonitor/netmonitor/internal/config"
	"github.com/netmonitor/netmonitor/internal/db"
	"github.com/netmonitor/netmonitor/internal/keyring"
	"github.com/netmonitor/netmonitor/internal/leader"
	"github.com/netmonitor/netmonitor/internal/poller"
	"github.com/netmonitor/netmonitor/internal/receivers"
	"github.com/netmonitor/netmonitor/internal/secure"
	"github.com/netmonitor/netmonitor/internal/state"
	"github.com/netmonitor/netmonitor/internal/ws"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("insecure configuration: %v", err)
	}
	key, err := keyring.Resolve(cfg)
	if err != nil {
		log.Fatalf("encryption key: %v", err)
	}
	secure.SetKeys(key.Current, key.Previous)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, cfg.SeedDemo); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	if err := db.EnsureAdmin(ctx, pool, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("admin seed: %v", err)
	}
	if err := db.EncryptExistingSecrets(ctx, pool); err != nil {
		log.Fatalf("secret migration: %v", err)
	}
	if err := db.ValidateSecrets(ctx, pool); err != nil {
		log.Fatalf("secret validation: %v", err)
	}
	startKeyRotation(ctx, pool, cfg, key)

	store, err := state.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer store.Close()

	hub := ws.NewHub()
	hub.EnableFanout(ctx, store.RDB)
	agentMgr := agents.New(pool)
	p := poller.New(pool, store, hub, cfg)
	p.SetAgents(agentMgr)
	startWork := func() {
		p.Start(ctx)
		recv := receivers.New(pool, store, hub)
		recv.SetNotifier(p.NotifyTrap, p.NotifyTrapChannels)
		recv.Start(ctx, cfg.TrapAddr, cfg.SyslogAddr, cfg.EnableTraps, cfg.EnableSyslog)
	}
	if cfg.LeaderElection {
		elector := leader.New(pool)
		go func() {
			for {
				ok, err := elector.Acquire(ctx)
				if err != nil {
					log.Printf("leader election: %v", err)
				}
				if ok {
					log.Printf("leader election: this instance is the active core")
					startWork()
					<-ctx.Done()
					elector.Release(context.Background())
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	} else {
		startWork()
	}

	srv := api.New(pool, store, hub, agentMgr, cfg)
	srv.SetAckNotifier(p.NotifyAck)
	srv.StartBackground(ctx)
	if cfg.AgentTLSEnabled {
		go startAgentTLS(cfg, srv)
	}
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0, // WebSocket connections are long-lived.
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("NetMonitor API listening on %s (env=%s)", cfg.HTTPAddr, cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")
	cancel()
	p.Stop()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func startAgentTLS(cfg config.Config, srv *api.Server) {
	cert, err := tls.LoadX509KeyPair(cfg.AgentTLSCert, cfg.AgentTLSKey)
	if err != nil {
		log.Fatalf("agent mTLS enabled but certificate failed: %v", err)
	}
	caPEM, err := os.ReadFile(cfg.AgentTLSCA)
	if err != nil {
		log.Fatalf("agent mTLS enabled but CA failed: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		log.Fatalf("agent mTLS CA certificate is invalid")
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
	tlsServer := &http.Server{
		Addr:              cfg.AgentTLSAddr,
		Handler:           srv.AgentWSHandler(),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("agent mTLS listening on %s", cfg.AgentTLSAddr)
	if err := tlsServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("agent mTLS server: %v", err)
	}
}
