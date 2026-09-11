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
	}
	listen := os.Getenv("SCOUT_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	var database control.Database
	if db != nil {
		database = db
	}
	server := &http.Server{Addr: listen, Handler: control.Handler(database), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Scout development API listening on %s", listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
