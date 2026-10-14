package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A release contains the exact bytes checked with nginx -t. Secrets stay
// encrypted in MySQL and are materialized only into the node's release dir.
type orpRelease struct {
	CenterID int64             `json:"centerId"`
	Config   string            `json:"config"`
	Files    map[string]string `json:"files"`
	Digest   string            `json:"digest"`
}

var nginxName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
var nginxHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var nginxHashKey = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*(?:\$[A-Za-z_][A-Za-z0-9_]*)*$`)

func safeNginxAtom(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, " \t\r\n;{}\x00#\"'") {
		return "", fmt.Errorf("NGINX 参数包含不安全字符: %q", value)
	}
	return value, nil
}

func safeNginxDirective(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n;{}\x00#\"'") {
		return "", errors.New("指令值含有不允许的换行或配置分隔符")
	}
	return strings.TrimSpace(value), nil
}

func releaseDirective(builder *strings.Builder, indent string, value any) error {
	item, ok := value.(map[string]any)
	if !ok || !nginxName.MatchString(toString(item["name"])) {
		return errors.New("指令名称无效")
	}
	argument, err := safeNginxDirective(toString(item["value"]))
	if err != nil {
		return err
	}
	builder.WriteString(indent + toString(item["name"]))
	if argument != "" {
		builder.WriteString(" " + argument)
	}
	builder.WriteString(";\n")
	return nil
}

func releaseDirectives(builder *strings.Builder, indent string, value any) error {
	items, _ := value.([]any)
	for _, item := range items {
		if err := releaseDirective(builder, indent, item); err != nil {
			return err
		}
	}
	return nil
}

func releaseIPPolicy(builder *strings.Builder, indent string, value any) error {
	policy, _ := value.(map[string]any)
	if policy == nil || policy["enabled"] != true {
		return nil
	}
	order := []string{"allowList", "denyList"}
	if policy["priority"] == "deny-first" {
		order[0], order[1] = order[1], order[0]
	}
	for _, key := range order {
		command := "allow"
		if key == "denyList" {
			command = "deny"
		}
		items, _ := policy[key].([]any)
		for _, item := range items {
			address, err := safeNginxAtom(toString(item))
			if err != nil {
				return err
			}
			builder.WriteString(indent + command + " " + address + ";\n")
		}
	}
	return nil
}

func resourceNumber(value any) (int64, error) {
	switch number := value.(type) {
	case int64:
		return number, nil
	case float64:
		if number == float64(int64(number)) {
			return int64(number), nil
		}
	}
	return strconv.ParseInt(fmt.Sprint(value), 10, 64)
}

func resourcePort(value any) (string, error) {
	number, err := resourceNumber(value)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("监听端口无效")
	}
	return strconv.FormatInt(number, 10), nil
}

func loadReleaseInput(tx *sql.Tx, centerID int64) (map[string][]orpDocument, map[string]any, error) {
	kinds := []string{"upstream-groups", "http-listeners", "stream-services", "dns-resolvers", "certificates"}
	resources := make(map[string][]orpDocument, len(kinds))
	for _, kind := range kinds {
		rows, err := tx.Query("SELECT id,document FROM orp_resource WHERE kind=? ORDER BY id", kind)
		if err != nil {
			return nil, nil, err
		}
		resources[kind] = []orpDocument{}
		for rows.Next() {
			var id int64
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return nil, nil, err
			}
			var item orpDocument
			if err := json.Unmarshal(raw, &item); err != nil {
				rows.Close()
				return nil, nil, err
			}
			item["id"] = id
			if kind == "certificates" || fmt.Sprint(item["centerId"]) == strconv.FormatInt(centerID, 10) {
				resources[kind] = append(resources[kind], item)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rows.Close()
	}
	singletons := map[string]any{}
	rows, err := tx.Query("SELECT name,document FROM orp_singleton ORDER BY name")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, nil, err
		}
		var item any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, nil, err
		}
		singletons[name] = item
	}
	return resources, singletons, rows.Err()
}

func renderRelease(centerID int64, releaseID string, resources map[string][]orpDocument, singletons map[string]any) (orpRelease, error) {
	return renderReleaseForTarget(centerID, releaseID, resources, singletons, 18080)
}

// renderReleaseForTarget renders a release for the target's management probe
// port. Demo nodes share the host network, so the probe listener must not use
// the same port on every node.
func renderReleaseForTarget(centerID int64, releaseID string, resources map[string][]orpDocument, singletons map[string]any, healthPort int) (orpRelease, error) {
	return renderReleaseForTargetWithPortOffset(centerID, releaseID, resources, singletons, healthPort, 0)
}

// renderDemoReleaseForTarget assigns every local host-network demo node its
// own data-plane ports. Production nodes on separate hosts keep their
// configured ports unchanged; only the three Quickstart demo endpoints use
// this mapping.
func renderDemoReleaseForTarget(centerID int64, releaseID string, resources map[string][]orpDocument, singletons map[string]any, healthPort int) (orpRelease, error) {
	offset := 0
	switch healthPort {
	case 18080:
		offset = 20000
	case 18180:
		offset = 30000
	case 18280:
		offset = 40000
	default:
		return orpRelease{}, errors.New("未知的 Quickstart 演示节点端口")
	}
	return renderReleaseForTargetWithPortOffset(centerID, releaseID, resources, singletons, healthPort, offset)
}

func renderReleaseForTargetWithPortOffset(centerID int64, releaseID string, resources map[string][]orpDocument, singletons map[string]any, healthPort, portOffset int) (orpRelease, error) {
	if !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(releaseID) {
		return orpRelease{}, errors.New("候选标识无效")
	}
	if healthPort < 1 || healthPort > 65535 {
		return orpRelease{}, errors.New("节点探针端口无效")
	}
	release := orpRelease{CenterID: centerID, Files: map[string]string{}}
	var b strings.Builder
	b.WriteString("worker_processes auto;\nerror_log /dev/stderr notice;\npid /var/run/nginx.pid;\n")
	b.WriteString("env RUNTIME_CONTROL_PLANE_URL;\nenv RUNTIME_CENTER_ID;\nenv RUNTIME_POLL_INTERVAL_SECONDS;\n")
	b.WriteString("events { worker_connections 1024; }\nhttp {\n")
	b.WriteString("  include /usr/local/openresty/nginx/conf/mime.types;\n  default_type application/octet-stream;\n")
	b.WriteString("  lua_package_path \"/usr/local/openresty/lualib/?.lua;/etc/openresty/lua/?.lua;;\";\n")
	b.WriteString("  lua_package_cpath \"/usr/local/openresty/lualib/?.so;;\";\n")
	b.WriteString("  lua_shared_dict runtime_configuration 10m;\n  init_worker_by_lua_block { require(\"runtime\").start() }\n")
	b.WriteString("  log_format orp_http_access escape=json '{\"ts\":\"$time_iso8601\",\"status\":$status,\"rt\":$request_time,\"host\":\"$host\",\"remoteAddr\":\"$remote_addr\",\"uri\":\"$request_uri\",\"bytes\":$body_bytes_sent,\"requestBytes\":$request_length}';\n")
	// The demo node exposes the active release from a worker response. A
	// successful reload command alone only proves that a signal was sent.
	b.WriteString(fmt.Sprintf("  server { listen %d; server_name _;\n", healthPort))
	b.WriteString("    location = /__orp/release { default_type text/plain; return 200 \"" + releaseID + "\"; }\n")
	b.WriteString("    location = /__orp/status { access_log off; stub_status; }\n")
	b.WriteString("    location = /health { default_type application/json; return 200 '{\"status\":\"UP\",\"service\":\"openresty\"}'; }\n")
	b.WriteString("    location = /runtime/status { default_type application/json; content_by_lua_block { ngx.say(require(\"cjson\").encode(require(\"runtime\").status())) } }\n")
	b.WriteString("    location = /runtime/logs { default_type application/json; content_by_lua_block { require(\"logs\").read() } }\n")
	b.WriteString("  }\n")
	if err := releaseDirectives(&b, "  ", singletons["http-directives"]); err != nil {
		return release, err
	}
	if err := releaseLogFormat(&b, "  ", singletons["http-log-format"]); err != nil {
		return release, err
	}
	globalErrorPages, err := releaseErrorPages(singletons["http-error-pages"])
	if err != nil {
		return release, err
	}
	if err := releaseResolvers(&b, resources["dns-resolvers"]); err != nil {
		return release, err
	}
	groups := resources["upstream-groups"]
	for _, group := range groups {
		if err := renderUpstream(&b, group, false); err != nil {
			return release, err
		}
	}
	for _, listener := range resources["http-listeners"] {
		if err := renderHTTPServer(&b, listener, releaseID, resources["certificates"], globalErrorPages, release.Files, portOffset); err != nil {
			return release, err
		}
	}
	b.WriteString("}\nstream {\n")
	b.WriteString("  lua_package_path \"/usr/local/openresty/lualib/?.lua;/etc/openresty/lua/?.lua;;\";\n")
	b.WriteString("  lua_package_cpath \"/usr/local/openresty/lualib/?.so;;\";\n")
	b.WriteString("  log_format orp_stream_access escape=json '{\"ts\":\"$time_iso8601\",\"status\":\"$status\",\"duration\":$session_time,\"bytesIn\":$bytes_received,\"bytesOut\":$bytes_sent}';\n")
	if err := releaseDirectives(&b, "  ", singletons["stream-directives"]); err != nil {
		return release, err
	}
	if err := releaseLogFormat(&b, "  ", singletons["stream-log-format"]); err != nil {
		return release, err
	}
	for _, service := range resources["stream-services"] {
		upstream, err := renderStreamUpstream(&b, service, groups)
		if err != nil {
			return release, err
		}
		if err := renderStreamServer(&b, service, upstream, portOffset); err != nil {
			return release, err
		}
	}
	b.WriteString("}\n")
	release.Config = b.String()
	canonical, err := json.Marshal(struct {
		CenterID int64             `json:"centerId"`
		Config   string            `json:"config"`
		Files    map[string]string `json:"files"`
	}{release.CenterID, release.Config, release.Files})
	if err != nil {
		return release, err
	}
	sum := sha256.Sum256(canonical)
	release.Digest = hex.EncodeToString(sum[:])
	return release, nil
}

func releaseLogFormat(b *strings.Builder, indent string, value any) error {
	item, _ := value.(map[string]any)
	if item == nil || toString(item["name"]) == "" {
		return nil
	}
	name := toString(item["name"])
	if !nginxName.MatchString(name) {
		return errors.New("日志格式名称无效")
	}
	format := toString(item["format"])
	if strings.ContainsAny(format, "\r\n\x00'") {
		return errors.New("日志格式包含不允许的字符")
	}
	b.WriteString(indent + "log_format " + name + " '" + format + "';\n")
	return nil
}

func releaseResolvers(b *strings.Builder, resolvers []orpDocument) error {
	addresses := []string{}
	for _, resolver := range resolvers {
		address, err := safeNginxAtom(toString(resolver["address"]))
		if err != nil {
			return err
		}
		port, err := resourcePort(resolver["port"])
		if err != nil {
			return err
		}
		addresses = append(addresses, address+":"+port)
	}
	if len(addresses) > 0 {
		b.WriteString("  resolver " + strings.Join(addresses, " ") + ";\n")
	}
	return nil
}

func renderUpstream(b *strings.Builder, group orpDocument, stream bool) error {
	name := toString(group["name"])
	if !nginxName.MatchString(name) {
		return errors.New("Upstream 名称无效")
	}
	if health, ok := group["healthCheck"].(map[string]any); ok {
		if mode := toString(health["type"]); mode != "" && mode != "none" {
			return errors.New("当前节点不支持主动健康检查，不能忽略该 Upstream 配置")
		}
	}
	b.WriteString("  upstream " + name + " {\n")
	switch toString(group["lbPolicy"]) {
	case "least_conn":
		b.WriteString("    least_conn;\n")
	case "ip_hash":
		if stream {
			// Stream has no ip_hash directive. Hashing the client address gives
			// the same sticky-client behavior without silently changing policy.
			b.WriteString("    hash $remote_addr;\n")
		} else {
			b.WriteString("    ip_hash;\n")
		}
	case "hash", "consistent_hash":
		key := toString(group["hashKey"])
		if !nginxHashKey.MatchString(key) {
			return errors.New("Hash 负载策略需要安全的变量键表达式，例如 $remote_addr")
		}
		b.WriteString("    hash " + key)
		if toString(group["lbPolicy"]) == "consistent_hash" {
			b.WriteString(" consistent")
		}
		b.WriteString(";\n")
	case "round_robin", "":
	default:
		return errors.New("不支持的 Upstream 负载策略")
	}
	nodes, _ := group["nodes"].([]any)
	if len(nodes) == 0 {
		return errors.New("Upstream 至少需要一个目标节点")
	}
	for _, value := range nodes {
		node, _ := value.(map[string]any)
		if node == nil || !nginxHost.MatchString(toString(node["host"])) {
			return errors.New("Upstream 目标主机无效")
		}
		port, err := resourcePort(node["port"])
		if err != nil {
			return err
		}
		b.WriteString("    server " + toString(node["host"]) + ":" + port)
		if weight, err := resourceNumber(node["weight"]); err == nil && weight > 0 {
			b.WriteString(" weight=" + strconv.FormatInt(weight, 10))
		}
		if slowStart, err := resourceNumber(node["slowStartSec"]); err == nil && slowStart > 0 {
			return errors.New("当前节点不支持 Upstream slow_start")
		}
		if maxFails, err := resourceNumber(node["maxFails"]); err == nil && maxFails >= 0 {
			b.WriteString(" max_fails=" + strconv.FormatInt(maxFails, 10))
		}
		if failTimeout, err := resourceNumber(node["failTimeoutSec"]); err == nil && failTimeout > 0 {
			b.WriteString(" fail_timeout=" + strconv.FormatInt(failTimeout, 10) + "s")
		}
		if node["backup"] == true {
			b.WriteString(" backup")
		}
		b.WriteString(";\n")
	}
	if err := releaseDirectives(b, "    ", group["directives"]); err != nil {
		return err
	}
	b.WriteString("  }\n")
	return nil
}

func renderHTTPServer(b *strings.Builder, listener orpDocument, releaseID string, certificates []orpDocument, globalErrorPages map[string]string, files map[string]string, portOffset int) error {
	listenerID, err := resourceNumber(listener["id"])
	if err != nil || listenerID < 1 {
		return errors.New("HTTP 监听 ID 无效")
	}
	port, err := resourcePort(listener["port"])
	if err != nil {
		return err
	}
	port, err = offsetReleasePort(port, portOffset)
	if err != nil {
		return err
	}
	domain, err := safeNginxAtom(toString(listener["domain"]))
	if err != nil {
		return err
	}
	listenerPages, err := releaseErrorPages(listener["errorPages"])
	if err != nil {
		return err
	}
	pages := make(map[string]string, len(globalErrorPages)+len(listenerPages))
	for key, value := range globalErrorPages {
		pages[key] = value
	}
	for key, value := range listenerPages {
		pages[key] = value
	}
	pageURIs, err := renderErrorPageMappings(b, listenerID, releaseID, pages, files)
	if err != nil {
		return err
	}
	b.WriteString("  server {\n    listen " + port)
	if listener["tlsCertId"] != nil {
		b.WriteString(" ssl")
	}
	b.WriteString(";\n    server_name " + domain + ";\n")
	for _, uri := range uniqueErrorPageURIs(pageURIs) {
		if strings.HasSuffix(uri, "/fallback") {
			code, _ := strconv.Atoi(path.Base(path.Dir(uri)))
			b.WriteString(fmt.Sprintf("    location = %s { internal; return %d; }\n", uri, code))
			continue
		}
		file := path.Base(uri)
		contentType := errorPageResponseType(uri, pageURIs)
		b.WriteString(fmt.Sprintf("    location = %s { internal; default_type %s; alias /etc/openresty/generated/candidates/%s/%s; }\n", uri, contentType, releaseID, file))
	}
	b.WriteString(fmt.Sprintf("    access_log /var/log/nginx/orp-http-%d.access.log orp_http_access;\n", listenerID))
	for _, code := range sortedErrorCodes(pages) {
		defaultURI := pageURIs[fmt.Sprintf("%d", code)]
		typeEntries := errorPageTypes(pages, code)
		if len(typeEntries) > 0 {
			variable := fmt.Sprintf("$orp_error_page_%d_%d", listenerID, code)
			b.WriteString(fmt.Sprintf("    error_page %d %s;\n", code, variable))
		} else if defaultURI != "" {
			b.WriteString(fmt.Sprintf("    error_page %d %s;\n", code, defaultURI))
		}
	}
	if listener["tlsCertId"] != nil {
		certID, err := resourceNumber(listener["tlsCertId"])
		if err != nil {
			return err
		}
		found := false
		for _, cert := range certificates {
			if cert["id"] != certID {
				continue
			}
			privateKey, err := orpOpen(toString(cert["privateKey"]))
			if err != nil {
				return err
			}
			files[fmt.Sprintf("cert-%d.pem", certID)] = toString(cert["certificate"])
			files[fmt.Sprintf("key-%d.pem", certID)] = privateKey
			b.WriteString(fmt.Sprintf("    ssl_certificate /etc/openresty/generated/candidates/%s/cert-%d.pem;\n", releaseID, certID))
			b.WriteString(fmt.Sprintf("    ssl_certificate_key /etc/openresty/generated/candidates/%s/key-%d.pem;\n", releaseID, certID))
			found = true
			break
		}
		if !found {
			return errors.New("监听引用的 TLS 证书不存在")
		}
	}
	if err := releaseDirectives(b, "    ", listener["directives"]); err != nil {
		return err
	}
	if err := releaseIPPolicy(b, "    ", listener["ipPolicy"]); err != nil {
		return err
	}
	routes, _ := listener["routes"].([]any)
	for _, value := range routes {
		route, _ := value.(map[string]any)
		if err := renderLocation(b, route); err != nil {
			return err
		}
	}
	b.WriteString("  }\n")
	return nil
}

func releaseErrorPages(value any) (map[string]string, error) {
	if value == nil {
		return map[string]string{}, nil
	}
	pages := map[string]string{}
	switch data := value.(type) {
	case map[string]string:
		pages = data
	case map[string]any:
		for key, raw := range data {
			text, ok := raw.(string)
			if !ok {
				return nil, errors.New("错误页面内容必须是文本")
			}
			pages[key] = text
		}
	default:
		return nil, errors.New("错误页面配置格式无效")
	}
	if !validErrorPages(pages) {
		return nil, errors.New("错误页面配置无效")
	}
	canonical := make(map[string]string, len(pages))
	for key, content := range pages {
		parts := strings.SplitN(key, "|", 2)
		code, _ := strconv.Atoi(parts[0])
		canonicalKey := strconv.Itoa(code)
		if len(parts) == 2 {
			canonicalKey += "|" + strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(parts[1])), " "))
		}
		canonical[canonicalKey] = content
	}
	pages = canonical
	return pages, nil
}

func sortedErrorCodes(pages map[string]string) []int {
	seen := map[int]bool{}
	var codes []int
	for key := range pages {
		parts := strings.SplitN(key, "|", 2)
		code, _ := strconv.Atoi(parts[0])
		if !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	sort.Ints(codes)
	return codes
}

func errorPageTypes(pages map[string]string, code int) []string {
	var values []string
	for key := range pages {
		parts := strings.SplitN(key, "|", 2)
		status, _ := strconv.Atoi(parts[0])
		if status == code && len(parts) == 2 {
			values = append(values, parts[1])
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	return values
}

func renderErrorPageMappings(b *strings.Builder, listenerID int64, releaseID string, pages map[string]string, files map[string]string) (map[string]string, error) {
	uris := map[string]string{}
	for _, code := range sortedErrorCodes(pages) {
		defaultKey := strconv.Itoa(code)
		defaultValue, hasDefault := pages[defaultKey]
		types := errorPageTypes(pages, code)
		if hasDefault {
			uri, err := addErrorPageFile(listenerID, releaseID, code, "", defaultValue, files)
			if err != nil {
				return nil, err
			}
			uris[defaultKey] = uri
		} else if len(types) > 0 {
			uris[defaultKey] = fmt.Sprintf("/__orp_error_pages/%d/%d/fallback", listenerID, code)
		}
		for _, mediaType := range types {
			uri, err := addErrorPageFile(listenerID, releaseID, code, mediaType, pages[defaultKey+"|"+mediaType], files)
			if err != nil {
				return nil, err
			}
			uris[defaultKey+"|"+mediaType] = uri
		}
		if len(types) > 0 {
			variable := fmt.Sprintf("orp_error_page_%d_%d", listenerID, code)
			fallbackURI := uris[defaultKey]
			if fallbackURI == "" {
				return nil, errors.New("错误页面缺少可用的默认页面")
			}
			b.WriteString(fmt.Sprintf("  map $http_content_type $%s {\n    default %s;\n", variable, fallbackURI))
			for _, mediaType := range types {
				uri := uris[defaultKey+"|"+mediaType]
				pattern := errorPageContentTypePattern(mediaType)
				b.WriteString(fmt.Sprintf("    ~*^%s %s;\n", pattern, uri))
			}
			b.WriteString("  }\n")
		}
	}
	return uris, nil
}

func addErrorPageFile(listenerID int64, releaseID string, code int, mediaType, content string, files map[string]string) (string, error) {
	extension := "html"
	slug := "default"
	if mediaType != "" {
		base := strings.ToLower(strings.SplitN(mediaType, ";", 2)[0])
		base = strings.TrimSpace(base)
		slug = strings.NewReplacer("/", "-", "+", "-", ".", "-", " ", "").Replace(base)
		switch base {
		case "application/json":
			extension = "json"
		case "text/html":
			extension = "html"
		default:
			extension = "txt"
		}
		digest := sha256.Sum256([]byte(mediaType))
		slug += "-" + hex.EncodeToString(digest[:3])
	}
	file := fmt.Sprintf("error-%d-%d-%s.%s", listenerID, code, slug, extension)
	uri := fmt.Sprintf("/__orp_error_pages/%d/%d/%s", listenerID, code, file)
	files[file] = content
	return uri, nil
}

func uniqueErrorPageURIs(values map[string]string) []string {
	seen := map[string]bool{}
	var uris []string
	for _, uri := range values {
		if uri != "" && !seen[uri] {
			seen[uri] = true
			uris = append(uris, uri)
		}
	}
	sort.Strings(uris)
	return uris
}

func errorPageResponseType(uri string, values map[string]string) string {
	for key, candidate := range values {
		if candidate != uri {
			continue
		}
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.SplitN(parts[1], ";", 2)[0])
		}
	}
	return "text/html"
}

func errorPageContentTypePattern(mediaType string) string {
	parts := strings.Split(mediaType, ";")
	pattern := regexp.QuoteMeta(strings.TrimSpace(parts[0]))
	for _, parameter := range parts[1:] {
		name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		pattern += `\s*\x3b\s*` + regexp.QuoteMeta(strings.TrimSpace(name)) + `\s*=\s*` + regexp.QuoteMeta(value)
	}
	return pattern + `(?:\s*\x3b|$)`
}

func renderLocation(b *strings.Builder, route map[string]any) error {
	if route == nil {
		return errors.New("路由格式无效")
	}
	path, err := safeNginxAtom(toString(route["path"]))
	if err != nil {
		return err
	}
	match := toString(route["matchType"])
	if match != "" && match != "prefix" && match != "=" && match != "^~" && match != "~" && match != "~*" {
		return errors.New("Location 匹配方式无效")
	}
	b.WriteString("    location ")
	if match != "" && match != "prefix" {
		b.WriteString(match + " ")
	}
	b.WriteString(path + " {\n")
	switch toString(route["type"]) {
	case "proxy":
		target, err := safeNginxAtom(toString(route["upstream"]))
		if err != nil {
			return err
		}
		b.WriteString("      proxy_pass " + target + ";\n")
	case "static":
		root, err := safeNginxAtom(toString(route["rootPath"]))
		if err != nil {
			return err
		}
		command := "root"
		if route["staticMode"] == "alias" {
			command = "alias"
		}
		b.WriteString("      " + command + " " + root + ";\n")
	case "direct":
		code, err := resourceNumber(route["returnCode"])
		if err != nil || code < 100 || code > 599 {
			return errors.New("直接响应状态码无效")
		}
		body, err := safeNginxDirective(toString(route["returnBody"]))
		if err != nil {
			return err
		}
		b.WriteString("      return " + strconv.FormatInt(code, 10))
		if body != "" {
			b.WriteString(" " + body)
		}
		b.WriteString(";\n")
	default:
		return errors.New("不支持的 Location 类型")
	}
	if err := releaseDirectives(b, "      ", route["directives"]); err != nil {
		return err
	}
	if err := releaseIPPolicy(b, "      ", route["ipPolicy"]); err != nil {
		return err
	}
	b.WriteString("    }\n")
	return nil
}

func renderStreamUpstream(b *strings.Builder, service orpDocument, groups []orpDocument) (string, error) {
	serviceID, err := resourceNumber(service["id"])
	if err != nil || serviceID < 1 {
		return "", errors.New("Stream 服务 ID 无效")
	}
	backends, ok := service["backends"].([]any)
	if !ok || len(backends) == 0 {
		return "", errors.New("Stream 服务至少需要一个上游组")
	}
	byName := make(map[string]orpDocument, len(groups))
	for _, group := range groups {
		byName[toString(group["name"])] = group
	}
	name := fmt.Sprintf("orp_stream_service_%d", serviceID)
	combined := orpDocument{"name": name, "lbPolicy": "round_robin"}
	nodes := make([]any, 0)
	seen := map[string]bool{}
	for _, value := range backends {
		parts := strings.Split(toString(value), ":")
		if len(parts) != 2 || !nginxName.MatchString(parts[0]) {
			return "", errors.New("Stream 后端须为上游组名:端口")
		}
		port, err := resourcePort(parts[1])
		if err != nil {
			return "", fmt.Errorf("Stream 上游组 %s 的目标端口无效", parts[0])
		}
		backendKey := parts[0] + ":" + port
		if seen[backendKey] {
			return "", errors.New("Stream 后端上游组与目标端口不能重复")
		}
		seen[backendKey] = true
		group, exists := byName[parts[0]]
		if !exists {
			return "", fmt.Errorf("Stream 引用的上游组 %s 不存在", parts[0])
		}
		if health, ok := group["healthCheck"].(map[string]any); ok {
			if mode := toString(health["type"]); mode != "" && mode != "none" {
				return "", fmt.Errorf("Stream 上游组 %s 的主动健康检查尚未支持", parts[0])
			}
		}
		if len(backends) == 1 {
			combined["lbPolicy"] = group["lbPolicy"]
			combined["hashKey"] = group["hashKey"]
			combined["healthCheck"] = group["healthCheck"]
			combined["directives"] = group["directives"]
		} else if policy := toString(group["lbPolicy"]); (policy != "" && policy != "round_robin") || hasReleaseDirectives(group["directives"]) {
			return "", fmt.Errorf("Stream 多组后端不能无损合并 %s 的负载策略或组指令", parts[0])
		}
		members, _ := group["nodes"].([]any)
		if len(members) == 0 {
			return "", fmt.Errorf("Stream 上游组 %s 没有目标节点", parts[0])
		}
		for _, member := range members {
			original, ok := member.(map[string]any)
			if !ok {
				return "", errors.New("Stream 上游节点格式无效")
			}
			copy := make(map[string]any, len(original)+1)
			for key, item := range original {
				copy[key] = item
			}
			copy["port"] = port
			nodes = append(nodes, copy)
		}
	}
	combined["nodes"] = nodes
	if err := renderUpstream(b, combined, true); err != nil {
		return "", err
	}
	return name, nil
}

func hasReleaseDirectives(value any) bool {
	items, _ := value.([]any)
	return len(items) > 0
}

func renderStreamServer(b *strings.Builder, service orpDocument, upstream string, portOffset int) error {
	serviceID, err := resourceNumber(service["id"])
	if err != nil || serviceID < 1 {
		return errors.New("Stream 服务 ID 无效")
	}
	port, err := resourcePort(service["listenPort"])
	if err != nil {
		return err
	}
	port, err = offsetReleasePort(port, portOffset)
	if err != nil {
		return err
	}
	address := toString(service["listenAddress"])
	if address == "" {
		address = "0.0.0.0"
	}
	if !nginxHost.MatchString(address) {
		return errors.New("Stream 监听地址无效")
	}
	b.WriteString("  server {\n    listen " + address + ":" + port)
	if service["protocol"] == "udp" {
		b.WriteString(" udp")
	} else if service["protocol"] != "tcp" {
		return errors.New("Stream 协议无效")
	}
	b.WriteString(";\n")
	b.WriteString(fmt.Sprintf("    access_log /var/log/nginx/orp-stream-%d.access.log orp_stream_access;\n", serviceID))
	b.WriteString("    proxy_pass " + upstream + ";\n")
	if err := releaseDirectives(b, "    ", service["directives"]); err != nil {
		return err
	}
	if err := releaseIPPolicy(b, "    ", service["ipPolicy"]); err != nil {
		return err
	}
	b.WriteString("  }\n")
	return nil
}

func offsetReleasePort(port string, offset int) (string, error) {
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value+offset > 65535 {
		return "", errors.New("监听端口超出有效范围")
	}
	return strconv.Itoa(value + offset), nil
}

func sortedReleaseFiles(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
