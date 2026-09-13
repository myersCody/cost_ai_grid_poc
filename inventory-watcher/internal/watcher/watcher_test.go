package watcher

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/osac-project/cost-event-consumer/internal/osac"
)

type recordingPublisher struct {
	eventType string
	resource  string
	tenant    string
	payload   []byte
}

func (p *recordingPublisher) PublishEvent(_ context.Context, eventType, resourceID, tenantID string, payload []byte) {
	p.eventType = eventType
	p.resource = resourceID
	p.tenant = tenantID
	p.payload = payload
}

func TestHandleEventWithKafkaPublisherUsesExclusiveHandoff(t *testing.T) {
	publisher := &recordingPublisher{}
	w := New(nil, nil, nil, slog.Default())
	w.SetKafkaPublisher(publisher)

	event := osac.Event{
		ID:   "event-1",
		Type: osac.EventTypeCreated,
		Project: &osac.Project{
			ID:       "project-1",
			Metadata: osac.Metadata{Tenant: "tenant-1"},
		},
	}

	if err := w.handleEvent(context.Background(), event); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}

	if publisher.eventType != osac.EventTypeCreated {
		t.Fatalf("published event type = %q, want %q", publisher.eventType, osac.EventTypeCreated)
	}
	if publisher.resource != "project-1" {
		t.Fatalf("published resource = %q, want project-1", publisher.resource)
	}
	if publisher.tenant != "tenant-1" {
		t.Fatalf("published tenant = %q, want tenant-1", publisher.tenant)
	}

	var got osac.Event
	if err := json.Unmarshal(publisher.payload, &got); err != nil {
		t.Fatalf("decode published event: %v", err)
	}
	if got.ID != event.ID {
		t.Fatalf("published event ID = %q, want %q", got.ID, event.ID)
	}
}
