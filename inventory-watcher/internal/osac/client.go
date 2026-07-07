package osac

import (
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
	grpcAddr   string
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

// TLSConfig returns the TLS configuration from the HTTP transport,
// or nil if no custom TLS is configured.
func (c *Client) TLSConfig() *tls.Config {
	if t, ok := c.httpClient.Transport.(*http.Transport); ok {
		return t.TLSClientConfig
	}
	return nil
}

// Token returns the bearer token.
func (c *Client) Token() string { return c.token }

// Logger returns the client's logger.
func (c *Client) Logger() *slog.Logger { return c.logger }

// SetGRPCAddress sets the gRPC server address for the gRPC watch client.
func (c *Client) SetGRPCAddress(addr string) { c.grpcAddr = addr }

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

// ListTenants returns all tenants from OSAC.
func (c *Client) ListTenants(ctx context.Context) ([]Tenant, error) {
	return listAll[Tenant](ctx, c, "/api/fulfillment/v1/tenants")
}

// ListBareMetalInstances returns all bare metal instances from OSAC.
func (c *Client) ListBareMetalInstances(ctx context.Context) ([]BareMetalInstance, error) {
	return listAll[BareMetalInstance](ctx, c, "/api/fulfillment/v1/baremetal_instances")
}

// ListClusterCatalogItems returns all cluster catalog items from OSAC.
func (c *Client) ListClusterCatalogItems(ctx context.Context) ([]CatalogItem, error) {
	return listAll[CatalogItem](ctx, c, "/api/fulfillment/v1/cluster_catalog_items")
}

// ListComputeInstanceCatalogItems returns all compute instance catalog items from OSAC.
func (c *Client) ListComputeInstanceCatalogItems(ctx context.Context) ([]CatalogItem, error) {
	return listAll[CatalogItem](ctx, c, "/api/fulfillment/v1/compute_instance_catalog_items")
}

// ListBareMetalInstanceCatalogItems returns all bare metal catalog items from OSAC.
func (c *Client) ListBareMetalInstanceCatalogItems(ctx context.Context) ([]CatalogItem, error) {
	return listAll[CatalogItem](ctx, c, "/api/fulfillment/v1/baremetal_instance_catalog_items")
}

type listResponse[T any] struct {
	Items []T `json:"items"`
	Size  int `json:"size"`
	Total int `json:"total"`
}

const listPageSize = 100
const listMaxItems = 10000

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
		if len(all) >= listMaxItems {
			c.logger.Warn("list pagination capped", "path", path, "items", len(all), "total", result.Total)
			break
		}
		offset += result.Size
	}

	return all, nil
}
