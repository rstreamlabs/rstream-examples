// See LICENSE file in the project root for license information.

package authz

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type Checker struct {
	header string
	token  string
}

func New(header, token string) Checker {
	header = strings.TrimSpace(header)
	if header == "" {
		header = "X-Egress-Token"
	}
	return Checker{
		header: http.CanonicalHeaderKey(header),
		token:  strings.TrimSpace(token),
	}
}

func (c Checker) Enabled() bool {
	return c.token != ""
}

func (c Checker) Header() string {
	return c.header
}

func (c Checker) Allow(r *http.Request) bool {
	if c.token == "" {
		return true
	}
	value := strings.TrimSpace(r.Header.Get(c.header))
	if value == "" && strings.EqualFold(c.header, "Proxy-Authorization") {
		value = strings.TrimSpace(r.Header.Get("Authorization"))
	}
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = strings.TrimSpace(value[len("bearer "):])
	}
	if value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(c.token)) == 1
}
