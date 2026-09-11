package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	ServerAddr           string   `json:"serverAddr"`
	ServerPort         int      `json:"serverPort"`
	AuthToken          string   `json:"authToken"`
	Protocol           string   `json:"protocol"`
	WireProtocolEnabled bool     `json:"wireProtocolEnabled"`
	WireProtocol       string   `json:"wireProtocol"`
	NatHoleStunServer string   `json:"natHoleStunServer"`
	TcpMux             bool     `json:"tcpMux"`
	HeartbeatInterval    int      `json:"heartbeatInterval"`
	HeartbeatTimeout     int      `json:"heartbeatTimeout"`
	TcpMuxKeepaliveInterval int `json:"tcpMuxKeepaliveInterval"`
	LogTo              string   `json:"logTo"`
	LogLevel           string   `json:"logLevel"`
	LogMaxDays         int      `json:"logMaxDays"`
	Enabled            bool     `json:"enabled"`
	Proxies            []Proxy  `json:"proxies"`
}

type Proxy struct {
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	LocalIP         string                 `json:"localIP"`
	LocalPort       int                    `json:"localPort"`
	RemotePort      int                    `json:"remotePort,omitempty"`
	CustomDomain    string                 `json:"customDomain,omitempty"`
	CustomDomains   []string               `json:"customDomains,omitempty"`
	SubDomain       string                 `json:"subDomain,omitempty"`
	Subdomain       string                 `json:"subdomain,omitempty"`
	Locations       []string               `json:"locations,omitempty"`
	SecretKey       string                 `json:"secretKey,omitempty"`
	Secret_key      string                 `json:"secret_key,omitempty"`
	HttpUser        string                 `json:"httpUser,omitempty"`
	HttpPassword    string                 `json:"httpPassword,omitempty"`
	UseEncryption   bool                   `json:"useEncryption,omitempty"`
	UseCompression  bool                   `json:"useCompression,omitempty"`
	BandwidthLimit  string                 `json:"bandwidthLimit,omitempty"`
	Enabled         bool                   `json:"enabled,omitempty"`
	BandwidthLimitMode string              `json:"bandwidthLimitMode,omitempty"`
	LoadBalancer    struct {
		Group    string `json:"group,omitempty"`
		GroupKey string `json:"groupKey,omitempty"`
	} `json:"loadBalancer,omitempty"`
	HealthCheck struct {
		Type              string `json:"type,omitempty"`
		TimeoutSeconds    int    `json:"timeoutSeconds,omitempty"`
		MaxFailed         int    `json:"maxFailed,omitempty"`
		IntervalSeconds   int    `json:"intervalSeconds,omitempty"`
		Path              string `json:"path,omitempty"`
		HttpHeaders       []struct {
			Name  string `json:"name,omitempty"`
			Value string `json:"value,omitempty"`
		} `json:"httpHeaders,omitempty"`
	} `json:"healthCheck,omitempty"`
	RouteByHTTPUser string `json:"routeByHTTPUser,omitempty"`
	HostHeaderRewrite string `json:"hostHeaderRewrite,omitempty"`
	RequestHeaders  struct {
		Set map[string]string `json:"set,omitempty"`
	} `json:"requestHeaders,omitempty"`
	ResponseHeaders struct {
		Set map[string]string `json:"set,omitempty"`
	} `json:"responseHeaders,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Plugin      struct {
		Type                     string `json:"type,omitempty"`
		UnixPath                 string `json:"unixPath,omitempty"`
		HttpUser                 string `json:"httpUser,omitempty"`
		HttpPassword             string `json:"httpPassword,omitempty"`
		LocalPath                string `json:"localPath,omitempty"`
		StripPrefix              string `json:"stripPrefix,omitempty"`
		LocalAddr                string `json:"localAddr,omitempty"`
		CrtPath                  string `json:"crtPath,omitempty"`
		KeyPath                  string `json:"keyPath,omitempty"`
		TransportProxyProtocolVersion string `json:"proxyProtocolVersion,omitempty"`
		EnableHTTP2            bool `json:"enableHTTP2,omitempty"`
		Username               string `json:"username,omitempty"`
		Password               string `json:"password,omitempty"`
	} `json:"plugin,omitempty"`
	Multiplexer      string `json:"multiplexer,omitempty"`
	NatTraversal     struct {
		DisableAssistedAddrs bool `json:"disableAssistedAddrs,omitempty"`
	} `json:"natTraversal,omitempty"`
	AllowUsers        []string `json:"allowUsers,omitempty"`
	ServerName        string   `json:"serverName,omitempty"`
	ServerUser        string   `json:"serverUser,omitempty"`
	BindAddr          string   `json:"bindAddr,omitempty"`
	BindPort          int      `json:"bindPort,omitempty"`
	KeepTunnelOpen    bool     `json:"keepTunnelOpen,omitempty"`
	MaxRetriesAnHour  int      `json:"maxRetriesAnHour,omitempty"`
	MinRetryInterval  int      `json:"minRetryInterval,omitempty"`
	FallbackTo        string   `json:"fallbackTo,omitempty"`
	FallbackTimeoutMs int      `json:"fallbackTimeoutMs,omitempty"`
	DestinationIP     string   `json:"destinationIP,omitempty"`
}

type InstanceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	HasConfig bool   `json:"hasConfig"`
	IsDefault bool   `json:"isDefault"`
}

type InstanceMeta struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

const (
	appRoot                     = "/var/apps/frpc"
	varRoot                     = appRoot + "/var"
	configRoot                  = appRoot + "/shares/frpc"
	legacyConfigPath            = varRoot + "/config/frpc.toml"
	legacyConfigBackupPath      = varRoot + "/config/frpc.toml.bak"
	legacyLogPath               = varRoot + "/frpc.log"
	legacyPIDPath               = varRoot + "/frpc.pid"
	runtimeInstancesRoot        = varRoot + "/instances"
	managerLockDir              = varRoot + "/manager.lock"
	managerLockOwnerPath        = managerLockDir + "/pid"
	defaultInstanceID           = "default"
	defaultInstanceConfigDir    = configRoot + "/" + defaultInstanceID
	defaultInstanceRuntimeDir   = runtimeInstancesRoot + "/" + defaultInstanceID
	defaultInstanceConfigPath   = defaultInstanceConfigDir + "/frpc.toml"
	defaultInstanceLogPath      = defaultInstanceRuntimeDir + "/frpc.log"
	defaultInstancePIDPath      = defaultInstanceRuntimeDir + "/frpc.pid"
	managerScriptPath           = appRoot + "/target/ui/restart.sh"
	uiLogPath                   = varRoot + "/ui.log"
	mandatoryLoginFailExitComment = "# 不能删除, 否则连接失败会直接退出"
	mandatoryWebServerComment     = "# 热重载配置，不能删除"
)

var instanceIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func managerLockActive() bool {
	info, err := os.Stat(managerLockDir)
	if err != nil || !info.IsDir() {
		return false
	}
	data, err := os.ReadFile(managerLockOwnerPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	return processExists(pid)
}

func ensureDefaultInstanceLayout() error {
	if err := os.MkdirAll(defaultInstanceConfigDir, 0755); err != nil {
		return err
	}

	if fileExists(defaultInstanceConfigPath) {
		return nil
	}

	sourcePath := instanceLegacyConfigPath(defaultInstanceID)
	if !fileExists(sourcePath) {
		sourcePath = legacyConfigPath
	}

	body, err := os.ReadFile(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if err := os.WriteFile(defaultInstanceConfigPath, body, 0666); err != nil {
		return err
	}
	if sourcePath == legacyConfigPath && !fileExists(legacyConfigBackupPath) {
		_ = os.WriteFile(legacyConfigBackupPath, body, 0666)
	}
	if err := writeInstanceMeta(defaultInstanceID, readInstanceMeta(defaultInstanceID)); err != nil {
		return err
	}
	return nil
}

func normalizeInstanceID(raw string) string {
	id := strings.TrimSpace(strings.ToLower(raw))
	if id == "" {
		return defaultInstanceID
	}
	id = instanceIDSanitizer.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-_")
	if id == "" {
		return defaultInstanceID
	}
	return id
}

func instanceConfigDirPath(instanceID string) string {
	return filepath.Join(configRoot, normalizeInstanceID(instanceID))
}

func instanceRuntimeDirPath(instanceID string) string {
	return filepath.Join(runtimeInstancesRoot, normalizeInstanceID(instanceID))
}

func instanceLegacyConfigPath(instanceID string) string {
	return filepath.Join(instanceRuntimeDirPath(instanceID), "frpc.toml")
}

func instanceLegacyMetaPath(instanceID string) string {
	return filepath.Join(instanceRuntimeDirPath(instanceID), "meta.json")
}

func instanceConfigPath(instanceID string) string {
	return filepath.Join(instanceConfigDirPath(instanceID), "frpc.toml")
}

func instanceLogPath(instanceID string) string {
	return filepath.Join(instanceRuntimeDirPath(instanceID), "frpc.log")
}

func instancePIDPath(instanceID string) string {
	return filepath.Join(instanceRuntimeDirPath(instanceID), "frpc.pid")
}

func instanceMetaPath(instanceID string) string {
	return filepath.Join(instanceConfigDirPath(instanceID), "meta.json")
}

func ensureInstanceConfigDir(instanceID string) error {
	return os.MkdirAll(instanceConfigDirPath(instanceID), 0755)
}

func ensureInstanceRuntimeDir(instanceID string) error {
	return os.MkdirAll(instanceRuntimeDirPath(instanceID), 0755)
}

func defaultInstanceMeta(instanceID string) InstanceMeta {
	instanceID = normalizeInstanceID(instanceID)
	return InstanceMeta{
		Name:    instanceID,
		Enabled: true,
	}
}

func readInstanceMeta(instanceID string) InstanceMeta {
	instanceID = normalizeInstanceID(instanceID)
	meta := defaultInstanceMeta(instanceID)
	metaPath := instanceMetaPath(instanceID)
	if !fileExists(metaPath) {
		metaPath = instanceLegacyMetaPath(instanceID)
	}
	body, err := os.ReadFile(metaPath)
	if err != nil {
		return meta
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return defaultInstanceMeta(instanceID)
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = instanceID
	}
	return meta
}

func writeInstanceMeta(instanceID string, meta InstanceMeta) error {
	instanceID = normalizeInstanceID(instanceID)
	if err := ensureInstanceConfigDir(instanceID); err != nil {
		return err
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = instanceID
	}
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(instanceMetaPath(instanceID), body, 0666)
}

func isInstanceEnabled(instanceID string) bool {
	instanceID = normalizeInstanceID(instanceID)
	return readInstanceMeta(instanceID).Enabled
}

func instanceConfigReadPath(instanceID string) string {
	instanceID = normalizeInstanceID(instanceID)
	configPath := instanceConfigPath(instanceID)
	if fileExists(configPath) {
		return configPath
	}
	legacyInstanceConfigPath := instanceLegacyConfigPath(instanceID)
	if fileExists(legacyInstanceConfigPath) {
		return legacyInstanceConfigPath
	}
	if instanceID == defaultInstanceID && fileExists(legacyConfigPath) {
		return legacyConfigPath
	}
	return configPath
}

func instanceConfigWritePath(instanceID string) string {
	instanceID = normalizeInstanceID(instanceID)
	if instanceID == defaultInstanceID {
		if err := ensureDefaultInstanceLayout(); err != nil {
			log.Printf("ensureDefaultInstanceLayout failed: %v", err)
		}
	} else if err := ensureInstanceConfigDir(instanceID); err != nil {
		log.Printf("ensureInstanceConfigDir failed for %s: %v", instanceID, err)
	}
	return instanceConfigPath(instanceID)
}

func instanceLogReadPath(instanceID string) string {
	instanceID = normalizeInstanceID(instanceID)
	logPath := instanceLogPath(instanceID)
	pidPath := instancePIDPath(instanceID)
	if fileExists(logPath) || fileExists(pidPath) {
		return logPath
	}
	if instanceID == defaultInstanceID && (fileExists(legacyLogPath) || fileExists(legacyPIDPath)) {
		return legacyLogPath
	}
	return logPath
}

func instancePIDReadPath(instanceID string) string {
	instanceID = normalizeInstanceID(instanceID)
	pidPath := instancePIDPath(instanceID)
	if fileExists(pidPath) {
		return pidPath
	}
	if instanceID == defaultInstanceID && fileExists(legacyPIDPath) {
		return legacyPIDPath
	}
	return pidPath
}

func isLegacyInstanceDir(entry os.DirEntry, root string) bool {
	if !entry.IsDir() {
		return false
	}
	basePath := filepath.Join(root, entry.Name())
	return fileExists(filepath.Join(basePath, "frpc.toml")) || fileExists(filepath.Join(basePath, "meta.json"))
}

func knownInstanceIDs() []string {
	ids := make(map[string]struct{})

	if err := os.MkdirAll(configRoot, 0755); err == nil {
		if entries, err := os.ReadDir(configRoot); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				ids[normalizeInstanceID(entry.Name())] = struct{}{}
			}
		}
	}

	if err := os.MkdirAll(runtimeInstancesRoot, 0755); err == nil {
		if entries, err := os.ReadDir(runtimeInstancesRoot); err == nil {
			for _, entry := range entries {
				if !isLegacyInstanceDir(entry, runtimeInstancesRoot) {
					continue
				}
				ids[normalizeInstanceID(entry.Name())] = struct{}{}
			}
		}
	}

	instanceIDs := make([]string, 0, len(ids))
	for id := range ids {
		instanceIDs = append(instanceIDs, id)
	}
	sort.Strings(instanceIDs)
	return instanceIDs
}

func instanceExists(instanceID string) bool {
	instanceID = normalizeInstanceID(instanceID)
	if _, err := os.Stat(instanceConfigDirPath(instanceID)); err == nil {
		return true
	}
	return fileExists(instanceLegacyConfigPath(instanceID)) || fileExists(instanceLegacyMetaPath(instanceID))
}

func instanceHasPersistedConfig(instanceID string) bool {
	instanceID = normalizeInstanceID(instanceID)
	if fileExists(instanceConfigPath(instanceID)) || fileExists(instanceLegacyConfigPath(instanceID)) {
		return true
	}
	return instanceID == defaultInstanceID && fileExists(legacyConfigPath)
}

func instanceHasRuntimeArtifacts(instanceID string) bool {
	instanceID = normalizeInstanceID(instanceID)
	if fileExists(instanceLogPath(instanceID)) || fileExists(instancePIDPath(instanceID)) {
		return true
	}
	return instanceID == defaultInstanceID && (fileExists(legacyLogPath) || fileExists(legacyPIDPath))
}

func listInstances() ([]InstanceSummary, error) {
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(runtimeInstancesRoot, 0755); err != nil {
		return nil, err
	}

	instanceIDs := knownInstanceIDs()
	instances := make([]InstanceSummary, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		meta := readInstanceMeta(id)
		instances = append(instances, InstanceSummary{
			ID:        id,
			Name:      meta.Name,
			Enabled:   meta.Enabled,
			HasConfig: fileExists(instanceConfigReadPath(id)),
			IsDefault: id == defaultInstanceID,
		})
	}

	if len(instances) == 0 {
		instances = append(instances, InstanceSummary{
			ID:        defaultInstanceID,
			Name:      defaultInstanceID,
			Enabled:   true,
			HasConfig: fileExists(currentDefaultConfigReadPath()),
			IsDefault: true,
		})
	}

	sort.Slice(instances, func(i, j int) bool {
		if instances[i].IsDefault != instances[j].IsDefault {
			return instances[i].IsDefault
		}
		return instances[i].ID < instances[j].ID
	})
	return instances, nil
}

func resolveInstanceID(r *http.Request) string {
	instanceID := normalizeInstanceID(r.URL.Query().Get("instanceId"))
	if instanceID == "" {
		return defaultInstanceID
	}
	return instanceID
}

func extractWebServerPort(config string) int {
	re := regexp.MustCompile(`webServer\.port\s*=\s*(\d+)`)
	matches := re.FindStringSubmatch(config)
	if len(matches) < 2 {
		return 0
	}
	port, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return port
}

func canListenLocalPort(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func instanceProcessRunning(instanceID string) bool {
	data, err := os.ReadFile(instancePIDReadPath(instanceID))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	return processExists(pid)
}

func canKeepWebServerPort(instanceID string, port int) bool {
	if canListenLocalPort(port) {
		return true
	}
	return instanceProcessRunning(instanceID)
}

func resolveInstanceWebServerPort(instanceID string) int {
	instanceID = normalizeInstanceID(instanceID)
	if body, err := os.ReadFile(instanceConfigReadPath(instanceID)); err == nil {
		if port := extractWebServerPort(string(body)); port > 0 && canKeepWebServerPort(instanceID, port) {
			return port
		}
	}

	used := map[int]bool{}
	for _, otherInstanceID := range knownInstanceIDs() {
		if otherInstanceID == instanceID {
			continue
		}
		body, readErr := os.ReadFile(instanceConfigReadPath(otherInstanceID))
		if readErr != nil {
			continue
		}
		if port := extractWebServerPort(string(body)); port > 0 {
			used[port] = true
		}
	}

	if instanceID == defaultInstanceID {
		if !used[7400] && canListenLocalPort(7400) {
			return 7400
		}
	}

	start := 7401
	for port := start; port <= 65535; port++ {
		if !used[port] && canListenLocalPort(port) {
			return port
		}
	}
	return 0
}

func currentDefaultConfigReadPath() string {
	if fileExists(defaultInstanceConfigPath) {
		return defaultInstanceConfigPath
	}
	if fileExists(instanceLegacyConfigPath(defaultInstanceID)) {
		return instanceLegacyConfigPath(defaultInstanceID)
	}
	if fileExists(legacyConfigPath) {
		return legacyConfigPath
	}
	return defaultInstanceConfigPath
}

func currentDefaultConfigWritePath() string {
	if err := ensureDefaultInstanceLayout(); err != nil {
		log.Printf("ensureDefaultInstanceLayout failed: %v", err)
	}
	return defaultInstanceConfigPath
}

func currentDefaultLogPath() string {
	if fileExists(defaultInstanceLogPath) || fileExists(defaultInstancePIDPath) {
		return defaultInstanceLogPath
	}
	if fileExists(legacyLogPath) || fileExists(legacyPIDPath) {
		return legacyLogPath
	}
	return legacyLogPath
}

func currentDefaultPIDPath() string {
	if fileExists(defaultInstancePIDPath) {
		return defaultInstancePIDPath
	}
	if fileExists(legacyPIDPath) {
		return legacyPIDPath
	}
	return legacyPIDPath
}

func startManagerCommand(args ...string) error {
	cmd := exec.Command("/bin/bash", append([]string{managerScriptPath}, args...)...)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()

	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func runManagerCommand(args ...string) error {
	cmd := exec.Command("/bin/bash", append([]string{managerScriptPath}, args...)...)
	return cmd.Run()
}

func resolveTargetRoot() string {
	if targetRoot := strings.TrimSpace(os.Getenv("FRPC_APPDEST")); targetRoot != "" {
		return targetRoot
	}
	if targetRoot := strings.TrimSpace(os.Getenv("TRIM_APPDEST")); targetRoot != "" {
		return targetRoot
	}
	return filepath.Join(appRoot, "target")
}

func resolveUISocketPath() string {
	if socketPath := strings.TrimSpace(os.Getenv("FRPC_UI_SOCKET")); socketPath != "" {
		return socketPath
	}
	return filepath.Join(resolveTargetRoot(), "app.sock")
}

func resolveUIListenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("FRPC_UI_LISTEN")); addr != "" {
		return addr
	}
	if addr := strings.TrimSpace(os.Getenv("FRPC_UI_ADDR")); addr != "" {
		return addr
	}
	return ":9999"
}

func useUnixSocketListener() bool {
	if strings.TrimSpace(os.Getenv("FRPC_UI_LISTEN")) != "" || strings.TrimSpace(os.Getenv("FRPC_UI_ADDR")) != "" {
		return false
	}
	return strings.TrimSpace(os.Getenv("FRPC_UI_SOCKET")) != ""
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func newUIServer() *http.Server {
	return &http.Server{Handler: recoverMiddleware(securityHeaders(http.HandlerFunc(handleRequest)))}
}

func watchUIShutdown(server *http.Server) (stop func()) {
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopCh
		_ = server.Close()
	}()
	return func() {
		signal.Stop(stopCh)
	}
}

func resolveUIBaseDir() string {
	if uiRoot := strings.TrimSpace(os.Getenv("FRPC_UI_ROOT")); uiRoot != "" {
		return uiRoot
	}
	if exePath, err := os.Executable(); err == nil {
		return filepath.Dir(exePath)
	}
	return "."
}

func readUIFile(relativePath string) ([]byte, error) {
	candidates := []string{
		filepath.Join(resolveUIBaseDir(), relativePath),
		relativePath,
	}
	for _, candidate := range candidates {
		body, err := os.ReadFile(candidate)
		if err == nil {
			return body, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("ui request panic: %v", rec)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func serveUIUnix(socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer func() {
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			log.Printf("remove ui socket failed: %v", err)
		}
	}()

	if err := os.Chmod(socketPath, 0666); err != nil {
		log.Printf("chmod ui socket failed: %v", err)
	}

	server := newUIServer()
	defer watchUIShutdown(server)()

	log.Printf("ui server listening on unix socket %s", socketPath)
	err = server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func serveUITCP(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	server := newUIServer()
	defer watchUIShutdown(server)()

	log.Printf("ui server listening on http://%s", addr)
	fmt.Printf("frpc Web UI: http://%s\n", addr)
	err = server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func serveUIServer() error {
	if useUnixSocketListener() {
		return serveUIUnix(resolveUISocketPath())
	}
	return serveUITCP(resolveUIListenAddr())
}

func main() {
	if err := os.MkdirAll(varRoot, 0755); err != nil {
		fmt.Println("Error preparing ui log directory:", err)
		os.Exit(1)
	}
	fh, err := os.OpenFile(uiLogPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, fh))
		defer fh.Close()
	} else {
		log.SetOutput(os.Stdout)
	}
	if err := initWebAuth(); err != nil {
		log.Printf("init web auth failed: %v", err)
		fmt.Println("Error initializing web auth:", err)
		os.Exit(1)
	}
	if err := serveUIServer(); err != nil {
		log.Printf("ui server exited with error: %v", err)
		fmt.Println("Error running UI server:", err)
		os.Exit(1)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/app/frpc" || r.URL.Path == "/app/frpc/" {
		target := "/"
		if r.URL.RawQuery != "" {
			target = "/?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	if r.URL.Path == "/login" {
		handleLogin(w, r)
		return
	}
	if r.URL.Path == "/logout" {
		handleLogout(w, r)
		return
	}
	if servePublicAsset(w, r) {
		return
	}
	if !authRequired(w, r) {
		return
	}

	action := r.URL.Query().Get("action")

	if action == "getConfig" {
		handleGetConfig(w, r)
		return
	}
	if action == "listInstances" {
		handleListInstances(w, r)
		return
	}
	if action == "createInstance" {
		handleCreateInstance(w, r)
		return
	}
	if action == "updateInstance" {
		handleUpdateInstance(w, r)
		return
	}
	if action == "deleteInstance" {
		handleDeleteInstance(w, r)
		return
	}
	if action == "getRawConfig" {
		handleGetRawConfig(w, r)
		return
	}
	if action == "getLogs" {
		handleGetLogs(w, r)
		return
	}
	if action == "clearLogs" {
		handleClearLogs(w, r)
		return
	}
	if action == "saveLogConfig" {
		handleSaveLogConfig(w, r)
		return
	}
	if action == "saveConfig" {
		handleSaveConfig(w, r)
		return
	}
	if action == "restart" {
		handleRestart(w, r)
		return
	}
	if action == "status" {
		handleStatus(w, r)
		return
	}
	if action == "ping" {
		handlePing(w, r)
		return
	}

	if r.Method == "POST" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		r.ParseForm()
		configContent := r.FormValue("configContent")

		if configContent != "" {
			if err := os.WriteFile(currentDefaultConfigWritePath(), []byte(configContent), 0666); err == nil {
				if isInstanceEnabled(defaultInstanceID) {
					err := startManagerCommand("restart-one", defaultInstanceID)
					if err != nil {
						log.Printf("cmd.Start() failed with %s\n", err)
					}
				} else if err := runManagerCommand("stop-one", defaultInstanceID); err != nil {
					log.Printf("stop disabled default instance failed: %v", err)
				}
			} else {
				log.Println(err)
			}
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	requestPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if safePath, ok := safeStaticPath(requestPath); ok {
		fileContent, err := readUIFile(safePath)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		mimeType := mime.TypeByExtension(filepath.Ext(safePath))
		if mimeType == "" {
			mimeType = "text/plain"
		}
		w.Header().Set("Content-Type", mimeType)
		w.Write(fileContent)
		return
	}

	fileContent, err := readUIFile("statics/index.html")
	if err != nil {
		http.Error(w, "File reading error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(fileContent)
}

func handleListInstances(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	instances, err := listInstances()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"instances": instances,
	})
}

func handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var data struct {
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	instanceID := normalizeInstanceID(data.InstanceID)
	if instanceID == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例名称不能为空"})
		return
	}
	if instanceID == defaultInstanceID {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "默认实例已存在"})
		return
	}

	if instanceExists(instanceID) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例已存在"})
		return
	}

	if err := ensureInstanceConfigDir(instanceID); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	meta := defaultInstanceMeta(instanceID)
	meta.Enabled = false
	if err := writeInstanceMeta(instanceID, meta); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"instanceId": instanceID,
	})
}

func handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var data struct {
		InstanceID string `json:"instanceId"`
		Name       string `json:"name"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	instanceID := normalizeInstanceID(data.InstanceID)
	if !instanceExists(instanceID) && instanceID != defaultInstanceID {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例不存在"})
		return
	}

	meta := readInstanceMeta(instanceID)
	if strings.TrimSpace(data.Name) != "" {
		meta.Name = strings.TrimSpace(data.Name)
	}
	if data.Enabled != nil {
		meta.Enabled = *data.Enabled
	}
	if err := writeInstanceMeta(instanceID, meta); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	message := ""
	applied := false
	if data.Enabled != nil {
		if *data.Enabled {
			if fileExists(instanceConfigReadPath(instanceID)) {
				if err := runManagerCommand("start-one", instanceID); err != nil {
					log.Printf("start enabled instance failed: %v", err)
					message = "实例已启用，但启动失败"
				} else {
					applied = true
					message = "实例已启用，已启动"
				}
			} else {
				message = "实例已启用，首次保存配置后会自动启动"
			}
		} else {
			if err := runManagerCommand("stop-one", instanceID); err != nil {
				log.Printf("stop disabled instance failed: %v", err)
				meta.Enabled = true
				if revertErr := writeInstanceMeta(instanceID, meta); revertErr != nil {
					log.Printf("revert enabled state failed for %s: %v", instanceID, revertErr)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"success": false,
						"meta":    meta,
						"applied": false,
						"error":   "实例停止失败，且恢复启用状态失败: " + revertErr.Error(),
					})
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"meta":    meta,
					"applied": false,
					"error":   "实例停止失败，已恢复为启用状态",
				})
				return
			}
			message = "实例已禁用，已停止运行"
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"meta":    meta,
		"applied": applied,
		"message": message,
	})
}

func handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var data struct {
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	instanceID := normalizeInstanceID(data.InstanceID)
	if instanceID == defaultInstanceID {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "默认实例不能删除"})
		return
	}

	if instanceHasPersistedConfig(instanceID) || instanceHasRuntimeArtifacts(instanceID) {
		status := buildInstanceStatus(instanceID)
		if loading, _ := status["loading"].(bool); loading {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例正在启动或重载中，请稍后再删除"})
			return
		}
		if managerLockActive() {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例操作进行中，请稍后再删除"})
			return
		}

		if err := runManagerCommand("stop-one", instanceID); err != nil {
			log.Printf("stop instance before delete failed: %v", err)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例停止失败，请稍后重试删除"})
			return
		}

		status = buildInstanceStatus(instanceID)
		if running, _ := status["running"].(bool); running {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例仍在运行中，请稍后再删除"})
			return
		}
		if loading, _ := status["loading"].(bool); loading {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例正在收尾处理中，请稍后再删除"})
			return
		}
	}

	if err := os.RemoveAll(instanceConfigDirPath(instanceID)); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if err := os.RemoveAll(instanceRuntimeDirPath(instanceID)); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	instanceID := resolveInstanceID(r)

	config := Config{
		ServerAddr:           "127.0.0.1",
		ServerPort:         7000,
		Protocol:           "tcp",
		WireProtocol:       "v1",
		TcpMux:             true,
		HeartbeatInterval:    30,
		HeartbeatTimeout:     90,
		TcpMuxKeepaliveInterval: 30,
		Enabled:            isInstanceEnabled(instanceID),
		Proxies:            []Proxy{},
	}

	if body, err := os.ReadFile(instanceConfigReadPath(instanceID)); err == nil {
		content := string(body)

		serverAddrRe := regexp.MustCompile(`serverAddr\s*=\s*"([^"]+)"`)
		if matches := serverAddrRe.FindStringSubmatch(content); len(matches) > 1 {
			config.ServerAddr = matches[1]
		}

		serverPortRe := regexp.MustCompile(`serverPort\s*=\s*(\d+)`)
		if matches := serverPortRe.FindStringSubmatch(content); len(matches) > 1 {
			if port, err := strconv.Atoi(matches[1]); err == nil {
				config.ServerPort = port
			}
		}

		authTokenRe := regexp.MustCompile(`auth\.token\s*=\s*"([^"]+)"`)
		if matches := authTokenRe.FindStringSubmatch(content); len(matches) > 1 {
			config.AuthToken = matches[1]
		}

		protocolRe := regexp.MustCompile(`transport\.protocol\s*=\s*"([^"]+)"`)
		if matches := protocolRe.FindStringSubmatch(content); len(matches) > 1 {
			config.Protocol = matches[1]
		}

		wireProtocolRe := regexp.MustCompile(`transport\.wireProtocol\s*=\s*"([^"]+)"`)
		if matches := wireProtocolRe.FindStringSubmatch(content); len(matches) > 1 {
			config.WireProtocolEnabled = true
			config.WireProtocol = strings.ToLower(strings.TrimSpace(matches[1]))
			if config.WireProtocol == "" {
				config.WireProtocol = "v1"
			}
		}

		natHoleRe := regexp.MustCompile(`natHoleStunServer\s*=\s*"([^"]+)"`)
		if matches := natHoleRe.FindStringSubmatch(content); len(matches) > 1 {
			config.NatHoleStunServer = matches[1]
		}

		tcpMuxRe := regexp.MustCompile(`transport\.tcpMux\s*=\s*(true|false)`)
		if matches := tcpMuxRe.FindStringSubmatch(content); len(matches) > 1 {
			config.TcpMux = matches[1] == "true"
		}

		heartbeatIntervalRe := regexp.MustCompile(`transport\.heartbeatInterval\s*=\s*(\d+)`)
		if matches := heartbeatIntervalRe.FindStringSubmatch(content); len(matches) > 1 {
			if val, err := strconv.Atoi(matches[1]); err == nil {
				config.HeartbeatInterval = val
			}
		}

		heartbeatTimeoutRe := regexp.MustCompile(`transport\.heartbeatTimeout\s*=\s*(\d+)`)
		if matches := heartbeatTimeoutRe.FindStringSubmatch(content); len(matches) > 1 {
			if val, err := strconv.Atoi(matches[1]); err == nil {
				config.HeartbeatTimeout = val
			}
		}

		tcpMuxKeepaliveIntervalRe := regexp.MustCompile(`transport\.tcpMuxKeepaliveInterval\s*=\s*(\d+)`)
		if matches := tcpMuxKeepaliveIntervalRe.FindStringSubmatch(content); len(matches) > 1 {
			if val, err := strconv.Atoi(matches[1]); err == nil {
				config.TcpMuxKeepaliveInterval = val
			}
		}

		logToRe := regexp.MustCompile(`to\s*=\s*"([^"]+)"`)
		if matches := logToRe.FindStringSubmatch(content); len(matches) > 1 {
			config.LogTo = matches[1]
		}

		logLevelRe := regexp.MustCompile(`level\s*=\s*"([^"]+)"`)
		if matches := logLevelRe.FindStringSubmatch(content); len(matches) > 1 {
			config.LogLevel = matches[1]
		}

		logMaxDaysRe := regexp.MustCompile(`maxDays\s*=\s*(\d+)`)
		if matches := logMaxDaysRe.FindStringSubmatch(content); len(matches) > 1 {
			if days, err := strconv.Atoi(matches[1]); err == nil {
				config.LogMaxDays = days
			}
		}

		proxyBlocks := parseProxyBlocks(content)
		for _, block := range proxyBlocks {
			proxy := Proxy{
				LocalIP: "127.0.0.1",
			}

			nameRe := regexp.MustCompile(`name\s*=\s*"([^"]+)"`)
			if matches := nameRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.Name = matches[1]
			}

			typeRe := regexp.MustCompile(`type\s*=\s*"([^"]+)"`)
			if matches := typeRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.Type = matches[1]
				// 兼容老用户的 p2p，自动转成 xtcp
				if proxy.Type == "p2p" {
					proxy.Type = "xtcp"
				}
			}

			localIPRe := regexp.MustCompile(`localIP\s*=\s*"([^"]+)"`)
			if matches := localIPRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.LocalIP = matches[1]
			}

			localPortRe := regexp.MustCompile(`localPort\s*=\s*(\d+)`)
			if matches := localPortRe.FindStringSubmatch(block); len(matches) > 1 {
				if port, err := strconv.Atoi(matches[1]); err == nil {
					proxy.LocalPort = port
				}
			}

			remotePortRe := regexp.MustCompile(`remotePort\s*=\s*(\d+)`)
			if matches := remotePortRe.FindStringSubmatch(block); len(matches) > 1 {
				if port, err := strconv.Atoi(matches[1]); err == nil {
					proxy.RemotePort = port
				}
			}

			customDomainRe := regexp.MustCompile(`customDomain\s*=\s*"([^"]+)"`)
			if matches := customDomainRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.CustomDomain = matches[1]
			}

			customDomainsRe := regexp.MustCompile(`customDomains\s*=\s*\[([^\]]+)\]`)
			if matches := customDomainsRe.FindStringSubmatch(block); len(matches) > 1 {
				domainStr := matches[1]
				// 同时支持双引号和单引号
				domainRe := regexp.MustCompile(`["']([^"']+)["']`)
				domainMatches := domainRe.FindAllStringSubmatch(domainStr, -1)
				for _, d := range domainMatches {
					if len(d) > 1 {
						// 如果单个域名里还有逗号，再分割一次
						for _, singleDomain := range strings.Split(d[1], ",") {
							singleDomain = strings.TrimSpace(singleDomain)
							if singleDomain != "" {
								proxy.CustomDomains = append(proxy.CustomDomains, singleDomain)
							}
						}
					}
				}
			}

			// 同时支持 subdomain（小写）和 subDomain（大写），兼容老用户
			subDomainRe := regexp.MustCompile(`subDomain\s*=\s*"([^"]+)"`)
			if matches := subDomainRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.SubDomain = matches[1]
			} else {
				subDomainRe2 := regexp.MustCompile(`subdomain\s*=\s*"([^"]+)"`)
				if matches := subDomainRe2.FindStringSubmatch(block); len(matches) > 1 {
					proxy.SubDomain = matches[1]
				}
			}

			locationsRe := regexp.MustCompile(`locations\s*=\s*\[([^\]]+)\]`)
			if matches := locationsRe.FindStringSubmatch(block); len(matches) > 1 {
				locStr := matches[1]
				// 同时支持双引号和单引号
				locRe := regexp.MustCompile(`["']([^"']+)["']`)
				locMatches := locRe.FindAllStringSubmatch(locStr, -1)
				for _, loc := range locMatches {
					if len(loc) > 1 {
						// 如果单个路径里还有逗号，再分割一次
						for _, singleLoc := range strings.Split(loc[1], ",") {
							singleLoc = strings.TrimSpace(singleLoc)
							if singleLoc != "" {
								proxy.Locations = append(proxy.Locations, singleLoc)
							}
						}
					}
				}
			}

			// 同时支持 secretKey 和 secret_key，兼容老用户
			secretKeyRe := regexp.MustCompile(`secret_key\s*=\s*"([^"]+)"`)
			if matches := secretKeyRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.SecretKey = matches[1]
			} else {
				secretKeyRe2 := regexp.MustCompile(`secretKey\s*=\s*"([^"]+)"`)
				if matches := secretKeyRe2.FindStringSubmatch(block); len(matches) > 1 {
					proxy.SecretKey = matches[1]
				}
			}

			httpUserRe := regexp.MustCompile(`httpUser\s*=\s*"([^"]+)"`)
			if matches := httpUserRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.HttpUser = matches[1]
			}

			httpPasswordRe := regexp.MustCompile(`httpPassword\s*=\s*"([^"]+)"`)
			if matches := httpPasswordRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.HttpPassword = matches[1]
			}

			useEncryptionRe := regexp.MustCompile(`useEncryption\s*=\s*(true|false)`)
			if matches := useEncryptionRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.UseEncryption = matches[1] == "true"
			}

			useCompressionRe := regexp.MustCompile(`useCompression\s*=\s*(true|false)`)
			if matches := useCompressionRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.UseCompression = matches[1] == "true"
			}

			bandwidthLimitRe := regexp.MustCompile(`bandwidthLimit\s*=\s*"([^"]+)"`)
			if matches := bandwidthLimitRe.FindStringSubmatch(block); len(matches) > 1 {
				proxy.BandwidthLimit = matches[1]
			}

			config.Proxies = append(config.Proxies, proxy)
		}
	}

	json.NewEncoder(w).Encode(config)
}

func handleGetRawConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	instanceID := resolveInstanceID(r)

	if body, err := os.ReadFile(instanceConfigReadPath(instanceID)); err == nil {
		w.Write(body)
	} else {
		w.Write([]byte(""))
	}
}

func handleGetLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	instanceID := resolveInstanceID(r)

	const maxLines = 500
	if body, err := os.ReadFile(instanceLogReadPath(instanceID)); err == nil {
		lines := strings.Split(string(body), "\n")
		if len(lines) > maxLines {
			lines = lines[len(lines)-maxLines:]
		}
		w.Write([]byte(strings.Join(lines, "\n")))
	} else {
		w.Write([]byte(""))
	}
}

func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	instanceID := resolveInstanceID(r)
	if err := os.WriteFile(instanceLogReadPath(instanceID), []byte(""), 0666); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handleSaveLogConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var data struct {
		InstanceID string `json:"instanceId"`
		LogLevel  string `json:"logLevel"`
		LogMaxDays int    `json:"logMaxDays"`
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	instanceID := normalizeInstanceID(data.InstanceID)
	content, err := os.ReadFile(instanceConfigReadPath(instanceID))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	configStr := string(content)

	hasLog := hasLogSection(configStr)

	var newLogSection string
	if data.LogLevel != "" || data.LogMaxDays > 0 {
		newLogSection = "\n[log]\n"
		if data.LogLevel != "" {
			newLogSection += fmt.Sprintf(`level = "%s"
`, data.LogLevel)
		}
		if data.LogMaxDays > 0 {
			newLogSection += fmt.Sprintf("maxDays = %d\n", data.LogMaxDays)
		}
	}

	if hasLog {
		configStr = removeLogSection(configStr)
		if newLogSection != "" {
			configStr = newLogSection + "\n" + configStr
		}
	} else if newLogSection != "" {
		insertIdx := strings.LastIndex(configStr, "[[proxies]]")
		if insertIdx == -1 {
			insertIdx = len(configStr)
		} else {
			insertIdx--
		}
		configStr = configStr[:insertIdx] + newLogSection + configStr[insertIdx:]
	}

	configStr = ensureRequiredConfigEntries(configStr, resolveInstanceWebServerPort(instanceID))
	if err := os.WriteFile(instanceConfigWritePath(instanceID), []byte(configStr), 0666); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func parseProxyBlocks(content string) []string {
	var blocks []string
	lines := strings.Split(content, "\n")
	var currentBlock string
	inProxy := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[[proxies]]") {
			if inProxy && currentBlock != "" {
				blocks = append(blocks, currentBlock)
			}
			currentBlock = ""
			inProxy = true
		} else if inProxy {
			if strings.HasPrefix(trimmed, "[[") && !strings.HasPrefix(trimmed, "[[proxies]]") {
				blocks = append(blocks, currentBlock)
				currentBlock = ""
				inProxy = false
			} else {
				currentBlock += line + "\n"
			}
		}
	}

	if inProxy && currentBlock != "" {
		blocks = append(blocks, currentBlock)
	}

	return blocks
}

func handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	defer func() {
		if r := recover(); r != nil {
			log.Printf("handleSaveConfig panic: %v", r)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("内部错误: %v", r)})
		}
	}()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Read body error: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	log.Printf("Received body: %s", string(body))

	var data struct {
		InstanceID            string                   `json:"instanceId"`
		Config                string                   `json:"config"`
		ApplyMode             string                   `json:"applyMode"`
		ServerAddr            string                   `json:"serverAddr"`
		ServerPort            int                      `json:"serverPort"`
		AuthToken             string                   `json:"authToken"`
		Protocol              string                   `json:"protocol"`
		WireProtocolEnabled   bool                     `json:"wireProtocolEnabled"`
		WireProtocol          string                   `json:"wireProtocol"`
		NatHoleStunServer     string                   `json:"natHoleStunServer"`
		TcpMux                bool                     `json:"tcpMux"`
		HeartbeatInterval     int                      `json:"heartbeatInterval"`
		HeartbeatTimeout      int                      `json:"heartbeatTimeout"`
		TcpMuxKeepaliveInterval int                    `json:"tcpMuxKeepaliveInterval"`
		LogTo                 string                   `json:"logTo"`
		LogLevel              string                   `json:"logLevel"`
		LogMaxDays            int                      `json:"logMaxDays"`
		ProxiesRaw            []map[string]interface{} `json:"proxies"`
		Proxies               []Proxy                  `json:"-"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("JSON decode error: %v", err)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	for _, rawProxy := range data.ProxiesRaw {
		proxy := Proxy{}
		if v, ok := rawProxy["name"].(string); ok {
			proxy.Name = v
		}
		if v, ok := rawProxy["type"].(string); ok {
			proxy.Type = v
			if proxy.Type == "p2p" {
				proxy.Type = "xtcp"
			}
		}
		if v, ok := rawProxy["localIP"].(string); ok {
			proxy.LocalIP = v
		}
		if v, ok := rawProxy["localPort"]; ok {
			switch val := v.(type) {
			case float64:
				proxy.LocalPort = int(val)
			case int:
				proxy.LocalPort = val
			}
		}
		if v, ok := rawProxy["remotePort"]; ok {
			switch val := v.(type) {
			case float64:
				proxy.RemotePort = int(val)
			case int:
				proxy.RemotePort = val
			}
		}
		if v, ok := rawProxy["customDomain"].(string); ok {
			proxy.CustomDomain = v
		}
		if v, ok := rawProxy["customDomains"]; ok {
			if arr, ok := v.([]interface{}); ok {
				for _, d := range arr {
					if s, ok := d.(string); ok {
						proxy.CustomDomains = append(proxy.CustomDomains, s)
					}
				}
			}
		}
		if v, ok := rawProxy["subDomain"].(string); ok {
			proxy.SubDomain = v
		} else if v, ok := rawProxy["subdomain"].(string); ok {
			proxy.SubDomain = v
		}
		if v, ok := rawProxy["locations"]; ok {
			if arr, ok := v.([]interface{}); ok {
				for _, l := range arr {
					if s, ok := l.(string); ok {
						proxy.Locations = append(proxy.Locations, s)
					}
				}
			}
		}
		if v, ok := rawProxy["secretKey"].(string); ok {
			proxy.SecretKey = v
		} else if v, ok := rawProxy["secret_key"].(string); ok {
			proxy.SecretKey = v
		}
		if v, ok := rawProxy["httpUser"].(string); ok {
			proxy.HttpUser = v
		}
		if v, ok := rawProxy["httpPassword"].(string); ok {
			proxy.HttpPassword = v
		}
		if v, ok := rawProxy["useEncryption"].(bool); ok {
			proxy.UseEncryption = v
		}
		if v, ok := rawProxy["useCompression"].(bool); ok {
			proxy.UseCompression = v
		}
		if v, ok := rawProxy["bandwidthLimit"].(string); ok {
			proxy.BandwidthLimit = v
		}
		data.Proxies = append(data.Proxies, proxy)
	}

	// 验证 Proxies 中的带宽限制
	instanceID := normalizeInstanceID(data.InstanceID)
	configPath := instanceConfigWritePath(instanceID)
	for i, proxy := range data.Proxies {
		if proxy.BandwidthLimit != "" {
			if !validateBandwidthUnit(proxy.BandwidthLimit) {
				json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("第 %d 个代理的带宽限制单位必须为 KB/MB/GB（如 1MB, 500KB）", i+1)})
				return
			}
		}
	}

	previousConfig, previousConfigErr := os.ReadFile(configPath)
	hasPreviousConfig := previousConfigErr == nil
	if previousConfigErr != nil && !os.IsNotExist(previousConfigErr) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": previousConfigErr.Error()})
		return
	}

	if data.Config != "" {
		configStr := data.Config

		// 验证用户原始配置中的带宽限制
		bwRe := regexp.MustCompile(`bandwidthLimit\s*=\s*"([^"]+)"`)
		matches := bwRe.FindAllStringSubmatch(configStr, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				if !validateBandwidthUnit(match[1]) {
					json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("带宽限制 \"%s\" 单位必须为 KB/MB/GB（如 1MB, 500KB）", match[1])})
					return
				}
			}
		}

		// 自动修复带宽单位
		configStr = fixAllBandwidthUnitsInConfig(configStr)

		configStr = ensureRequiredConfigEntries(configStr, resolveInstanceWebServerPort(instanceID))
		if err := os.WriteFile(configPath, []byte(configStr), 0666); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
	} else {
		tomlContent := generateToml(instanceID, data.ServerAddr, data.ServerPort, data.AuthToken, data.Protocol, data.WireProtocolEnabled, data.WireProtocol, data.NatHoleStunServer, data.TcpMux, data.HeartbeatInterval, data.HeartbeatTimeout, data.TcpMuxKeepaliveInterval, data.LogTo, data.LogLevel, data.LogMaxDays, data.Proxies)
		if err := os.WriteFile(configPath, []byte(tomlContent), 0666); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
	}

	log.Println("Starting to restart frpc service...")
	instanceEnabled := isInstanceEnabled(instanceID)
	managerArgs := []string{"restart-one", instanceID}
	if data.ApplyMode == "reload" {
		managerArgs = []string{"reload-one", instanceID}
	}
	message := "配置保存成功，服务正在重启"
	applied := false
	if instanceEnabled {
		err = runManagerCommand(managerArgs...)
		if err != nil {
			log.Printf("Failed to apply config for instance %s: %s", instanceID, err.Error())

			rollbackMessage := "新配置应用失败"
			if hasPreviousConfig {
				if writeErr := os.WriteFile(configPath, previousConfig, 0666); writeErr != nil {
					log.Printf("Rollback config write failed for instance %s: %v", instanceID, writeErr)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"success": false,
						"applied": false,
						"enabled": instanceEnabled,
						"error":   rollbackMessage + "，且回滚旧配置失败: " + writeErr.Error(),
					})
					return
				}

				if restartErr := runManagerCommand("restart-one", instanceID); restartErr != nil {
					log.Printf("Rollback restart failed for instance %s: %v", instanceID, restartErr)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"success": false,
						"applied": false,
						"enabled": instanceEnabled,
						"error":   rollbackMessage + "，已恢复旧配置文件，但恢复启动失败: " + restartErr.Error(),
					})
					return
				}

				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"applied": false,
					"enabled": instanceEnabled,
					"error":   rollbackMessage + "，已自动回滚到上一版配置",
				})
				return
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"applied": false,
				"enabled": instanceEnabled,
				"error":   rollbackMessage + ": " + err.Error(),
			})
			return
		} else {
			applied = true
			if data.ApplyMode == "reload" {
				message = "配置保存成功，热重载已完成"
			} else {
				message = "配置保存成功，服务已重启"
			}
			log.Println("Config applied successfully")
		}
	} else {
		message = "配置已保存，实例已禁用，未启动"
		if err := runManagerCommand("stop-one", instanceID); err != nil {
			log.Printf("stop disabled instance after save failed: %v", err)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"applied": applied,
		"enabled": instanceEnabled,
		"message": message,
	})
}

func fixBandwidthUnit(limit string) string {
	if limit == "" {
		return ""
	}
	
	re := regexp.MustCompile(`^([\d.]+)([KkMm][Bb]?)$`)
	matches := re.FindStringSubmatch(limit)
	if len(matches) == 3 {
		num := matches[1]
		unit := strings.ToUpper(matches[2])
		
		switch unit {
		case "K", "KB":
			return num + "KB"
		case "M", "MB":
			return num + "MB"
		}
	}
	
	return limit
}

func validateBandwidthUnit(limit string) bool {
	if limit == "" {
		return true
	}
	re := regexp.MustCompile(`^([\d.]+)(KB|MB|kb|mb|K|M|k|m)$`)
	return re.MatchString(limit)
}

func removeConfigLine(config, key string) string {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `\s*=.*\n?`)
	return re.ReplaceAllString(config, "")
}

func removeCommentLine(config, comment string) string {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(comment) + `[ \t]*\n?`)
	return re.ReplaceAllString(config, "")
}

func ensureRequiredConfigEntries(config string, webServerPort int) string {
	if webServerPort <= 0 {
		webServerPort = 7400
	}
	config = removeCommentLine(config, mandatoryWebServerComment)
	config = removeCommentLine(config, mandatoryLoginFailExitComment)
	config = removeConfigLine(config, "webServer.addr")
	config = removeConfigLine(config, "webServer.port")
	config = removeConfigLine(config, "loginFailExit")
	config = strings.TrimLeft(config, "\n")

	requiredBlock := strings.Join([]string{
		mandatoryWebServerComment,
		`webServer.addr = "127.0.0.1"`,
		fmt.Sprintf("webServer.port = %d", webServerPort),
		mandatoryLoginFailExitComment,
		`loginFailExit = false`,
		"",
	}, "\n")

	if config == "" {
		return requiredBlock
	}
	return requiredBlock + "\n" + config
}

func removeLogSection(config string) string {
	lines := strings.Split(config, "\n")
	var result []string
	inLogSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[log]" {
			inLogSection = true
			continue
		}
		if inLogSection && (strings.HasPrefix(trimmed, "[") || trimmed == "") {
			inLogSection = false
		}
		if !inLogSection {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func removeAllProxiesSections(config string) string {
	lines := strings.Split(config, "\n")
	var result []string
	inProxiesSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[[proxies]]") || strings.HasPrefix(trimmed, "[proxies]") || (strings.HasPrefix(trimmed, "[proxies.") && !strings.HasPrefix(trimmed, "[[proxies")) {
			inProxiesSection = true
			continue
		}
		if inProxiesSection && strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[proxies.") {
			inProxiesSection = false
		}
		if !inProxiesSection {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func normalizeHTTPProxyDomains(proxy Proxy) Proxy {
	if proxy.Type != "http" && proxy.Type != "https" {
		return proxy
	}
	if proxy.SubDomain != "" {
		proxy.CustomDomain = ""
		proxy.CustomDomains = nil
		return proxy
	}
	if len(proxy.CustomDomains) > 0 {
		proxy.CustomDomain = ""
		return proxy
	}
	if proxy.CustomDomain != "" {
		proxy.CustomDomains = nil
		return proxy
	}
	return proxy
}

func hasLogSection(config string) bool {
	return strings.Contains(config, "[log]")
}

func fixAllBandwidthUnitsInConfig(config string) string {
	// 替换 bandwidthLimit 字段的单位，支持以下格式：
	// 100mb → 100MB, 500kb → 500KB
	// 100m → 100MB, 500k → 500KB
	re := regexp.MustCompile(`bandwidthLimit\s*=\s*"([\d.]+)([KkMm][Bb]?)"`)
	return re.ReplaceAllStringFunc(config, func(m string) string {
		matches := re.FindStringSubmatch(m)
		if len(matches) == 3 {
			num := matches[1]
			unit := strings.ToUpper(matches[2])
			
			// 统一单位
			fixedUnit := unit
			switch unit {
			case "K", "KB":
				fixedUnit = "KB"
			case "M", "MB":
				fixedUnit = "MB"
			}
			
			return fmt.Sprintf(`bandwidthLimit = "%s%s"`, num, fixedUnit)
		}
		return m
	})
}

func generateToml(instanceID, serverAddr string, serverPort int, authToken, protocol string, wireProtocolEnabled bool, wireProtocol string, natHoleStunServer string, tcpMux bool, heartbeatInterval, heartbeatTimeout, tcpMuxKeepaliveInterval int, logTo, logLevel string, logMaxDays int, proxies []Proxy) string {
	var configStr string
	if existing, err := os.ReadFile(instanceConfigReadPath(instanceID)); err == nil {
		configStr = string(existing)
		configStr = fixAllBandwidthUnitsInConfig(configStr)
	} else {
		configStr = ""
	}

	replaceOrAdd := func(key string, value interface{}, optional bool) {
		var newLine string
		switch v := value.(type) {
		case string:
			if optional && v == "" {
				re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `\s*=.*\n?`)
				configStr = re.ReplaceAllString(configStr, "")
				return
			}
			newLine = fmt.Sprintf(`%s = "%s"`, key, v)
		case int:
			if optional && v <= 0 {
				re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `\s*=.*\n?`)
				configStr = re.ReplaceAllString(configStr, "")
				return
			}
			newLine = fmt.Sprintf("%s = %d", key, v)
		case bool:
			newLine = fmt.Sprintf("%s = %v", key, v)
		}

		re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `\s*=.*\n?`)
		if re.MatchString(configStr) {
			configStr = re.ReplaceAllString(configStr, newLine+"\n")
		} else {
			configStr = newLine + "\n" + configStr
		}
	}

	replaceOrAdd("serverAddr", serverAddr, false)
	replaceOrAdd("serverPort", serverPort, false)
	replaceOrAdd("auth.token", authToken, true)
	replaceOrAdd("transport.protocol", protocol, false)
	if wireProtocolEnabled {
		version := strings.ToLower(strings.TrimSpace(wireProtocol))
		if version != "v2" {
			version = "v1"
		}
		replaceOrAdd("transport.wireProtocol", version, true)
	} else {
		replaceOrAdd("transport.wireProtocol", "", true)
	}
	replaceOrAdd("natHoleStunServer", natHoleStunServer, true)
	replaceOrAdd("transport.tcpMux", tcpMux, false)
	replaceOrAdd("transport.heartbeatInterval", heartbeatInterval, true)
	replaceOrAdd("transport.heartbeatTimeout", heartbeatTimeout, true)
	replaceOrAdd("transport.tcpMuxKeepaliveInterval", tcpMuxKeepaliveInterval, true)

	hasLog := hasLogSection(configStr)
	var newLogSection string
	if logTo != "" || logLevel != "" || logMaxDays > 0 {
		newLogSection = "\n[log]\n"
		if logTo != "" {
			newLogSection += fmt.Sprintf(`to = "%s"
`, logTo)
		}
		if logLevel != "" {
			newLogSection += fmt.Sprintf(`level = "%s"
`, logLevel)
		}
		if logMaxDays > 0 {
			newLogSection += fmt.Sprintf("maxDays = %d\n", logMaxDays)
		}
	}

	if hasLog {
		configStr = removeLogSection(configStr)
		if newLogSection != "" {
			configStr = newLogSection + "\n" + configStr
		}
	} else if newLogSection != "" {
		configStr += newLogSection
	}

	configStr = removeAllProxiesSections(configStr)

	for _, proxy := range proxies {
		proxy = normalizeHTTPProxyDomains(proxy)
		configStr += "\n[[proxies]]\n"
		configStr += fmt.Sprintf(`name = "%s"
type = "%s"
`, proxy.Name, proxy.Type)
		configStr += fmt.Sprintf(`localIP = "%s"
localPort = %d
`, proxy.LocalIP, proxy.LocalPort)

		if proxy.RemotePort > 0 {
			configStr += fmt.Sprintf("remotePort = %d\n", proxy.RemotePort)
		}
		if len(proxy.CustomDomains) > 0 {
			configStr += fmt.Sprintf(`customDomains = [%s]
`, formatStringArray(proxy.CustomDomains))
		} else if proxy.CustomDomain != "" {
			configStr += fmt.Sprintf(`customDomain = "%s"
`, proxy.CustomDomain)
		}
		if proxy.SubDomain != "" {
			configStr += fmt.Sprintf(`subDomain = "%s"
`, proxy.SubDomain)
		}
		if len(proxy.Locations) > 0 {
			configStr += fmt.Sprintf(`locations = [%s]
`, formatStringArray(proxy.Locations))
		}
		if proxy.SecretKey != "" {
			configStr += fmt.Sprintf(`secretKey = "%s"
`, proxy.SecretKey)
		}
		if proxy.HttpUser != "" {
			configStr += fmt.Sprintf(`httpUser = "%s"
`, proxy.HttpUser)
		}
		if proxy.HttpPassword != "" {
			configStr += fmt.Sprintf(`httpPassword = "%s"
`, proxy.HttpPassword)
		}

		if proxy.UseEncryption || proxy.UseCompression || proxy.BandwidthLimit != "" {
			configStr += "\n[proxies.transport]\n"
			if proxy.UseEncryption {
				configStr += "useEncryption = true\n"
			}
			if proxy.UseCompression {
				configStr += "useCompression = true\n"
			}
			if proxy.BandwidthLimit != "" {
				fixedLimit := fixBandwidthUnit(proxy.BandwidthLimit)
				configStr += fmt.Sprintf(`bandwidthLimit = "%s"
`, fixedLimit)
			}
		}
	}

	return ensureRequiredConfigEntries(configStr, resolveInstanceWebServerPort(instanceID))
}

func formatStringArray(arr []string) string {
	var result []string
	for _, s := range arr {
		result = append(result, fmt.Sprintf(`"%s"`, s))
	}
	return strings.Join(result, ", ")
}

func handleRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	instanceID := resolveInstanceID(r)

	if !isInstanceEnabled(instanceID) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "实例已禁用，请先启用后再重启"})
		return
	}

	err := startManagerCommand("restart-one", instanceID)

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ip := r.URL.Query().Get("ip")
	portStr := r.URL.Query().Get("port")

	if ip == "" || portStr == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "缺少参数"})
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "端口格式错误"})
		return
	}

	addr := fmt.Sprintf("%s:%d", ip, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	defer conn.Close()

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "latency": latency})
}

func normalizeStatusLogLine(line string) string {
	line = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(line, "")
	return strings.ToLower(strings.TrimSpace(line))
}

func parseStatusLogTimestamp(line string) (time.Time, bool) {
	line = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(line, "")
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?`)
	prefix := re.FindString(strings.TrimSpace(line))
	if prefix == "" {
		return time.Time{}, false
	}

	layouts := []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, prefix, time.Local); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func isLoadingStatusLogLine(line string) bool {
	normalized := normalizeStatusLogLine(line)
	return strings.Contains(normalized, "[manager] restart requested") ||
		strings.Contains(normalized, "[manager] start requested") ||
		strings.Contains(normalized, "[manager] starting frpc") ||
		strings.Contains(normalized, "start frpc service")
}

func isHotReloadStartLogLine(line string) bool {
	return strings.Contains(normalizeStatusLogLine(line), "[manager] trying hot reload")
}

func isHotReloadSuccessLogLine(line string) bool {
	normalized := normalizeStatusLogLine(line)
	return strings.Contains(normalized, "[manager] hot reload succeeded") ||
		strings.Contains(normalized, "[manager] hot reload preserved connected state")
}

func resolveStableStatusFromLogLine(line string) (string, bool, string, bool) {
	normalized := normalizeStatusLogLine(line)
	if normalized == "" {
		return "", false, "", false
	}

	switch {
	case strings.Contains(normalized, "login to server success"):
		return "success", true, "已连接", true
	case strings.Contains(normalized, "[manager] hot reload preserved connected state"):
		return "success", true, "已连接", true
	case strings.Contains(normalized, "token in login doesn't match token"):
		return "error", false, "秘钥不匹配", true
	case strings.Contains(normalized, "connect to server error"):
		switch {
		case strings.Contains(normalized, "connection refused"):
			return "error", false, "服务器拒绝连接", true
		case strings.Contains(normalized, "i/o timeout"), strings.Contains(normalized, "timeout"):
			return "error", false, "连接超时", true
		case strings.Contains(normalized, "no such host"):
			return "error", false, "域名解析失败", true
		case strings.Contains(normalized, "network is unreachable"):
			return "error", false, "网络不可达", true
		default:
			return "error", false, "连接失败", true
		}
	}

	return "", false, "", false
}

func resolveProgressStatusFromLogLine(line string) (string, bool, string, bool) {
	normalized := normalizeStatusLogLine(line)
	if normalized == "" {
		return "", false, "", false
	}

	switch {
	case strings.Contains(normalized, "start frpc service"),
		strings.Contains(normalized, "try to connect to server"),
		strings.Contains(normalized, "[manager] starting frpc"),
		strings.Contains(normalized, "[manager] start requested"),
		strings.Contains(normalized, "[manager] restart requested"):
		return "warning", false, "连接中", true
	}

	return "", false, "", false
}

func resolveStatusFromLogLine(line string) (string, bool, string, bool) {
	if level, connected, message, matched := resolveStableStatusFromLogLine(line); matched {
		return level, connected, message, matched
	}
	return resolveProgressStatusFromLogLine(line)
}

func buildInstanceStatus(instanceID string) map[string]interface{} {
	enabled := isInstanceEnabled(instanceID)

	status := map[string]interface{}{
		"running":          false,
		"connected":        false,
		"enabled":          enabled,
		"message":          "未运行",
		"level":            "offline",
		"loading":          false,
		"loadingSeconds":   0,
		"loadingMessage":   "",
	}

	// 检查进程是否在运行
	if data, err := os.ReadFile(instancePIDReadPath(instanceID)); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil {
			cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
			if cmd.Run() == nil {
				status["running"] = true
			}
		}
	}

	// 检查日志获取连接状态
	if logData, err := os.ReadFile(instanceLogReadPath(instanceID)); err == nil {
		lines := strings.Split(string(logData), "\n")
		var latestLoadingAt time.Time
		latestHotReloadSuccessIdx := -1
		var latestHotReloadSuccessAt time.Time
		for i := len(lines) - 1; i >= 0; i-- {
			if latestLoadingAt.IsZero() && isLoadingStatusLogLine(lines[i]) {
				if ts, ok := parseStatusLogTimestamp(lines[i]); ok {
					latestLoadingAt = ts
				}
			}
			if latestHotReloadSuccessIdx == -1 && isHotReloadSuccessLogLine(lines[i]) {
				latestHotReloadSuccessIdx = i
				if ts, ok := parseStatusLogTimestamp(lines[i]); ok {
					latestHotReloadSuccessAt = ts
				}
			}
		}

		statusResolved := false
		if latestHotReloadSuccessIdx >= 0 {
			hasLaterResolvedStatus := false
			for i := len(lines) - 1; i > latestHotReloadSuccessIdx; i-- {
				if _, _, _, matched := resolveStatusFromLogLine(lines[i]); matched {
					hasLaterResolvedStatus = true
					break
				}
			}
			if !hasLaterResolvedStatus {
				hotReloadStartIdx := -1
				for i := latestHotReloadSuccessIdx - 1; i >= 0; i-- {
					if isHotReloadStartLogLine(lines[i]) {
						hotReloadStartIdx = i
						break
					}
				}

				if !latestHotReloadSuccessAt.IsZero() {
					elapsed := time.Since(latestHotReloadSuccessAt)
					if elapsed >= 0 && elapsed < 5*time.Second {
						remaining := int((5*time.Second - elapsed + time.Second - 1) / time.Second)
						status["message"] = "热重载中"
						status["level"] = "warning"
						status["loading"] = true
						status["loadingSeconds"] = remaining
						status["loadingMessage"] = fmt.Sprintf("热重载中，预计还需 %d 秒", remaining)
						statusResolved = true
					}
				}

				if !statusResolved {
					searchEnd := latestHotReloadSuccessIdx - 1
					if hotReloadStartIdx >= 0 {
						searchEnd = hotReloadStartIdx - 1
					}
					for i := searchEnd; i >= 0; i-- {
						level, connected, message, matched := resolveStableStatusFromLogLine(lines[i])
						if !matched {
							continue
						}
						status["connected"] = connected
						status["message"] = message
						status["level"] = level
						statusResolved = true
						break
					}
				}
			}
		}

		if !statusResolved {
			for i := len(lines) - 1; i >= 0; i-- {
				level, connected, message, matched := resolveStatusFromLogLine(lines[i])
				if !matched {
					continue
				}
				status["connected"] = connected
				status["message"] = message
				status["level"] = level
				statusResolved = true
				break
			}
		}

		if status["running"].(bool) && status["message"] == "未运行" {
			status["message"] = "运行中"
			status["level"] = "warning"
		}
		if !status["loading"].(bool) && !latestLoadingAt.IsZero() {
			elapsed := time.Since(latestLoadingAt)
			if elapsed >= 0 && elapsed < 10*time.Second {
				remaining := int((10*time.Second - elapsed + time.Second - 1) / time.Second)
				status["loading"] = true
				status["loadingSeconds"] = remaining
				status["loadingMessage"] = fmt.Sprintf("进程加载中，预计还需 %d 秒", remaining)
			}
		}
	} else {
		// 没有日志文件但进程在运行
		if status["running"].(bool) {
			status["message"] = "运行中"
			status["level"] = "warning"
		}
	}

	if !status["running"].(bool) {
		if status["connected"].(bool) || status["message"] == "连接中" {
			status["connected"] = false
			status["message"] = "未运行"
			status["level"] = "offline"
		}
	}

	if !enabled {
		if status["connected"].(bool) {
			status["message"] = "已连接（已禁用）"
			status["level"] = "success"
		} else if status["running"].(bool) {
			status["message"] = "运行中（已禁用）"
			status["level"] = "warning"
		} else {
			status["message"] = "已禁用"
			status["level"] = "offline"
		}
		status["loading"] = false
		status["loadingSeconds"] = 0
		status["loadingMessage"] = ""
	}

	return status
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	instanceID := resolveInstanceID(r)
	json.NewEncoder(w).Encode(buildInstanceStatus(instanceID))
}
