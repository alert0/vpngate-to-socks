package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"vpngate/internal/runnerclient"
	"vpngate/internal/web"
)

func main() {
	logger := log.New(os.Stdout, "[VPNGate] ", log.LstdFlags)
	runnerClient := runnerclient.New(runnerAPIURL(), nil)

	app, err := web.NewApp(logger, nil, runnerClient)
	if err != nil {
		logger.Fatalf("初始化页面服务失败：%v", err)
	}

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()

	if err := app.Refresh(startupCtx); err != nil {
		logger.Printf("启动时首次刷新失败，服务仍会继续启动：%v", err)
	}

	certFile, keyFile, tlsEnabled, err := webTLSConfig()
	if err != nil {
		logger.Fatalf("Web TLS 配置错误：%v", err)
	}

	rootMux := http.NewServeMux()
	rootMux.Handle("/settings/socks", web.NewSOCKSSettingsHandler(logger, runnerClient))
	rootMux.Handle("/", app.Routes())

	handler, err := web.NewAuthHandler(logger, rootMux, web.AuthConfig{
		Username:     envString("WEB_USERNAME", "admin"),
		Password:     os.Getenv("WEB_PASSWORD"),
		SessionTTL:   envDuration("WEB_SESSION_TTL", 12*time.Hour),
		SecureCookie: tlsEnabled,
	})
	if err != nil {
		logger.Fatalf("初始化 Web 登录认证失败：%v", err)
	}

	listenAddr := webListenAddr()
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if tlsEnabled {
			logger.Printf("Web 管理服务启动成功：https://%s", displayListenAddr(listenAddr))
			logger.Printf("SOCKS5 后台配置页面：https://%s/settings/socks", displayListenAddr(listenAddr))
			if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
				logger.Fatalf("启动 HTTPS 服务失败：%v", err)
			}
			return
		}

		logger.Printf("Web 管理服务启动成功：http://%s", displayListenAddr(listenAddr))
		logger.Printf("SOCKS5 后台配置页面：http://%s/settings/socks", displayListenAddr(listenAddr))
		logger.Printf("警告：当前未启用 HTTPS，公网访问时登录密码和 Session 可能被窃听；建议配置 WEB_TLS_CERT 与 WEB_TLS_KEY")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("启动 HTTP 服务失败：%v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Println("收到停止信号，正在关闭服务……")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Fatalf("关闭服务失败：%v", err)
	}

	logger.Println("服务已安全退出")
}

func webListenAddr() string {
	if value := strings.TrimSpace(os.Getenv("WEB_LISTEN_ADDR")); value != "" {
		return value
	}
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return ":" + port
	}
	return "0.0.0.0:5777"
}

func runnerAPIURL() string {
	if value := strings.TrimSpace(os.Getenv("RUNNER_API_URL")); value != "" {
		return value
	}
	return "http://127.0.0.1:18081"
}

func webTLSConfig() (certFile, keyFile string, enabled bool, err error) {
	certFile = strings.TrimSpace(os.Getenv("WEB_TLS_CERT"))
	keyFile = strings.TrimSpace(os.Getenv("WEB_TLS_KEY"))
	if certFile == "" && keyFile == "" {
		return "", "", false, nil
	}
	if certFile == "" || keyFile == "" {
		return "", "", false, fmt.Errorf("WEB_TLS_CERT 与 WEB_TLS_KEY 必须同时配置")
	}
	return certFile, keyFile, true, nil
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func displayListenAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}
