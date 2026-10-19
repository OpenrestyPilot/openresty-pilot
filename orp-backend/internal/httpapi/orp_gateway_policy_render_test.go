package httpapi

import (
	"strings"
	"testing"
)

const gatewayPolicyReleaseID = "00000000-0000-4000-8000-000000000017"

func TestRenderHTTPGatewayMethodAndRatePolicies(t *testing.T) {
	release, err := renderRelease(1, gatewayPolicyReleaseID, map[string][]orpDocument{
		"http-listeners": {{
			"id": int64(7), "domain": "gateway.test", "port": float64(18087),
			"routes": []any{map[string]any{
				"id": int64(17), "path": "/guarded", "type": "direct", "returnCode": float64(200), "returnBody": "ok",
				"allowedMethods":   []any{"GET", "POST"},
				"rateLimitEnabled": true, "ratePerSecond": float64(5), "rateLimitBurst": float64(2), "rateLimitNodelay": true,
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"limit_req_zone $binary_remote_addr zone=orp_rate_7_17:10m rate=5r/s;",
		"limit_except GET POST { deny all; }",
		"limit_req zone=orp_rate_7_17 burst=2 nodelay;",
	} {
		if !strings.Contains(release.Config, expected) {
			t.Errorf("generated gateway config missing %q", expected)
		}
	}
}

func TestRenderHTTPGatewayPolicyDefaultsRemainCompatible(t *testing.T) {
	release, err := renderRelease(1, gatewayPolicyReleaseID, map[string][]orpDocument{
		"http-listeners": {{
			"id": int64(7), "domain": "gateway.test", "port": float64(18087),
			"routes": []any{map[string]any{"id": int64(17), "path": "/open", "type": "direct", "returnCode": float64(200), "returnBody": "ok"}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(release.Config, "limit_except") || strings.Contains(release.Config, "limit_req") {
		t.Fatal("legacy route without gateway policy unexpectedly rendered method or rate limits")
	}
}

func TestRenderHTTPGatewayRejectsInvalidMethodAndRatePolicies(t *testing.T) {
	cases := []struct {
		name  string
		route map[string]any
	}{
		{name: "unknown method", route: map[string]any{"allowedMethods": []any{"GET", "BREW"}}},
		{name: "duplicate method", route: map[string]any{"allowedMethods": []any{"GET", "GET"}}},
		{name: "fractional rate", route: map[string]any{"rateLimitEnabled": true, "ratePerSecond": float64(1.5), "rateLimitBurst": float64(0)}},
		{name: "missing burst", route: map[string]any{"rateLimitEnabled": true, "ratePerSecond": float64(1)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			route := map[string]any{"id": int64(17), "path": "/guarded", "type": "direct", "returnCode": float64(200), "returnBody": "ok"}
			for key, value := range test.route {
				route[key] = value
			}
			_, err := renderRelease(1, gatewayPolicyReleaseID, map[string][]orpDocument{
				"http-listeners": {{"id": int64(7), "domain": "gateway.test", "port": float64(18087), "routes": []any{route}}},
			}, nil)
			if err == nil {
				t.Fatal("invalid gateway policy was accepted")
			}
		})
	}
}
