// See LICENSE file in the project root for license information.

package mobileconfig

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/xml"
	"fmt"
	"net"
	"net/url"
	"strings"
	"text/template"
)

type Options struct {
	DisplayName   string
	Identifier    string
	RelayUUID     string
	ProfileUUID   string
	HTTP3RelayURL string
	HTTP2RelayURL string
	MatchDomains  []string
	HeaderName    string
	HeaderValue   string
}

func Render(opts Options) ([]byte, error) {
	if strings.TrimSpace(opts.DisplayName) == "" {
		opts.DisplayName = "rstream private MASQUE egress"
	}
	if strings.TrimSpace(opts.Identifier) == "" {
		opts.Identifier = "io.rstream.examples.private-masque-egress"
	}
	if strings.TrimSpace(opts.RelayUUID) == "" {
		uuid, err := randomUUID()
		if err != nil {
			return nil, fmt.Errorf("generate relay UUID: %w", err)
		}
		opts.RelayUUID = uuid
	}
	if strings.TrimSpace(opts.ProfileUUID) == "" {
		uuid, err := randomUUID()
		if err != nil {
			return nil, fmt.Errorf("generate profile UUID: %w", err)
		}
		opts.ProfileUUID = uuid
	}
	if strings.TrimSpace(opts.HTTP3RelayURL) == "" {
		return nil, fmt.Errorf("HTTP3RelayURL is required")
	}
	if _, err := url.ParseRequestURI(opts.HTTP3RelayURL); err != nil {
		return nil, fmt.Errorf("invalid HTTP3 relay URL: %w", err)
	}
	if opts.HTTP2RelayURL != "" {
		if _, err := url.ParseRequestURI(opts.HTTP2RelayURL); err != nil {
			return nil, fmt.Errorf("invalid HTTP2 relay URL: %w", err)
		}
	}
	var out bytes.Buffer
	err := profileTemplate.Execute(&out, opts)
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func RelayURLsFromForwarding(raw string) (http3URL, http2URL string, err error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", fmt.Errorf("forwarding address is empty")
	}
	if i := strings.IndexAny(value, " \t\r\n"); i >= 0 {
		value = value[:i]
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", "", err
	}
	host := parsed.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		if strings.Contains(err.Error(), "missing port in address") {
			host = net.JoinHostPort(host, "443")
		} else {
			return "", "", err
		}
	}
	base := "https://" + host
	return base + "/.well-known/masque/udp/{target_host}/{target_port}/", base + "/", nil
}

func xmlEscape(value string) (string, error) {
	var out bytes.Buffer
	if err := xml.EscapeText(&out, []byte(value)); err != nil {
		return "", err
	}
	return out.String(), nil
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])), nil
}

var profileTemplate = template.Must(template.New("mobileconfig").Funcs(template.FuncMap{
	"xml": xmlEscape,
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>PayloadType</key>
      <string>com.apple.relay.managed</string>
      <key>PayloadVersion</key>
      <integer>1</integer>
      <key>PayloadIdentifier</key>
      <string>{{ xml .Identifier }}.relay</string>
      <key>PayloadUUID</key>
      <string>{{ xml .RelayUUID }}</string>
      <key>PayloadDisplayName</key>
      <string>{{ xml .DisplayName }}</string>
      <key>RelayUUID</key>
      <string>{{ xml .RelayUUID }}</string>
      <key>Relays</key>
      <array>
        <dict>
          <key>HTTP3RelayURL</key>
          <string>{{ xml .HTTP3RelayURL }}</string>
          {{- if .HTTP2RelayURL }}
          <key>HTTP2RelayURL</key>
          <string>{{ xml .HTTP2RelayURL }}</string>
          {{- end }}
          {{- if and .HeaderName .HeaderValue }}
          <key>AdditionalHTTPHeaderFields</key>
          <dict>
            <key>{{ xml .HeaderName }}</key>
            <string>{{ xml .HeaderValue }}</string>
          </dict>
          {{- end }}
        </dict>
      </array>
      {{- if .MatchDomains }}
      <key>MatchDomains</key>
      <array>
        {{- range .MatchDomains }}
        <string>{{ xml . }}</string>
        {{- end }}
      </array>
      {{- end }}
    </dict>
  </array>
  <key>PayloadDisplayName</key>
  <string>{{ xml .DisplayName }}</string>
  <key>PayloadIdentifier</key>
  <string>{{ xml .Identifier }}</string>
  <key>PayloadType</key>
  <string>Configuration</string>
  <key>PayloadUUID</key>
  <string>{{ xml .ProfileUUID }}</string>
  <key>PayloadVersion</key>
  <integer>1</integer>
</dict>
</plist>
`))
