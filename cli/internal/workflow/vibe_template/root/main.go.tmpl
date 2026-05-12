package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed templates/*.html static/*
var assets embed.FS

type note struct {
	ID        int64
	Title     string
	Body      string
	CreatedAt string
}

type app struct {
	db        *sql.DB
	templates *template.Template
	static    http.Handler
}

const maxNotes = 50

func main() {
	addr := env("ADDR", ":8080")
	dbPath := env("DB_PATH", filepath.Join("data", "app.sqlite"))

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	application, err := newApp(db)
	if err != nil {
		log.Fatalf("initialize app: %v", err)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           application.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func newApp(db *sql.DB) (*app, error) {
	if err := migrate(db); err != nil {
		return nil, err
	}

	templates, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}

	staticFiles, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}

	return &app{
		db:        db,
		templates: templates,
		static:    http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))),
	}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", a.static)
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /", a.home)
	mux.HandleFunc("POST /notes", a.createNote)
	return mux
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) home(w http.ResponseWriter, r *http.Request) {
	notes, err := a.listNotes(r.Context())
	if err != nil {
		http.Error(w, "could not load notes", http.StatusInternalServerError)
		return
	}

	var body bytes.Buffer
	if err := a.templates.ExecuteTemplate(&body, "index.html", map[string]any{"Notes": notes}); err != nil {
		log.Printf("render home: %v", err)
		http.Error(w, "could not render page", http.StatusInternalServerError)
		return
	}
	_, _ = body.WriteTo(w)
}

func (a *app) createNote(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if _, err := a.db.ExecContext(ctx, `INSERT INTO notes (title, body) VALUES (?, ?)`, title, body); err != nil {
		http.Error(w, "could not save note", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) listNotes(ctx context.Context) ([]note, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := a.db.QueryContext(ctx, `SELECT id, title, body, created_at FROM notes ORDER BY id DESC LIMIT ?`, maxNotes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []note
	for rows.Next() {
		var item note
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.CreatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, item)
	}
	return notes, rows.Err()
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
