package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	webSessionCookieName = "vpngate_session"
	loginWindow          = time.Minute
	loginBlockDuration   = 10 * time.Minute
	maxLoginFailures     = 5
)

type AuthConfig struct {
	Username     string
	Password     string
	SessionTTL   time.Duration
	SecureCookie bool
}

type authHandler struct {
	logger       *log.Logger
	next         http.Handler
	usernameHash [sha256.Size]byte
	passwordHash [sha256.Size]byte
	sessionTTL   time.Duration
	secureCookie bool

	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string]loginAttempt
}

type loginAttempt struct {
	WindowStart time.Time
	Failures    int
	BlockedTill time.Time
}

type loginPageData struct {
	Error string
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>VPNGate 登录</title>
<style>
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f5f7fb;font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#111827}
.card{width:min(360px,calc(100% - 32px));background:#fff;border:1px solid #e5e7eb;border-radius:16px;padding:28px;box-shadow:0 12px 36px rgba(15,23,42,.08)}
h1{margin:0 0 8px;font-size:24px}.sub{margin:0 0 22px;color:#6b7280;font-size:14px}.field{margin:14px 0}.field label{display:block;margin-bottom:6px;font-size:13px;font-weight:600}.field input{box-sizing:border-box;width:100%;padding:11px 12px;border:1px solid #d1d5db;border-radius:9px;font-size:15px}.btn{width:100%;margin-top:8px;padding:11px 14px;border:0;border-radius:9px;background:#111827;color:#fff;font-size:15px;font-weight:700;cursor:pointer}.error{margin:0 0 14px;padding:10px 12px;border-radius:9px;background:#fef2f2;color:#b91c1c;font-size:13px}
</style>
</head>
<body>
<main class="card">
<h1>VPNGate 管理登录</h1>
<p class="sub">请输入管理账号后继续。</p>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="post" action="/login" autocomplete="on">
<div class="field"><label for="username">用户名</label><input id="username" name="username" type="text" autocomplete="username" required autofocus></div>
<div class="field"><label for="password">密码</label><input id="password" name="password" type="password" autocomplete="current-password" required></div>
<button class="btn" type="submit">登录</button>
</form>
</main>
</body>
</html>`))

func NewAuthHandler(logger *log.Logger, next http.Handler, cfg AuthConfig) (http.Handler, error) {
	if logger == nil {
		logger = log.Default()
	}
	if next == nil {
		return nil, fmt.Errorf("Web 认证缺少下游 Handler")
	}

	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		return nil, fmt.Errorf("WEB_USERNAME 不能为空")
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("WEB_PASSWORD 不能为空")
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}

	return &authHandler{
		logger:       logger,
		next:         next,
		usernameHash: sha256.Sum256([]byte(username)),
		passwordHash: sha256.Sum256([]byte(cfg.Password)),
		sessionTTL:   cfg.SessionTTL,
		secureCookie: cfg.SecureCookie,
		sessions:     make(map[string]time.Time),
		attempts:     make(map[string]loginAttempt),
	}, nil
}

func (a *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setWebSecurityHeaders(w)

	switch r.URL.Path {
	case "/health":
		a.next.ServeHTTP(w, r)
		return
	case "/login":
		a.handleLogin(w, r)
		return
	case "/logout":
		if !a.requireSession(w, r) {
			return
		}
		a.handleLogout(w, r)
		return
	default:
		if !a.requireSession(w, r) {
			return
		}
		a.next.ServeHTTP(w, r)
	}
}

func (a *authHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if a.hasValidSession(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		a.renderLogin(w, http.StatusOK, "")
		return
	case http.MethodPost:
		if err := validateSameOriginRequest(r); err != nil {
			a.renderLogin(w, http.StatusForbidden, "请求来源校验失败")
			return
		}
	default:
		http.Error(w, "仅支持 GET 或 POST 请求", http.StatusMethodNotAllowed)
		return
	}

	ip := requestIP(r)
	if retryAfter, blocked := a.loginBlocked(ip); blocked {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
		a.renderLogin(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderLogin(w, http.StatusBadRequest, "无法读取登录表单")
		return
	}

	if !a.credentialsMatch(r.FormValue("username"), r.FormValue("password")) {
		a.recordLoginFailure(ip)
		a.logger.Printf("Web 登录失败，来源：%s", ip)
		a.renderLogin(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	token, err := newSessionToken()
	if err != nil {
		a.logger.Printf("创建 Web Session 失败：%v", err)
		http.Error(w, "创建会话失败", http.StatusInternalServerError)
		return
	}

	a.resetLoginFailures(ip)
	expiresAt := time.Now().Add(a.sessionTTL)
	a.mu.Lock()
	a.pruneSessionsLocked(time.Now())
	a.sessions[token] = expiresAt
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(a.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   a.secureCookie || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	a.logger.Printf("Web 登录成功，来源：%s", ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *authHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	if err := validateSameOriginRequest(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if cookie, err := r.Cookie(webSessionCookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     webSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secureCookie || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *authHandler) requireSession(w http.ResponseWriter, r *http.Request) bool {
	if a.hasValidSession(r) {
		return true
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return false
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"ok":false,"error":"登录已失效，请重新登录"}`))
	return false
}

func (a *authHandler) hasValidSession(r *http.Request) bool {
	cookie, err := r.Cookie(webSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}

	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	expiresAt, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if !expiresAt.After(now) {
		delete(a.sessions, cookie.Value)
		return false
	}

	return true
}

func (a *authHandler) credentialsMatch(username, password string) bool {
	userHash := sha256.Sum256([]byte(strings.TrimSpace(username)))
	passHash := sha256.Sum256([]byte(password))
	userOK := subtle.ConstantTimeCompare(userHash[:], a.usernameHash[:])
	passOK := subtle.ConstantTimeCompare(passHash[:], a.passwordHash[:])
	return userOK&passOK == 1
}

func (a *authHandler) loginBlocked(ip string) (time.Duration, bool) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	attempt, ok := a.attempts[ip]
	if !ok || !attempt.BlockedTill.After(now) {
		if ok && !attempt.BlockedTill.IsZero() && !attempt.BlockedTill.After(now) {
			delete(a.attempts, ip)
		}
		return 0, false
	}
	return time.Until(attempt.BlockedTill), true
}

func (a *authHandler) recordLoginFailure(ip string) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	attempt := a.attempts[ip]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) > loginWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Failures++
	if attempt.Failures >= maxLoginFailures {
		attempt.BlockedTill = now.Add(loginBlockDuration)
	}
	a.attempts[ip] = attempt
}

func (a *authHandler) resetLoginFailures(ip string) {
	a.mu.Lock()
	delete(a.attempts, ip)
	a.mu.Unlock()
}

func (a *authHandler) pruneSessionsLocked(now time.Time) {
	for token, expiresAt := range a.sessions {
		if !expiresAt.After(now) {
			delete(a.sessions, token)
		}
	}
}

func (a *authHandler) renderLogin(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := loginTemplate.Execute(w, loginPageData{Error: message}); err != nil {
		a.logger.Printf("渲染登录页面失败：%v", err)
	}
}

func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}

func setWebSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}
