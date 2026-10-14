package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

const (
	geoIPDatabaseSingleton = "geoip-database"
	geoIPDatabaseField     = "GeoLite2-City.mmdb"
	maxGeoIPDatabaseBytes  = 256 << 20
)

var geoIPImportMu sync.Mutex

type geoIPDatabaseInfo struct {
	Enabled      bool   `json:"enabled"`
	State        string `json:"state"`
	Source       string `json:"source"`
	FileName     string `json:"fileName,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	DatabaseType string `json:"databaseType,omitempty"`
	BuildTime    string `json:"buildTime,omitempty"`
	ImportedAt   string `json:"importedAt,omitempty"`
	ImportedBy   string `json:"importedBy,omitempty"`
	Message      string `json:"message,omitempty"`
}

type geoIPImportAudit struct {
	FileName     string `json:"fileName"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	DatabaseType string `json:"databaseType"`
	ImportedBy   string `json:"importedBy"`
	ImportedAt   string `json:"importedAt"`
}

func geoIPManagedPath() (string, error) {
	directory := strings.TrimSpace(os.Getenv("ORP_GEOIP_STORAGE_DIR"))
	if directory == "" {
		directory = filepath.Join("runtime", "geoip")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	return filepath.Join(absolute, geoIPDatabaseField), nil
}

func activeGeoIPDatabasePath() (string, string) {
	managed, err := geoIPManagedPath()
	if err == nil {
		if info, statErr := os.Stat(managed); statErr == nil && !info.IsDir() {
			return managed, "managed"
		}
	}
	if configured := strings.TrimSpace(os.Getenv("ORP_GEOIP_DB_PATH")); configured != "" {
		return configured, "environment"
	}
	return "", "none"
}

func (s *Server) geoIPManagedPath() (string, error) {
	directory := strings.TrimSpace(s.runtimeValue("system.geoip_storage_dir"))
	if directory == "" {
		return geoIPManagedPath()
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	return filepath.Join(absolute, geoIPDatabaseField), nil
}

func (s *Server) activeGeoIPDatabasePath() (string, string) {
	managed, err := s.geoIPManagedPath()
	if err == nil {
		if info, statErr := os.Stat(managed); statErr == nil && !info.IsDir() {
			return managed, "managed"
		}
	}
	if configured := strings.TrimSpace(s.runtimeValue("system.geoip_db_path")); configured != "" {
		source := "database"
		if effective, ok := s.runtimeSettings["system.geoip_db_path"]; ok && effective.Source == "environment" {
			source = "environment"
		}
		return configured, source
	}
	if s.runtimeSettings == nil {
		return activeGeoIPDatabasePath()
	}
	return "", "none"
}

func isGeoIPCityDatabase(databaseType string) bool {
	return strings.Contains(strings.ToLower(databaseType), "city")
}

func inspectGeoIPCityDatabase(path string) (maxminddb.Metadata, error) {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return maxminddb.Metadata{}, errors.New("文件不是有效的 MaxMind DB 数据库")
	}
	metadata := reader.Metadata
	verifyErr := reader.Verify()
	closeErr := reader.Close()
	if verifyErr != nil || closeErr != nil {
		return maxminddb.Metadata{}, errors.New("MaxMind DB 文件校验失败")
	}
	if !isGeoIPCityDatabase(metadata.DatabaseType) {
		return maxminddb.Metadata{}, fmt.Errorf("数据库类型 %q 不是 City 数据库", metadata.DatabaseType)
	}
	return metadata, nil
}

func geoIPFileInfo(path string, source string) geoIPDatabaseInfo {
	info := geoIPDatabaseInfo{Source: source, State: "disabled"}
	if path == "" {
		info.Message = "尚未导入或配置 GeoIP City 数据库"
		return info
	}
	file, err := os.Open(path)
	if err != nil {
		info.State = "error"
		info.Message = "GeoIP 数据库文件无法读取"
		return info
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		info.State = "error"
		info.Message = "GeoIP 数据库路径不是普通文件"
		return info
	}
	metadata, err := inspectGeoIPCityDatabase(path)
	if err != nil {
		info.State = "error"
		info.Message = err.Error()
		return info
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		info.State = "error"
		info.Message = "计算 GeoIP 数据库摘要失败"
		return info
	}
	info.Enabled = true
	info.State = "ready"
	info.FileName = filepath.Base(path)
	info.SizeBytes = stat.Size()
	info.SHA256 = hex.EncodeToString(hash.Sum(nil))
	info.DatabaseType = metadata.DatabaseType
	if !metadata.BuildTime().IsZero() {
		info.BuildTime = metadata.BuildTime().UTC().Format(time.RFC3339)
	}
	return info
}

func (s *Server) orpGeoIPDatabaseGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:system:manage"); !ok {
		return
	}
	path, source := s.activeGeoIPDatabasePath()
	info := geoIPFileInfo(path, source)
	if source == "managed" {
		var raw []byte
		if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_singleton WHERE name=?", geoIPDatabaseSingleton).Scan(&raw); err == nil {
			var stored geoIPImportAudit
			if json.Unmarshal(raw, &stored) == nil {
				info.FileName = stored.FileName
				info.ImportedAt = stored.ImportedAt
				info.ImportedBy = stored.ImportedBy
				if stored.SHA256 != "" && info.SHA256 != stored.SHA256 {
					info.Enabled = false
					info.State = "error"
					info.Message = "数据库文件已变化，请重新导入以通过摘要校验"
				}
			}
		}
	}
	orpReply(w, http.StatusOK, info)
}

func (s *Server) orpGeoIPDatabaseImport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	geoIPImportMu.Lock()
	defer geoIPImportMu.Unlock()
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		orpFailure(w, http.StatusUnsupportedMediaType, "请使用 multipart/form-data 上传 .mmdb 文件")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxGeoIPDatabaseBytes+(2<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		orpFailure(w, http.StatusBadRequest, "上传数据无效或超过 256 MB 限制")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	} else {
		orpFailure(w, http.StatusBadRequest, "上传表单中没有文件")
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		orpFailure(w, http.StatusBadRequest, "请上传一个 .mmdb 文件")
		return
	}
	header := files[0]
	if !strings.EqualFold(filepath.Ext(filepath.Base(header.Filename)), ".mmdb") {
		orpFailure(w, http.StatusBadRequest, "只接受 .mmdb 文件")
		return
	}
	if header.Size < 1 || header.Size > maxGeoIPDatabaseBytes {
		orpFailure(w, http.StatusBadRequest, "数据库文件必须大于 0 且不超过 256 MB")
		return
	}
	managed, err := s.geoIPManagedPath()
	if err != nil {
		orpFailure(w, http.StatusInternalServerError, "无法确定 GeoIP 数据库存储位置")
		return
	}
	directory := filepath.Dir(managed)
	if err := os.MkdirAll(directory, 0700); err != nil {
		orpFailure(w, http.StatusInternalServerError, "无法创建 GeoIP 数据库存储目录")
		return
	}
	if info, err := os.Lstat(managed); err == nil && info.Mode()&os.ModeSymlink != 0 {
		orpFailure(w, http.StatusInternalServerError, "GeoIP 数据库目标路径不能是符号链接")
		return
	}
	source, err := header.Open()
	if err != nil {
		orpFailure(w, http.StatusBadRequest, "无法读取上传文件")
		return
	}
	defer source.Close()
	temporary, err := os.CreateTemp(directory, ".geoip-upload-*.mmdb")
	if err != nil {
		orpFailure(w, http.StatusInternalServerError, "无法创建临时数据库文件")
		return
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(source, maxGeoIPDatabaseBytes+1))
	if copyErr == nil && size <= maxGeoIPDatabaseBytes {
		copyErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil || size < 1 || size > maxGeoIPDatabaseBytes {
		orpFailure(w, http.StatusBadRequest, "读取失败或文件超过 256 MB 限制")
		return
	}
	metadata, err := inspectGeoIPCityDatabase(temporaryPath)
	if err != nil {
		orpFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	backupPath := managed + ".previous"
	_ = os.Remove(backupPath)
	hadPrevious := false
	if _, err := os.Lstat(managed); err == nil {
		if err := os.Rename(managed, backupPath); err != nil {
			orpFailure(w, http.StatusInternalServerError, "无法安全替换当前 GeoIP 数据库")
			return
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		orpFailure(w, http.StatusInternalServerError, "无法检查当前 GeoIP 数据库")
		return
	}
	if err := os.Rename(temporaryPath, managed); err != nil {
		if hadPrevious {
			_ = os.Rename(backupPath, managed)
		}
		orpFailure(w, http.StatusInternalServerError, "无法启用导入的 GeoIP 数据库")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stored := geoIPImportAudit{FileName: filepath.Base(header.Filename), SizeBytes: size, SHA256: hex.EncodeToString(hasher.Sum(nil)), DatabaseType: metadata.DatabaseType, ImportedBy: actor, ImportedAt: now}
	storedRaw, _ := json.Marshal(stored)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		var before []byte
		queryErr := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_singleton WHERE name=? FOR UPDATE", geoIPDatabaseSingleton).Scan(&before)
		if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
			err = queryErr
		} else if _, err = tx.ExecContext(r.Context(), "INSERT INTO orp_singleton(name,document) VALUES(?,?) ON DUPLICATE KEY UPDATE document=VALUES(document)", geoIPDatabaseSingleton, storedRaw); err == nil {
			after, _ := json.Marshal(map[string]any{"fileName": stored.FileName, "sizeBytes": size, "sha256": stored.SHA256, "databaseType": metadata.DatabaseType, "buildTime": metadata.BuildTime().UTC().Format(time.RFC3339), "importedAt": now, "importedBy": actor})
			err = orpAudit(r.Context(), tx, actor, "import", "geoip-database", 0, before, after, orpClientIP(r))
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if err != nil {
		_ = os.Remove(managed)
		if hadPrevious {
			_ = os.Rename(backupPath, managed)
		}
		orpFailure(w, http.StatusInternalServerError, "保存 GeoIP 数据库记录失败，已恢复原数据库")
		return
	}
	if hadPrevious {
		_ = os.Remove(backupPath)
	}
	info := geoIPFileInfo(managed, "managed")
	info.FileName = stored.FileName
	info.ImportedAt = stored.ImportedAt
	info.ImportedBy = stored.ImportedBy
	orpReply(w, http.StatusOK, info)
}
