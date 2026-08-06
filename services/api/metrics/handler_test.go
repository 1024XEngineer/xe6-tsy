package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsHandlerReturnsCounters(t *testing.T) {
	RecordDeliveryProcessed()
	RecordDeliveryFailed()
	RecordUsageRecorded()
	RecordUsageRejected()

	mux := http.NewServeMux()
	Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var snapshot Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if snapshot.DeliveryProcessed == 0 || snapshot.DeliveryFailed == 0 || snapshot.UsageRecorded == 0 || snapshot.UsageRejected == 0 {
		t.Fatalf("snapshot = %#v, want non-zero counters", snapshot)
	}
}
