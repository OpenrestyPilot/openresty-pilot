package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const defaultHTTPLogFormat = `orp '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent'`
const defaultStreamLogFormat = `orp_stream '$remote_addr [$time_local] $protocol $status $bytes_sent $bytes_received $session_time'`

type httpSettings struct {
	RootPath                  *string           `json:"rootPath"`
	HideVersion               bool              `json:"hideVersion"`
	ResponseHeaders           []string          `json:"responseHeaders"`
	SendfileEnabled           bool              `json:"sendfileEnabled"`
	TCPNopushEnabled          bool              `json:"tcpNopushEnabled"`
	TCPNodelayEnabled         bool              `json:"tcpNodelayEnabled"`
	KeepaliveTimeoutSeconds   int               `json:"keepaliveTimeoutSeconds"`
	ClientMaxBodySize         string            `json:"clientMaxBodySize"`
	ClientHeaderBufferSize    string            `json:"clientHeaderBufferSize"`
	LargeClientHeaderBuffers  string            `json:"largeClientHeaderBuffers"`
	ServerNamesHashBucketSize int               `json:"serverNamesHashBucketSize"`
	GzipEnabled               bool              `json:"gzipEnabled"`
	GzipMinLength             string            `json:"gzipMinLength"`
	GzipCompLevel             int               `json:"gzipCompLevel"`
	HTTPLogFormat             string            `json:"httpLogFormat"`
	StreamLogFormat           string            `json:"streamLogFormat"`
	DefaultPages              map[string]string `json:"defaultPages"`
	DefaultPageKey            string            `json:"defaultPageKey"`
	ErrorPages                map[string]string `json:"errorPages"`
}

func defaultSettings() httpSettings {
	return httpSettings{HideVersion: true, ResponseHeaders: []string{}, SendfileEnabled: true, TCPNopushEnabled: true, TCPNodelayEnabled: true, KeepaliveTimeoutSeconds: 65, ClientMaxBodySize: "10m", ClientHeaderBufferSize: "1k", LargeClientHeaderBuffers: "4 8k", ServerNamesHashBucketSize: 512, GzipEnabled: true, GzipMinLength: "1k", GzipCompLevel: 2, HTTPLogFormat: defaultHTTPLogFormat, StreamLogFormat: defaultStreamLogFormat, DefaultPages: map[string]string{}, DefaultPageKey: "404.html", ErrorPages: map[string]string{}}
}
func (server *Server) getHTTPSettings(w http.ResponseWriter, r *http.Request) {
	center, ok := pathUUID(w, r, "centerID")
	if !ok {
		return
	}
	value, err := server.loadHTTPSettings(center)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, value)
}
func (server *Server) putHTTPSettings(w http.ResponseWriter, r *http.Request) {
	center, ok := pathUUID(w, r, "centerID")
	if !ok {
		return
	}
	var value httpSettings
	if !decode(r, &value) || !validHTTPSettings(&value) {
		writeError(w, 400, "invalid HTTP settings")
		return
	}
	value.HTTPLogFormat = normalizeLogFormat(value.HTTPLogFormat, "orp", defaultHTTPLogFormat)
	value.StreamLogFormat = normalizeLogFormat(value.StreamLogFormat, "orp_stream", defaultStreamLogFormat)
	headers, _ := json.Marshal(value.ResponseHeaders)
	pages, _ := json.Marshal(value.DefaultPages)
	errors, _ := json.Marshal(value.ErrorPages)
	_, err := server.db.Exec(`INSERT INTO http_configuration (center_id,root_path,hide_version,response_headers,sendfile_enabled,tcp_nopush_enabled,tcp_nodelay_enabled,keepalive_timeout_seconds,client_max_body_size,client_header_buffer_size,large_client_header_buffers,server_names_hash_bucket_size,gzip_enabled,gzip_min_length,gzip_comp_level,http_log_format,stream_log_format,default_pages,default_page_key,error_pages) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE root_path=VALUES(root_path),hide_version=VALUES(hide_version),response_headers=VALUES(response_headers),sendfile_enabled=VALUES(sendfile_enabled),tcp_nopush_enabled=VALUES(tcp_nopush_enabled),tcp_nodelay_enabled=VALUES(tcp_nodelay_enabled),keepalive_timeout_seconds=VALUES(keepalive_timeout_seconds),client_max_body_size=VALUES(client_max_body_size),client_header_buffer_size=VALUES(client_header_buffer_size),large_client_header_buffers=VALUES(large_client_header_buffers),server_names_hash_bucket_size=VALUES(server_names_hash_bucket_size),gzip_enabled=VALUES(gzip_enabled),gzip_min_length=VALUES(gzip_min_length),gzip_comp_level=VALUES(gzip_comp_level),http_log_format=VALUES(http_log_format),stream_log_format=VALUES(stream_log_format),default_pages=VALUES(default_pages),default_page_key=VALUES(default_page_key),error_pages=VALUES(error_pages)`, center[:], value.RootPath, value.HideVersion, headers, value.SendfileEnabled, value.TCPNopushEnabled, value.TCPNodelayEnabled, value.KeepaliveTimeoutSeconds, value.ClientMaxBodySize, value.ClientHeaderBufferSize, value.LargeClientHeaderBuffers, value.ServerNamesHashBucketSize, value.GzipEnabled, value.GzipMinLength, value.GzipCompLevel, value.HTTPLogFormat, value.StreamLogFormat, pages, value.DefaultPageKey, errors)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	server.audit(center, "HTTP_CONFIGURATION_UPDATED", "HTTP_CONFIGURATION", center)
	writeJSON(w, 200, value)
}
func (server *Server) loadHTTPSettings(center uuid.UUID) (httpSettings, error) {
	value := defaultSettings()
	var headers, pages, errors []byte
	var defaultPageKey *string
	err := server.db.QueryRow(`SELECT root_path,hide_version,response_headers,sendfile_enabled,tcp_nopush_enabled,tcp_nodelay_enabled,keepalive_timeout_seconds,client_max_body_size,client_header_buffer_size,large_client_header_buffers,server_names_hash_bucket_size,gzip_enabled,gzip_min_length,gzip_comp_level,http_log_format,stream_log_format,default_pages,default_page_key,error_pages FROM http_configuration WHERE center_id=?`, center[:]).Scan(&value.RootPath, &value.HideVersion, &headers, &value.SendfileEnabled, &value.TCPNopushEnabled, &value.TCPNodelayEnabled, &value.KeepaliveTimeoutSeconds, &value.ClientMaxBodySize, &value.ClientHeaderBufferSize, &value.LargeClientHeaderBuffers, &value.ServerNamesHashBucketSize, &value.GzipEnabled, &value.GzipMinLength, &value.GzipCompLevel, &value.HTTPLogFormat, &value.StreamLogFormat, &pages, &defaultPageKey, &errors)
	if err != nil {
		if err == sql.ErrNoRows {
			value.HTTPLogFormat = logFormatBody(value.HTTPLogFormat, "orp")
			value.StreamLogFormat = logFormatBody(value.StreamLogFormat, "orp_stream")
			return value, nil
		}
		return value, err
	}
	_ = json.Unmarshal(headers, &value.ResponseHeaders)
	_ = json.Unmarshal(pages, &value.DefaultPages)
	_ = json.Unmarshal(errors, &value.ErrorPages)
	if defaultPageKey != nil && *defaultPageKey != "" {
		value.DefaultPageKey = *defaultPageKey
	}
	if value.DefaultPages == nil {
		value.DefaultPages = map[string]string{}
	}
	if value.ErrorPages == nil {
		value.ErrorPages = map[string]string{}
	}
	value.HTTPLogFormat = logFormatBody(value.HTTPLogFormat, "orp")
	value.StreamLogFormat = logFormatBody(value.StreamLogFormat, "orp_stream")
	return value, nil
}

func normalizeLogFormat(value, identifier, fallback string) string {
	body := logFormatBody(value, identifier)
	if body == "" {
		return fallback
	}
	return identifier + " " + body
}

func logFormatBody(value, identifier string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, identifier+" ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, identifier))
	}
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) == 2 && isLogFormatIdentifier(parts[0]) {
		return strings.TrimSpace(parts[1])
	}
	return trimmed
}

func isLogFormatIdentifier(value string) bool {
	if value == "" || value[0] == '$' || value[0] == '\'' || value[0] == '"' || value[0] == '{' || value[0] == '[' {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

var headerPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}: [^\r\n]*$`)

func validHTTPSettings(value *httpSettings) bool {
	if value.RootPath != nil && (*value.RootPath == "" || !strings.HasPrefix(*value.RootPath, "/") || strings.Contains(*value.RootPath, "..")) {
		return false
	}
	if value.KeepaliveTimeoutSeconds < 1 || value.KeepaliveTimeoutSeconds > 3600 || value.ServerNamesHashBucketSize < 32 || value.ServerNamesHashBucketSize > 65536 || value.GzipCompLevel < 1 || value.GzipCompLevel > 9 {
		return false
	}
	for _, header := range value.ResponseHeaders {
		if len(header) > 1090 || !headerPattern.MatchString(header) {
			return false
		}
	}
	if value.HTTPLogFormat == "" {
		value.HTTPLogFormat = defaultHTTPLogFormat
	}
	if value.StreamLogFormat == "" {
		value.StreamLogFormat = defaultStreamLogFormat
	}
	if value.DefaultPages == nil {
		value.DefaultPages = map[string]string{}
	}
	if value.ErrorPages == nil {
		value.ErrorPages = map[string]string{}
	}
	if value.DefaultPageKey == "" {
		value.DefaultPageKey = "404.html"
	}
	return true
}
