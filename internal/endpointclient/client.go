// Package endpointclient 通用「主备切换 HTTP 客户端」, 给 Kairo 内部所有
// 调 Java / 第三方 HTTP 后端的代码用。
//
// 设计目标:
//   - 一行配置就能注册一个 endpoint, 后续调 Call() 自动主备切换
//   - 主备都挂才报错, 错误信息包含所有失败原因
//   - 内部状态线程安全
//   - 永远不返回 Go 业务错误, 业务错误由调用方解析 resp.Body
//
// 不管什么:
//   - URL 上的 query 参数 (各家 endpoint 参数 schema 不同, 由调用方自己拼)
//   - 响应体格式 (返回 *http.Response, 调用方 io.ReadAll 后自己反序列化)
//   - 鉴权方式 (现在只支持 Basic Auth, 未来要 Bearer / 自签就再加字段)
//
// 用法:
//
//	// 1. main.go 启动时注册
//	endpointclient.Register("license_activate", endpointclient.Config{
//	    Primary:   "http://66.0.34.199:9080/credit/httpInterface?...",
//	    Secondary: "http://66.0.34.198:9080/credit/httpInterface?...",
//	    Auth:      "anN5aDpqc3loQDEyMw==",
//	    Timeout:   5 * time.Second,
//	})
//
//	// 2. 业务代码调
//	resp, err := endpointclient.Call(cfg, body)
//	if err != nil { return err }
//	defer resp.Body.Close()
//	// 自己解析 resp.Body
package endpointclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 一次外部调用的配置。
//
// Primary 是必填的 (空字符串 Call 会直接报错)。
// Secondary 可选, 留空就只试 Primary。
// Auth 是 base64 串, 不含 "Basic " 前缀; 空 = 不带 Authorization 头。
// Timeout 0 = 默认 5 秒。
type Config struct {
	Primary   string
	Secondary string
	Auth      string
	Timeout   time.Duration
}

// DefaultTimeout 没填 Timeout 时的默认值
const DefaultTimeout = 5 * time.Second

// Call POST JSON body 到 cfg 配置的 endpoint, 主备自动切换。
//
// 行为:
//   - cfg.Primary 必填, 空直接报错
//   - 先打 Primary, 失败 (网络错 / 4xx / 5xx / 超时) 自动打 Secondary
//   - 都没有就返回合并错误信息 (含所有失败 URL + 原因)
//   - 2xx 才算成功, 返回 *http.Response 给调用方读 Body
//
// 调用方约定:
//   - resp.Body 必须自己 Close()
//   - 业务响应 (即使 {ok: false}) 都是 2xx, 直接解析 Body 即可
//   - 4xx 5xx (Java 端内部错误 / 网关错) 算 Call 失败
func Call(cfg Config, body []byte) (*http.Response, error) {
	// Primary 必填, 空直接报"配置错误", 不去打 secondary (避免用户配错被静默路由)
	if cfg.Primary == "" {
		return nil, fmt.Errorf("endpointclient: primary 必填 (secondary 单独存在不算, 必须有 primary)")
	}

	urls := buildURLs(cfg)

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// 聚合所有失败信息, 方便一次看到所有问题
	var errs []string
	for i, u := range urls {
		resp, err := doOne(u, cfg.Auth, body, timeout)
		if err == nil {
			return resp, nil
		}
		errs = append(errs, fmt.Sprintf("[%d/%d] %s: %v", i+1, len(urls), u, err))
	}
	return nil, fmt.Errorf("endpointclient: 所有地址都失败: %s", strings.Join(errs, "; "))
}

// buildURLs 返回按 [primary, secondary] 顺序的非空 URL 列表
func buildURLs(cfg Config) []string {
	var urls []string
	if cfg.Primary != "" {
		urls = append(urls, cfg.Primary)
	}
	if cfg.Secondary != "" {
		urls = append(urls, cfg.Secondary)
	}
	return urls
}

// doOne 真正的单次 POST, 不切换备地址
func doOne(url, auth string, body []byte, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	// Content-Type 跟对方 /credit/httpInterface 接口文档要求一致
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Basic "+auth)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// 4xx 5xx 视为失败, 关掉 Body 后返回 error 让主备切换走下一个
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 读 body 拿到错误信息, 方便主备错误日志聚合
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return resp, nil
}
