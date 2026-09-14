// Package dbconsole implements Kairo's read-only database workbench.
// Connection metadata is stored separately from config.yaml and secrets are
// resolved through the credentials package only when a connection is opened.
package dbconsole

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	KindOracle = "oracle"
	KindMySQL  = "mysql"
	KindRedis  = "redis"
)

const CredentialNamespace = "database"

// SSHCredentialNamespace deliberately differs from CredentialNamespace.  A
// jump-host password must never be accidentally reused as the database
// password (or returned through the database source API).
const SSHCredentialNamespace = "database_ssh"

// SSHTunnelConfig describes an optional local-to-remote database tunnel.  The
// database password and SSH password are stored independently in the
// credentials backend; this struct contains connection policy only.
type SSHTunnelConfig struct {
	Enabled              bool   `json:"enabled,omitempty"`
	Host                 string `json:"host,omitempty"`
	Port                 int    `json:"port,omitempty"`
	Username             string `json:"username,omitempty"`
	RemoteHost           string `json:"remote_host,omitempty"`
	RemotePort           int    `json:"remote_port,omitempty"`
	HostKeySHA256        string `json:"host_key_sha256,omitempty"`
	SSHProfile           string `json:"ssh_profile,omitempty"`
	AllowInsecureHostKey bool   `json:"allow_insecure_host_key,omitempty"`
}

type Source struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	Kind                 string           `json:"kind"`
	Host                 string           `json:"host"`
	Port                 int              `json:"port"`
	Username             string           `json:"username"`
	Database             string           `json:"database,omitempty"`
	OracleConnectBy      string           `json:"oracle_connect_by,omitempty"` // service_name / sid
	OracleService        string           `json:"oracle_service,omitempty"`
	OracleClientCharset  string           `json:"oracle_client_charset,omitempty"`
	OracleDriver         string           `json:"oracle_driver,omitempty"`     // godror (OCI, Windows企业默认) / go-ora (纯 Go 便携/降级)
	OracleLibDir         string           `json:"oracle_lib_dir,omitempty"`    // 自定义 Oracle Client / Instant Client 目录
	OracleConfigDir      string           `json:"oracle_config_dir,omitempty"` // 自定义 tnsnames.ora / sqlnet.ora 目录
	RedisDB              int              `json:"redis_db,omitempty"`
	RedisMode            string           `json:"redis_mode,omitempty"`        // standalone / cluster / sentinel
	RedisMasterName      string           `json:"redis_master_name,omitempty"` // Sentinel master name
	RedisNodes           []string         `json:"redis_nodes,omitempty"`       // extra seed / sentinel host:port
	AllowRedisWrite      bool             `json:"allow_redis_write,omitempty"` // explicit capability gate; default false
	TLSMode              string           `json:"tls_mode,omitempty"`          // disabled / preferred / required / skip-verify
	TLSCAFile            string           `json:"tls_ca_file,omitempty"`
	TLSClientCertFile    string           `json:"tls_client_cert_file,omitempty"`
	TLSClientKeyFile     string           `json:"tls_client_key_file,omitempty"`
	TLSServerName        string           `json:"tls_server_name,omitempty"`
	Environment          string           `json:"environment,omitempty"` // development / staging / production
	ReadOnly             bool             `json:"read_only"`             // server-enforced source write lock
	ReadOnlyConfigured   bool             `json:"-"`                     // distinguishes an explicit false from an omitted field
	AllowDDL             bool             `json:"allow_ddl,omitempty"`   // explicit DDL capability gate
	SSHTunnel            *SSHTunnelConfig `json:"ssh_tunnel,omitempty"`
	QueryTimeoutSeconds  int              `json:"query_timeout_seconds"`
	MaxRows              int              `json:"max_rows"`
	MaxResultBytes       int64            `json:"max_result_bytes"`
	MaxOpenConnections   int              `json:"max_open_connections"`
	MaxIdleConnections   int              `json:"max_idle_connections"`
	ConnectionMaxMinutes int              `json:"connection_max_minutes"`
	AllowedUsers         []string         `json:"allowed_users,omitempty"`
	CreatedAt            string           `json:"created_at"`
	UpdatedAt            string           `json:"updated_at"`
}

// Clone returns a deep copy of the Source, isolating slices and pointer fields.
func (s Source) Clone() Source {
	out := s
	if len(s.AllowedUsers) > 0 {
		out.AllowedUsers = append([]string(nil), s.AllowedUsers...)
	}
	if len(s.RedisNodes) > 0 {
		out.RedisNodes = append([]string(nil), s.RedisNodes...)
	}
	if s.SSHTunnel != nil {
		tunnelCopy := *s.SSHTunnel
		out.SSHTunnel = &tunnelCopy
	}
	return out
}

// UnmarshalJSON remembers whether read_only was present. A plain bool cannot
// distinguish an explicit writable choice from an omitted field; security
// defaults must never treat omission as permission to write.
func (s *Source) UnmarshalJSON(data []byte) error {
	type sourceAlias Source
	var decoded sourceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*s = Source(decoded)
	if raw, ok := fields["read_only"]; ok && string(raw) != "null" {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		s.ReadOnly = value
		s.ReadOnlyConfigured = true
	}
	return nil
}

func (s *Source) Defaults() {
	if !s.ReadOnlyConfigured {
		s.ReadOnly = true
		s.ReadOnlyConfigured = true
	}
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	s.Host = strings.TrimSpace(s.Host)
	s.Name = strings.TrimSpace(s.Name)
	s.Username = strings.TrimSpace(s.Username)
	seenUsers := make(map[string]struct{}, len(s.AllowedUsers))
	users := make([]string, 0, len(s.AllowedUsers))
	for _, user := range s.AllowedUsers {
		user = strings.TrimSpace(user)
		key := strings.ToLower(user)
		if user == "" {
			continue
		}
		if _, exists := seenUsers[key]; exists {
			continue
		}
		seenUsers[key] = struct{}{}
		users = append(users, user)
	}
	s.AllowedUsers = users
	if s.Port == 0 {
		switch s.Kind {
		case KindOracle:
			s.Port = 1521
		case KindMySQL:
			s.Port = 3306
		case KindRedis:
			s.Port = 6379
		}
	}
	if s.OracleConnectBy == "" {
		s.OracleConnectBy = "service_name"
	}
	if s.QueryTimeoutSeconds == 0 {
		s.QueryTimeoutSeconds = 60
	}
	if s.MaxRows == 0 {
		s.MaxRows = 1000
	}
	if s.MaxResultBytes == 0 {
		s.MaxResultBytes = 16 << 20
	}
	if s.MaxOpenConnections == 0 {
		if s.Kind == KindOracle {
			s.MaxOpenConnections = 4
		} else {
			s.MaxOpenConnections = 8
		}
	}
	if s.MaxIdleConnections == 0 {
		if s.Kind == KindOracle {
			s.MaxIdleConnections = 1
		} else {
			s.MaxIdleConnections = 2
		}
	}
	if s.ConnectionMaxMinutes == 0 {
		s.ConnectionMaxMinutes = 10
	}
	if s.TLSMode == "" {
		s.TLSMode = "disabled"
	}
	s.Environment = strings.ToLower(strings.TrimSpace(s.Environment))
	if s.Environment == "" {
		s.Environment = "production"
	}
	s.TLSCAFile = strings.TrimSpace(s.TLSCAFile)
	s.TLSClientCertFile = strings.TrimSpace(s.TLSClientCertFile)
	s.TLSClientKeyFile = strings.TrimSpace(s.TLSClientKeyFile)
	s.TLSServerName = strings.TrimSpace(s.TLSServerName)
	if s.SSHTunnel != nil {
		s.SSHTunnel.Host = strings.TrimSpace(s.SSHTunnel.Host)
		s.SSHTunnel.Username = strings.TrimSpace(s.SSHTunnel.Username)
		s.SSHTunnel.RemoteHost = strings.TrimSpace(s.SSHTunnel.RemoteHost)
		s.SSHTunnel.SSHProfile = strings.ToLower(strings.TrimSpace(s.SSHTunnel.SSHProfile))
		if s.SSHTunnel.Port == 0 {
			s.SSHTunnel.Port = 22
		}
		if s.SSHTunnel.RemotePort == 0 {
			s.SSHTunnel.RemotePort = s.Port
		}
		if s.SSHTunnel.RemoteHost == "" {
			s.SSHTunnel.RemoteHost = s.Host
		}
	}
	s.RedisMode = strings.ToLower(strings.TrimSpace(s.RedisMode))
	if s.Kind == KindRedis && s.RedisMode == "" {
		s.RedisMode = "standalone"
	}
	s.RedisMasterName = strings.TrimSpace(s.RedisMasterName)
	nodes := make([]string, 0, len(s.RedisNodes))
	seenNode := make(map[string]struct{}, len(s.RedisNodes))
	for _, node := range s.RedisNodes {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		if _, ok := seenNode[node]; ok {
			continue
		}
		seenNode[node] = struct{}{}
		nodes = append(nodes, node)
	}
	s.RedisNodes = nodes
}

func (s Source) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("数据源 id 不能为空")
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("数据源名称不能为空")
	}
	if len(s.Name) > 100 {
		return errors.New("数据源名称不能超过 100 字节")
	}
	switch s.Kind {
	case KindOracle, KindMySQL, KindRedis:
	default:
		return fmt.Errorf("不支持的数据源类型 %q", s.Kind)
	}
	if s.Host == "" || net.ParseIP(s.Host) == nil && !validHostname(s.Host) {
		return errors.New("host 必须是有效 IP 或主机名")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("port 必须在 1..65535 之间")
	}
	if s.Username == "" && s.Kind != KindRedis {
		return errors.New("username 不能为空")
	}
	if len(s.Username) > 256 || len(s.Database) > 256 || len(s.OracleService) > 256 {
		return errors.New("username/database/service 不能超过 256 字节")
	}
	if s.Kind == KindOracle {
		if s.OracleConnectBy != "service_name" && s.OracleConnectBy != "sid" {
			return errors.New("Oracle 连接方式仅支持 service_name 或 sid")
		}
		if strings.TrimSpace(s.OracleService) == "" {
			return errors.New("Oracle Service Name/SID 不能为空")
		}
	}
	if s.Kind == KindMySQL && strings.TrimSpace(s.Database) == "" {
		return errors.New("MySQL database 不能为空")
	}
	if s.RedisDB < 0 || s.RedisDB > 1024 {
		return errors.New("Redis DB 必须在 0..1024 之间")
	}
	if s.Kind == KindRedis {
		switch s.RedisMode {
		case "standalone", "cluster", "sentinel":
		default:
			return errors.New("Redis 拓扑仅支持 standalone / cluster / sentinel")
		}
		if s.RedisMode == "cluster" && s.RedisDB != 0 {
			return errors.New("Redis Cluster 不支持 SELECT，DB 必须为 0")
		}
		if s.RedisMode == "sentinel" && s.RedisMasterName == "" {
			return errors.New("Sentinel 必须填写 Master 名称")
		}
		if len(s.RedisMasterName) > 256 {
			return errors.New("Redis Master 名称不能超过 256 字节")
		}
		if len(s.RedisNodes) > 32 {
			return errors.New("Redis 附加节点不能超过 32 个")
		}
		for _, node := range s.RedisNodes {
			if _, err := parseRedisAddr(node); err != nil {
				return err
			}
		}
	}
	switch s.TLSMode {
	case "disabled", "preferred", "required", "skip-verify":
	default:
		return errors.New("tls_mode 仅支持 disabled/preferred/required/skip-verify")
	}
	if s.TLSMode == "preferred" && s.Kind != KindMySQL {
		return errors.New("TLS preferred 仅 MySQL 支持；Oracle/Redis 请选择 required 或 disabled")
	}
	if s.Environment != "development" && s.Environment != "staging" && s.Environment != "production" {
		return errors.New("environment 仅支持 development / staging / production")
	}
	for name, value := range map[string]string{
		"tls_ca_file": s.TLSCAFile, "tls_client_cert_file": s.TLSClientCertFile,
		"tls_client_key_file": s.TLSClientKeyFile, "tls_server_name": s.TLSServerName,
	} {
		if len(value) > 1024 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s 非法", name)
		}
	}
	if (s.TLSClientCertFile == "") != (s.TLSClientKeyFile == "") {
		return errors.New("tls_client_cert_file 与 tls_client_key_file 必须同时填写")
	}
	if s.SSHTunnel != nil && s.SSHTunnel.Enabled {
		t := s.SSHTunnel
		if t.Host == "" || !validHostname(t.Host) && net.ParseIP(t.Host) == nil {
			return errors.New("SSH 隧道 host 必须是有效 IP 或主机名")
		}
		if t.Port < 1 || t.Port > 65535 || t.RemotePort < 1 || t.RemotePort > 65535 {
			return errors.New("SSH 隧道端口必须在 1..65535 之间")
		}
		if t.Username == "" || len(t.Username) > 256 {
			return errors.New("SSH 隧道 username 不能为空且不能超过 256 字节")
		}
		if t.RemoteHost == "" || !validHostname(t.RemoteHost) && net.ParseIP(t.RemoteHost) == nil {
			return errors.New("SSH 隧道 remote_host 必须是有效 IP 或主机名")
		}
		if len(t.HostKeySHA256) > 256 || strings.ContainsAny(t.HostKeySHA256, "\r\n\x00") {
			return errors.New("SSH 隧道 host_key_sha256 非法")
		}
		if t.SSHProfile != "" && t.SSHProfile != "auto" && t.SSHProfile != "modern" && t.SSHProfile != "compat" && t.SSHProfile != "no-ecdh" && t.SSHProfile != "legacy" {
			return errors.New("SSH 隧道 ssh_profile 非法")
		}
	}
	if s.QueryTimeoutSeconds < 1 || s.QueryTimeoutSeconds > 600 {
		return errors.New("query_timeout_seconds 必须在 1..600 之间")
	}
	if s.MaxRows < 1 || s.MaxRows > 20000 {
		return errors.New("max_rows 必须在 1..20000 之间")
	}
	if s.MaxResultBytes < 1<<20 || s.MaxResultBytes > 64<<20 {
		return errors.New("max_result_bytes 必须在 1MB..64MB 之间")
	}
	if s.MaxOpenConnections < 1 || s.MaxOpenConnections > 16 || s.MaxIdleConnections < 0 || s.MaxIdleConnections > s.MaxOpenConnections {
		return errors.New("连接池参数非法")
	}
	if len(s.AllowedUsers) > 100 {
		return errors.New("allowed_users 不能超过 100 个")
	}
	for _, user := range s.AllowedUsers {
		if len(user) > 128 || strings.ContainsAny(user, "\r\n\x00") {
			return errors.New("allowed_users 中的用户名非法")
		}
	}
	return nil
}

func (s Source) Timeout() time.Duration { return time.Duration(s.QueryTimeoutSeconds) * time.Second }

// IsProduction is intentionally strict and case-insensitive after Defaults;
// callers that receive a hand-built Source still get the same behaviour.
func (s Source) IsProduction() bool {
	return strings.EqualFold(strings.TrimSpace(s.Environment), "production")
}

// MutationAllowed reports the source-level write capability.  Production
// confirmation is a request concern and is checked by the HTTP layer; this
// method only enforces the durable read-only lock.
func (s Source) MutationAllowed() bool { return !s.ReadOnly }

// DDLAllowed reports whether the source explicitly permits schema-changing
// statements. Existing sources default false so a production/admin policy
// cannot be broadened by an omitted field.
func (s Source) DDLAllowed() bool { return s.AllowDDL && !s.ReadOnly }

func (s Source) RedisTopology() string {
	switch strings.ToLower(strings.TrimSpace(s.RedisMode)) {
	case "cluster", "sentinel":
		return strings.ToLower(strings.TrimSpace(s.RedisMode))
	default:
		return "standalone"
	}
}

func (s Source) RedisAddrs() []string {
	addrs := []string{net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}
	seen := map[string]struct{}{addrs[0]: {}}
	for _, node := range s.RedisNodes {
		addr, err := parseRedisAddr(node)
		if err != nil {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}
	return addrs
}

func parseRedisAddr(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Redis 节点地址不能为空")
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("Redis 节点 %q 必须是 host:port", raw)
	}
	if host == "" || net.ParseIP(host) == nil && !validHostname(host) {
		return "", fmt.Errorf("Redis 节点主机 %q 非法", host)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("Redis 节点端口 %q 非法", port)
	}
	return net.JoinHostPort(host, port), nil
}

func (s Source) CredentialUser() string {
	if strings.TrimSpace(s.Username) == "" {
		return "__default__"
	}
	return s.Username
}

func (s Source) UserAllowed(name, role string) bool {
	if role == "admin" {
		return true
	}
	for _, allowed := range s.AllowedUsers {
		if allowed == "*" {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func validHostname(host string) bool {
	if len(host) > 253 || strings.ContainsAny(host, " /\\:@") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

type SourceView struct {
	Source
	HasPassword    bool `json:"has_password"`
	HasSSHPassword bool `json:"has_ssh_password,omitempty"`
}

type Column struct {
	Name     string `json:"name"`
	Database string `json:"database_type,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
}

type QuerySummary struct {
	Rows               int    `json:"rows"`
	ElapsedMS          int64  `json:"elapsed_ms"`
	Truncated          bool   `json:"truncated"`
	Bytes              int64  `json:"bytes"`
	QueryLimit         int    `json:"query_limit"`
	Page               int    `json:"page,omitempty"`
	PageSize           int    `json:"page_size,omitempty"`
	Offset             int64  `json:"offset,omitempty"`
	HasNext            bool   `json:"has_next,omitempty"`
	HasPrev            bool   `json:"has_prev,omitempty"`
	TotalRows          *int64 `json:"total_rows,omitempty"`
	TotalKnown         bool   `json:"total_known,omitempty"`
	PaginationMode     string `json:"pagination_mode,omitempty"`
	RetryCount         int    `json:"retry_count,omitempty"`
	Ordered            bool   `json:"ordered"`
	RowsAffected       int64  `json:"rows_affected,omitempty"`
	StatementType      string `json:"statement_type,omitempty"`
	Message            string `json:"message,omitempty"`
	TransactionPending bool   `json:"transaction_pending,omitempty"`
}
