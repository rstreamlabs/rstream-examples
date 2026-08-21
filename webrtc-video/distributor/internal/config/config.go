package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

const (
	defaultMediaMTXURL         = "http://127.0.0.1:8889"
	defaultResolverAudience    = "rstream-video-source-resolver"
	defaultResolverIssuer      = "rstream-video-distributor"
	maxPathBytes               = 512
	maxAuthorization           = 8 * 1024
	maxResolverPrivateKeyBytes = 8 * 1024
)

type Config struct {
	Path                  string
	MediaMTXURL           *url.URL
	SourceURL             *url.URL
	SourceAuthorization   string
	MediaMTXAuthorization string
	ResolverURL           *url.URL
	ResolverAudience      string
	ResolverInstance      string
	ResolverIssuer        string
	ResolverPrivateKey    string
}

func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	path, err := validPath(getenv("MTX_PATH"))
	if err != nil {
		return Config{}, err
	}
	mediaMTXURL, err := parseURL(valueOrDefault(getenv("RSTREAM_MEDIAMTX_URL"), defaultMediaMTXURL), false)
	if err != nil {
		return Config{}, fmt.Errorf("RSTREAM_MEDIAMTX_URL: %w", err)
	}
	if err := requireLoopback(mediaMTXURL); err != nil {
		return Config{}, fmt.Errorf("RSTREAM_MEDIAMTX_URL: %w", err)
	}
	sourceValue := strings.TrimSpace(getenv("RSTREAM_SOURCE_URL"))
	resolverValue := strings.TrimSpace(getenv("RSTREAM_SOURCE_RESOLVER_URL"))
	if (sourceValue == "") == (resolverValue == "") {
		return Config{}, errors.New("exactly one of RSTREAM_SOURCE_URL or RSTREAM_SOURCE_RESOLVER_URL is required")
	}
	config := Config{Path: path, MediaMTXURL: mediaMTXURL}
	if sourceValue != "" {
		config.SourceURL, err = parseURL(sourceValue, true)
		if err != nil {
			return Config{}, fmt.Errorf("RSTREAM_SOURCE_URL: %w", err)
		}
		config.SourceAuthorization, err = parseAuthorization(getenv("RSTREAM_SOURCE_AUTHORIZATION"))
		if err != nil {
			return Config{}, fmt.Errorf("RSTREAM_SOURCE_AUTHORIZATION: %w", err)
		}
		config.MediaMTXAuthorization, err = parseAuthorization(getenv("RSTREAM_MEDIAMTX_AUTHORIZATION"))
		if err != nil {
			return Config{}, fmt.Errorf("RSTREAM_MEDIAMTX_AUTHORIZATION: %w", err)
		}
		return config, nil
	}
	config.ResolverURL, err = parseURL(resolverValue, true)
	if err != nil {
		return Config{}, fmt.Errorf("RSTREAM_SOURCE_RESOLVER_URL: %w", err)
	}
	config.ResolverPrivateKey, err = requiredValue(getenv("RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64"), maxResolverPrivateKeyBytes)
	if err != nil {
		return Config{}, fmt.Errorf("RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64: %w", err)
	}
	config.ResolverInstance, err = requiredValue(getenv("RSTREAM_SOURCE_RESOLVER_INSTANCE_ID"), maxPathBytes)
	if err != nil {
		return Config{}, fmt.Errorf("RSTREAM_SOURCE_RESOLVER_INSTANCE_ID: %w", err)
	}
	config.ResolverIssuer = valueOrDefault(getenv("RSTREAM_SOURCE_RESOLVER_ISSUER"), defaultResolverIssuer)
	config.ResolverAudience = valueOrDefault(getenv("RSTREAM_SOURCE_RESOLVER_AUDIENCE"), defaultResolverAudience)
	return config, nil
}

func (c Config) DestinationURL() *url.URL {
	destination := *c.MediaMTXURL
	destination.Path = strings.TrimSuffix(destination.Path, "/") + "/" + c.Path + "/whip"
	return &destination
}

func validPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", errors.New("MTX_PATH is required")
	}
	if len(path) > maxPathBytes {
		return "", fmt.Errorf("MTX_PATH exceeds %d bytes", maxPathBytes)
	}
	if path != strings.Trim(path, "/") || strings.ContainsAny(path, "?#\\\x00\r\n\t ") {
		return "", errors.New("MTX_PATH contains unsupported characters")
	}
	for index := 0; index < len(path); index++ {
		character := path[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("-._~/", rune(character)) {
			continue
		}
		return "", errors.New("MTX_PATH contains unsupported characters")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("MTX_PATH contains an invalid segment")
		}
	}
	return path, nil
}

func parseURL(raw string, allowRemote bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("URL scheme must be http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("URL must have a host and must not contain user information or a fragment")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, errors.New("URL query is invalid")
	}
	edgeCredentials, present := query["rstream.token"]
	if present && (len(edgeCredentials) != 1 || strings.TrimSpace(edgeCredentials[0]) == "") {
		return nil, errors.New("URL edge credential is invalid")
	}
	if parsed.Scheme == "http" {
		if err := requireLoopback(parsed); err != nil {
			return nil, errors.New("remote URL must use HTTPS")
		}
	}
	if !allowRemote {
		if err := requireLoopback(parsed); err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

func requireLoopback(endpoint *url.URL) error {
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("URL host must be loopback")
	}
	return nil
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func parseAuthorization(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > maxAuthorization || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("value is invalid")
	}
	return value, nil
}

func requiredValue(raw string, maximumBytes int) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("value is required")
	}
	if len(value) > maximumBytes || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("value is invalid")
	}
	return value, nil
}
