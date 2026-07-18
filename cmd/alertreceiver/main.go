// Command alertreceiver accepts Alertmanager webhook notifications on a
// loopback-only listener and stores a bounded, secret-free event journal.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultListenAddress = "127.0.0.1:19193"
	maxRequestBytes      = 256 << 10
	maxAlerts            = 64
)

type webhookPayload struct {
	Status string         `json:"status"`
	Alerts []webhookAlert `json:"alerts"`
}

type webhookAlert struct {
	Status   string            `json:"status"`
	Labels   map[string]string `json:"labels"`
	StartsAt time.Time         `json:"startsAt"`
	EndsAt   time.Time         `json:"endsAt"`
}

type journalEvent struct {
	ReceivedAt time.Time      `json:"received_at"`
	Status     string         `json:"status"`
	Alerts     []journalAlert `json:"alerts"`
}

type journalAlert struct {
	AlertName string    `json:"alert_name"`
	Severity  string    `json:"severity,omitempty"`
	Service   string    `json:"service,omitempty"`
	Status    string    `json:"status"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at,omitempty"`
}

type journal struct {
	mu   sync.Mutex
	file *os.File
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	listen := flag.String("listen", defaultListenAddress, "loopback HTTP listen address")
	output := flag.String("output", "", "0600 JSONL journal path")
	flag.Parse()
	if strings.TrimSpace(*output) == "" {
		logger.Error("output path is required")
		os.Exit(2)
	}

	store, err := openJournal(*output)
	if err != nil {
		logger.Error("open alert journal", "error", err)
		os.Exit(1)
	}
	defer store.close()

	server := &http.Server{
		Addr:              *listen,
		Handler:           newHandler(store),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	logger.Info("alert receiver listening", "address", server.Addr, "output", filepath.Clean(*output))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("alert receiver stopped", "error", err)
		os.Exit(1)
	}
}

func openJournal(filename string) (*journal, error) {
	filename = filepath.Clean(filename)
	if filename == "." || filepath.IsAbs(filename) == false {
		return nil, fmt.Errorf("journal path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return nil, fmt.Errorf("create journal directory: %w", err)
	}
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("protect journal: %w", err)
	}
	return &journal{file: file}, nil
}

func (store *journal) close() {
	store.mu.Lock()
	defer store.mu.Unlock()
	_ = store.file.Close()
}

func (store *journal) append(event journalEvent) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	store.mu.Lock()
	defer store.mu.Unlock()
	_, err = store.file.Write(encoded)
	return err
}

func newHandler(store *journal) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(response, "ok\n")
	})
	mux.HandleFunc("/alerts", func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		payload, err := decodePayload(request.Body)
		if err != nil {
			http.Error(response, "invalid alert payload", http.StatusBadRequest)
			return
		}
		event := journalEvent{ReceivedAt: time.Now().UTC(), Status: strings.TrimSpace(payload.Status), Alerts: make([]journalAlert, 0, len(payload.Alerts))}
		if event.Status == "" {
			event.Status = "unknown"
		}
		for _, alert := range payload.Alerts {
			status := strings.TrimSpace(alert.Status)
			if status == "" {
				status = event.Status
			}
			event.Alerts = append(event.Alerts, journalAlert{
				AlertName: boundedLabel(alert.Labels["alertname"]),
				Severity:  boundedLabel(alert.Labels["severity"]),
				Service:   boundedLabel(alert.Labels["service"]),
				Status:    status,
				StartsAt:  alert.StartsAt.UTC(),
				EndsAt:    alert.EndsAt.UTC(),
			})
		}
		if err := store.append(event); err != nil {
			http.Error(response, "journal unavailable", http.StatusInternalServerError)
			return
		}
		response.WriteHeader(http.StatusAccepted)
	})
	return mux
}

func decodePayload(body io.Reader) (webhookPayload, error) {
	decoder := json.NewDecoder(io.LimitReader(body, maxRequestBytes+1))
	var payload webhookPayload
	if err := decoder.Decode(&payload); err != nil {
		return webhookPayload{}, err
	}
	if len(payload.Alerts) == 0 || len(payload.Alerts) > maxAlerts {
		return webhookPayload{}, fmt.Errorf("alert count is out of bounds")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return webhookPayload{}, fmt.Errorf("payload contains trailing data")
	}
	return payload, nil
}

func boundedLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
