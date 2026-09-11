package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	authFilePath      = varRoot + "/auth.json"
	bootstrapAuthPath = varRoot + "/auth.bootstrap"
	sessionSecretPath = varRoot + "/session.key"
	sessionCookieName = "frpc_session"
	sessionTTL        = 12 * time.Hour
	pbkdf2Iter        = 120000
	pbkdf2KeyLen      = 32
	maxLoginFails     = 5
	loginLockDuration = 5 * time.Minute
)

type storedAuth struct {
	Username string `json:"username"`
	Salt     string `json:"salt"`
	Hash     string `json:"hash"`
	Iter     int    `json:"iter"`
}

type bootstrapAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]loginFail
}

type loginFail struct {
	count    int
	lockedAt time.Time
}

var (
	webAuth     storedAuth
	webAuthOK   bool
	sessionKey  []byte
	loginLimits = &loginLimiter{fails: map[string]loginFail{}}
)

func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	var out []byte
	u := make([]byte, 0, hashLen)
	for i := 1; i <= blocks; i++ {
		prf.Reset()
		prf.Write(salt)
		var ib [4]byte
		ib[0] = byte(i >> 24)
		ib[1] = byte(i >> 16)
		ib[2] = byte(i >> 8)
		ib[3] = byte(i)
		prf.Write(ib[:])
		u = prf.Sum(u[:0])
		t := append([]byte(nil), u...)
		for j := 2; j <= iter; j++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for k := range t {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

func hashPassword(password string, salt []byte, iter int) string {
	key := pbkdf2SHA256([]byte(password), salt, iter, pbkdf2KeyLen)
	return hex.EncodeToString(key)
}

func constantEqual(a, b string) bool {
	if len(a) != len(b) {
		subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func loadOrCreateSessionKey() ([]byte, error) {
	if body, err := os.ReadFile(sessionSecretPath); err == nil {
		key := strings.TrimSpace(string(body))
		if decoded, err := hex.DecodeString(key); err == nil && len(decoded) >= 32 {
			return decoded, nil
		}
		if len(body) >= 32 {
			return body[:32], nil
		}
	}
	key, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(varRoot, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(sessionSecretPath, []byte(hex.EncodeToString(key)+"\n"), 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func writeStoredAuth(auth storedAuth) error {
	if err := os.MkdirAll(varRoot, 0755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	tmp := authFilePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, authFilePath)
}

func consumeBootstrapAuth() error {
	body, err := os.ReadFile(bootstrapAuthPath)
	if err != nil {
		return err
	}
	var boot bootstrapAuth
	if err := json.Unmarshal(body, &boot); err != nil {
		return err
	}
	boot.Username = strings.TrimSpace(boot.Username)
	if boot.Username == "" || boot.Password == "" {
		return fmt.Errorf("bootstrap auth is empty")
	}
	salt, err := randomBytes(16)
	if err != nil {
		return err
	}
	auth := storedAuth{
		Username: boot.Username,
		Salt:     hex.EncodeToString(salt),
		Hash:     hashPassword(boot.Password, salt, pbkdf2Iter),
		Iter:     pbkdf2Iter,
	}
	if err := writeStoredAuth(auth); err != nil {
		return err
	}
	_ = os.Remove(bootstrapAuthPath)
	webAuth = auth
	webAuthOK = true
	log.Printf("web login account initialized for user %s", auth.Username)
	return nil
}

func loadStoredAuth() error {
	body, err := os.ReadFile(authFilePath)
	if err != nil {
		return err
	}
	var auth storedAuth
	if err := json.Unmarshal(body, &auth); err != nil {
		return err
	}
	if strings.TrimSpace(auth.Username) == "" || auth.Salt == "" || auth.Hash == "" {
		return fmt.Errorf("auth file incomplete")
	}
	if auth.Iter < 10000 {
		auth.Iter = pbkdf2Iter
	}
	webAuth = auth
	webAuthOK = true
	return nil
}

func initWebAuth() error {
	key, err := loadOrCreateSessionKey()
	if err != nil {
		return err
	}
	sessionKey = key
	if fileExists(bootstrapAuthPath) {
		if err := consumeBootstrapAuth(); err != nil {
			log.Printf("bootstrap auth failed: %v", err)
		}
	}
	if !webAuthOK {
		if err := loadStoredAuth(); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	item, ok := l.fails[ip]
	if !ok {
		return false
	}
	if item.count >= maxLoginFails && time.Since(item.lockedAt) < loginLockDuration {
		return true
	}
	if item.count >= maxLoginFails && time.Since(item.lockedAt) >= loginLockDuration {
		delete(l.fails, ip)
	}
	return false
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	item := l.fails[ip]
	item.count++
	if item.count >= maxLoginFails {
		item.lockedAt = time.Now()
	}
	l.fails[ip] = item
}

func (l *loginLimiter) success(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

func verifyPassword(password string) bool {
	if !webAuthOK {
		return false
	}
	salt, err := hex.DecodeString(webAuth.Salt)
	if err != nil {
		return false
	}
	got := hashPassword(password, salt, webAuth.Iter)
	return constantEqual(got, webAuth.Hash)
}

func signSession(username string, exp int64, nonce string) string {
	msg := fmt.Sprintf("%s|%d|%s", username, exp, nonce)
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func encodeSession(username string) (string, error) {
	nonceBytes, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nonceBytes)
	exp := time.Now().Add(sessionTTL).Unix()
	sig := signSession(username, exp, nonce)
	raw := fmt.Sprintf("%s|%d|%s|%s", username, exp, nonce, sig)
	return base64.RawURLEncoding.EncodeToString([]byte(raw)), nil
}

func decodeSession(value string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return "", false
	}
	username := parts[0]
	exp, err := strconvParseInt(parts[1])
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	nonce := parts[2]
	sig := parts[3]
	expect := signSession(username, exp, nonce)
	if !hmac.Equal([]byte(sig), []byte(expect)) {
		return "", false
	}
	if webAuthOK && !constantEqual(username, webAuth.Username) {
		return "", false
	}
	return username, true
}

func strconvParseInt(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid int")
		}
		n = n*10 + int64(c-'0')
		if n < 0 {
			return 0, fmt.Errorf("overflow")
		}
	}
	return n, nil
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return decodeSession(c.Value)
}

func loginURL(next string) string {
	if next == "" || next == "/" {
		return "/login"
	}
	return "/login?next=" + pathEscape(next)
}

func pathEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "%20"), "&", "%26")
}

func safeNextPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || strings.Contains(raw, "..") {
		return "/"
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "/"
	}
	return raw
}

func requestHost(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = r.Header.Get("X-Forwarded-Host")
	}
	return strings.ToLower(strings.TrimSpace(host))
}

func sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	host := requestHost(r)
	if host == "" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		return originHost(origin) == host
	}
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer != "" {
		return originHost(referer) == host
	}
	return false
}

func originHost(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(raw)
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func publicAssetName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "\\", "/")
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimPrefix(raw, "/")
	switch raw {
	case "favicon.ico", "favicon.png", "logo.png", "icon.png",
		"statics/favicon.png", "statics/logo.png", "statics/icon.png":
		if strings.HasPrefix(raw, "statics/") {
			return raw, true
		}
		if raw == "favicon.ico" {
			return "statics/favicon.png", true
		}
		return "statics/" + raw, true
	default:
		return "", false
	}
}

func servePublicAsset(w http.ResponseWriter, r *http.Request) bool {
	name, ok := publicAssetName(r.URL.Path)
	if !ok {
		return false
	}
	body, err := readUIFile(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".ico":
		w.Header().Set("Content-Type", "image/x-icon")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(body)
	return true
}

func fallbackLoginPage() []byte {
	return []byte(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>登录 - frpc客户端</title><link rel="icon" type="image/png" href="/favicon.png"><style>body{font-family:"Microsoft YaHei",sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#eef4ff;margin:0}.card{width:100%;max-width:400px;padding:28px;border-radius:24px;background:#fff;box-shadow:0 22px 54px rgba(15,23,42,.14)}h1{margin:0 0 8px}p{color:#64748b}label{display:block;margin:12px 0 6px}input{width:100%;height:44px;border:1px solid #cbd5e1;border-radius:12px;padding:0 12px;box-sizing:border-box}button{width:100%;height:44px;margin-top:16px;border:0;border-radius:12px;background:#6366f1;color:#fff;font-weight:700}.err{display:none;color:#b91c1c;margin:8px 0}</style></head><body><form class="card" id="loginForm"><h1>frpc客户端</h1><p>请输入安装时设置的账号密码</p><div class="err" id="errBox"></div><label>用户名</label><input id="username" required><label>密码</label><input id="password" type="password" required><button type="submit">登录</button></form><script>document.getElementById("loginForm").addEventListener("submit",async e=>{e.preventDefault();const err=document.getElementById("errBox");err.style.display="none";try{const res=await fetch("/login",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json","Accept":"application/json"},body:JSON.stringify({username:document.getElementById("username").value.trim(),password:document.getElementById("password").value})});const data=await res.json().catch(()=>({}));if(!res.ok||!data.success)throw new Error(data.error||"登录失败");location.replace("/");}catch(x){err.textContent=x.message||"登录失败";err.style.display="block";}});</script></body></html>`)
}

func serveLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUser(r); ok {
		http.Redirect(w, r, safeNextPath(r.URL.Query().Get("next")), http.StatusFound)
		return
	}
	body, err := readUIFile("statics/login.html")
	if err != nil {
		body = fallbackLoginPage()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(body)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		serveLoginPage(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"success": false, "error": "非法来源"})
		return
	}
	ip := clientIP(r)
	if loginLimits.blocked(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{"success": false, "error": "尝试次数过多，请稍后再试"})
		return
	}
	var data struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "请求格式错误"})
			return
		}
	} else {
		_ = r.ParseForm()
		data.Username = r.FormValue("username")
		data.Password = r.FormValue("password")
	}
	data.Username = strings.TrimSpace(data.Username)
	if !webAuthOK {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"success": false, "error": "尚未设置登录账号，请重新运行安装脚本"})
		return
	}
	okUser := constantEqual(data.Username, webAuth.Username)
	okPass := verifyPassword(data.Password)
	if !okUser || !okPass {
		loginLimits.fail(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "用户名或密码错误"})
		return
	}
	token, err := encodeSession(webAuth.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "创建会话失败"})
		return
	}
	loginLimits.success(ip)
	setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	if r.Header.Get("Accept") == "application/json" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func authRequired(w http.ResponseWriter, r *http.Request) bool {
	if !webAuthOK {
		return true
	}
	if _, ok := currentUser(r); ok {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
		return true
	}
	if r.Header.Get("Accept") == "application/json" || r.URL.Query().Get("action") != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "未登录"})
		return false
	}
	http.Redirect(w, r, loginURL(r.URL.RequestURI()), http.StatusFound)
	return false
}

func safeStaticPath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" || strings.Contains(raw, "..") || strings.Contains(raw, "\x00") {
		return "", false
	}
	clean := path.Clean("/" + raw)
	if clean != "/statics" && !strings.HasPrefix(clean, "/statics/") {
		return "", false
	}
	return strings.TrimPrefix(clean, "/"), true
}
