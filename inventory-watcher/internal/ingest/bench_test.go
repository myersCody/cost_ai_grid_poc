package ingest_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func BenchmarkIngestMaaSEvent(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventID := fmt.Sprintf("bench-maas-%d-%d", time.Now().UnixNano(), i)
		event := map[string]interface{}{
			"specversion": "1.0",
			"type":        "osac.model.lifecycle",
			"source":      "bench",
			"id":          eventID,
			"time":        time.Now().UTC().Format(time.RFC3339),
			"subject":     "tenant-bench",
			"data": map[string]interface{}{
				"tenant_id":        "tenant-bench",
				"model_id":         fmt.Sprintf("bench-model-%d", i%10),
				"model_name":       "bench-model",
				"state":            "MODEL_STATE_RUNNING",
				"tokens_in":        1000,
				"tokens_out":       500,
				"requests":         1,
				"duration_seconds": 1,
			},
		}

		body, _ := json.Marshal(event)
		resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkIngestVMEvent(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventID := fmt.Sprintf("bench-vm-%d-%d", time.Now().UnixNano(), i)
		event := map[string]interface{}{
			"specversion": "1.0",
			"type":        "osac.compute_instance.lifecycle",
			"source":      "bench",
			"id":          eventID,
			"time":        time.Now().UTC().Format(time.RFC3339),
			"data": map[string]interface{}{
				"tenant_id":        "tenant-bench",
				"instance_id":      fmt.Sprintf("bench-vm-%d", i%100),
				"cores":            4,
				"memory_gib":       16,
				"state":            "COMPUTE_INSTANCE_STATE_RUNNING",
				"duration_seconds": 60,
				"cpu_core_seconds": 240,
				"memory_gib_seconds": 960,
			},
		}

		body, _ := json.Marshal(event)
		resp, err := http.Post(testServer.URL+"/api/v1/events", "application/json", bytes.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}
