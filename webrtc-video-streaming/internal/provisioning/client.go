package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-examples/webrtc-video-streaming/internal/config"
	"github.com/rstreamlabs/rstream-go"
)

const userAgent = "rstream-webrtc-video-streaming/guide-2"

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
	rawEndpoint := strings.TrimSpace(cfg.Tunnel.Provisioning.Endpoint)
	parsed, err := url.Parse(rawEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid provisioning endpoint: %w", err)
	}
	return &Client{
		endpoint: parsed,
		secret:   strings.TrimSpace(cfg.Tunnel.Provisioning.Secret),
		http: &http.Client{
			Timeout: timeout,
		},
	}, nil
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

func (c *Client) do(ctx context.Context, method, pathname string, input any, output any) error {
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
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&problem)
		message := strings.TrimSpace(problem.Error)
		if message == "" {
			message = res.Status
		}
		return fmt.Errorf("provisioning request failed: %s", message)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(output)
}

func join(basePath, subPath string) string {
	joined := path.Join("/", strings.TrimSpace(basePath), strings.TrimSpace(subPath))
	if strings.HasSuffix(subPath, "/") && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	return joined
}

func TURNExpires(credentials *rstream.TURNCredentials, now time.Time) time.Time {
	if credentials == nil || credentials.TTL <= 0 {
		return now
	}
	expires := now.Add(time.Duration(credentials.TTL)*time.Second - 5*time.Minute)
	if expires.Before(now) {
		return now
	}
	return expires
}
