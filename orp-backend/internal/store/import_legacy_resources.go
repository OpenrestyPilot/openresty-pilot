package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var legacyNginxName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
var legacyNginxHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// LegacyImportReport distinguishes resources imported into the numeric console
// model from classes that remain available only in the legacy UUID model.
type LegacyImportReport struct {
	Imported map[string]int
	Blocked  map[string]string
}

type legacyImportResource struct {
	ctx           context.Context
	tx            *sql.Tx
	centers       map[string]int64
	groups        map[string]string
	certs         map[string]int64
	disabledCerts map[string]bool
	report        LegacyImportReport
}

// ImportLegacyResources imports the safe subset of the legacy UUID schema into
// orp_resource. A single MySQL advisory lock and transaction serialize imports;
// a savepoint keeps an unsupported class from partially importing while
// allowing independent classes to proceed. Source rows are never changed.
func ImportLegacyResources(db *sql.DB) (LegacyImportReport, error) {
	ctx := context.Background()
	connection, err := db.Conn(ctx)
	if err != nil {
		return LegacyImportReport{}, fmt.Errorf("get legacy resource import connection: %w", err)
	}
	defer connection.Close()
	var lockResult sql.NullInt64
	if err := connection.QueryRowContext(ctx, "SELECT GET_LOCK('orp-legacy-resource-import', 30)").Scan(&lockResult); err != nil {
		return LegacyImportReport{}, fmt.Errorf("acquire legacy resource import lock: %w", err)
	}
	if !lockResult.Valid || lockResult.Int64 != 1 {
		return LegacyImportReport{}, errors.New("could not acquire legacy resource import lock")
	}
	defer connection.ExecContext(ctx, "SELECT RELEASE_LOCK('orp-legacy-resource-import')")

	tx, err := connection.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return LegacyImportReport{}, fmt.Errorf("begin legacy resource import: %w", err)
	}
	defer tx.Rollback()
	imp := &legacyImportResource{
		ctx: ctx, tx: tx, centers: map[string]int64{}, groups: map[string]string{}, certs: map[string]int64{}, disabledCerts: map[string]bool{},
		report: LegacyImportReport{Imported: map[string]int{}, Blocked: map[string]string{}},
	}
	if err := imp.importCenters(); err != nil {
		return imp.report, fmt.Errorf("import legacy centers: %w", err)
	}
	if err := imp.loadCenterMappings(); err != nil {
		return imp.report, err
	}
	// Dependencies are imported in FK order. A blocked upstream or certificate
	// class causes dependent listeners/services to be blocked as well.
	imp.runClass("nodes", imp.importNodes)
	imp.runClass("http-upstreams", imp.importHTTPUpstreams)
	imp.runClass("stream-upstreams", imp.importStreamUpstreams)
	imp.runClass("certificates", imp.importCertificates)
	imp.runClass("dns-resolvers", imp.importDNSResolvers)
	imp.blockClassIfRows("http-listeners", "http_location")
	imp.blockClassIfRows("http-listeners", "ip_policy", "api_policy", "api_policy_rule")
	imp.blockClassIfRows("http-listeners", "http_configuration")
	imp.runClass("http-listeners", imp.importHTTPListeners)
	imp.blockClassIfRows("stream-services", "ip_policy")
	imp.blockClassIfRows("stream-services", "http_configuration")
	imp.runClass("stream-services", imp.importStreamServices)
	imp.blockIfRows("IP/API policies", "ip_policy", "api_policy", "api_policy_rule")
	imp.blockIfRows("HTTP locations", "http_location")
	imp.blockIfRows("HTTP singleton configuration", "http_configuration")
	imp.blockIfRows("runtime configuration history", "runtime_configuration_version")
	imp.blockIfRows("configuration_dictionary", "configuration_dictionary")
	imp.blockIfRows("audit_event", "audit_event")
	imp.blockIfRows("reload task history", "control_api_reload_task", "control_api_reload_node_result")
	if err := imp.persistImportReport(); err != nil {
		return imp.report, fmt.Errorf("persist legacy resource import report: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return imp.report, fmt.Errorf("commit legacy resource import: %w", err)
	}
	return imp.report, nil
}

// persistImportReport makes the decisions from this import attempt durable.
// The report is append-only and committed atomically with imported resources;
// it never stores source payloads or changes source rows.
func (r *legacyImportResource) persistImportReport() error {
	runID := uuid.New()
	classes := make(map[string]struct{}, len(r.report.Imported)+len(r.report.Blocked))
	for class := range r.report.Imported {
		classes[class] = struct{}{}
	}
	for class := range r.report.Blocked {
		classes[class] = struct{}{}
	}
	ordered := make([]string, 0, len(classes))
	for class := range classes {
		ordered = append(ordered, class)
	}
	sort.Strings(ordered)
	for _, class := range ordered {
		status := "imported"
		var reason any
		if blocked, ok := r.report.Blocked[class]; ok {
			status = "blocked"
			reason = blocked
		}
		if _, err := r.tx.ExecContext(r.ctx, `INSERT INTO legacy_resource_import_report
			(run_id, resource_class, status, imported_count, blocked_reason)
			VALUES (?, ?, ?, ?, ?)`, runID[:], class, status, r.report.Imported[class], reason); err != nil {
			return fmt.Errorf("record class %s: %w", class, err)
		}
	}
	return nil
}

// ImportLegacyCenters remains as a compatibility wrapper for callers that used
// the previous center-only startup hook. It now runs the complete safe import.
func ImportLegacyCenters(db *sql.DB) error {
	report, err := ImportLegacyResources(db)
	if err != nil {
		return err
	}
	if len(report.Blocked) > 0 {
		return fmt.Errorf("legacy resource classes require manual migration: %s", report.blockedSummary())
	}
	return nil
}

func (r LegacyImportReport) blockedSummary() string {
	keys := make([]string, 0, len(r.Blocked))
	for key := range r.Blocked {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+": "+r.Blocked[key])
	}
	return strings.Join(parts, "; ")
}

func (r *legacyImportResource) runClass(name string, fn func() error) {
	if _, blocked := r.report.Blocked[name]; blocked {
		return
	}
	point := "legacy_" + strings.ReplaceAll(name, "-", "_")
	if _, err := r.tx.ExecContext(r.ctx, "SAVEPOINT "+point); err != nil {
		r.report.Blocked[name] = "could not create migration savepoint"
		return
	}
	importedBefore := make(map[string]int, len(r.report.Imported))
	for key, value := range r.report.Imported {
		importedBefore[key] = value
	}
	centersBefore := cloneIntMap(r.centers)
	groupsBefore := cloneStringMap(r.groups)
	certsBefore := cloneIntMap(r.certs)
	disabledCertsBefore := cloneBoolMap(r.disabledCerts)
	if err := fn(); err != nil {
		_, _ = r.tx.ExecContext(r.ctx, "ROLLBACK TO SAVEPOINT "+point)
		_, _ = r.tx.ExecContext(r.ctx, "RELEASE SAVEPOINT "+point)
		r.report.Imported = importedBefore
		r.centers, r.groups, r.certs, r.disabledCerts = centersBefore, groupsBefore, certsBefore, disabledCertsBefore
		r.report.Blocked[name] = err.Error()
		return
	}
	_, _ = r.tx.ExecContext(r.ctx, "RELEASE SAVEPOINT "+point)
}

func cloneIntMap(source map[string]int64) map[string]int64 {
	copy := make(map[string]int64, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
func cloneBoolMap(source map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
func cloneStringMap(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func (r *legacyImportResource) blockClassIfRows(name string, tables ...string) {
	if _, alreadyBlocked := r.report.Blocked[name]; alreadyBlocked {
		return
	}
	for _, table := range tables {
		var count int64
		if err := r.tx.QueryRowContext(r.ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			r.report.Blocked[name] = fmt.Sprintf("cannot inspect legacy table %s: %v", table, err)
			return
		}
		if count > 0 {
			r.report.Blocked[name] = fmt.Sprintf("%d rows in %s contain policy/routing fields absent from the current document model; source rows were retained", count, table)
			return
		}
	}
}

func (r *legacyImportResource) blockIfRows(name string, tables ...string) {
	for _, table := range tables {
		var count int64
		query := "SELECT COUNT(*) FROM " + table
		if err := r.tx.QueryRowContext(r.ctx, query).Scan(&count); err != nil {
			r.report.Blocked[name] = fmt.Sprintf("cannot inspect legacy table %s: %v", table, err)
			return
		}
		if count > 0 {
			r.report.Blocked[name] = fmt.Sprintf("%d rows in %s have no lossless field mapping to the current resource model; source rows were retained", count, table)
			return
		}
	}
}

func (r *legacyImportResource) importCenters() error {
	rows, err := r.tx.QueryContext(r.ctx, "SELECT id,code,name,enabled FROM center ORDER BY code,id")
	if err != nil {
		return err
	}
	type row struct {
		id         uuid.UUID
		code, name string
		enabled    bool
	}
	values := []row{}
	for rows.Next() {
		var raw []byte
		var value row
		if err := rows.Scan(&raw, &value.code, &value.name, &value.enabled); err != nil {
			rows.Close()
			return err
		}
		value.id, err = uuid.FromBytes(raw)
		if err != nil {
			rows.Close()
			return fmt.Errorf("invalid legacy Center UUID: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, value := range values {
		doc := map[string]any{"code": value.code, "name": value.name, "description": "", "enabled": value.enabled, "legacyId": value.id.String()}
		if _, err := r.insert("centers", value.id.String(), "code", value.code, doc); err != nil {
			return err
		}
	}
	return nil
}

func (r *legacyImportResource) loadCenterMappings() error {
	rows, err := r.tx.QueryContext(r.ctx, "SELECT id,document FROM orp_resource WHERE kind='centers'")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		var doc map[string]any
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		legacyID := stringValue(doc["legacyId"])
		if legacyID != "" {
			if _, duplicate := r.centers[legacyID]; duplicate {
				return fmt.Errorf("legacy Center UUID %s maps to multiple console resources", legacyID)
			}
			r.centers[legacyID] = id
		}
	}
	return rows.Err()
}

func (r *legacyImportResource) insert(kind, legacyID, identityField string, identityValue any, document map[string]any) (int64, error) {
	identitySQL := ""
	if identityField != "" {
		identitySQL = fmt.Sprintf("kind=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.%s'))=?", identityField)
		if identityField == "name" || identityField == "code" {
			identitySQL = fmt.Sprintf("kind=? AND LOWER(JSON_UNQUOTE(JSON_EXTRACT(document,'$.%s')))=LOWER(?)", identityField)
		}
	}
	return r.insertWithIdentity(kind, legacyID, identitySQL, []any{kind, fmt.Sprint(identityValue)}, document)
}

func (r *legacyImportResource) insertWithIdentity(kind, legacyID, identityPredicate string, identityArgs []any, document map[string]any) (int64, error) {
	var existing int64
	var matches int
	if err := r.tx.QueryRowContext(r.ctx, "SELECT COUNT(*) FROM orp_resource WHERE kind=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.legacyId'))=?", kind, legacyID).Scan(&matches); err != nil {
		return 0, fmt.Errorf("find %s legacy mapping %s: %w", kind, legacyID, err)
	}
	if matches > 1 {
		return 0, fmt.Errorf("%s legacy UUID %s maps to multiple console resources", kind, legacyID)
	}
	if matches == 1 {
		if err := r.tx.QueryRowContext(r.ctx, "SELECT id FROM orp_resource WHERE kind=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.legacyId'))=? LIMIT 1", kind, legacyID).Scan(&existing); err != nil {
			return 0, err
		}
		return existing, nil
	}
	if identityPredicate != "" {
		// Every identity predicate is supplied only by this package. Scope
		// comparisons use JSON values because orp_resource has no typed columns.
		query := "SELECT id FROM orp_resource WHERE " + identityPredicate + " LIMIT 1"
		err := r.tx.QueryRowContext(r.ctx, query, identityArgs...).Scan(&existing)
		if err == nil {
			return 0, fmt.Errorf("%s identity conflicts with console resource %d without matching legacyId; refusing overwrite", kind, existing)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return 0, fmt.Errorf("encode %s legacy UUID %s: %w", kind, legacyID, err)
	}
	result, err := r.tx.ExecContext(r.ctx, "INSERT INTO orp_resource(kind,document) VALUES(?,?)", kind, encoded)
	if err != nil {
		return 0, fmt.Errorf("import %s legacy UUID %s: %w", kind, legacyID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.report.Imported[kind]++
	return id, nil
}

func (r *legacyImportResource) importNodes() error {
	rows, err := r.tx.QueryContext(r.ctx, "SELECT id,center_id,name,protocol,host,service_port,control_api_url,enabled FROM nginx_node ORDER BY center_id,name,id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var rawID, rawCenter []byte
		var name, host string
		var protocol, controlURL sql.NullString
		var port int
		var enabled bool
		if err := rows.Scan(&rawID, &rawCenter, &name, &protocol, &host, &port, &controlURL, &enabled); err != nil {
			return err
		}
		id, err := uuid.FromBytes(rawID)
		if err != nil {
			return fmt.Errorf("invalid nginx_node UUID: %w", err)
		}
		centerUUID, err := uuid.FromBytes(rawCenter)
		if err != nil {
			return fmt.Errorf("invalid nginx_node center UUID: %w", err)
		}
		centerID, ok := r.centers[centerUUID.String()]
		if !ok {
			return fmt.Errorf("node %s refers to a Center that was not imported", id)
		}
		controlEndpoint := ""
		if controlURL.Valid && strings.TrimSpace(controlURL.String) != "" {
			if err := safeControlURL(controlURL.String); err != nil {
				return fmt.Errorf("node %s control_api_url cannot be safely migrated: %w", id, err)
			}
			controlEndpoint = strings.TrimSpace(controlURL.String)
		}
		status := "paused"
		if enabled {
			status = "running"
		}
		doc := map[string]any{"legacyId": id.String(), "centerId": centerID, "name": name, "host": host, "servicePort": port, "status": status, "weight": 1}
		if controlEndpoint != "" {
			doc["controlEndpoint"] = controlEndpoint
		}
		if protocol.Valid {
			doc["legacyProtocol"] = protocol.String
		}
		if _, err := r.insert("nodes", id.String(), "name", name, doc); err != nil {
			return err
		}
	}
	return rows.Err()
}

func safeControlURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("not an absolute URL")
	}
	if parsed.User != nil {
		return errors.New("URL user information is not copied")
	}
	if parsed.RawQuery != "" {
		return errors.New("query parameters are not copied because they may contain credentials")
	}
	return nil
}

func (r *legacyImportResource) importHTTPUpstreams() error {
	rows, err := r.tx.QueryContext(r.ctx, "SELECT id,center_id,name,keepalive_connections,zone_size_kilobytes,health_check_enabled FROM http_upstream ORDER BY center_id,name,id")
	if err != nil {
		return err
	}
	type upstream struct {
		id, center      uuid.UUID
		name            string
		keepalive, zone int
		health          bool
	}
	values := []upstream{}
	for rows.Next() {
		var rawID, rawCenter []byte
		var v upstream
		if err := rows.Scan(&rawID, &rawCenter, &v.name, &v.keepalive, &v.zone, &v.health); err != nil {
			rows.Close()
			return err
		}
		v.id, err = uuid.FromBytes(rawID)
		if err != nil {
			rows.Close()
			return err
		}
		v.center, err = uuid.FromBytes(rawCenter)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, upstream := range values {
		centerID, ok := r.centers[upstream.center.String()]
		if !ok {
			return fmt.Errorf("HTTP upstream %s references an unmapped Center", upstream.id)
		}
		if upstream.health {
			return fmt.Errorf("HTTP upstream %s has active health checks that the current release renderer cannot preserve", upstream.id)
		}
		nodes, err := r.httpTargets(upstream.id)
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			return fmt.Errorf("HTTP upstream %s has no importable target nodes", upstream.id)
		}
		directives := []any{}
		if !legacyNginxName.MatchString(upstream.name) || upstream.keepalive < 1 || upstream.zone < 8 {
			return fmt.Errorf("HTTP upstream %s has invalid keepalive or zone settings", upstream.id)
		}
		directives = append(directives, map[string]any{"name": "keepalive", "value": strconv.Itoa(upstream.keepalive)})
		directives = append(directives, map[string]any{"name": "zone", "value": legacyZoneName(upstream.id) + " " + strconv.Itoa(upstream.zone) + "k"})
		doc := map[string]any{"legacyId": upstream.id.String(), "centerId": centerID, "name": upstream.name, "description": "", "lbPolicy": "round_robin", "nodes": nodes, "healthCheck": map[string]any{"type": "none", "path": "/health", "intervalSec": 10, "expectedStatus": []any{}}, "directives": directives, "tags": []any{}}
		id, err := r.insert("upstream-groups", upstream.id.String(), "name", upstream.name, doc)
		if err != nil {
			return err
		}
		r.groups[upstream.id.String()] = upstream.name
		_ = id
	}
	return nil
}

func (r *legacyImportResource) httpTargets(upstream uuid.UUID) ([]any, error) {
	var memberCount, targetCount int
	if err := r.tx.QueryRowContext(r.ctx, "SELECT COUNT(*) FROM http_upstream_member WHERE upstream_id=?", upstream[:]).Scan(&memberCount); err != nil {
		return nil, err
	}
	if err := r.tx.QueryRowContext(r.ctx, "SELECT COUNT(*) FROM http_upstream_target WHERE upstream_id=?", upstream[:]).Scan(&targetCount); err != nil {
		return nil, err
	}
	if memberCount > 0 && targetCount > 0 {
		return nil, fmt.Errorf("HTTP upstream %s has rows in both member and target tables; refusing ambiguous merge", upstream)
	}
	query := "SELECT id,host,port,weight,max_fails,fail_timeout_seconds FROM http_upstream_member WHERE upstream_id=? ORDER BY host,port,id"
	if targetCount > 0 {
		query = "SELECT id,target_host,target_port,weight,max_fails,fail_timeout_seconds,backup,enabled,resolve_enabled FROM http_upstream_target WHERE upstream_id=? ORDER BY target_host,target_port,id"
	}
	rows, err := r.tx.QueryContext(r.ctx, query, upstream[:])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []any{}
	seenTargets := map[string]struct{}{}
	for rows.Next() {
		var raw []byte
		var host string
		var port, weight, maxFails, timeout int
		node := map[string]any{}
		if targetCount > 0 {
			var backup, enabled, resolve bool
			if err := rows.Scan(&raw, &host, &port, &weight, &maxFails, &timeout, &backup, &enabled, &resolve); err != nil {
				return nil, err
			}
			if !enabled || resolve {
				return nil, fmt.Errorf("HTTP upstream %s contains disabled or dynamic-DNS targets that the current renderer cannot preserve", upstream)
			}
			node["backup"] = backup
		} else {
			if err := rows.Scan(&raw, &host, &port, &weight, &maxFails, &timeout); err != nil {
				return nil, err
			}
			node["backup"] = false
		}
		legacyID, err := uuid.FromBytes(raw)
		if err != nil {
			return nil, err
		}
		if !legacyNginxHost.MatchString(host) || port < 1 || port > 65535 || weight < 1 || maxFails < 0 || timeout < 1 {
			return nil, fmt.Errorf("HTTP upstream %s has a target outside the current renderer's safe bounds", upstream)
		}
		targetKey := strings.ToLower(host) + ":" + strconv.Itoa(port)
		if _, exists := seenTargets[targetKey]; exists {
			return nil, fmt.Errorf("HTTP upstream %s has duplicate target %s", upstream, targetKey)
		}
		seenTargets[targetKey] = struct{}{}
		node["legacyId"] = legacyID.String()
		node["host"] = host
		node["port"] = port
		node["weight"] = weight
		node["maxFails"] = maxFails
		node["failTimeoutSec"] = timeout
		node["slowStartSec"] = 0
		result = append(result, node)
	}
	return result, rows.Err()
}

func legacyZoneName(id uuid.UUID) string {
	return "orp_legacy_" + strings.ReplaceAll(id.String(), "-", "")[:16]
}

func (r *legacyImportResource) importStreamUpstreams() error {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT id,center_id,name,target_host,target_port,resolve_enabled,zone_size_kilobytes,health_check_enabled
		FROM stream_upstream ORDER BY center_id,name,id`)
	if err != nil {
		return err
	}
	type source struct {
		id, center      uuid.UUID
		name, host      string
		port, zone      int
		resolve, health bool
	}
	values := []source{}
	for rows.Next() {
		var rawID, rawCenter []byte
		var item source
		if err := rows.Scan(&rawID, &rawCenter, &item.name, &item.host, &item.port, &item.resolve, &item.zone, &item.health); err != nil {
			rows.Close()
			return err
		}
		item.id, err = uuid.FromBytes(rawID)
		if err != nil {
			rows.Close()
			return err
		}
		item.center, err = uuid.FromBytes(rawCenter)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range values {
		centerID, ok := r.centers[item.center.String()]
		if !ok {
			return fmt.Errorf("Stream upstream %s references an unmapped Center", item.id)
		}
		if item.resolve || item.health {
			return fmt.Errorf("Stream upstream %s uses dynamic DNS or active health checks not supported by the current release renderer", item.id)
		}
		if !legacyNginxName.MatchString(item.name) || !legacyNginxHost.MatchString(item.host) || item.port < 1 || item.port > 65535 || item.zone < 8 || item.zone > 65536 {
			return fmt.Errorf("Stream upstream %s has invalid target or zone settings", item.id)
		}
		node := map[string]any{"legacyId": item.id.String(), "host": item.host, "port": item.port, "weight": 1, "maxFails": 1, "failTimeoutSec": 10, "slowStartSec": 0, "backup": false}
		doc := map[string]any{"legacyId": item.id.String(), "centerId": centerID, "name": item.name, "description": "", "lbPolicy": "round_robin", "nodes": []any{node}, "healthCheck": map[string]any{"type": "none", "path": "/health", "intervalSec": 10, "expectedStatus": []any{}}, "directives": []any{map[string]any{"name": "zone", "value": legacyZoneName(item.id) + " " + strconv.Itoa(item.zone) + "k"}}, "tags": []any{}}
		if _, err := r.insert("upstream-groups", item.id.String(), "name", item.name, doc); err != nil {
			return err
		}
		r.groups[item.id.String()] = item.name
	}
	return nil
}

func (r *legacyImportResource) importCertificates() error {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT id,center_id,name,common_name,certificate_pem,private_key_pem,COALESCE(chain_pem,''),enabled
		FROM tls_certificate ORDER BY center_id,name,id`)
	if err != nil {
		return err
	}
	type source struct {
		id, center                            uuid.UUID
		name, common, certificate, key, chain string
		enabled                               bool
	}
	values := []source{}
	for rows.Next() {
		var rawID, rawCenter []byte
		var item source
		if err := rows.Scan(&rawID, &rawCenter, &item.name, &item.common, &item.certificate, &item.key, &item.chain, &item.enabled); err != nil {
			rows.Close()
			return err
		}
		item.id, err = uuid.FromBytes(rawID)
		if err != nil {
			rows.Close()
			return err
		}
		item.center, err = uuid.FromBytes(rawCenter)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range values {
		centerID, ok := r.centers[item.center.String()]
		if !ok {
			return fmt.Errorf("TLS certificate %s references an unmapped Center", item.id)
		}
		block, _ := pem.Decode([]byte(item.certificate))
		if block == nil {
			return fmt.Errorf("TLS certificate %s has invalid certificate PEM", item.id)
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("TLS certificate %s has invalid X.509 content", item.id)
		}
		if _, err := tls.X509KeyPair([]byte(item.certificate), []byte(item.key)); err != nil {
			return fmt.Errorf("TLS certificate %s key pair is invalid", item.id)
		}
		sealed, err := sealLegacySecret(item.key)
		if err != nil {
			return fmt.Errorf("TLS certificate %s cannot be encrypted with ORP_DATA_KEY: %w", item.id, err)
		}
		domains := append([]string{}, parsed.DNSNames...)
		if len(domains) == 0 && parsed.Subject.CommonName != "" {
			domains = append(domains, parsed.Subject.CommonName)
		}
		if len(domains) == 0 {
			return fmt.Errorf("TLS certificate %s has no domain name to map", item.id)
		}
		doc := map[string]any{"legacyId": item.id.String(), "name": item.name, "centerScope": []any{centerID}, "domains": strings.Join(domains, ","), "certificate": item.certificate, "certificateChain": item.chain, "privateKey": sealed, "notBefore": parsed.NotBefore.UTC().Format("2006-01-02"), "notAfter": parsed.NotAfter.UTC().Format("2006-01-02"), "enabled": item.enabled}
		if _, err := r.insert("certificates", item.id.String(), "name", item.name, doc); err != nil {
			return err
		}
		var numericID int64
		var encoded []byte
		if err := r.tx.QueryRowContext(r.ctx, "SELECT id,document FROM orp_resource WHERE kind='certificates' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.legacyId'))=? LIMIT 1", item.id.String()).Scan(&numericID, &encoded); err != nil {
			return err
		}
		var current map[string]any
		if err := json.Unmarshal(encoded, &current); err != nil {
			return err
		}
		r.certs[item.id.String()] = numericID
		enabled, validEnabled := current["enabled"].(bool)
		r.disabledCerts[item.id.String()] = !validEnabled || !enabled
	}
	return nil
}

func sealLegacySecret(value string) (string, error) {
	key, err := hex.DecodeString(os.Getenv("ORP_DATA_KEY"))
	if err != nil || len(key) != 32 {
		return "", errors.New("missing or invalid ORP_DATA_KEY")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, []byte(value), nil)
	return "enc:v1:" + base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (r *legacyImportResource) importDNSResolvers() error {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT id,center_id,scope,target_resource_id,resolver_addresses,valid_seconds,timeout_milliseconds,ipv6_enabled,enabled
		FROM dns_resolver_configuration ORDER BY center_id,scope,id`)
	if err != nil {
		return err
	}
	type source struct {
		id, center     uuid.UUID
		scope          string
		target         []byte
		addresses      []byte
		valid, timeout int
		ipv6, enabled  bool
	}
	values := []source{}
	for rows.Next() {
		var rawID, rawCenter []byte
		var item source
		if err := rows.Scan(&rawID, &rawCenter, &item.scope, &item.target, &item.addresses, &item.valid, &item.timeout, &item.ipv6, &item.enabled); err != nil {
			rows.Close()
			return err
		}
		item.id, err = uuid.FromBytes(rawID)
		if err != nil {
			rows.Close()
			return err
		}
		item.center, err = uuid.FromBytes(rawCenter)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range values {
		centerID, ok := r.centers[item.center.String()]
		if !ok {
			return fmt.Errorf("DNS resolver %s references an unmapped Center", item.id)
		}
		var addresses []string
		if err := json.Unmarshal(item.addresses, &addresses); err != nil {
			return fmt.Errorf("DNS resolver %s has invalid addresses", item.id)
		}
		if item.scope != "HTTP" || len(item.target) != 0 || !item.enabled || item.ipv6 || item.timeout < 100 || item.timeout > 60000 || item.timeout%1000 != 0 || item.valid < 1 || item.valid > 3600 || len(addresses) != 1 {
			return fmt.Errorf("DNS resolver %s has a scope, address count, enabled, IPv6, or timeout value the current model cannot represent", item.id)
		}
		address := strings.TrimSpace(addresses[0])
		if net.ParseIP(address) == nil {
			return fmt.Errorf("DNS resolver %s has a non-IP address", item.id)
		}
		doc := map[string]any{"legacyId": item.id.String(), "centerId": centerID, "address": address, "port": 53, "cacheTtlSec": item.valid, "timeoutSec": item.timeout / 1000}
		predicate := "kind=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.address'))=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.port'))='53'"
		if _, err := r.insertWithIdentity("dns-resolvers", item.id.String(), predicate, []any{"dns-resolvers", strconv.FormatInt(centerID, 10), address}, doc); err != nil {
			return err
		}
	}
	return nil
}

func (r *legacyImportResource) importHTTPListeners() error {
	rows, err := r.tx.QueryContext(r.ctx, "SELECT id,center_id,domain,listen_port,ssl_enabled,certificate_id,upstream_id,access_log,error_log,ip_policy_enabled,api_policy_enabled,root_path,hide_version,response_headers,error_pages FROM http_server ORDER BY center_id,domain,listen_port,id")
	if err != nil {
		return err
	}
	type source struct {
		id, center             uuid.UUID
		domain                 string
		port                   int
		ssl, ip, api, hide     bool
		cert, upstream         []byte
		access, errorLog, root sql.NullString
		headers, pages         []byte
	}
	values := []source{}
	for rows.Next() {
		var rawID, rawCenter []byte
		var v source
		if err := rows.Scan(&rawID, &rawCenter, &v.domain, &v.port, &v.ssl, &v.cert, &v.upstream, &v.access, &v.errorLog, &v.ip, &v.api, &v.root, &v.hide, &v.headers, &v.pages); err != nil {
			rows.Close()
			return err
		}
		v.id, err = uuid.FromBytes(rawID)
		if err != nil {
			rows.Close()
			return err
		}
		v.center, err = uuid.FromBytes(rawCenter)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range values {
		centerID, ok := r.centers[item.center.String()]
		if !ok {
			return fmt.Errorf("HTTP server %s references an unmapped Center", item.id)
		}
		if !legacyDomain(item.domain) || item.port < 1 || item.port > 65535 {
			return fmt.Errorf("HTTP server %s has an invalid domain or listen port", item.id)
		}
		if item.ip || item.api || item.root.Valid && strings.TrimSpace(item.root.String) != "" || !item.hide {
			return fmt.Errorf("HTTP server %s has policy or server directives not representable by the current HTTP listener model", item.id)
		}
		if len(item.headers) > 0 && string(item.headers) != "[]" && string(item.headers) != "null" {
			var headers []string
			if err := json.Unmarshal(item.headers, &headers); err != nil || len(headers) > 0 {
				return fmt.Errorf("HTTP server %s has custom response headers the current model cannot preserve", item.id)
			}
		}
		var upstreamName string
		if len(item.upstream) > 0 {
			upstreamID, err := uuid.FromBytes(item.upstream)
			if err != nil {
				return err
			}
			upstreamName, ok = r.groups[upstreamID.String()]
			if !ok {
				return fmt.Errorf("HTTP server %s refers to a blocked or unmapped HTTP upstream", item.id)
			}
		}
		var certID any
		if len(item.cert) > 0 {
			legacyCert, err := uuid.FromBytes(item.cert)
			if err != nil {
				return err
			}
			mapped, ok := r.certs[legacyCert.String()]
			if !ok {
				return fmt.Errorf("HTTP server %s refers to a blocked or unmapped TLS certificate", item.id)
			}
			if r.disabledCerts[legacyCert.String()] {
				return fmt.Errorf("HTTP server %s refers to a disabled TLS certificate", item.id)
			}
			var certificatePEM string
			if err := r.tx.QueryRowContext(r.ctx, "SELECT certificate_pem FROM tls_certificate WHERE id=?", item.cert).Scan(&certificatePEM); err != nil {
				return err
			}
			block, _ := pem.Decode([]byte(certificatePEM))
			if block == nil {
				return fmt.Errorf("HTTP server %s refers to invalid TLS certificate PEM", item.id)
			}
			parsedCertificate, err := x509.ParseCertificate(block.Bytes)
			if err != nil || parsedCertificate.VerifyHostname(item.domain) != nil {
				return fmt.Errorf("TLS certificate selected by HTTP server %s does not cover its domain", item.id)
			}
			certID = mapped
		}
		if item.ssl && certID == nil {
			return fmt.Errorf("TLS server %s has no safely imported certificate", item.id)
		}
		if !item.ssl && certID != nil {
			return fmt.Errorf("HTTP server %s has a selected but disabled TLS certificate; the current model would activate it", item.id)
		}
		pages := map[string]string{}
		if len(item.pages) > 0 {
			if err := json.Unmarshal(item.pages, &pages); err != nil {
				return fmt.Errorf("HTTP server %s has invalid error page configuration", item.id)
			}
		}
		if !safeLegacyErrorPages(pages) {
			return fmt.Errorf("HTTP server %s has error pages outside the current listener contract", item.id)
		}
		routes := []any{}
		if upstreamName != "" {
			routes = append(routes, map[string]any{"legacyId": item.id.String(), "path": "/", "type": "proxy", "matchType": "prefix", "upstream": "http://" + upstreamName})
		}
		access := strings.TrimSpace(item.access.String)
		errorLog := strings.TrimSpace(item.errorLog.String)
		if _, err := safeLegacyLogPath(access); err != nil {
			return fmt.Errorf("HTTP server %s access log is unsafe to migrate", item.id)
		}
		if _, err := safeLegacyLogPath(errorLog); err != nil {
			return fmt.Errorf("HTTP server %s error log is unsafe to migrate", item.id)
		}
		doc := map[string]any{"legacyId": item.id.String(), "centerId": centerID, "domain": item.domain, "port": item.port, "tlsCertId": certID, "routes": routes, "errorPages": pages, "legacyAccessLog": access, "legacyErrorLog": errorLog}
		identity := "kind=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=? AND LOWER(JSON_UNQUOTE(JSON_EXTRACT(document,'$.domain')))=LOWER(?) AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.port'))=?"
		if _, err := r.insertWithIdentity("http-listeners", item.id.String(), identity, []any{"http-listeners", strconv.FormatInt(centerID, 10), item.domain, strconv.Itoa(item.port)}, doc); err != nil {
			return err
		}
	}
	return nil
}

func safeLegacyLogPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n;{}\x00#\"'") {
		return "", errors.New("empty or unsafe log path")
	}
	return value, nil
}

func legacyDomain(value string) bool {
	if value == "_" {
		return true
	}
	if legacyNginxHost.MatchString(value) {
		return true
	}
	return strings.HasPrefix(value, "*.") && legacyNginxHost.MatchString(strings.TrimPrefix(value, "*."))
}

func safeLegacyErrorPages(values map[string]string) bool {
	mediaTypePattern := regexp.MustCompile(`^[A-Za-z0-9!#$&^_.+-]+/[A-Za-z0-9!#$&^_.+-]+(?:\s*;\s*[A-Za-z0-9!#$&^_.+-]+=(?:[A-Za-z0-9!#$&^_.+-]+|"[^"]*"))*$`)
	seen := map[string]bool{}
	for key, body := range values {
		parts := strings.Split(key, "|")
		if len(parts) > 2 || strings.TrimSpace(key) != key || strings.TrimSpace(body) == "" || len(body) > 1024*1024 || strings.ContainsRune(body, 0) {
			return false
		}
		status, err := strconv.Atoi(parts[0])
		if err != nil || status < 400 || status > 599 || strconv.Itoa(status) != parts[0] {
			return false
		}
		canonical := parts[0]
		if len(parts) == 2 {
			media := parts[1]
			if strings.TrimSpace(media) == "" || len(media) > 256 || strings.ContainsAny(media, "\r\n\\'") || !mediaTypePattern.MatchString(media) {
				return false
			}
			canonical += "|" + strings.ToLower(strings.Join(strings.Fields(media), " "))
		}
		if seen[canonical] {
			return false
		}
		seen[canonical] = true
	}
	return true
}

func (r *legacyImportResource) importStreamServices() error {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT id,center_id,service_name,listen_port,protocol,upstream_id,access_log,error_log,dynamic_dns_enabled,dynamic_dns_host,dynamic_dns_port,ip_policy_enabled FROM stream_server ORDER BY center_id,listen_port,id`)
	if err != nil {
		return err
	}
	type source struct {
		id, center                       uuid.UUID
		name, protocol, access, errorLog string
		port                             int
		upstream                         []byte
		dynamic, policy                  bool
		dnsHost                          sql.NullString
		dnsPort                          sql.NullInt64
	}
	values := []source{}
	for rows.Next() {
		var rawID, rawCenter []byte
		var v source
		if err := rows.Scan(&rawID, &rawCenter, &v.name, &v.port, &v.protocol, &v.upstream, &v.access, &v.errorLog, &v.dynamic, &v.dnsHost, &v.dnsPort, &v.policy); err != nil {
			rows.Close()
			return err
		}
		v.id, err = uuid.FromBytes(rawID)
		if err != nil {
			rows.Close()
			return err
		}
		v.center, err = uuid.FromBytes(rawCenter)
		if err != nil {
			rows.Close()
			return err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range values {
		centerID, ok := r.centers[item.center.String()]
		if !ok {
			return fmt.Errorf("Stream server %s references an unmapped Center", item.id)
		}
		if item.dynamic || item.policy {
			return fmt.Errorf("Stream server %s has dynamic DNS or IP policy settings not representable by the current model", item.id)
		}
		if !legacyNginxName.MatchString(item.name) || item.port < 1 || item.port > 65535 {
			return fmt.Errorf("Stream server %s has an invalid service name or listener port", item.id)
		}
		upstreamID, err := uuid.FromBytes(item.upstream)
		if err != nil {
			return err
		}
		upstreamName, ok := r.groups[upstreamID.String()]
		if !ok {
			return fmt.Errorf("Stream server %s refers to a blocked or unmapped upstream", item.id)
		}
		var port int
		if err := r.tx.QueryRowContext(r.ctx, "SELECT target_port FROM stream_upstream WHERE id=?", upstreamID[:]).Scan(&port); err != nil {
			return err
		}
		protocol := strings.ToLower(item.protocol)
		if protocol != "tcp" && protocol != "udp" {
			return fmt.Errorf("Stream server %s has unsupported protocol", item.id)
		}
		access, err := safeLegacyLogPath(item.access)
		if err != nil {
			return fmt.Errorf("Stream server %s access log is unsafe to migrate", item.id)
		}
		errorLog, err := safeLegacyLogPath(item.errorLog)
		if err != nil {
			return fmt.Errorf("Stream server %s error log is unsafe to migrate", item.id)
		}
		doc := map[string]any{"legacyId": item.id.String(), "centerId": centerID, "name": item.name, "description": "", "listenAddress": "0.0.0.0", "listenPort": item.port, "protocol": protocol, "backends": []any{upstreamName + ":" + strconv.Itoa(port)}, "legacyAccessLog": access, "legacyErrorLog": errorLog}
		identity := "kind=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.listenPort'))=? AND LOWER(JSON_UNQUOTE(JSON_EXTRACT(document,'$.protocol')))=LOWER(?)"
		if _, err := r.insertWithIdentity("stream-services", item.id.String(), identity, []any{"stream-services", strconv.FormatInt(centerID, 10), strconv.Itoa(item.port), protocol}, doc); err != nil {
			return err
		}
	}
	return nil
}

func stringValue(value any) string { text, _ := value.(string); return text }
