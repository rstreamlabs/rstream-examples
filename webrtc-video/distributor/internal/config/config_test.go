package config

import (
	"strings"
	"testing"
)

func TestLoadStaticSourceBuildsLoopbackDestination(t *testing.T) {
	values := map[string]string{
		"MTX_PATH":                       "camera/fd8X_2-0",
		"RSTREAM_SOURCE_URL":             "https://camera.example/whep",
		"RSTREAM_SOURCE_AUTHORIZATION":   "Bearer source-token",
		"RSTREAM_MEDIAMTX_AUTHORIZATION": "Bearer publisher-token",
	}
	config, err := load(func(name string) string { return values[name] })
	if err != nil {
		t.Fatalf("load static source: %v", err)
	}
	if got := config.DestinationURL().String(); got != "http://127.0.0.1:8889/camera/fd8X_2-0/whip" {
		t.Fatalf("destination URL = %q", got)
	}
	if config.SourceURL.String() != values["RSTREAM_SOURCE_URL"] || config.SourceAuthorization != values["RSTREAM_SOURCE_AUTHORIZATION"] || config.MediaMTXAuthorization != values["RSTREAM_MEDIAMTX_AUTHORIZATION"] {
		t.Fatalf("unexpected source configuration: %+v", config)
	}
}

func TestLoadResolverRequiresACompleteSigningIdentity(t *testing.T) {
	values := map[string]string{
		"MTX_PATH":                    "camera/opaque",
		"RSTREAM_SOURCE_RESOLVER_URL": "https://platform.example/api/distributor/source",
	}
	_, err := load(func(name string) string { return values[name] })
	if err == nil || !strings.Contains(err.Error(), "RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64") {
		t.Fatalf("load error = %v, want resolver private-key error", err)
	}
	values["RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64"] = "encoded-key"
	_, err = load(func(name string) string { return values[name] })
	if err == nil || !strings.Contains(err.Error(), "RSTREAM_SOURCE_RESOLVER_INSTANCE_ID") {
		t.Fatalf("load error = %v, want resolver instance error", err)
	}
	values["RSTREAM_SOURCE_RESOLVER_INSTANCE_ID"] = "mediamtx-one"
	config, err := load(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.ResolverIssuer != defaultResolverIssuer || config.ResolverAudience != defaultResolverAudience {
		t.Fatalf("resolver claims = issuer %q audience %q", config.ResolverIssuer, config.ResolverAudience)
	}
}

func TestLoadRejectsAmbiguousSourceConfiguration(t *testing.T) {
	tests := []map[string]string{
		{"MTX_PATH": "camera/opaque"},
		{
			"MTX_PATH":                    "camera/opaque",
			"RSTREAM_SOURCE_URL":          "https://camera.example/whep",
			"RSTREAM_SOURCE_RESOLVER_URL": "https://platform.example/api/distributor/source",
		},
	}
	for index, values := range tests {
		if _, err := load(func(name string) string { return values[name] }); err == nil {
			t.Fatalf("case %d accepted ambiguous source configuration", index)
		}
	}
}

func TestLoadRejectsUnsafeMediaMTXAndPathValues(t *testing.T) {
	tests := []map[string]string{
		{
			"MTX_PATH":             "camera/opaque",
			"RSTREAM_MEDIAMTX_URL": "http://mediamtx.example:8889",
			"RSTREAM_SOURCE_URL":   "https://camera.example/whep",
		},
		{
			"MTX_PATH":           "../camera",
			"RSTREAM_SOURCE_URL": "https://camera.example/whep",
		},
		{
			"MTX_PATH":           "camera/opaque?token=secret",
			"RSTREAM_SOURCE_URL": "https://camera.example/whep",
		},
	}
	for index, values := range tests {
		if _, err := load(func(name string) string { return values[name] }); err == nil {
			t.Fatalf("case %d accepted unsafe configuration", index)
		}
	}
}

func TestLoadRequiresHTTPSOutsideLoopback(t *testing.T) {
	remote := map[string]string{
		"MTX_PATH":                    "camera/opaque",
		"RSTREAM_SOURCE_RESOLVER_URL": "http://platform.example/source",
	}
	if _, err := load(func(name string) string { return remote[name] }); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("remote HTTP resolver error = %v, want HTTPS rejection", err)
	}
	loopback := map[string]string{
		"MTX_PATH":                       "camera/opaque",
		"RSTREAM_SOURCE_URL":             "http://127.0.0.1:8080/whep",
		"RSTREAM_SOURCE_AUTHORIZATION":   "Bearer source-token",
		"RSTREAM_MEDIAMTX_AUTHORIZATION": "Bearer publisher-token",
	}
	if _, err := load(func(name string) string { return loopback[name] }); err != nil {
		t.Fatalf("load loopback HTTP source: %v", err)
	}
}

func TestLoadRejectsUnsafeCredentialValues(t *testing.T) {
	tests := []map[string]string{
		{
			"MTX_PATH":                     "camera/opaque",
			"RSTREAM_SOURCE_URL":           "https://camera.example/whep",
			"RSTREAM_SOURCE_AUTHORIZATION": "Bearer accepted\r\nX-Injected: true",
		},
		{
			"MTX_PATH":           "camera/opaque",
			"RSTREAM_SOURCE_URL": "https://camera.example/whep?rstream.token=one&rstream.token=two",
		},
		{
			"MTX_PATH":           "camera/opaque",
			"RSTREAM_SOURCE_URL": "https://camera.example/whep?rstream.token=",
		},
		{
			"MTX_PATH":           "camera/opaque",
			"RSTREAM_SOURCE_URL": "https://camera.example/whep?rstream.token=one&broken=%zz",
		},
		{
			"MTX_PATH":                    "camera/opaque",
			"RSTREAM_SOURCE_RESOLVER_URL": "https://platform.example/source",
			"RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64": strings.Repeat("a", maxResolverPrivateKeyBytes+1),
			"RSTREAM_SOURCE_RESOLVER_INSTANCE_ID":        "mediamtx-one",
		},
	}
	for index, values := range tests {
		if _, err := load(func(name string) string { return values[name] }); err == nil {
			t.Fatalf("case %d accepted unsafe credential", index)
		}
	}
}
