package vpngate

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// IPhoneAPIURL is the VPN Gate endpoint that returns the iPhone/OpenVPN server list.
	IPhoneAPIURL = "https://www.vpngate.net/api/iphone/"
	mirrorSitesURL = "https://www.vpngate.net/en/sites.aspx"

	iphoneListMarker = "*vpn_servers"
	responseFooter   = "*"
	maxResponseBytes = 10 << 20
	maxMirrorBytes   = 1 << 20
	mirrorCacheTTL   = 30 * time.Minute
	upstreamTimeout  = 45 * time.Second
	unknownPing      = -1
)

var (
	mirrorURLPattern = regexp.MustCompile(`https?://(?:[0-9]{1,3}\.){3}[0-9]{1,3}:[0-9]{2,5}/en/?`)
	mirrorCache      struct {
		sync.Mutex
		expiresAt time.Time
		urls      []string
	}
)

var iphoneHeader = []string{
	"HostName",
	"IP",
	"Score",
	"Ping",
	"Speed",
	"CountryLong",
	"CountryShort",
	"NumVpnSessions",
	"Uptime",
	"TotalUsers",
	"TotalTraffic",
	"LogType",
	"Operator",
	"Message",
	"OpenVPN_ConfigData_Base64",
}

// Server describes a single VPN Gate server row from the iPhone API response.
type Server struct {
	HostName                string
	IP                      string
	Score                   int64
	Ping                    int
	Speed                   int64
	CountryLong             string
	CountryShort            string
	NumVPNSessions          int64
	Uptime                  int64
	TotalUsers              int64
	TotalTraffic            int64
	LogType                 string
	Operator                string
	Message                 string
	OpenVPNConfigDataBase64 string
}

// FetchIPhoneServers downloads and parses the VPN Gate iPhone API response.
func FetchIPhoneServers(ctx context.Context, client *http.Client) ([]Server, error) {
	if client == nil {
		client = http.DefaultClient
	}

	primaryCtx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	servers, primaryErr := fetchIPhoneServersFromURL(primaryCtx, client, IPhoneAPIURL)
	cancel()
	if primaryErr == nil {
		return servers, nil
	}

	mirrorListCtx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	mirrorURLs, mirrorListErr := fetchMirrorAPIURLs(mirrorListCtx, client)
	cancel()
	if mirrorListErr != nil {
		return nil, fmt.Errorf("主站请求失败，且获取 VPNGate 镜像列表失败: %w", mirrorListErr)
	}

	servers, mirrorErr := fetchFromMirrors(ctx, client, mirrorURLs)
	if mirrorErr != nil {
		return nil, fmt.Errorf("主站请求失败，尝试 %d 个 VPNGate 镜像后仍失败: %w", len(mirrorURLs), mirrorErr)
	}

	return servers, nil
}

func fetchFromMirrors(ctx context.Context, client *http.Client, mirrorURLs []string) ([]Server, error) {
	if len(mirrorURLs) == 0 {
		return nil, fmt.Errorf("没有可用的 VPNGate 镜像入口")
	}

	type result struct {
		url     string
		servers []Server
		err     error
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, len(mirrorURLs))
	for _, apiURL := range mirrorURLs {
		go func(apiURL string) {
			requestCtx, requestCancel := context.WithTimeout(attemptCtx, upstreamTimeout)
			servers, err := fetchIPhoneServersFromURL(requestCtx, client, apiURL)
			requestCancel()
			results <- result{url: apiURL, servers: servers, err: err}
		}(apiURL)
	}

	var lastErr error
	for range mirrorURLs {
		result := <-results
		if result.err == nil {
			return result.servers, nil
		}
		lastErr = fmt.Errorf("镜像 %s 请求失败: %w", result.url, result.err)
	}

	return nil, lastErr
}

func fetchIPhoneServersFromURL(ctx context.Context, client *http.Client, apiURL string) ([]Server, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	// Some VPNGate mirrors hang when they receive Go's default gzip request.
	// Ask for the plain response so the mirror can stream the API body normally.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取 iPhone API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("接口返回异常状态: %s", resp.Status)
	}

	return ParseIPhoneResponse(io.LimitReader(resp.Body, maxResponseBytes))
}

func fetchMirrorAPIURLs(ctx context.Context, client *http.Client) ([]string, error) {
	now := time.Now()

	mirrorCache.Lock()
	if now.Before(mirrorCache.expiresAt) {
		urls := append([]string(nil), mirrorCache.urls...)
		mirrorCache.Unlock()
		return urls, nil
	}
	mirrorCache.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mirrorSitesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 VPNGate 镜像列表请求失败: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取 VPNGate 镜像列表失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("镜像列表返回异常状态: %s", resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMirrorBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 VPNGate 镜像列表失败: %w", err)
	}

	urls := buildMirrorAPIURLs(string(raw))
	if len(urls) == 0 {
		return nil, fmt.Errorf("VPNGate 镜像列表中没有可用入口")
	}

	mirrorCache.Lock()
	mirrorCache.urls = append([]string(nil), urls...)
	mirrorCache.expiresAt = time.Now().Add(mirrorCacheTTL)
	mirrorCache.Unlock()

	return urls, nil
}

func buildMirrorAPIURLs(raw string) []string {
	matches := mirrorURLPattern.FindAllString(raw, -1)
	urls := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		base := strings.TrimSuffix(strings.TrimRight(match, "/"), "/en")
		apiURL := base + "/api/iphone/"
		if apiURL == IPhoneAPIURL {
			continue
		}
		if _, ok := seen[apiURL]; ok {
			continue
		}
		seen[apiURL] = struct{}{}
		urls = append(urls, apiURL)
	}

	return urls
}

// ParseIPhoneResponse parses the VPN Gate iPhone API response body into server records.
func ParseIPhoneResponse(r io.Reader) ([]Server, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	lines := splitNonEmptyLines(string(raw))
	if len(lines) < 3 {
		return nil, fmt.Errorf("响应内容过短")
	}

	if lines[0] != iphoneListMarker {
		return nil, fmt.Errorf("响应标记不正确: %q", lines[0])
	}

	headerLine := strings.TrimPrefix(lines[1], "#")
	if err := validateHeader(headerLine); err != nil {
		return nil, err
	}

	servers := make([]Server, 0, len(lines)-2)
	for i, line := range lines[2:] {
		if strings.TrimSpace(line) == responseFooter {
			break
		}

		record := strings.Split(line, ",")
		if len(record) != len(iphoneHeader) {
			return nil, fmt.Errorf("第 %d 行字段数量不正确: 实际 %d，期望 %d", i+1, len(record), len(iphoneHeader))
		}

		server, err := newServer(record)
		if err != nil {
			return nil, fmt.Errorf("解析第 %d 行失败: %w", i+1, err)
		}

		servers = append(servers, server)
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("响应中没有任何节点数据")
	}

	return servers, nil
}

// DecodeOpenVPNConfig decodes the base64-encoded OpenVPN configuration text.
func (s Server) DecodeOpenVPNConfig() (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(s.OpenVPNConfigDataBase64)
	if err != nil {
		return "", fmt.Errorf("解码 OpenVPN 配置失败: %w", err)
	}

	return string(decoded), nil
}

func splitNonEmptyLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	lines := strings.Split(raw, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		result = append(result, trimmed)
	}

	return result
}

func validateHeader(headerLine string) error {
	fields := strings.Split(headerLine, ",")
	if len(fields) != len(iphoneHeader) {
		return fmt.Errorf("表头字段数量不正确: 实际 %d，期望 %d", len(fields), len(iphoneHeader))
	}

	for i, field := range fields {
		if field != iphoneHeader[i] {
			return fmt.Errorf("表头第 %d 列不正确: 实际 %q，期望 %q", i, field, iphoneHeader[i])
		}
	}

	return nil
}

func newServer(record []string) (Server, error) {
	score, err := parseInt64Field("Score", record[2])
	if err != nil {
		return Server{}, err
	}

	ping, err := parsePingField(record[3])
	if err != nil {
		return Server{}, err
	}

	speed, err := parseInt64Field("Speed", record[4])
	if err != nil {
		return Server{}, err
	}

	numVPNSessions, err := parseInt64Field("NumVpnSessions", record[7])
	if err != nil {
		return Server{}, err
	}

	uptime, err := parseInt64Field("Uptime", record[8])
	if err != nil {
		return Server{}, err
	}

	totalUsers, err := parseInt64Field("TotalUsers", record[9])
	if err != nil {
		return Server{}, err
	}

	totalTraffic, err := parseInt64Field("TotalTraffic", record[10])
	if err != nil {
		return Server{}, err
	}

	return Server{
		HostName:                record[0],
		IP:                      record[1],
		Score:                   score,
		Ping:                    ping,
		Speed:                   speed,
		CountryLong:             record[5],
		CountryShort:            record[6],
		NumVPNSessions:          numVPNSessions,
		Uptime:                  uptime,
		TotalUsers:              totalUsers,
		TotalTraffic:            totalTraffic,
		LogType:                 record[11],
		Operator:                record[12],
		Message:                 record[13],
		OpenVPNConfigDataBase64: record[14],
	}, nil
}

func parseIntField(fieldName, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败: %w", fieldName, err)
	}

	return parsed, nil
}

func parsePingField(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "-" {
		return unknownPing, nil
	}

	return parseIntField("Ping", trimmed)
}

func parseInt64Field(fieldName, value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 %s 失败: %w", fieldName, err)
	}

	return parsed, nil
}
