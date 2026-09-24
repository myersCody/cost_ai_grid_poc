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

func TestNetworkClassPayloadUsesCurrentSchema(t *testing.T) {
	payload, err := json.Marshal(ncPayload{
		Metadata:      metadata{Name: "sim-nc"},
		Title:         "OSAC Simulator",
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
