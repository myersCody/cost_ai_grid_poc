package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type CloudEvent struct {
	SpecVersion     string    `json:"specversion"`
	Type            string    `json:"type"`
	Source          string    `json:"source"`
	ID             string    `json:"id"`
	Time            time.Time `json:"time"`
	Subject         string    `json:"subject"`
	DataContentType string    `json:"datacontenttype"`
	Data            EventData `json:"data"`
}

type EventData struct {
	TenantID        string `json:"tenant_id"`
	ModelID         string `json:"model_id"`
	ModelName       string `json:"model_name"`
	Template        string `json:"template"`
	State           string `json:"state"`
	TokensIn        int64  `json:"tokens_in"`
	TokensOut       int64  `json:"tokens_out"`
	Requests        int64  `json:"requests"`
	DurationSeconds int    `json:"duration_seconds"`
}

var models = []struct {
	id   string
	name string
}{
	{"model-llama-3-8b", "llama-3-8b"},
	{"model-llama-3-70b", "llama-3-70b"},
	{"model-mistral-7b", "mistral-7b"},
	{"model-granite-34b", "granite-34b"},
}

var tenants = []string{"tenant-acme", "tenant-globex", "tenant-initech"}

func main() {
	target := flag.String("target", "http://localhost:8020", "ingest endpoint base URL")
	count := flag.Int("count", 100, "total number of events to send")
	rate := flag.Int("rate", 50, "events per second (0 = unlimited)")
	workers := flag.Int("workers", 4, "concurrent sender goroutines")
	flag.Parse()

	fmt.Printf("MaaS Simulator\n")
	fmt.Printf("  target:  %s/api/v1/events\n", *target)
	fmt.Printf("  events:  %d\n", *count)
	fmt.Printf("  rate:    %d/s\n", *rate)
	fmt.Printf("  workers: %d\n", *workers)
	fmt.Println()

	url := *target + "/api/v1/events"
	client := &http.Client{Timeout: 5 * time.Second}

	var sent, errors atomic.Int64
	start := time.Now()

	ch := make(chan CloudEvent, *workers*2)

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ce := range ch {
				body, _ := json.Marshal(ce)
				resp, err := client.Post(url, "application/json", bytes.NewReader(body))
				if err != nil {
					errors.Add(1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == http.StatusAccepted {
					sent.Add(1)
				} else {
					errors.Add(1)
				}
			}
		}()
	}

	var interval time.Duration
	if *rate > 0 {
		interval = time.Second / time.Duration(*rate)
	}

	for i := 0; i < *count; i++ {
		model := models[rand.Intn(len(models))]
		tenant := tenants[rand.Intn(len(tenants))]
		tokensIn := int64(rand.Intn(50000) + 1000)
		tokensOut := int64(rand.Intn(20000) + 500)

		ce := CloudEvent{
			SpecVersion:     "1.0",
			Type:            "osac.model.lifecycle",
			Source:          "maas-simulator",
			ID:              fmt.Sprintf("sim-%d-%d", time.Now().UnixNano(), i),
			Time:            time.Now().UTC(),
			Subject:         tenant,
			DataContentType: "application/json",
			Data: EventData{
				TenantID:        tenant,
				ModelID:         model.id,
				ModelName:       model.name,
				Template:        "osac.templates.maas_small",
				State:           "MODEL_STATE_RUNNING",
				TokensIn:        tokensIn,
				TokensOut:       tokensOut,
				Requests:        int64(rand.Intn(200) + 1),
				DurationSeconds: 60,
			},
		}
		ch <- ce

		if interval > 0 {
			time.Sleep(interval)
		}

		if (i+1)%100 == 0 || i+1 == *count {
			elapsed := time.Since(start).Seconds()
			s := sent.Load()
			e := errors.Load()
			fmt.Printf("\r  sent: %d  errors: %d  rate: %.0f/s", s, e, float64(s)/elapsed)
		}
	}

	close(ch)
	wg.Wait()

	elapsed := time.Since(start)
	s := sent.Load()
	e := errors.Load()
	fmt.Printf("\n\nDone: %d sent, %d errors in %s (%.0f events/s)\n", s, e, elapsed.Round(time.Millisecond), float64(s)/elapsed.Seconds())

	if e > 0 {
		os.Exit(1)
	}
}
