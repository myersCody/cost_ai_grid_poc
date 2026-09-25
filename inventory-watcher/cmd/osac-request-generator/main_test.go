package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoRequestReadsWrappedObjectID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":{"id":"resource-id"}}`))
	}))
	defer server.Close()

	id, err := doRequest(server.Client(), http.MethodPost, server.URL, "token", struct{}{})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if id != "resource-id" {
		t.Fatalf("got id %q, want resource-id", id)
	}
}

func TestCreateInstanceTypeFallsBackForLegacyCRC(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if requests == 1 {
			if _, ok := body["spec"]["vcpus"]; !ok {
				t.Fatalf("first request did not use current vcpus field: %v", body)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"proto: unknown field \"vcpus\""}`))
			return
		}

		if _, ok := body["spec"]["cores"]; !ok {
			t.Fatalf("fallback request did not use legacy cores field: %v", body)
		}
		if _, ok := body["spec"]["vcpus"]; ok {
			t.Fatalf("fallback request still contains vcpus field: %v", body)
		}
		_, _ = w.Write([]byte(`{"id":"legacy-instance-type"}`))
	}))
	defer server.Close()

	id, err := createInstanceType(server.Client(), server.URL, "token", "sim-type")
	if err != nil {
		t.Fatalf("create instance type failed: %v", err)
	}
	if id != "legacy-instance-type" {
		t.Fatalf("got id %q, want legacy-instance-type", id)
	}
	if requests != 2 {
		t.Fatalf("got %d requests, want current request plus one fallback", requests)
	}
}

func TestNetworkClassPayloadUsesCurrentSchema(t *testing.T) {
	payload, err := json.Marshal(ncPayload{
		Metadata:      metadata{Name: "sim-nc"},
		Title:         "OSAC Request Generator",
		Description:   "test",
		FabricManager: "test",
	})
	if err != nil {
		t.Fatalf("marshal network class: %v", err)
	}

	body := string(payload)
	if strings.Contains(body, "implementation_strategy") {
		t.Fatalf("network class contains removed implementation_strategy field: %s", body)
	}
	if !strings.Contains(body, `"fabric_manager":"test"`) {
		t.Fatalf("network class does not contain fabric_manager: %s", body)
	}
}

func TestComputeInstancePayloadUsesCurrentSchema(t *testing.T) {
	payload, err := json.Marshal(vmPayload{
		Metadata: metadata{Name: "sim-vm", Tenant: "test"},
		Spec: vmSpec{
			Template: templateRef{ID: "template-id"},
			NetworkAttachments: []netAttachment{
				{Subnet: networkClassRef{ID: "subnet-id"}},
			},
			BootDisk:     bootDisk{SizeGiB: 20, StorageTier: storageTierRef{ID: "tier-id"}},
			RunStrategy:  "COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS",
			InstanceType: instanceTypeRef{ID: "instance-type-id"},
			DiskImage:    diskImageRef{ID: "disk-image-id"},
		},
	})
	if err != nil {
		t.Fatalf("marshal compute instance: %v", err)
	}

	body := string(payload)
	for _, removedField := range []string{`"cores"`, `"memory_gib"`, `"image"`, `"implementation_strategy"`} {
		if strings.Contains(body, removedField) {
			t.Fatalf("compute instance contains removed field %s: %s", removedField, body)
		}
	}
	for _, requiredField := range []string{`"template":{"id":"template-id"}`, `"storage_tier":{"id":"tier-id"}`, `"instance_type":{"id":"instance-type-id"}`, `"disk_image":{"id":"disk-image-id"}`, `"subnet":{"id":"subnet-id"}`} {
		if !strings.Contains(body, requiredField) {
			t.Fatalf("compute instance is missing current field %s: %s", requiredField, body)
		}
	}
}
