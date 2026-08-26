package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SOCKSConfig is safe to expose through the local Runner API. It never returns
// the configured password, only whether a password exists.
type SOCKSConfig struct {
	ListenAddr         string `json:"listenAddr"`
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"passwordConfigured"`
}

// SOCKSConfigUpdate is accepted from the authenticated Web admin page.
// An empty Password means "keep the current password".
type SOCKSConfigUpdate struct {
	ListenAddr string `json:"listenAddr"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
}

type storedSOCKSConfig struct {
	ListenAddr string `json:"listenAddr"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

func loadInitialSOCKSConfig(defaultListenAddr string) (storedSOCKSConfig, string, error) {
	configPath := socksConfigPath()
	data, err := os.ReadFile(configPath)
	if err == nil {
		var stored storedSOCKSConfig
		if err := json.Unmarshal(data, &stored); err != nil {
			return storedSOCKSConfig{}, configPath, fmt.Errorf("解析 SOCKS5 配置文件失败: %w", err)
		}
		stored.ListenAddr = normalizeSOCKSListenAddr(stored.ListenAddr, defaultListenAddr)
		stored.Username = strings.TrimSpace(stored.Username)
		if err := validateStoredSOCKSConfig(stored, true); err != nil {
			return storedSOCKSConfig{}, configPath, fmt.Errorf("SOCKS5 配置文件无效: %w", err)
		}
		return stored, configPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return storedSOCKSConfig{}, configPath, fmt.Errorf("读取 SOCKS5 配置文件失败: %w", err)
	}

	stored := storedSOCKSConfig{
		ListenAddr: normalizeSOCKSListenAddr(os.Getenv("SOCKS_LISTEN_ADDR"), defaultListenAddr),
		Username:   strings.TrimSpace(os.Getenv("SOCKS_USERNAME")),
		Password:   os.Getenv("SOCKS_PASSWORD"),
	}
	if err := validateStoredSOCKSConfig(stored, true); err != nil {
		return storedSOCKSConfig{}, configPath, fmt.Errorf("SOCKS5 环境变量配置无效: %w", err)
	}
	return stored, configPath, nil
}

func socksConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("SOCKS_CONFIG_FILE")); value != "" {
		return value
	}

	configDir, err := os.UserConfigDir()
	if err == nil && strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, "vpngate", "socks.json")
	}
	return filepath.Join(".vpngate", "socks.json")
}

func normalizeSOCKSListenAddr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return "0.0.0.0:1080"
}

func validateStoredSOCKSConfig(cfg storedSOCKSConfig, allowUnconfigured bool) error {
	if err := validateSOCKSListenAddr(cfg.ListenAddr); err != nil {
		return err
	}
	if len([]byte(cfg.Username)) > 255 {
		return fmt.Errorf("SOCKS5 用户名不能超过 255 字节")
	}
	if len([]byte(cfg.Password)) > 255 {
		return fmt.Errorf("SOCKS5 密码不能超过 255 字节")
	}

	usernameSet := strings.TrimSpace(cfg.Username) != ""
	passwordSet := cfg.Password != ""
	if usernameSet != passwordSet {
		return fmt.Errorf("SOCKS5 用户名和密码必须同时设置")
	}
	if !allowUnconfigured && !usernameSet {
		return fmt.Errorf("SOCKS5 用户名和密码不能为空")
	}
	return nil
}

func validateSOCKSListenAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("SOCKS5 监听地址不能为空")
	}
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("SOCKS5 监听地址格式无效，应类似 0.0.0.0:1080: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("SOCKS5 端口必须在 1-65535 之间")
	}
	return nil
}

func saveSOCKSConfig(path string, cfg storedSOCKSConfig) error {
	if err := validateStoredSOCKSConfig(cfg, false); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 SOCKS5 配置失败: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建 SOCKS5 配置目录失败: %w", err)
		}
	}

	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("创建 SOCKS5 临时配置文件失败: %w", err)
	}
	writeErr := error(nil)
	if _, err := file.Write(data); err != nil {
		writeErr = err
	}
	if err := file.Sync(); err != nil && writeErr == nil {
		writeErr = err
	}
	if err := file.Close(); err != nil && writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("写入 SOCKS5 配置失败: %w", writeErr)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("保存 SOCKS5 配置失败: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func publicSOCKSConfig(cfg storedSOCKSConfig) SOCKSConfig {
	return SOCKSConfig{
		ListenAddr:         cfg.ListenAddr,
		Username:           cfg.Username,
		PasswordConfigured: cfg.Password != "",
	}
}

func (r *Runner) SOCKSConfig() SOCKSConfig {
	if r == nil || r.socks == nil {
		return SOCKSConfig{}
	}
	return r.socks.Config()
}

func (r *Runner) UpdateSOCKSConfig(update SOCKSConfigUpdate) (SOCKSConfig, error) {
	if r == nil || r.socks == nil {
		return SOCKSConfig{}, fmt.Errorf("SOCKS5 服务未启动")
	}
	return r.socks.UpdateConfig(update)
}
