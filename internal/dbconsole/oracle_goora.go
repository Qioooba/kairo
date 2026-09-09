package dbconsole

import (
	"crypto/tls"
	"database/sql/driver"
	go_ora "github.com/sijms/go-ora/v2"
	"runtime"
	"strconv"
)

func openOracleViaGoOraStub(source Source, password string, dialer funcDialer, tlsConfig *tls.Config, targetHost string, targetPort int) (driver.Connector, string, error) {
	options := map[string]string{
		"TIMEOUT":            strconv.Itoa(source.QueryTimeoutSeconds),
		"CONNECTION TIMEOUT": "10",
		"LOB FETCH":          "STREAM", // 结论 1.2: STREAM / POST 模式避开 INLINE 错位
		"PREFETCH_ROWS":      "100",
	}
	if source.OracleConnectBy == "sid" {
		options["SID"] = source.OracleService
	}
	if source.OracleClientCharset != "" {
		options["CLIENT CHARSET"] = source.OracleClientCharset
	} else {
		options["CLIENT CHARSET"] = "AL32UTF8"
	}
	if source.TLSMode != "disabled" {
		options["SSL"] = "ENABLE"
		if source.TLSMode == "skip-verify" {
			options["SSL VERIFY"] = "FALSE"
		}
	}
	service := source.OracleService
	if source.OracleConnectBy == "sid" {
		service = ""
	}
	dsnStr := go_ora.BuildUrl(targetHost, targetPort, service, source.Username, password, options)
	connector := go_ora.NewConnector(dsnStr)
	if c, ok := connector.(*go_ora.OracleConnector); ok {
		if dialer.dial != nil { c.Dialer(dialer) }
		if tlsConfig != nil {
			c.WithTLSConfig(tlsConfig)
		}
	}
	return connector, dsnStr, nil
}

func bitnessStub() string {
	if s := runtime.GOARCH; s == "amd64" || s == "arm64" {
		return "64-bit"
	}
	return runtime.GOARCH
}
