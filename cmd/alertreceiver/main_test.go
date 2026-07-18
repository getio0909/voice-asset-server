package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlertHandlerStoresOnlyAllowlistedFields(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "alerts.jsonl")
	store, err := openJournal(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()

	request := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(`{
  "status": "firing",
  "externalURL": "https://alerts.example.test/secret-token",
  "alerts": [{
    "status": "firing",
    "labels": {"alertname": "VoiceAssetAPIDown", "severity": "critical", "service": "voiceasset-api", "secret": "do-not-store"},
    "annotations": {"description": "private detail"},
    "startsAt": "2026-07-18T12:00:00Z",
    "generatorURL": "https://prometheus.example.test/secret"
  }]
}`))
	recorder := httptest.NewRecorder()
	newHandler(store).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	value, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(value), "secret-token") || strings.Contains(string(value), "do-not-store") || strings.Contains(string(value), "private detail") {
		t.Fatalf("journal retained unallowlisted data: %s", value)
	}
	var event journalEvent
	if err := json.Unmarshal(bytes.TrimSpace(value), &event); err != nil {
		t.Fatal(err)
	}
	if event.Status != "firing" || len(event.Alerts) != 1 || event.Alerts[0].AlertName != "VoiceAssetAPIDown" {
		t.Fatalf("event = %+v", event)
	}
}

func TestAlertHandlerRejectsInvalidPayload(t *testing.T) {
	store, err := openJournal(filepath.Join(t.TempDir(), "alerts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	recorder := httptest.NewRecorder()
	newHandler(store).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(`{"status":"firing","alerts":[]}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestHealthHandler(t *testing.T) {
	store, err := openJournal(filepath.Join(t.TempDir(), "alerts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	recorder := httptest.NewRecorder()
	newHandler(store).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "ok" {
		t.Fatalf("health response = %d/%q", recorder.Code, recorder.Body.String())
	}
}
