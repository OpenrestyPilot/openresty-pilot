package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) profileAccount(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	username := usernameFromToken(r.Header.Get("Authorization"))
	if username == "" {
		orpFailure(w, 401, "登录状态已失效")
		return 0, "", false
	}
	var id int64
	var status string
	if err := s.db.QueryRowContext(r.Context(), "SELECT id,status FROM orp_account WHERE username=?", username).Scan(&id, &status); err != nil || status != "enabled" {
		orpFailure(w, 401, "用户不存在或已禁用")
		return 0, "", false
	}
	return id, username, true
}

func (s *Server) profileGet(w http.ResponseWriter, r *http.Request) {
	id, username, ok := s.profileAccount(w, r)
	if !ok {
		return
	}
	var realName string
	var nickname, gender, email, phone sql.NullString
	var otpEnabled bool
	var created time.Time
	var lastLogin sql.NullTime
	if err := s.db.QueryRowContext(r.Context(), "SELECT real_name,nickname,gender,email,phone,otp_enabled,created_at,last_login_at FROM orp_account WHERE id=?", id).Scan(&realName, &nickname, &gender, &email, &phone, &otpEnabled, &created, &lastLogin); err != nil {
		orpFailure(w, 500, "读取个人信息失败")
		return
	}
	roles, err := s.accountRoles(r, username)
	if err != nil {
		orpFailure(w, 500, "读取角色失败")
		return
	}
	roleCodes := []string{}
	for _, role := range roles {
		roleCodes = append(roleCodes, role.Code)
	}
	var last any
	if lastLogin.Valid {
		last = lastLogin.Time.Format("2006-01-02 15:04:05")
	}
	avatarText := "?"
	if letters := []rune(realName); len(letters) > 0 {
		avatarText = strings.ToUpper(string(letters[0]))
	}
	orpReply(w, 200, map[string]any{"username": username, "realName": realName, "nickname": orpNullableString(nickname), "gender": orpNullableString(gender), "email": maskEmail(email.String), "phone": maskPhone(phone.String), "otpEnabled": otpEnabled, "roles": roleCodes, "createdAt": created.Format("2006-01-02 15:04:05"), "lastLoginAt": last, "homePath": "/dashboard", "avatarText": avatarText})
}

func maskEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 || len(parts[0]) < 2 {
		return email
	}
	return parts[0][:1] + "***@" + parts[1]
}
func maskPhone(phone string) string {
	if len(phone) < 7 {
		return phone
	}
	return phone[:3] + "****" + phone[len(phone)-4:]
}

func (s *Server) profilePut(w http.ResponseWriter, r *http.Request) {
	id, username, ok := s.profileAccount(w, r)
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	nickname := strings.TrimSpace(toString(body["nickname"]))
	if len(nickname) > 128 {
		orpFailure(w, 400, "昵称过长")
		return
	}
	gender := toString(body["gender"])
	if gender != "" && gender != "男" && gender != "女" && gender != "其他" {
		orpFailure(w, 400, "性别无效")
		return
	}
	email := strings.TrimSpace(toString(body["email"]))
	phone := strings.TrimSpace(toString(body["phone"]))
	if strings.Contains(email, "*") || strings.Contains(phone, "*") {
		var previousEmail, previousPhone sql.NullString
		if err := s.db.QueryRowContext(r.Context(), "SELECT email,phone FROM orp_account WHERE id=?", id).Scan(&previousEmail, &previousPhone); err != nil {
			orpFailure(w, 500, "读取联系方式失败")
			return
		}
		if strings.Contains(email, "*") {
			email = previousEmail.String
		}
		if strings.Contains(phone, "*") {
			phone = previousPhone.String
		}
	}
	if email != "" && !strings.Contains(email, "@") {
		orpFailure(w, 400, "邮箱格式无效")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE orp_account SET nickname=?,gender=?,email=?,phone=? WHERE id=?", nullableInput(nickname), nullableInput(gender), nullableInput(email), nullableInput(phone), id); err != nil {
		orpFailure(w, 500, "更新个人信息失败")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), "INSERT INTO orp_audit(actor,action,kind,resource_id,client_ip) VALUES(?,'update','profile',?,?)", username, id, orpClientIP(r))
	s.profileGet(w, r)
}

func (s *Server) profilePasswordPut(w http.ResponseWriter, r *http.Request) {
	id, username, ok := s.profileAccount(w, r)
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	oldPassword, newPassword := toString(body["oldPassword"]), toString(body["newPassword"])
	if len(newPassword) < 12 {
		orpFailure(w, 400, "新密码至少 12 位")
		return
	}
	var hash string
	if err := s.db.QueryRowContext(r.Context(), "SELECT password_hash FROM orp_account WHERE id=?", id).Scan(&hash); err != nil {
		orpFailure(w, 500, "读取账户失败")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)) != nil {
		orpFailure(w, 403, "旧密码错误")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		orpFailure(w, 500, "加密密码失败")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE orp_account SET password_hash=? WHERE id=?", string(newHash), id); err != nil {
		orpFailure(w, 500, "修改密码失败")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), "INSERT INTO orp_audit(actor,action,kind,resource_id,client_ip) VALUES(?,'password','profile',?,?)", username, id, orpClientIP(r))
	orpReply(w, 200, nil)
}

func (s *Server) profileOTPSetup(w http.ResponseWriter, r *http.Request) {
	id, username, ok := s.profileAccount(w, r)
	if !ok {
		return
	}
	var enabled bool
	if err := s.db.QueryRowContext(r.Context(), "SELECT otp_enabled FROM orp_account WHERE id=?", id).Scan(&enabled); err != nil {
		orpFailure(w, 500, "读取 OTP 状态失败")
		return
	}
	if enabled {
		orpFailure(w, 409, "OTP 已启用")
		return
	}
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		orpFailure(w, 500, "生成 OTP 密钥失败")
		return
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	sealed, err := orpSeal(encoded)
	if err != nil {
		orpFailure(w, 500, "加密 OTP 密钥失败")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE orp_account SET otp_secret=? WHERE id=?", sealed, id); err != nil {
		orpFailure(w, 500, "保存 OTP 密钥失败")
		return
	}
	uri := "otpauth://totp/" + url.PathEscape("ORP:"+username) + "?secret=" + url.QueryEscape(encoded) + "&issuer=ORP&algorithm=SHA1&digits=6&period=30"
	orpReply(w, 200, map[string]string{"otpSecret": encoded, "otpauthUrl": uri})
}

func (s *Server) profileOTPChange(w http.ResponseWriter, r *http.Request, enable bool) {
	id, username, ok := s.profileAccount(w, r)
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	code := toString(body["otpCode"])
	var encrypted sql.NullString
	var current bool
	if err := s.db.QueryRowContext(r.Context(), "SELECT otp_secret,otp_enabled FROM orp_account WHERE id=?", id).Scan(&encrypted, &current); err != nil {
		orpFailure(w, 500, "读取 OTP 状态失败")
		return
	}
	if !encrypted.Valid {
		orpFailure(w, 409, "请先生成 OTP 密钥")
		return
	}
	secret, err := orpOpen(encrypted.String)
	if err != nil {
		orpFailure(w, 500, "解密 OTP 密钥失败")
		return
	}
	if !verifyTOTP(secret, code, time.Now()) {
		orpFailure(w, 403, "动态口令错误")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE orp_account SET otp_enabled=?,otp_secret=IF(?,otp_secret,NULL) WHERE id=?", enable, enable, id); err != nil {
		orpFailure(w, 500, "更新 OTP 状态失败")
		return
	}
	action := "otp-disable"
	if enable {
		action = "otp-enable"
	}
	_, _ = s.db.ExecContext(r.Context(), "INSERT INTO orp_audit(actor,action,kind,resource_id,client_ip) VALUES(?,?,'profile',?,?)", username, action, id, orpClientIP(r))
	orpReply(w, 200, nil)
}

func (s *Server) profileOTPEnable(w http.ResponseWriter, r *http.Request) {
	s.profileOTPChange(w, r, true)
}
func (s *Server) profileOTPDisable(w http.ResponseWriter, r *http.Request) {
	s.profileOTPChange(w, r, false)
}

func verifyTOTP(secret, code string, now time.Time) bool {
	if len(code) != 6 {
		return false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}
	for step := -1; step <= 1; step++ {
		counter := uint64(now.Unix()/30 + int64(step))
		message := make([]byte, 8)
		binary.BigEndian.PutUint64(message, counter)
		mac := hmac.New(sha1.New, key)
		mac.Write(message)
		digest := mac.Sum(nil)
		offset := int(digest[len(digest)-1] & 0x0f)
		value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
		expected := fmt.Sprintf("%06d", value%1000000)
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) profileAudit(w http.ResponseWriter, r *http.Request) {
	_, username, ok := s.profileAccount(w, r)
	if !ok {
		return
	}
	pageNo, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNo < 1 {
		pageNo = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 200 {
		pageSize = 200
	}
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_audit WHERE actor=?", username).Scan(&total); err != nil {
		orpFailure(w, 500, "读取操作记录失败")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,action,kind,resource_id,created_at FROM orp_audit WHERE actor=? ORDER BY id DESC LIMIT ? OFFSET ?", username, pageSize, (pageNo-1)*pageSize)
	if err != nil {
		orpFailure(w, 500, "读取操作记录失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, resourceID int64
		var action, kind string
		var created time.Time
		if rows.Scan(&id, &action, &kind, &resourceID, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "action": action, "module": kind, "operator": username, "target": fmt.Sprintf("%s:%d", kind, resourceID), "detail": "", "ip": "", "createdAt": created.Format("2006-01-02 15:04:05")})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取操作记录失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items, "total": total})
}
