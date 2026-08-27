package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"movie-showtimes/internal/letterboxd"
	"movie-showtimes/internal/server"
	"movie-showtimes/internal/tmdb"
)

//go:embed web/templates/* web/static/*
var webFS embed.FS

func main() {
	_ = godotenv.Load()
	tmdbSvc := tmdb.NewService()
	lb := letterboxd.New()
	srv, err := server.New(webFS, tmdbSvc, lb)
	if err != nil {
		log.Fatal(err)
	}
	// Bare ListenAndServe has no read deadline, so a client that opens a
	// connection and dribbles headers forever pins a goroutine — enough of them
	// and the server stops accepting. WriteTimeout stays unset on purpose:
	// /api/refresh holds the response open for the length of a full scrape.
	httpSrv := &http.Server{
		Addr:              ":8080",
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Graceful shutdown so `systemctl restart` doesn't cut off an in-flight
	// request. The window is generous because /api/refresh can be mid-scrape.
	idleClosed := make(chan struct{})
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		<-sigs
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		close(idleClosed)
	}()

	log.Println("Movie Showtimes on http://localhost:8080")
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-idleClosed
}
