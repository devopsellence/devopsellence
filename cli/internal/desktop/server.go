package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Options struct {
	Addr          string
	WorkspaceRoot string
	StatePath     string
	OpenBrowser   bool
	Out           io.Writer
	Err           io.Writer
}

type ReadyEvent struct {
	SchemaVersion int    `json:"schema_version"`
	Event         string `json:"event"`
	URL           string `json:"url"`
	Address       string `json:"address"`
	WorkspaceRoot string `json:"workspace_root"`
}

func Serve(ctx context.Context, opts Options) error {
	addr := strings.TrimSpace(opts.Addr)
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           NewHandler(SummaryOptions{WorkspaceRoot: opts.WorkspaceRoot, StatePath: opts.StatePath}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	url := "http://" + listener.Addr().String()
	if opts.Out != nil {
		_ = json.NewEncoder(opts.Out).Encode(ReadyEvent{
			SchemaVersion: apiSchemaVersion,
			Event:         "desktop_ready",
			URL:           url,
			Address:       listener.Addr().String(),
			WorkspaceRoot: opts.WorkspaceRoot,
		})
	}
	if opts.OpenBrowser {
		if err := openURL(url); err != nil && opts.Err != nil {
			fmt.Fprintf(opts.Err, "warning: could not open browser: %v\n", err)
		}
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		return ctx.Err()
	case err := <-serverErr:
		return err
	}
}

func NewHandler(summaryOpts SummaryOptions) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		serveIndex(w)
	})
	mux.HandleFunc("GET /api/summary", func(w http.ResponseWriter, r *http.Request) {
		summary, err := BuildSummary(summaryOpts)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"schema_version": apiSchemaVersion,
				"ok":             false,
				"error": map[string]any{
					"code":    "summary_failed",
					"message": err.Error(),
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, summary)
	})
	return mux
}

func serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}
