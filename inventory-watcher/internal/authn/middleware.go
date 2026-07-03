package authn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const claimsKey contextKey = "jwt_claims"

// ClaimsFromContext returns the JWT claims from the request context.
func ClaimsFromContext(ctx context.Context) (jwt.MapClaims, bool) {
	claims, ok := ctx.Value(claimsKey).(jwt.MapClaims)
	return claims, ok
}

// Middleware validates JWT bearer tokens against a JWKS endpoint.
// Compatible with OSAC's authentication model.
type Middleware struct {
	issuerURL string
	jwksURL   string
	keys      *sync.Map
	caPool    *x509.CertPool
	logger    *slog.Logger
	client    *http.Client
}

// New creates a JWT authentication middleware. If issuerURL is empty,
// authentication is disabled (all requests pass through).
func New(issuerURL, caCertPath string, logger *slog.Logger) (*Middleware, error) {
	if issuerURL == "" {
		logger.Warn("authentication disabled — no issuer URL configured")
		return &Middleware{logger: logger}, nil
	}

	m := &Middleware{
		issuerURL: issuerURL,
		keys:      &sync.Map{},
		logger:    logger,
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caCertPath != "" {
		caCert, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		m.caPool = pool
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	}

	m.client = &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	jwksURL, err := m.discoverJWKS()
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery failed: %w", err)
	}
	m.jwksURL = jwksURL

	if err := m.refreshKeys(); err != nil {
		return nil, fmt.Errorf("initial JWKS fetch failed: %w", err)
	}

	logger.Info("JWT authentication enabled", "issuer", issuerURL, "jwks", jwksURL)
	return m, nil
}

// Wrap returns an HTTP handler that validates JWT tokens before calling next.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if m.issuerURL == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeAuthError(w, "missing Authorization header")
			return
		}

		bearer, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok {
			writeAuthError(w, "Authorization header must use Bearer scheme")
			return
		}

		claims, err := m.validateToken(bearer)
		if err != nil {
			m.logger.Warn("token validation failed", "error", err)
			writeAuthError(w, "invalid token: "+err.Error())
			return
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) validateToken(bearer string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(bearer, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token missing kid header")
		}

		key, ok := m.keys.Load(kid)
		if !ok {
			if err := m.refreshKeys(); err != nil {
				return nil, fmt.Errorf("JWKS refresh failed: %w", err)
			}
			key, ok = m.keys.Load(kid)
			if !ok {
				return nil, fmt.Errorf("unknown kid: %s", kid)
			}
		}

		return key, nil
	}, jwt.WithIssuer(m.issuerURL), jwt.WithExpirationRequired())

	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token is not valid")
	}

	return claims, nil
}

type oidcDiscovery struct {
	JWKSURI string `json:"jwks_uri"`
	Issuer  string `json:"issuer"`
}

func (m *Middleware) discoverJWKS() (string, error) {
	url := strings.TrimRight(m.issuerURL, "/") + "/.well-known/openid-configuration"
	resp, err := m.client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("OIDC discovery returned %d: %s", resp.StatusCode, body)
	}

	var disc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return "", err
	}
	if disc.JWKSURI == "" {
		return "", errors.New("OIDC discovery response missing jwks_uri")
	}

	return disc.JWKSURI, nil
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

func (m *Middleware) refreshKeys() error {
	resp, err := m.client.Get(m.jwksURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("JWKS fetch returned %d: %s", resp.StatusCode, body)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return err
	}

	for _, key := range jwks.Keys {
		if key.Kty != "RSA" || key.Kid == "" {
			continue
		}

		rsaKey, parseErr := parseRSAPublicKey(key.N, key.E)
		if parseErr != nil {
			m.logger.Warn("failed to parse JWKS key", "kid", key.Kid, "error", parseErr)
			continue
		}

		m.keys.Store(key.Kid, rsaKey)
		m.logger.Debug("loaded JWKS key", "kid", key.Kid)
	}

	return nil
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
