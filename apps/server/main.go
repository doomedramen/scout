package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"scout.local/scout/internal/control"
	"scout.local/scout/internal/identity"
	"scout.local/scout/internal/store"
)

func main() {
	var db *sql.DB
	if dsn := os.Getenv("SCOUT_DATABASE_URL"); dsn != "" {
		var err error
		db, err = sql.Open("pgx", dsn)
		if err != nil {
			log.Fatal("invalid database configuration")
		}
		defer db.Close()
		db.SetMaxOpenConns(5)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := db.PingContext(ctx); err != nil && os.Getenv("SCOUT_PRODUCTION") == "true" {
			cancel()
			log.Fatal("database unavailable")
		}
		cancel()
		if err := store.RunMigrations(context.Background(), db); err != nil && os.Getenv("SCOUT_PRODUCTION") == "true" {
			log.Fatal("database migrations failed")
		}
	}
	listen := os.Getenv("SCOUT_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	var database control.Database
	if db != nil {
		database = db
	}
	production := os.Getenv("SCOUT_PRODUCTION") == "true"
	config := control.Config{Production: production, AllowedOrigin: os.Getenv("SCOUT_ALLOWED_ORIGIN"), SetupToken: os.Getenv("SCOUT_SETUP_TOKEN"), SetupTokenFile: os.Getenv("SCOUT_SETUP_TOKEN_FILE"), SecretKeyFile: os.Getenv("SCOUT_SECRET_KEY_FILE"), AgentCAFile: os.Getenv("SCOUT_AGENT_CA_FILE"), AgentCAKeyFile: os.Getenv("SCOUT_AGENT_CA_KEY_FILE"), StartRecovery: os.Getenv("SCOUT_RECOVERY_MODE") == "true", ReleaseTrustFile: os.Getenv("SCOUT_RELEASE_TRUST_FILE"), ArtifactDir: os.Getenv("SCOUT_ARTIFACT_DIR"), AgentRequireMTLS: os.Getenv("SCOUT_REQUIRE_AGENT_MTLS") == "true"}
	app, err := control.NewApp(func() *store.Store {
		if db != nil {
			return store.NewSQL(db)
		}
		return store.NewMemory()
	}(), database, config)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Scout API listening on %s", listen)
	tlsSettings := identity.TLSSettings{CertificateFile: os.Getenv("SCOUT_TLS_CERT_FILE"), PrivateKeyFile: os.Getenv("SCOUT_TLS_KEY_FILE"), ClientCAFile: os.Getenv("SCOUT_AGENT_CA_FILE"), RequireClient: config.AgentRequireMTLS, Production: production}
	if tlsSettings.CertificateFile != "" || production {
		tlsConfig, tlsErr := identity.LoadServerTLS(tlsSettings)
		if tlsErr != nil {
			log.Fatal(tlsErr)
		}
		server.TLSConfig = tlsConfig
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
		return
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
