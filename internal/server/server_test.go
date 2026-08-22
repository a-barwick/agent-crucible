package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthAndMeta(t *testing.T) {
	h := New()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != 200 {
		t.Fatalf("health %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/meta", nil))
	if rec.Code != 200 {
		t.Fatalf("meta %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["agent"] == nil || body["faults"] == nil {
		t.Fatalf("%v", body)
	}
}

func TestRunEndpoint(t *testing.T) {
	h := New()
	payload := []byte(`{"seed":1,"trials":8,"p":0,"faults":["timeout","malformed"]}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader(payload))
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("run %d %s", rec.Code, rec.Body.String())
	}
	var suite map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &suite); err != nil {
		t.Fatal(err)
	}
	if suite["survival"] != 1.0 {
		t.Fatalf("survival %v", suite["survival"])
	}
}

func TestIndexServed(t *testing.T) {
	h := New()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 {
		t.Fatalf("index %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Torture chamber")) {
		t.Fatal("index missing title")
	}
}
