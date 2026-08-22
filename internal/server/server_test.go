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

func TestGenerateCustomTools(t *testing.T) {
	h := New()
	payload := []byte(`{"seed":3,"n":3,"tools":[{"name":"search_ticket","required":["query"]},{"name":"update_ticket","required":["id","status"]}]}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/generate", bytes.NewReader(payload))
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("generate %d %s", rec.Code, rec.Body.String())
	}
	var drafts []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &drafts); err != nil {
		t.Fatal(err)
	}
	if len(drafts) == 0 {
		t.Fatal("no drafts")
	}
	if drafts[0]["id"] == "close-acme" {
		t.Fatalf("ticket tools should not emit close-acme: %v", drafts[0])
	}
	if drafts[0]["expect"] == nil || drafts[0]["fixtures"] == nil {
		t.Fatalf("draft missing expect/fixtures: %v", drafts[0])
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
