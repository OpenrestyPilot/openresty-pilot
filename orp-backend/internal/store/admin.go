package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// SeedAdmin creates the first local administrator after schema migration.
// Existing credentials are never reset by process restarts.
func SeedAdmin(db *sql.DB) error {
	username := strings.TrimSpace(os.Getenv("ORP_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("ORP_ADMIN_PASSWORD")
	if password == "" {
		return fmt.Errorf("ORP_ADMIN_PASSWORD is required")
	}
	builtInRoles := []struct {
		code, name, description string
		permissions             []string
	}{
		{"super", "超级管理员", "拥有全部权限，包括账号和角色管理。", []string{"*"}},
		{"platform-admin", "平台管理员", "管理平台配置、发布、节点运维和审计；不管理账号与角色。", []string{"orp:read", "orp:config:write", "orp:publish", "orp:node:operate", "orp:system:manage", "orp:audit:read"}},
		{"config-editor", "配置编辑员", "查看和维护中心、节点及代理配置，不能发布或执行节点运维。", []string{"orp:read", "orp:config:write"}},
		{"publish-operator", "发布操作员", "查看配置并执行发布、分批推进、终止和回滚。", []string{"orp:read", "orp:publish"}},
		{"audit-viewer", "审计只读", "只读查看配置与审计记录，不可修改或发布。", []string{"orp:read", "orp:audit:read"}},
		{"secret-reader", "敏感数据审计员", "仅在日常只读和审计权限上增加查看敏感配置的权限。", []string{"orp:read", "orp:audit:read", "orp:secrets:read"}},
	}
	for _, role := range builtInRoles {
		permissions, err := json.Marshal(role.permissions)
		if err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO orp_role(code,name,description,status,builtin,permission_keys,center_ids)
			VALUES(?,?,?,'enabled',true,?,JSON_ARRAY('*'))
			ON DUPLICATE KEY UPDATE name=VALUES(name),description=VALUES(description),status='enabled',builtin=true,permission_keys=VALUES(permission_keys)`,
			role.code, role.name, role.description, permissions); err != nil {
			return err
		}
	}
	var roleID int64
	if err := db.QueryRow("SELECT id FROM orp_role WHERE code='super'").Scan(&roleID); err != nil {
		return err
	}
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM orp_account WHERE username=?)", username).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	roles, _ := json.Marshal([]int64{roleID})
	_, err = db.Exec("INSERT INTO orp_account(username,password_hash,real_name,status,role_ids) VALUES(?,?,?,'enabled',?)", username, string(hash), username, roles)
	return err
}
