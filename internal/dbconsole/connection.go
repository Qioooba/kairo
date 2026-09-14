package dbconsole

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"kairo/internal/credentials"
	"kairo/internal/sshclient"

	"github.com/sijms/go-ora/v2/configurations"
	"golang.org/x/crypto/ssh"
)

const maxTLSMaterialBytes = 4 << 20

// sourceConnectionTarget returns the address that the database protocol must
// speak to.  With a jump host this is deliberately the SSH-side remote target
// rather than the jump host itself; the custom dialer only changes how the
// socket is opened, not which database endpoint the protocol addresses.
func sourceConnectionTarget(source Source) (string, int) {
	host, port := source.Host, source.Port
	if tunnel := source.SSHTunnel; tunnel != nil && tunnel.Enabled {
		if strings.TrimSpace(tunnel.RemoteHost) != "" {
			host = strings.TrimSpace(tunnel.RemoteHost)
		}
		if tunnel.RemotePort > 0 {
			port = tunnel.RemotePort
		}
	}
	return host, port
}

func redisConnectionAddrs(source Source) []string {
	addrs := source.RedisAddrs()
	if tunnel := source.SSHTunnel; tunnel != nil && tunnel.Enabled && len(addrs) > 0 {
		host, port := sourceConnectionTarget(source)
		addrs[0] = net.JoinHostPort(host, fmt.Sprintf("%d", port))
	}
	return addrs
}

// sshTunnelDialer adapts x/crypto/ssh's context-aware remote dial to all
// database drivers used by the workbench.  Keeping this as a tiny adapter
// avoids a local listener and therefore avoids another process/socket whose
// lifetime could outlive the database pool.
type sshTunnelDialer struct{ client *ssh.Client }

func (d sshTunnelDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.client == nil {
		return nil, errors.New("SSH 隧道未连接")
	}
	if network == "" {
		network = "tcp"
	}
	return d.client.DialContext(ctx, network, address)
}

// funcDialer is the common adapter type accepted by the MySQL and Oracle
// drivers.  It is also intentionally usable in tests without opening an SSH
// connection.
type funcDialer struct{ dial configurations.DialerContext }

func (d funcDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.dial == nil {
		return nil, errors.New("数据库拨号器未配置")
	}
	return d.dial.DialContext(ctx, network, address)
}

func readTLSMaterial(path, label string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("读取%s失败: %w", label, err)
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxTLSMaterialBytes {
		return nil, fmt.Errorf("%s大小非法", label)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取%s失败: %w", label, err)
	}
	return data, nil
}

// tlsConfigForSource builds one TLS config per source.  Files are read only at
// pool creation, and the returned config is immutable afterwards.  This keeps
// reconnects cheap on low-resource desktop machines while supporting private
// CAs and mutual TLS without storing certificate contents in the source store.
func tlsConfigForSource(source Source) (*tls.Config, error) {
	if source.TLSMode == "disabled" && source.TLSCAFile == "" && source.TLSClientCertFile == "" && source.TLSClientKeyFile == "" {
		return nil, nil
	}
	serverName := strings.TrimSpace(source.TLSServerName)
	if serverName == "" {
		serverName, _ = sourceConnectionTarget(source)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if source.TLSMode == "skip-verify" {
		cfg.InsecureSkipVerify = true // explicit source-level choice
	}
	caBytes, err := readTLSMaterial(source.TLSCAFile, "TLS CA 文件")
	if err != nil {
		return nil, err
	}
	if len(caBytes) > 0 {
		pool, poolErr := x509.SystemCertPool()
		if poolErr != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, errors.New("TLS CA 文件中没有可识别的 PEM 证书")
		}
		cfg.RootCAs = pool
	}
	if source.TLSClientCertFile != "" || source.TLSClientKeyFile != "" {
		certPEM, certErr := readTLSMaterial(source.TLSClientCertFile, "TLS 客户端证书")
		if certErr != nil {
			return nil, certErr
		}
		keyPEM, keyErr := readTLSMaterial(source.TLSClientKeyFile, "TLS 客户端私钥")
		if keyErr != nil {
			return nil, keyErr
		}
		cert, pairErr := tls.X509KeyPair(certPEM, keyPEM)
		if pairErr != nil {
			return nil, fmt.Errorf("解析 TLS 客户端证书失败: %w", pairErr)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// openSSHTunnel authenticates a jump host using a separate credentials entry.
// The target is intentionally not dialled here: each database connection
// obtains its own SSH channel through the one pooled SSH transport.
func openSSHTunnel(ctx context.Context, source Source) (*sshclient.Client, error) {
	return openSSHTunnelWithPassword(ctx, source, "")
}

func openSSHTunnelWithPassword(ctx context.Context, source Source, customPassword string) (*sshclient.Client, error) {
	t := source.SSHTunnel
	if t == nil || !t.Enabled {
		return nil, nil
	}
	password := customPassword
	if password == "" {
		var err error
		password, err = credentials.GetResource(SSHCredentialNamespace, source.ID, t.Username)
		if err != nil {
			return nil, fmt.Errorf("读取 SSH 隧道凭据失败: %w", err)
		}
	}
	timeout := source.Timeout()
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	return sshclient.Dial(ctx, sshclient.Server{
		Name: source.Name + " database tunnel",
		Host: t.Host, Port: t.Port, Username: t.Username,
		HostKeySHA256:        t.HostKeySHA256,
		SSHProfile:           t.SSHProfile,
		AllowInsecureHostKey: t.AllowInsecureHostKey,
	}, sshclient.Credentials{Password: password}, timeout)
}

