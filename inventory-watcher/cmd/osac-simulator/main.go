package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ─── HTTP helper ─────────────────────────────────────────────────────────────

type apiResp struct {
	ID string `json:"id"`
}

// doRequest executes an HTTP request with optional JSON body and Bearer auth.
// It returns the "id" field from the JSON response body (if any).
func doRequest(client *http.Client, method, url, token string, body interface{}) (string, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("marshal: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, data)
	}

	var r apiResp
	_ = json.Unmarshal(data, &r) // best-effort; DELETE/PATCH may return no body
	return r.ID, nil
}

// ─── Payload types ───────────────────────────────────────────────────────────

type metadata struct {
	Name   string            `json:"name"`
	Tenant string            `json:"tenant,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type ncPayload struct {
	Metadata      metadata `json:"metadata"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	IsDefault     bool     `json:"is_default"`
	FabricManager string   `json:"fabric_manager"`
}

type vnSpec struct {
	IPv4CIDR string `json:"ipv4_cidr"`
}

type resourceRef struct {
	ID string `json:"id"`
}

type vnPayload struct {
	Metadata metadata `json:"metadata"`
	Spec     vnSpec   `json:"spec"`
}

type vnPatchSpec struct {
	IPv4CIDR     string      `json:"ipv4_cidr"`
	Region       string      `json:"region"`
	NetworkClass resourceRef `json:"network_class"`
}

type vnPatchStatus struct {
	State string `json:"state"`
}

type vnPatchPayload struct {
	ID     string        `json:"id"`
	Spec   vnPatchSpec   `json:"spec"`
	Status vnPatchStatus `json:"status"`
}

type subnetSpec struct {
	VirtualNetwork resourceRef `json:"virtual_network"`
	IPv4CIDR       string      `json:"ipv4_cidr"`
}

type subnetStatus struct {
	State string `json:"state"`
}

type subnetPayload struct {
	Metadata metadata     `json:"metadata"`
	Spec     subnetSpec   `json:"spec"`
	Status   subnetStatus `json:"status"`
}

type tplPayload struct {
	Metadata    metadata `json:"metadata"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
}

type netAttachment struct {
	Subnet resourceRef `json:"subnet"`
}

type bootDisk struct {
	SizeGiB     int         `json:"size_gib"`
	StorageTier resourceRef `json:"storage_tier"`
}

type vmSpec struct {
	Template           resourceRef     `json:"template"`
	NetworkAttachments []netAttachment `json:"network_attachments"`
	BootDisk           bootDisk        `json:"boot_disk"`
	RunStrategy        string          `json:"run_strategy"`
	InstanceType       resourceRef     `json:"instance_type"`
	DiskImage          resourceRef     `json:"disk_image"`
}

type vmStatus struct {
	State string `json:"state"`
}

type vmPayload struct {
	Metadata metadata `json:"metadata"`
	Spec     vmSpec   `json:"spec"`
	Status   vmStatus `json:"status"`
}

type instanceTypePayload struct {
	Metadata metadata         `json:"metadata"`
	Spec     instanceTypeSpec `json:"spec"`
}

type instanceTypeSpec struct {
	Cores       int    `json:"cores"`
	MemoryGiB   int    `json:"memory_gib"`
	Description string `json:"description"`
	State       string `json:"state"`
}

type diskImagePayload struct {
	Metadata metadata      `json:"metadata"`
	Spec     diskImageSpec `json:"spec"`
}

type diskImageSpec struct {
	SourceType    string   `json:"source_type"`
	SourceRef     string   `json:"source_ref"`
	GuestOSFamily string   `json:"guest_os_family"`
	Architecture  []string `json:"architecture"`
	Lifecycle     string   `json:"lifecycle"`
}

type storageTierPayload struct {
	Metadata metadata        `json:"metadata"`
	Spec     storageTierSpec `json:"spec"`
}

type storageTierSpec struct {
	Description          string               `json:"description"`
	Protocol             string               `json:"protocol"`
	MaxReadBandwidthMbs  int                  `json:"max_read_bandwidth_mbs"`
	MaxWriteBandwidthMbs int                  `json:"max_write_bandwidth_mbs"`
	EncryptionEnabled    bool                 `json:"encryption_enabled"`
	Backends             []backendAssociation `json:"backends"`
}

type backendAssociation struct {
	BackendID            string `json:"backend_id"`
	MaxReadBandwidthMbs  int    `json:"max_read_bandwidth_mbs"`
	MaxWriteBandwidthMbs int    `json:"max_write_bandwidth_mbs"`
	EncryptionEnabled    bool   `json:"encryption_enabled"`
}

type storageBackendPayload struct {
	Metadata metadata           `json:"metadata"`
	Spec     storageBackendSpec `json:"spec"`
}

type storageBackendSpec struct {
	Provider    string                    `json:"provider"`
	Description string                    `json:"description"`
	Endpoint    string                    `json:"endpoint"`
	Credentials storageBackendCredentials `json:"credentials"`
}

type storageBackendCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ─── VM pool ─────────────────────────────────────────────────────────────────

type vmPool struct {
	mu  sync.Mutex
	ids []string
}

func (p *vmPool) add(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, id)
}

// remove picks and removes a random VM from the pool.
// Returns ("", false) if the pool is empty.
func (p *vmPool) remove() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.ids) == 0 {
		return "", false
	}
	i := rand.Intn(len(p.ids))
	id := p.ids[i]
	// swap-remove to avoid O(n) shift
	p.ids[i] = p.ids[len(p.ids)-1]
	p.ids = p.ids[:len(p.ids)-1]
	return id, true
}

func (p *vmPool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ids)
}

// ─── Prerequisites ────────────────────────────────────────────────────────────

type prereqs struct {
	ncID             string
	vnID             string
	subnetID         string
	tplID            string
	instanceTypeID   string
	diskImageID      string
	storageBackendID string
	storageTierID    string
	tenant           string
}

func existingNetworkClassID(err error) string {
	const marker = "existing NetworkClass id '"
	message := err.Error()
	start := strings.Index(message, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.IndexByte(message[start:], '\'')
	if end < 0 {
		return ""
	}
	return message[start : start+end]
}

// provision creates the one-time infrastructure prerequisites needed before
// VMs can be created: network class, virtual network (patched to READY),
// subnet, and compute instance template.
func provision(client *http.Client, base, token, tenant string) (*prereqs, error) {
	// 1. Network class (private API)
	ncID, err := doRequest(client, "POST", base+"/api/private/v1/network_classes", token, ncPayload{
		Metadata:      metadata{Name: "sim-nc", Tenant: tenant},
		Title:         "Simulator",
		Description:   "load test",
		IsDefault:     true,
		FabricManager: "test",
	})
	if err != nil {
		ncID = existingNetworkClassID(err)
		if ncID == "" {
			return nil, fmt.Errorf("create network class: %w", err)
		}
		fmt.Printf("  network class:   %s (reused)\n", ncID)
	} else {
		fmt.Printf("  network class:   %s\n", ncID)
	}

	// 2. Virtual network (fulfillment API)
	vnID, err := doRequest(client, "POST", base+"/api/fulfillment/v1/virtual_networks", token, vnPayload{
		Metadata: metadata{Name: "sim-vnet", Tenant: tenant},
		Spec:     vnSpec{IPv4CIDR: "10.99.0.0/16"},
	})
	if err != nil {
		return nil, fmt.Errorf("create virtual network: %w", err)
	}
	fmt.Printf("  virtual network: %s\n", vnID)

	// 3. PATCH VN to READY (private API)
	if _, err := doRequest(client, "PATCH", base+"/api/private/v1/virtual_networks/"+vnID, token, vnPatchPayload{
		ID: vnID,
		Spec: vnPatchSpec{
			IPv4CIDR:     "10.99.0.0/16",
			Region:       "default",
			NetworkClass: resourceRef{ID: ncID},
		},
		Status: vnPatchStatus{State: "VIRTUAL_NETWORK_STATE_READY"},
	}); err != nil {
		return nil, fmt.Errorf("patch virtual network to READY: %w", err)
	}
	fmt.Printf("  virtual network set to READY\n")

	// 4. Subnet (private API, pre-set to READY)
	subnetID, err := doRequest(client, "POST", base+"/api/private/v1/subnets", token, subnetPayload{
		Metadata: metadata{Name: "sim-subnet", Tenant: tenant},
		Spec: subnetSpec{
			VirtualNetwork: resourceRef{ID: vnID},
			IPv4CIDR:       "10.99.1.0/24",
		},
		Status: subnetStatus{State: "SUBNET_STATE_READY"},
	})
	if err != nil {
		return nil, fmt.Errorf("create subnet: %w", err)
	}
	fmt.Printf("  subnet:          %s\n", subnetID)

	// 5. Storage backend (private API, required by StorageTier)
	storageBackendID, err := doRequest(client, "POST", base+"/api/private/v1/storage_backends", token, storageBackendPayload{
		Metadata: metadata{Name: "sim-storage-backend"},
		Spec: storageBackendSpec{
			Provider:    "test",
			Description: "Load test storage backend",
			Endpoint:    "http://storage.example",
			Credentials: storageBackendCredentials{Username: "test", Password: "test"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create storage backend: %w", err)
	}
	fmt.Printf("  storage backend: %s\n", storageBackendID)

	// 6. Storage tier (private API, required by current ComputeInstance schema)
	storageTierID, err := doRequest(client, "POST", base+"/api/private/v1/storage_tiers", token, storageTierPayload{
		Metadata: metadata{Name: "sim-storage-tier"},
		Spec: storageTierSpec{
			Description:          "Load test block storage",
			Protocol:             "STORAGE_PROTOCOL_BLOCK",
			MaxReadBandwidthMbs:  100,
			MaxWriteBandwidthMbs: 100,
			Backends: []backendAssociation{{
				BackendID:            storageBackendID,
				MaxReadBandwidthMbs:  100,
				MaxWriteBandwidthMbs: 100,
			}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create storage tier: %w", err)
	}
	fmt.Printf("  storage tier:    %s\n", storageTierID)

	// 7. Instance type (private API, required by current ComputeInstance schema)
	instanceTypeID, err := doRequest(client, "POST", base+"/api/private/v1/instance_types", token, instanceTypePayload{
		Metadata: metadata{Name: "sim-instance-type"},
		Spec: instanceTypeSpec{
			Cores:       4,
			MemoryGiB:   16,
			Description: "Load test VM instance type",
			State:       "INSTANCE_TYPE_STATE_ACTIVE",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create instance type: %w", err)
	}
	fmt.Printf("  instance type:   %s\n", instanceTypeID)

	// 8. Disk image (private API, required by current ComputeInstance schema)
	diskImageID, err := doRequest(client, "POST", base+"/api/private/v1/disk_images", token, diskImagePayload{
		Metadata: metadata{Name: "sim-disk-image"},
		Spec: diskImageSpec{
			SourceType:    "SOURCE_TYPE_REGISTRY",
			SourceRef:     "quay.io/fedora/fedora:latest",
			GuestOSFamily: "GUEST_OS_FAMILY_LINUX",
			Architecture:  []string{"ARCHITECTURE_ARM64"},
			Lifecycle:     "DISK_IMAGE_LIFECYCLE_AVAILABLE",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create disk image: %w", err)
	}
	fmt.Printf("  disk image:      %s\n", diskImageID)

	// 9. Compute instance template (private API)
	tplID, err := doRequest(client, "POST", base+"/api/private/v1/compute_instance_templates", token, tplPayload{
		Metadata:    metadata{Name: "sim-tpl", Tenant: tenant},
		Title:       "Load Test VM",
		Description: "load test",
	})
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}
	fmt.Printf("  template:        %s\n", tplID)

	return &prereqs{
		ncID:             ncID,
		vnID:             vnID,
		subnetID:         subnetID,
		tplID:            tplID,
		instanceTypeID:   instanceTypeID,
		diskImageID:      diskImageID,
		storageBackendID: storageBackendID,
		storageTierID:    storageTierID,
		tenant:           tenant,
	}, nil
}

// ─── VM operations ────────────────────────────────────────────────────────────

func createVM(client *http.Client, base, token string, p *prereqs) (string, error) {
	name := fmt.Sprintf("sim-vm-%04x", rand.Intn(0x10000))
	return doRequest(client, "POST", base+"/api/private/v1/compute_instances", token, vmPayload{
		Metadata: metadata{
			Name:   name,
			Tenant: p.tenant,
			Labels: map[string]string{"env": "loadtest"},
		},
		Spec: vmSpec{
			Template: resourceRef{ID: p.tplID},
			NetworkAttachments: []netAttachment{
				{Subnet: resourceRef{ID: p.subnetID}},
			},
			BootDisk:     bootDisk{SizeGiB: 100, StorageTier: resourceRef{ID: p.storageTierID}},
			RunStrategy:  "Always",
			InstanceType: resourceRef{ID: p.instanceTypeID},
			DiskImage:    resourceRef{ID: p.diskImageID},
		},
		Status: vmStatus{State: "COMPUTE_INSTANCE_STATE_RUNNING"},
	})
}

func deleteVM(client *http.Client, base, token, id string) error {
	_, err := doRequest(client, "DELETE", base+"/api/fulfillment/v1/compute_instances/"+id, token, nil)
	return err
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	target := flag.String("target", "http://localhost:8011", "OSAC REST URL")
	tokenFlag := flag.String("token", os.Getenv("OSAC_TOKEN"), "bearer token (default: $OSAC_TOKEN)")
	tenant := flag.String("tenant", "test", "OSAC tenant for created resources")
	rate := flag.Float64("rate", 1.0, "target VM lifecycle operations/sec (creates + deletes each count as 1)")
	workers := flag.Int("workers", 4, "concurrent goroutines")
	vmCount := flag.Int("vm-count", 10, "target live VM pool size to maintain")
	duration := flag.Duration("duration", 0, "how long to run; 0 = forever")
	flag.Parse()

	token := *tokenFlag
	if token == "" {
		log.Fatal("OSAC token required: pass -token flag or set OSAC_TOKEN env var")
	}

	fmt.Println("OSAC Simulator")
	fmt.Printf("  target:   %s\n", *target)
	fmt.Printf("  tenant:   %s\n", *tenant)
	fmt.Printf("  rate:     %.1f ops/s\n", *rate)
	fmt.Printf("  workers:  %d\n", *workers)
	fmt.Printf("  vm-count: %d\n", *vmCount)
	if *duration > 0 {
		fmt.Printf("  duration: %s\n", *duration)
	} else {
		fmt.Printf("  duration: forever\n")
	}
	fmt.Println()

	client := &http.Client{Timeout: 15 * time.Second}

	// ── Provision prerequisites ───────────────────────────────────────────────
	fmt.Println("Provisioning prerequisites...")
	p, err := provision(client, *target, token, *tenant)
	if err != nil {
		log.Fatalf("provision failed: %v", err)
	}
	fmt.Println()

	// ── Pre-fill VM pool ──────────────────────────────────────────────────────
	pool := &vmPool{}
	fmt.Printf("Pre-filling VM pool to %d VMs...\n", *vmCount)
	for i := 0; i < *vmCount; i++ {
		id, err := createVM(client, *target, token, p)
		if err != nil {
			log.Printf("  pre-fill VM %d/%d: %v", i+1, *vmCount, err)
			continue
		}
		pool.add(id)
		fmt.Printf("\r  created %d/%d", i+1, *vmCount)
	}
	fmt.Printf("\n\n")

	// ── Context: honour -duration and OS signals ───────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigCh:
			fmt.Println("\nInterrupted — draining workers...")
			cancel()
		case <-ctx.Done():
		}
	}()

	if *duration > 0 {
		time.AfterFunc(*duration, cancel)
	}

	// ── Counters ──────────────────────────────────────────────────────────────
	var totalOps, totalCreates, totalDeletes, totalErrors atomic.Int64

	// Per-worker interval: each of N workers is responsible for 1/N of the
	// target rate, so it sleeps N/rate seconds between successful operations.
	var workerInterval time.Duration
	if *rate > 0 {
		workerInterval = time.Duration(float64(*workers) / *rate * float64(time.Second))
	}

	// ── Progress reporter ─────────────────────────────────────────────────────
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				fmt.Printf("[%s] ops=%d live_vms=%d creates=%d deletes=%d errors=%d\n",
					t.Format("15:04:05"),
					totalOps.Load(),
					pool.size(),
					totalCreates.Load(),
					totalDeletes.Load(),
					totalErrors.Load(),
				)
			}
		}
	}()

	// ── Workers ───────────────────────────────────────────────────────────────
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				sz := pool.size()
				opDone := false

				switch {
				case sz < *vmCount && rand.Intn(2) == 0:
					// Pool is below target and random says create.
					id, err := createVM(client, *target, token, p)
					if err != nil {
						totalErrors.Add(1)
					} else {
						pool.add(id)
						totalCreates.Add(1)
						totalOps.Add(1)
						opDone = true
					}

				case sz > 0 && (sz >= *vmCount || rand.Intn(2) == 0):
					// Pool has VMs and is at/above target (or random says delete).
					id, ok := pool.remove()
					if ok {
						if err := deleteVM(client, *target, token, id); err != nil {
							totalErrors.Add(1)
						} else {
							totalDeletes.Add(1)
							totalOps.Add(1)
							opDone = true
						}
					}
				}

				if opDone && workerInterval > 0 {
					// Rate-limit: sleep the per-worker share of the target interval.
					select {
					case <-ctx.Done():
						return
					case <-time.After(workerInterval):
					}
				} else if !opDone {
					// Neither condition triggered — brief pause to avoid busy-spinning.
					select {
					case <-ctx.Done():
						return
					case <-time.After(100 * time.Millisecond):
					}
				}
			}
		}()
	}

	wg.Wait()

	fmt.Printf("\nFinal: ops=%d live_vms=%d creates=%d deletes=%d errors=%d\n",
		totalOps.Load(),
		pool.size(),
		totalCreates.Load(),
		totalDeletes.Load(),
		totalErrors.Load(),
	)
}
