package web

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vpngate/internal/runner"
)

type SOCKSSettingsControl interface {
	Enabled() bool
	SOCKSConfig(ctx context.Context) (runner.SOCKSConfig, error)
	UpdateSOCKSConfig(ctx context.Context, update runner.SOCKSConfigUpdate) (runner.SOCKSConfig, error)
}

type socksSettingsHandler struct {
	logger  *log.Logger
	control SOCKSSettingsControl
}

type socksSettingsPageData struct {
	ListenAddr         string
	Username           string
	PasswordConfigured bool
	Notice             string
	Error              string
}

var socksSettingsTemplate = template.Must(template.New("socks-settings").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>SOCKS5 设置 · VPNGate</title>
<style>
:root{color-scheme:light;--bg:#f8fafc;--surface:#fff;--soft:#f1f5f9;--text:#0f172a;--muted:#64748b;--border:#e2e8f0;--primary:#2563eb;--danger:#b91c1c;--success:#047857}
*{box-sizing:border-box}body{margin:0;background:linear-gradient(180deg,#eff6ff 0%,var(--bg) 260px);font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:var(--text)}
.page{max-width:760px;margin:0 auto;padding:36px 18px 56px}.card{background:var(--surface);border:1px solid var(--border);border-radius:18px;padding:28px;box-shadow:0 14px 36px rgba(15,23,42,.08)}
h1{margin:0 0 8px;font-size:28px}.sub{margin:0 0 24px;color:var(--muted);line-height:1.7}.grid{display:grid;gap:18px}.field label{display:block;margin-bottom:7px;font-size:13px;font-weight:700}.field input{width:100%;min-height:46px;border:1px solid var(--border);border-radius:11px;padding:0 13px;background:var(--soft);font-size:15px;outline:none}.field input:focus{border-color:var(--primary);box-shadow:0 0 0 4px rgba(37,99,235,.1);background:#fff}.hint{margin-top:7px;color:var(--muted);font-size:12px;line-height:1.6}.status{display:inline-flex;margin-top:7px;padding:5px 9px;border-radius:999px;font-size:12px;font-weight:700}.ok{background:#d1fae5;color:var(--success)}.warn{background:#fef3c7;color:#b45309}.actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:8px}.btn{display:inline-flex;align-items:center;justify-content:center;min-height:44px;padding:0 17px;border:0;border-radius:11px;font-size:14px;font-weight:700;cursor:pointer;text-decoration:none}.primary{background:var(--primary);color:#fff}.secondary{background:var(--soft);color:var(--text);border:1px solid var(--border)}.notice,.error{margin:0 0 18px;padding:12px 14px;border-radius:11px;font-size:14px}.notice{background:#dbeafe;color:#1d4ed8}.error{background:#fee2e2;color:var(--danger)}.security{margin-top:22px;padding:15px;border-radius:12px;background:var(--soft);color:var(--muted);font-size:13px;line-height:1.7}code{background:#e2e8f0;padding:2px 5px;border-radius:5px}@media(max-width:640px){.card{padding:22px}.actions{display:grid}.btn{width:100%}}
</style>
</head>
<body>
<main class="page">
<section class="card">
<h1>SOCKS5 设置</h1>
<p class="sub">这里修改的是 Runner 当前实际使用的 SOCKS5 监听地址、端口和公网认证信息。保存成功后立即生效，无需重启 Runner。</p>
{{if .Notice}}<div class="notice">{{.Notice}}</div>{{end}}
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="post" action="/settings/socks" autocomplete="off">
<div class="grid">
<div class="field">
<label for="listen_addr">监听地址 / 端口</label>
<input id="listen_addr" name="listen_addr" type="text" value="{{.ListenAddr}}" placeholder="0.0.0.0:1080" required>
<div class="hint">需要外网访问时通常使用 <code>0.0.0.0:1080</code>。修改端口时，程序会先确认新端口可以绑定，再切换过去。</div>
</div>
<div class="field">
<label for="username">SOCKS5 用户名</label>
<input id="username" name="username" type="text" value="{{.Username}}" maxlength="255" autocomplete="off" required>
</div>
<div class="field">
<label for="password">SOCKS5 密码</label>
<input id="password" name="password" type="password" maxlength="255" autocomplete="new-password" placeholder="留空表示保持当前密码">
{{if .PasswordConfigured}}<span class="status ok">当前密码已设置</span>{{else}}<span class="status warn">当前还没有设置公网密码</span>{{end}}
<div class="hint">密码不会在后台页面回显。首次配置必须填写密码；以后只改端口或用户名时可以留空。</div>
</div>
<div class="actions">
<button class="btn primary" type="submit">保存并立即生效</button>
<a class="btn secondary" href="/">返回节点管理</a>
</div>
</div>
</form>
<div class="security">安全说明：非本机 SOCKS5 客户端必须使用用户名和密码认证；Runner 自己从 <code>127.0.0.1</code> 发起的健康检查仍允许无认证。配置会保存到 Runner 的本地配置文件，文件权限会尽量设置为仅当前系统用户可读写。</div>
</section>
</main>
</body>
</html>`))

func NewSOCKSSettingsHandler(logger *log.Logger, control SOCKSSettingsControl) http.Handler {
	if logger == nil {
		logger = log.Default()
	}
	return &socksSettingsHandler{logger: logger, control: control}
}

func (h *socksSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/settings/socks" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.renderCurrent(w, r, http.StatusOK, r.URL.Query().Get("notice"), r.URL.Query().Get("error"))
	case http.MethodPost:
		h.handleUpdate(w, r)
	default:
		http.Error(w, "仅支持 GET 或 POST 请求", http.StatusMethodNotAllowed)
	}
}

func (h *socksSettingsHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if err := validateSameOriginRequest(r); err != nil {
		h.renderCurrent(w, r, http.StatusForbidden, "", err.Error())
		return
	}
	if h.control == nil || !h.control.Enabled() {
		h.renderCurrent(w, r, http.StatusServiceUnavailable, "", "VPN Runner 未配置，无法修改 SOCKS5 设置")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		h.renderCurrent(w, r, http.StatusBadRequest, "", "读取 SOCKS5 设置表单失败")
		return
	}

	update := runner.SOCKSConfigUpdate{
		ListenAddr: strings.TrimSpace(r.FormValue("listen_addr")),
		Username:   strings.TrimSpace(r.FormValue("username")),
		Password:   r.FormValue("password"),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	config, err := h.control.UpdateSOCKSConfig(ctx, update)
	if err != nil {
		h.logger.Printf("Web 更新 SOCKS5 配置失败：%v", err)
		h.renderWithConfig(w, http.StatusBadRequest, config, "", err.Error())
		return
	}

	h.logger.Printf("Web 已更新 SOCKS5 配置：监听=%s 用户=%s", config.ListenAddr, config.Username)
	values := url.Values{}
	values.Set("notice", fmt.Sprintf("SOCKS5 配置已保存并立即生效：%s", config.ListenAddr))
	http.Redirect(w, r, "/settings/socks?"+values.Encode(), http.StatusSeeOther)
}

func (h *socksSettingsHandler) renderCurrent(w http.ResponseWriter, r *http.Request, status int, notice, message string) {
	if h.control == nil || !h.control.Enabled() {
		h.renderWithConfig(w, status, runner.SOCKSConfig{}, notice, firstNonEmpty(message, "VPN Runner 未配置"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	config, err := h.control.SOCKSConfig(ctx)
	if err != nil {
		message = firstNonEmpty(message, fmt.Sprintf("读取 SOCKS5 配置失败：%v", err))
	}
	h.renderWithConfig(w, status, config, notice, message)
}

func (h *socksSettingsHandler) renderWithConfig(w http.ResponseWriter, status int, config runner.SOCKSConfig, notice, message string) {
	if strings.TrimSpace(config.ListenAddr) == "" {
		config.ListenAddr = "0.0.0.0:1080"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := socksSettingsTemplate.Execute(w, socksSettingsPageData{
		ListenAddr:         config.ListenAddr,
		Username:           config.Username,
		PasswordConfigured: config.PasswordConfigured,
		Notice:             strings.TrimSpace(notice),
		Error:              strings.TrimSpace(message),
	}); err != nil {
		h.logger.Printf("渲染 SOCKS5 设置页面失败：%v", err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
