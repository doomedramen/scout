package enrollment

import (
	"errors"
	"net/url"
	"strings"
)

// RenderServiceUnit binds the installed agent to the Scout control endpoint.
// The endpoint is configuration, never discovered target input, and is
// validated before it is written into the unit.
func RenderServiceUnit(template []byte, server string) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(server))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.ContainsAny(server, "\r\n\"") {
		return nil, errors.New("server URL must be an http(s) URL without control characters or quotes")
	}
	if !strings.Contains(string(template), "__SCOUT_SERVER_URL__") {
		return nil, errors.New("agent service unit is missing the server URL placeholder")
	}
	return []byte(strings.ReplaceAll(string(template), "__SCOUT_SERVER_URL__", strings.TrimRight(server, "/"))), nil
}
