package metrics

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

var (
	deliveryProcessed atomic.Uint64
	deliveryFailed    atomic.Uint64
	usageRecorded     atomic.Uint64
	usageRejected     atomic.Uint64
)

// RecordDeliveryProcessed increments successful delivery worker completions.
func RecordDeliveryProcessed() { deliveryProcessed.Add(1) }

// RecordDeliveryFailed increments terminal delivery worker failures.
func RecordDeliveryFailed() { deliveryFailed.Add(1) }

// RecordUsageRecorded increments successfully persisted usage events.
func RecordUsageRecorded() { usageRecorded.Add(1) }

// RecordUsageRejected increments permanently rejected usage stream events.
func RecordUsageRejected() { usageRejected.Add(1) }

// Snapshot is a point-in-time operational counter set.
type Snapshot struct {
	DeliveryProcessed uint64 `json:"delivery_processed"`
	DeliveryFailed    uint64 `json:"delivery_failed"`
	UsageRecorded     uint64 `json:"usage_recorded"`
	UsageRejected     uint64 `json:"usage_rejected"`
}

// Current returns the latest counter values.
func Current() Snapshot {
	return Snapshot{
		DeliveryProcessed: deliveryProcessed.Load(),
		DeliveryFailed:    deliveryFailed.Load(),
		UsageRecorded:     usageRecorded.Load(),
		UsageRejected:     usageRejected.Load(),
	}
}

// Register mounts GET /metrics with JSON counters for local observability.
func Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Current())
	})
}
