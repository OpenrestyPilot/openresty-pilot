package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var tokenKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()
var revokedTokens sync.Map

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	OtpCode  string `json:"otpCode"`
	Captcha  bool   `json:"captcha"`
}

type localUserInfo struct {
	Avatar   string   `json:"avatar"`
	RealName string   `json:"realName"`
	Roles    []string `json:"roles"`
	UserID   string   `json:"userId"`
	Username string   `json:"username"`
	Desc     string   `json:"desc"`
	HomePath string   `json:"homePath"`
	Token    string   `json:"token"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.Username) == "" || request.Password == "" {
		writeAuthError(w, http.StatusBadRequest, "用户名和密码不能为空")
		return
	}
	if s.loginCaptchaEnabled() && !request.Captcha {
		writeAuthError(w, http.StatusBadRequest, "请先完成滑块验证")
		return
	}

	var hash, status string
	var otpEnabled bool
	var otpSecret sql.NullString
	err := s.db.QueryRowContext(r.Context(), "SELECT password_hash,status,otp_enabled,otp_secret FROM orp_account WHERE username=?", request.Username).Scan(&hash, &status, &otpEnabled, &otpSecret)
	if err != nil || status != "enabled" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(request.Password)) != nil {
		writeAuthError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if otpEnabled {
		if request.OtpCode == "" {
			writeJSON(w, 401, map[string]any{"code": "OTP_REQUIRED", "error": "OTP_REQUIRED", "message": "请输入动态口令"})
			return
		}
		secret, err := orpOpen(otpSecret.String)
		if err != nil || !verifyTOTP(secret, request.OtpCode, time.Now()) {
			writeAuthError(w, 401, "动态口令错误")
			return
		}
	}
	_, _ = s.db.ExecContext(r.Context(), "UPDATE orp_account SET last_login_at=NOW(6) WHERE username=?", request.Username)
	writeAuthData(w, http.StatusOK, map[string]string{"accessToken": localToken(request.Username)})
}

// loginConfig exposes the quickstart credentials so its local login page can be
// prefilled from the same .env used to initialize the administrator account.
func (s *Server) loginConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	username := strings.TrimSpace(os.Getenv("ORP_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("ORP_ADMIN_PASSWORD")
	writeAuthData(w, http.StatusOK, map[string]any{
		"username":       username,
		"password":       password,
		"captchaEnabled": s.loginCaptchaEnabled(),
	})
}

func loginCaptchaEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ORP_CAPTCHA_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) loginCaptchaEnabled() bool {
	if s.runtimeSettings != nil {
		return strings.EqualFold(strings.TrimSpace(s.runtimeValue("security.captcha_enabled")), "true") || strings.TrimSpace(s.runtimeValue("security.captcha_enabled")) == "1"
	}
	return loginCaptchaEnabled()
}

func (s *Server) refreshToken(w http.ResponseWriter, r *http.Request) {
	username := usernameFromToken(r.Header.Get("Authorization"))
	if username == "" {
		writeAuthError(w, http.StatusUnauthorized, "登录状态已失效")
		return
	}
	if _, err := s.accountRoles(r, username); err != nil {
		writeAuthError(w, http.StatusUnauthorized, "用户不存在或已禁用")
		return
	}
	// refreshTokenApi 使用未注册标准 code/data 拦截器的 baseRequestClient，
	// 因此保持其约定的响应结构。
	writeJSON(w, http.StatusOK, map[string]any{"data": localToken(username), "status": 200})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if usernameFromToken(r.Header.Get("Authorization")) != "" {
		revokedTokens.Store(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), time.Now().Add(8*time.Hour))
	}
	writeAuthData(w, http.StatusOK, nil)
}

func (s *Server) accessCodes(w http.ResponseWriter, r *http.Request) {
	username := usernameFromToken(r.Header.Get("Authorization"))
	if username == "" {
		writeAuthError(w, http.StatusUnauthorized, "未登录")
		return
	}
	roles, err := s.accountRoles(r, username)
	if err != nil {
		writeAuthError(w, 500, "读取权限失败")
		return
	}
	permissionSet := map[string]bool{}
	for _, role := range roles {
		for _, permission := range role.Permissions {
			permissionSet[permission] = true
		}
	}
	permissions := make([]string, 0, len(permissionSet))
	for permission := range permissionSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	writeAuthData(w, http.StatusOK, permissions)
}

func (s *Server) userInfo(w http.ResponseWriter, r *http.Request) {
	username := usernameFromToken(r.Header.Get("Authorization"))
	if username == "" {
		writeAuthError(w, http.StatusUnauthorized, "未登录")
		return
	}
	var id int64
	var realName, status string
	if err := s.db.QueryRowContext(r.Context(), "SELECT id,real_name,status FROM orp_account WHERE username=?", username).Scan(&id, &realName, &status); err != nil || status != "enabled" {
		writeAuthError(w, http.StatusUnauthorized, "用户不存在或已禁用")
		return
	}
	accountRoles, err := s.accountRoles(r, username)
	if err != nil {
		writeAuthError(w, 500, "读取角色失败")
		return
	}
	roleCodes := []string{}
	for _, role := range accountRoles {
		roleCodes = append(roleCodes, role.Code)
	}
	writeAuthData(w, http.StatusOK, localUserInfo{
		Avatar:   "",
		RealName: realName,
		Roles:    roleCodes,
		UserID:   strconv.FormatInt(id, 10),
		Username: username,
		Desc:     "本地控制面用户",
		HomePath: "/dashboard",
		Token:    localToken(username),
	})
}

func writeAuthData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"code": 0, "data": data, "message": "ok"})
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"code": status, "data": nil, "message": message})
}

func localToken(username string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(username + ":" + strconv.FormatInt(time.Now().Add(8*time.Hour).Unix(), 10)))
	mac := hmac.New(sha256.New, tokenKey)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func usernameFromToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ""
	}
	mac := hmac.New(sha256.New, tokenKey)
	mac.Write([]byte(parts[0]))
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac.Sum(nil), provided) {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	claims := strings.SplitN(string(payload), ":", 2)
	if len(claims) != 2 || strings.TrimSpace(claims[0]) == "" {
		return ""
	}
	expiry, err := strconv.ParseInt(claims[1], 10, 64)
	if err != nil || time.Now().Unix() >= expiry {
		return ""
	}
	if expiry, revoked := revokedTokens.Load(token); revoked {
		if time.Now().Before(expiry.(time.Time)) {
			return ""
		}
		revokedTokens.Delete(token)
	}
	return claims[0]
}

type accountRole struct {
	Code        string
	Permissions []string
	CenterIDs   []string
}

func normalizePermissionKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if key == "orp:write" {
			key = "orp:config:write"
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}

func (s *Server) accountRoles(r *http.Request, username string) ([]accountRole, error) {
	var roleIDsJSON []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT role_ids FROM orp_account WHERE username=? AND status='enabled'", username).Scan(&roleIDsJSON); err != nil {
		return nil, err
	}
	var ids []int64
	if err := json.Unmarshal(roleIDsJSON, &ids); err != nil {
		return nil, err
	}
	result := []accountRole{}
	for _, id := range ids {
		var code string
		var raw, centerIDsRaw []byte
		err := s.db.QueryRowContext(r.Context(), "SELECT code,permission_keys,center_ids FROM orp_role WHERE id=? AND status='enabled'", id).Scan(&code, &raw, &centerIDsRaw)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		var permissions []string
		if err := json.Unmarshal(raw, &permissions); err != nil {
			return nil, err
		}
		permissions = normalizePermissionKeys(permissions)
		var centerIDs []string
		if err := json.Unmarshal(centerIDsRaw, &centerIDs); err != nil {
			return nil, err
		}
		result = append(result, accountRole{Code: code, Permissions: permissions, CenterIDs: centerIDs})
	}
	return result, nil
}
