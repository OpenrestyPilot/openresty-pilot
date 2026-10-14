package httpapi

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderReleaseDeterministicAndContainsListener(t *testing.T) {
	resources := map[string][]orpDocument{
		"http-listeners": {{"id": int64(12), "domain": "release.local", "port": float64(18082), "routes": []any{map[string]any{"path": "/", "type": "direct", "returnCode": float64(200), "returnBody": "release-ok"}}}},
	}
	first, err := renderRelease(2, "00000000-0000-4000-8000-000000000001", resources, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderRelease(2, "00000000-0000-4000-8000-000000000001", resources, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Config != second.Config {
		t.Fatal("same frozen resources rendered differently")
	}
	for _, expected := range []string{"listen 18082;", "server_name release.local;", "return 200 release-ok;"} {
		if !strings.Contains(first.Config, expected) {
			t.Fatalf("generated config missing %q", expected)
		}
	}
}

func TestRenderReleaseUsesTargetHealthPort(t *testing.T) {
	for _, port := range []int{18080, 18180, 18280} {
		release, err := renderReleaseForTarget(2, "00000000-0000-4000-8000-000000000001", nil, nil, port)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(release.Config, fmt.Sprintf("server { listen %d;", port)) {
			t.Fatalf("generated config missing target probe listener %d", port)
		}
	}
	if _, err := renderReleaseForTarget(2, "00000000-0000-4000-8000-000000000001", nil, nil, 0); err == nil {
		t.Fatal("invalid target probe port was accepted")
	}
}

func TestRenderReleaseRejectsDirectiveInjection(t *testing.T) {
	_, err := renderRelease(1, "00000000-0000-4000-8000-000000000001", nil, map[string]any{
		"http-directives": []any{map[string]any{"name": "server_tokens", "value": "off; include /tmp/injected.conf"}},
	})
	if err == nil {
		t.Fatal("expected unsafe directive to be rejected")
	}
}

func TestRenderReleaseErrorPagesInheritAndOverrideByContentType(t *testing.T) {
	release, err := renderRelease(3, "00000000-0000-4000-8000-000000000003", map[string][]orpDocument{
		"http-listeners": {{"id": int64(12), "domain": "errors.local", "port": float64(8080), "errorPages": map[string]any{"404|application/json": "server json", "500": "server 500"}}},
	}, map[string]any{"http-error-pages": map[string]any{"404": "global html", "404|application/json": "global json", "503": "global 503"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"map $http_content_type $orp_error_page_12_404",
		"~*^application/json(?:\\s*\\x3b|$)",
		"error_page 404 $orp_error_page_12_404;",
		"/error-12-404-default.html;",
		"location = /__orp_error_pages/12/404/",
		"internal; default_type application/json; alias /etc/openresty/generated/candidates/00000000-0000-4000-8000-000000000003/",
	} {
		if !strings.Contains(release.Config, expected) {
			t.Fatalf("generated error page config missing %q", expected)
		}
	}
	if !strings.Contains(release.Config, "error_page 503 /__orp_error_pages/12/503/") || !strings.Contains(release.Config, "error_page 500 /__orp_error_pages/12/500/") {
		t.Fatal("default and inherited status pages were not emitted")
	}
	jsonPage := false
	for name, content := range release.Files {
		if strings.Contains(name, "application-json") {
			jsonPage = content == "server json"
		}
		if strings.Contains(name, "global-json") || strings.Contains(content, "global json") {
			t.Fatal("server override did not replace the inherited Content-Type page")
		}
	}
	if !jsonPage {
		t.Fatal("server Content-Type page file is missing")
	}
}

func TestValidErrorPagesRejectsUnsafeKeysAndOversizedContent(t *testing.T) {
	for _, pages := range []map[string]string{
		{"200": "not an error page"},
		{"0404": "non-canonical status"},
		{"404": "  "},
		{"404|application/json": "one", "404|APPLICATION/JSON": "duplicate"},
		{"404|application/json\ninclude": "unsafe"},
		{"404|application/json": strings.Repeat("x", 1024*1024+1)},
	} {
		if validErrorPages(pages) {
			t.Fatalf("unsafe error page config accepted: %#v", pages)
		}
	}
	if !validErrorPages(map[string]string{"404|application/json; charset=utf-8": `{"ok":true}`}) {
		t.Fatal("valid parameterized Content-Type was rejected")
	}
}

func TestContentTypeOnlyErrorPageKeepsDefaultResponseForUnmatchedTypes(t *testing.T) {
	release, err := renderRelease(1, "00000000-0000-4000-8000-000000000009", map[string][]orpDocument{
		"http-listeners": {{"id": int64(9), "domain": "only-json.local", "port": float64(8090), "errorPages": map[string]any{"404|application/json": `{"error":"missing"}`}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"default /__orp_error_pages/9/404/fallback;",
		"location = /__orp_error_pages/9/404/fallback { internal; return 404; }",
		"error_page 404 $orp_error_page_9_404;",
	} {
		if !strings.Contains(release.Config, expected) {
			t.Fatalf("content-type-only fallback missing %q", expected)
		}
	}
}

func TestRenderUpstreamHashAndStreamPolicy(t *testing.T) {
	group := orpDocument{"name": "backend", "lbPolicy": "consistent_hash", "hashKey": "$remote_addr", "nodes": []any{map[string]any{"host": "127.0.0.1", "port": float64(8080)}}}
	for _, stream := range []bool{false, true} {
		var b strings.Builder
		if err := renderUpstream(&b, group, stream); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "hash $remote_addr consistent;") {
			t.Fatal("hash directive was not rendered")
		}
	}
	group["hashKey"] = "$remote_addr;include /tmp/evil"
	if err := renderUpstream(&strings.Builder{}, group, false); err == nil {
		t.Fatal("unsafe hash key was accepted")
	}
	group["lbPolicy"] = "ip_hash"
	var stream strings.Builder
	if err := renderUpstream(&stream, group, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stream.String(), "hash $remote_addr;") {
		t.Fatal("stream ip_hash policy was not translated to a client-address hash")
	}
}

func TestRenderStreamMultipleGroupsWithPortOverride(t *testing.T) {
	resources := map[string][]orpDocument{
		"upstream-groups": {
			{"name": "alpha", "lbPolicy": "round_robin", "nodes": []any{map[string]any{"host": "127.0.0.1", "port": float64(9001)}}},
			{"name": "beta", "lbPolicy": "round_robin", "nodes": []any{map[string]any{"host": "127.0.0.2", "port": float64(9002)}}},
		},
		"stream-services": {{"id": int64(7), "protocol": "tcp", "listenAddress": "0.0.0.0", "listenPort": float64(19000), "backends": []any{"alpha:19001", "beta:19002"}}},
	}
	release, err := renderRelease(1, "00000000-0000-4000-8000-000000000001", resources, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"upstream orp_stream_service_7", "server 127.0.0.1:19001", "server 127.0.0.2:19002", "proxy_pass orp_stream_service_7;"} {
		if !strings.Contains(release.Config, expected) {
			t.Fatalf("generated stream config missing %q", expected)
		}
	}
	resources["upstream-groups"][1]["lbPolicy"] = "least_conn"
	if _, err := renderRelease(1, "00000000-0000-4000-8000-000000000001", resources, map[string]any{}); err == nil {
		t.Fatal("mixed group policy was silently discarded")
	}
}

func TestRenderReleaseForTargetKeepsTargetProbeAndServicesIndependent(t *testing.T) {
	resources := map[string][]orpDocument{
		"http-listeners":  {{"id": int64(1), "domain": "api.demo.local", "port": float64(80), "routes": []any{}}},
		"stream-services": {{"id": int64(2), "protocol": "tcp", "listenAddress": "0.0.0.0", "listenPort": float64(19090), "backends": []any{"demo:19002"}}},
		"upstream-groups": {{"name": "demo", "lbPolicy": "round_robin", "nodes": []any{map[string]any{"host": "127.0.0.1", "port": float64(19002)}}}},
	}
	first, err := renderReleaseForTarget(1, "00000000-0000-4000-8000-000000000001", resources, nil, 18080)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderReleaseForTarget(1, "00000000-0000-4000-8000-000000000001", resources, nil, 18180)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.Config, "listen 18080;") || !strings.Contains(second.Config, "listen 18180;") {
		t.Fatal("target-specific probe ports were not rendered")
	}
	if !strings.Contains(first.Config, "listen 80;") || !strings.Contains(first.Config, "listen 0.0.0.0:19090;") {
		t.Fatal("business listener ports were unexpectedly rewritten")
	}
}
