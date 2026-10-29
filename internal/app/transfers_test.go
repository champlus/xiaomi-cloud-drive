package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestStatusProvidesTransferListsWithoutCloudRequests(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/api/status", nil)
	r.Header.Set("Authorization", "Bearer admin-secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var state map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	var transfers map[string]json.RawMessage
	if json.Unmarshal(state["transfers"], &transfers) != nil {
		t.Fatal("status has no structured transfer lists")
	}
	for _, key := range []string{"active", "completed", "failed"} {
		if string(transfers[key]) != "[]" {
			t.Fatalf("empty %s list must be an array, got %s", key, transfers[key])
		}
	}
}
