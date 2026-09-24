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
	ID     string   `json:"id"`
	Object *apiResp `json:"object,omitempty"`
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
	if r.ID == "" && r.Object != nil {
		r.ID = r.Object.ID
	}
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
	FabricManager string   `json:"fabric_manager"`
	IsDefault     bool     `json:"is_default"`
}

type storageBackendCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type storageBackendSpec struct {
	Provider    string                    `json:"provider"`
	Description string                    `json:"description"`
	Endpoint    string                    `json:"endpoint"`
	Credentials storageBackendCredentials `json:"credentials"`
}

type storageBackendPayload struct {
	Metadata metadata           `json:"metadata"`
	Spec     storageBackendSpec `json:"spec"`
}

type backendAssociation struct {
	BackendID            string `json:"backend_id"`
	MaxReadBandwidthMBs  int    `json:"max_read_bandwidth_mbs,omitempty"`
	MaxWriteBandwidthMBs int    `json:"max_write_bandwidth_mbs,omitempty"`
	EncryptionEnabled    bool   `json:"encryption_enabled,omitempty"`
}

type storageTierSpec struct {
	Description string               `json:"description"`
	Protocol    string               `json:"protocol"`
	Backends    []backendAssociation `json:"backends"`
}

type storageTierPayload struct {
	Metadata metadata        `json:"metadata"`
	Spec     storageTierSpec `json:"spec"`
}

type instanceTypeSpec struct {
	VCPUs     int `json:"vcpus"`
	MemoryGiB int `json:"memory_gib"`
}

type instanceTypePayload struct {
	Metadata metadata         `json:"metadata"`
	Spec     instanceTypeSpec `json:"spec"`
}

// legacyInstanceTypePayload supports the schema used by the older OSAC image
// commonly deployed in local CRC. Current OSAC uses vcpus; the older image
// uses cores for the same resource.
type legacyInstanceTypeSpec struct {
	Cores       int    `json:"cores"`
	MemoryGiB   int    `json:"memory_gib"`
	Description string `json:"description"`
	State       string `json:"state"`
}

type legacyInstanceTypePayload struct {
	Metadata metadata               `json:"metadata"`
	Spec     legacyInstanceTypeSpec `json:"spec"`
}

type diskImageSpec struct {
	SourceType    string   `json:"source_type"`
	SourceRef     string   `json:"source_ref"`
	GuestOSFamily string   `json:"guest_os_family"`
	Architecture  []string `json:"architecture"`
}

type diskImagePayload struct {
	Metadata metadata      `json:"metadata"`
	Spec     diskImageSpec `json:"spec"`
}

type networkClassRef struct {
	ID string `json:"id"`
}

type vnSpec struct {
	IPv4CIDR     string          `json:"ipv4_cidr"`
	Region       string          `json:"region"`
	NetworkClass networkClassRef `json:"network_class"`
}

type vnPayload struct {
	Metadata metadata `json:"metadata"`
	Spec     vnSpec   `json:"spec"`
}

type vnPatchSpec struct {
	IPv4CIDR     string          `json:"ipv4_cidr"`
	Region       string          `json:"region"`
	NetworkClass networkClassRef `json:"network_class"`
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
	VirtualNetwork networkClassRef `json:"virtual_network"`
	IPv4CIDR       string          `json:"ipv4_cidr"`
}

type subnetPayload struct {
	Metadata metadata   `json:"metadata"`
	Spec     subnetSpec `json:"spec"`
}

type subnetPatchPayload struct {
	ID     string        `json:"id"`
	Spec   subnetSpec    `json:"spec"`
	Status vnPatchStatus `json:"status"`
}

type tplPayload struct {
	Metadata    metadata `json:"metadata"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
}

type templateRef struct {
	ID string `json:"id"`
}

type netAttachment struct {
	Subnet networkClassRef `json:"subnet"`
}

type storageTierRef struct {
	ID string `json:"id"`
}

type bootDisk struct {
	SizeGiB     int            `json:"size_gib"`
	StorageTier storageTierRef `json:"storage_tier"`
}

type instanceTypeRef struct {
	ID string `json:"id"`
}

type diskImageRef struct {
	ID string `json:"id"`
}

type vmSpec struct {
	Template           templateRef     `json:"template"`
	NetworkAttachments []netAttachment `json:"network_attachments"`
	BootDisk           bootDisk        `json:"boot_disk"`
	RunStrategy        string          `json:"run_strategy"`
	InstanceType       instanceTypeRef `json:"instance_type"`
	DiskImage          diskImageRef    `json:"disk_image"`
}

type vmPayload struct {
	Metadata metadata `json:"metadata"`
	Spec     vmSpec   `json:"spec"`
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
	ncID           string
	vnID           string
	subnetID       string
	tplID          string
	instanceTypeID string
	storageTierID  string
	diskImageID    string
	tenant         string
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

func createInstanceType(client *http.Client, base, token, name string) (string, error) {
	id, err := doRequest(client, "POST", base+"/api/private/v1/instance_types", token, instanceTypePayload{
		Metadata: metadata{Name: name},
		Spec:     instanceTypeSpec{VCPUs: 2, MemoryGiB: 4},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "vcpus") {
		return id, err
	}

	legacyID, legacyErr := doRequest(client, "POST", base+"/api/private/v1/instance_types", token, legacyInstanceTypePayload{
		Metadata: metadata{Name: name},
		Spec: legacyInstanceTypeSpec{
			Cores:       2,
			MemoryGiB:   4,
			Description: "OSAC simulator instance type",
			State:       "INSTANCE_TYPE_STATE_ACTIVE",
		},
	})
	if legacyErr != nil {
		return "", fmt.Errorf("current vcpus schema rejected; legacy cores schema also failed: %w", legacyErr)
	}
	return legacyID, nil
}

// provision creates the infrastructure prerequisites needed before VMs can be
// created. The payloads intentionally use the current OSAC API shape so the
// simulator can run against a current checkout without an out-of-band event
// generator or hand-written resource manifest.
func provision(client *http.Client, base, token, tenant, fabricManager string) (*prereqs, error) {
	runID := fmt.Sprintf("%x", time.Now().UnixNano())
	resourceName := func(kind string) string { return "sim-" + kind + "-" + runID }

	// 1. Storage resources used by the ComputeInstance boot disk.
	storageBackendID, err := doRequest(client, "POST", base+"/api/private/v1/storage_backends", token, storageBackendPayload{
		Metadata: metadata{Name: resourceName("sb")},
		Spec: storageBackendSpec{
			Provider:    "test",
			Description: "OSAC simulator storage backend",
			Endpoint:    "https://test-backend.example.com",
			Credentials: storageBackendCredentials{Username: "test-user", Password: "test-credential"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create storage backend: %w", err)
	}
	fmt.Printf("  storage backend:  %s\n", storageBackendID)

	storageTierID, err := doRequest(client, "POST", base+"/api/private/v1/storage_tiers", token, storageTierPayload{
		Metadata: metadata{Name: resourceName("tier")},
		Spec: storageTierSpec{
			Description: "OSAC simulator block storage",
			Protocol:    "STORAGE_PROTOCOL_BLOCK",
			Backends:    []backendAssociation{{BackendID: storageBackendID}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create storage tier: %w", err)
	}
	fmt.Printf("  storage tier:     %s\n", storageTierID)

	instanceTypeID, err := createInstanceType(client, base, token, resourceName("type"))
	if err != nil {
		return nil, fmt.Errorf("create instance type: %w", err)
	}
	fmt.Printf("  instance type:    %s\n", instanceTypeID)

	diskImageID, err := doRequest(client, "POST", base+"/api/private/v1/disk_images", token, diskImagePayload{
		Metadata: metadata{Name: resourceName("image")},
		Spec: diskImageSpec{
			SourceType:    "SOURCE_TYPE_REGISTRY",
			SourceRef:     "quay.io/containerdisks/fedora:41",
			GuestOSFamily: "GUEST_OS_FAMILY_LINUX",
			Architecture:  []string{"ARCHITECTURE_AMD64"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create disk image: %w", err)
	}
	fmt.Printf("  disk image:       %s\n", diskImageID)

	// 2. Compute instance template.
	tplID, err := doRequest(client, "POST", base+"/api/private/v1/compute_instance_templates", token, tplPayload{
		Metadata:    metadata{Name: resourceName("tpl")},
		Title:       "OSAC Simulator VM",
		Description: "OSAC simulator compute instance template",
	})
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}
	fmt.Printf("  template:         %s\n", tplID)

	// 3. Network class, virtual network, and subnet.
	ncID, err := doRequest(client, "POST", base+"/api/private/v1/network_classes", token, ncPayload{
		Metadata:      metadata{Name: resourceName("nc")},
		Title:         "OSAC Simulator",
		Description:   "OSAC simulator network class",
		FabricManager: fabricManager,
		IsDefault:     false,
	})
	if err != nil {
		ncID = existingNetworkClassID(err)
		if ncID == "" {
			return nil, fmt.Errorf("create network class: %w", err)
		}
		fmt.Printf("  network class:    %s (reused)\n", ncID)
	} else {
		fmt.Printf("  network class:    %s\n", ncID)
	}

	const region = "default"
	const vnetCIDR = "10.99.0.0/16"
	vnID, err := doRequest(client, "POST", base+"/api/private/v1/virtual_networks", token, vnPayload{
		Metadata: metadata{Name: resourceName("vnet"), Tenant: tenant},
		Spec: vnSpec{
			IPv4CIDR:     vnetCIDR,
			Region:       region,
			NetworkClass: networkClassRef{ID: ncID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create virtual network: %w", err)
	}
	fmt.Printf("  virtual network:  %s\n", vnID)

	if _, err := doRequest(client, "PATCH", base+"/api/private/v1/virtual_networks/"+vnID, token, vnPatchPayload{
		ID: vnID,
		Spec: vnPatchSpec{
			IPv4CIDR:     vnetCIDR,
			Region:       region,
			NetworkClass: networkClassRef{ID: ncID},
		},
		Status: vnPatchStatus{State: "VIRTUAL_NETWORK_STATE_READY"},
	}); err != nil {
		return nil, fmt.Errorf("patch virtual network to READY: %w", err)
	}
	fmt.Printf("  virtual network:  READY\n")

	subnetID, err := doRequest(client, "POST", base+"/api/private/v1/subnets", token, subnetPayload{
		Metadata: metadata{Name: resourceName("subnet"), Tenant: tenant},
		Spec: subnetSpec{
			VirtualNetwork: networkClassRef{ID: vnID},
			IPv4CIDR:       "10.99.1.0/24",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create subnet: %w", err)
	}
	fmt.Printf("  subnet:            %s\n", subnetID)

	if _, err := doRequest(client, "PATCH", base+"/api/private/v1/subnets/"+subnetID, token, subnetPatchPayload{
		ID: subnetID,
		Spec: subnetSpec{
			VirtualNetwork: networkClassRef{ID: vnID},
			IPv4CIDR:       "10.99.1.0/24",
		},
		Status: vnPatchStatus{State: "SUBNET_STATE_READY"},
	}); err != nil {
		return nil, fmt.Errorf("patch subnet to READY: %w", err)
	}
	fmt.Printf("  subnet:            READY\n")

	return &prereqs{
		ncID:           ncID,
		vnID:           vnID,
		subnetID:       subnetID,
		tplID:          tplID,
		instanceTypeID: instanceTypeID,
		storageTierID:  storageTierID,
		diskImageID:    diskImageID,
		tenant:         tenant,
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
			Template: templateRef{ID: p.tplID},
			NetworkAttachments: []netAttachment{
				{Subnet: networkClassRef{ID: p.subnetID}},
			},
			BootDisk:     bootDisk{SizeGiB: 20, StorageTier: storageTierRef{ID: p.storageTierID}},
			RunStrategy:  "COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS",
			InstanceType: instanceTypeRef{ID: p.instanceTypeID},
			DiskImage:    diskImageRef{ID: p.diskImageID},
		},
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
	tenant := flag.String("tenant", "test", "OSAC tenant for tenant-scoped resources")
	fabricManager := flag.String("fabric-manager", "test", "OSAC network class fabric manager")
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
	p, err := provision(client, *target, token, *tenant, *fabricManager)
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
