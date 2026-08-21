package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video/producer/internal/config"
	"github.com/rstreamlabs/rstream-go"
)

const (
	userAgent                   = "rstream-webrtc-video-producer/guide-2"
	maxProvisioningResponseSize = 64 * 1024
	maxProvisioningDialTimeout  = 3 * time.Second
)

type Client struct {
	endpoint *url.URL
	secret   string
	http     *http.Client
}

type Request struct {
	Agent string `json:"agent"`
}

type Tunnel struct {
	Device  string            `json:"device"`
	Engine  string            `json:"engine"`
	Token   string            `json:"token"`
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	Expires string            `json:"expires,omitempty"`
}

func NewClient(cfg config.Config) (*Client, error) {
	timeout, err := cfg.TunnelProvisioningTimeout()
	if err != nil {
		return nil, err
	}
	parsed, err := cfg.TunnelProvisioningEndpoint()
	if err != nil {
		return nil, err
	}
	secret, err := cfg.TunnelProvisioningSecret()
	if err != nil {
		return nil, err
	}
	dialTimeout := min(timeout, maxProvisioningDialTimeout)
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is unavailable")
	}
	transport := baseTransport.Clone()
	transport.DialContext = dialer.DialContext
	transport.DisableKeepAlives = true
	transport.ForceAttemptHTTP2 = true
	return &Client{
		endpoint: parsed,
		secret:   secret,
		http: &http.Client{
			Timeout:       timeout,
			CheckRedirect: rejectRedirect,
			Transport:     transport,
		},
	}, nil
}

func rejectRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (c *Client) Tunnel(ctx context.Context) (Tunnel, error) {
	var out Tunnel
	err := c.do(ctx, http.MethodPost, "/api/devices/tunnel", Request{
		Agent: userAgent,
	}, &out)
	if err != nil {
		return Tunnel{}, err
	}
	if strings.TrimSpace(out.Device) == "" {
		return Tunnel{}, errors.New("provisioning response did not include a device id")
	}
	if strings.TrimSpace(out.Engine) == "" {
		return Tunnel{}, errors.New("provisioning response did not include a tunnel engine")
	}
	if strings.TrimSpace(out.Token) == "" {
		return Tunnel{}, errors.New("provisioning response did not include a tunnel token")
	}
	if strings.TrimSpace(out.Name) == "" {
		return Tunnel{}, errors.New("provisioning response did not include a tunnel name")
	}
	return out, nil
}

func (c *Client) TURN(ctx context.Context) (*rstream.TURNCredentials, error) {
	var out rstream.TURNCredentials
	err := c.do(ctx, http.MethodPost, "/api/devices/turn", Request{
		Agent: userAgent,
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.TTL <= 0 {
		return nil, errors.New("provisioning response included invalid TURN credentials")
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, pathname string, input any, output any) (err error) {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return err
		}
	}
	endpoint := *c.endpoint
	endpoint.Path = join(endpoint.Path, pathname)
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	res, err := c.http.Do(req)
	if err != nil {
		c.http.CloseIdleConnections()
		return fmt.Errorf("perform provisioning request: %w", err)
	}
	defer func() { err = errors.Join(err, res.Body.Close()) }()
	responseBody, err := io.ReadAll(io.LimitReader(res.Body, maxProvisioningResponseSize+1))
	if err != nil {
		return fmt.Errorf("read provisioning response: %w", err)
	}
	if len(responseBody) > maxProvisioningResponseSize {
		return fmt.Errorf("provisioning response exceeds %d bytes", maxProvisioningResponseSize)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &problem)
		message := strings.TrimSpace(problem.Error)
		if message == "" {
			message = res.Status
		}
		return fmt.Errorf("provisioning request failed: %s", message)
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode provisioning response: %w", err)
	}
	return nil
}

func join(basePath, subPath string) string {
	joined := path.Join("/", strings.TrimSpace(basePath), strings.TrimSpace(subPath))
	if strings.HasSuffix(subPath, "/") && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	return joined
}
