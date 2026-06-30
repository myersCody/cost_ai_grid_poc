package osac

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Client connects to the OSAC fulfillment-service REST gateway.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(baseURL, token, caCertPath string, logger *slog.Logger) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if caCertPath != "" {
		caCert, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		transport.TLSClientConfig = &tls.Config{
			RootCAs: pool,
		}
	}

	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		logger: logger,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.httpClient.Do(req)
}

// WatchEvents opens a streaming connection to the OSAC events endpoint.
// It calls the handler for each event received. Blocks until the context
// is cancelled or the stream ends.
func (c *Client) WatchEvents(ctx context.Context, handler func(Event) error) error {
	streamClient := &http.Client{
		Transport: c.httpClient.Transport,
		// No timeout for streaming connection.
	}

	url := c.baseURL + "/api/private/v1/events/watch"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating watch request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		return fmt.Errorf("watch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("watch returned status %d: %s", resp.StatusCode, body)
	}

	c.logger.Info("watch stream connected")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line size
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var watchResp EventsWatchResponse
		if err := json.Unmarshal(line, &watchResp); err != nil {
			c.logger.Warn("failed to parse event", "error", err, "line", string(line))
			continue
		}

		if watchResp.Result == nil || watchResp.Result.Event == nil {
			continue
		}

		if err := handler(*watchResp.Result.Event); err != nil {
			c.logger.Error("event handler failed", "error", err, "eventID", watchResp.Result.Event.ID)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("watch stream error: %w", err)
	}

	return nil
}

// ListComputeInstances returns all compute instances from OSAC.
func (c *Client) ListComputeInstances(ctx context.Context) ([]ComputeInstance, error) {
	return listAll[ComputeInstance](ctx, c, "/api/fulfillment/v1/compute_instances")
}

// ListClusters returns all clusters from OSAC.
func (c *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	return listAll[Cluster](ctx, c, "/api/fulfillment/v1/clusters")
}

// ListInstanceTypes returns all instance types from OSAC.
func (c *Client) ListInstanceTypes(ctx context.Context) ([]InstanceType, error) {
	return listAll[InstanceType](ctx, c, "/api/fulfillment/v1/instance_types")
}

// ListProjects returns all projects from OSAC.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	return listAll[Project](ctx, c, "/api/fulfillment/v1/projects")
}

// ListBareMetalInstances returns all bare metal instances from OSAC.
func (c *Client) ListBareMetalInstances(ctx context.Context) ([]BareMetalInstance, error) {
	return listAll[BareMetalInstance](ctx, c, "/api/fulfillment/v1/baremetal_instances")
}

type listResponse[T any] struct {
	Items []T `json:"items"`
	Size  int `json:"size"`
	Total int `json:"total"`
}

const listPageSize = 100

func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	offset := 0

	for {
		pagePath := fmt.Sprintf("%s?offset=%d&limit=%d", path, offset, listPageSize)
		resp, err := c.doRequest(ctx, "GET", pagePath)
		if err != nil {
			return nil, fmt.Errorf("list request to %s: %w", pagePath, err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return nil, fmt.Errorf("list %s returned status %d: %s", pagePath, resp.StatusCode, body)
		}

		var result listResponse[T]
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding list response from %s: %w", pagePath, err)
		}
		resp.Body.Close()

		all = append(all, result.Items...)

		if len(all) >= result.Total || result.Size == 0 {
			break
		}
		offset += result.Size
	}

	return all, nil
}
