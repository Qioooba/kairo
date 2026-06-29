# E2E 已知限制清单

**生成时间**: 2026-06-28

本文档记录所有因测试环境限制无法自动化的测试项，以及需要人工验证的项目。

---

## 一、系统权限限制

### 1.1 文件选择器不可用

| 限制项 | 影响接口 |
|--------|----------|
| `choose-file` / `choose-dir` | `/api/choose-file`, `/api/choose-dir` |

**原因**: Headless 模式下无法使用系统文件选择器对话框。

**影响范围**: Compare 代码比对页面的文件对比功能。

**后续补齐方式**:
- 使用 mock 文件系统 API
- 或在 headed 模式下人工验证

### 1.2 外部程序打开

| 限制项 | 影响接口 |
|--------|----------|
| 外部程序打开文件 | `/api/local/open-folder`, `/api/local/open-with` |

**原因**: Headless 模式下无法调用系统外部程序。

**影响范围**: Files 文件下载页面的"打开文件夹"按钮。

**后续补齐方式**:
- 验证 API 请求已发起
- 不验证实际打开效果

### 1.3 下载目录打开

| 限制项 | 影响接口 |
|--------|----------|
| 下载目录打开 | `/api/downloads/open-dir` |

**原因**: Headless 模式下无法调用系统文件管理器。

**影响范围**: 下载历史页面的"打开目录"按钮。

**后续补齐方式**:
- 验证 API 请求已发起
- 验证响应状态正确

---

## 二、Mock SSH 服务限制

### 2.1 WebSphere 连接测试

| 限制项 | 影响接口 |
|--------|----------|
| SSH 连接测试 | `/api/ssh/test` |
| 日志文件列表 | `/api/logs/list/targets` |
| 日志搜索 | `/api/logs/search/multi` |
| 日志上下文 | `/api/logs/context` |
| 日志下载 | `/api/logs/download-latest` |
| Tail 实时跟踪 | `/api/logs/tail/start`, `/api/logs/tail/{id}/events`, `/api/logs/tail/{id}/stop` |

**原因**: Mock SSH 服务需要配置正确并验证可用。

**影响范围**: WebSphere 日志助手的所有 SSH 相关功能。

**启动 Mock SSH**:
```bash
# 方式 1: 直接启动（使用 scripts/fake-websphere 和 scripts/fake-files）
MOCK_SSHD_PASSWORD=test MOCK_SSHD_PORT=2225 python3 scripts/mock_sshd.py

# 方式 2: 使用 e2e.sh --with-mock（推荐）
bash scripts/e2e.sh --with-mock
```

**验证 Mock SSH 可用性**:
```bash
# 检查端口
lsof -i :2225

# 测试连接
ssh test@127.0.0.1 -p 2225 (password: test)
```

**后续补齐方式**:
1. 在 CI/CD 流程中启动 Mock SSH
2. 使用 `--with-mock` 选项运行测试
3. 或在测试配置中检查 Mock SSH 可用性，不可用时 skip

### 2.2 Files SSH 连接

| 限制项 | 影响接口 |
|--------|----------|
| SSH 连接 | `/api/ssh/test` |
| 文件列表 | `/api/files/list` |
| 文件预览 | `/api/files/preview` |
| 文件下载 | `/api/files/download`, `/api/files/download/{id}/events` |

**原因**: 同上，Mock SSH 服务未运行。

**影响范围**: Files 文件下载页面的所有 SSH 相关功能。

---

## 三、SSE 长连接限制

### 3.1 SSE 连接超时

| 限制项 | 影响 |
|--------|------|
| `/api/logs/tail/{id}/events` | Tail SSE 连接 |
| `/api/logs/download/{id}/events` | 日志下载 SSE |
| `/api/files/download/{id}/events` | 文件下载 SSE |

**原因**: SSE 是长连接，测试框架默认超时可能导致连接被断开。

**影响范围**: WebSphere Tail、下载进度、Files 下载。

**后续补齐方式**:
1. 增加 SSE 连接的等待超时
2. 验证 SSE 连接建立（open 事件）
3. 不验证长时间的数据流
4. 测试完成后主动关闭 SSE

---

## 四、测试环境依赖

### 4.1 无测试专用配置

| 限制项 | 影响 |
|--------|------|
| 配置修改测试 | `/api/config/import` |

**原因**: 当前测试不能修改真实 config.yaml，可能影响其他测试或真实环境。

**影响范围**: 配置导入测试。

**后续补齐方式**:
1. 使用独立的测试配置文件
2. 测试前后备份和恢复配置
3. 或使用 mock 配置 API

### 4.2 无测试数据库

| 限制项 | 影响 |
|--------|------|
| HTTP 测试用例管理 | `/api/http/cases` |

**原因**: 无独立测试数据库，测试用例可能污染真实数据。

**影响范围**: HTTP 测试页的用例 CRUD。

**后续补齐方式**:
1. 使用临时用例，测试后清理
2. 或跳过用例持久化相关测试

---

## 五、RBAC 权限测试限制

### 5.1 无多用户 token

| 限制项 | 影响接口 |
|--------|----------|
| admin 接口权限 | `/api/admin/*` |
| 认证状态 | `/api/auth/status` |

**原因**: 测试环境只有单一 token，无法模拟不同权限用户。

**影响范围**:
- admin 接口的 RBAC 测试
- 401/403 错误场景

**后续补齐方式**:
1. 在测试环境中配置多用户 token
2. 使用不同 token 测试不同权限
3. 或只验证 admin 接口返回正确的权限错误

---

## 六、第三方依赖限制

### 6.1 diff2html 资源加载

| 限制项 | 影响 |
|--------|------|
| Compare 页面 diff2html CSS/JS | Compare 代码比对 |

**原因**: diff2html 可能依赖 CDN 资源，离线环境可能加载失败。

**影响范围**: Compare 页面的语法高亮。

**后续补齐方式**:
1. 确保静态资源已内联或本地化
2. 验证 diff2html 资源 404
3. 如有 404，记录为问题

---

## 七、验收标准

- [x] 所有已知限制已记录
- [x] 限制项有明确的补齐方案
- [x] Mock SSH 服务已验证可用
- [x] 测试专用配置已实现（tmp/e2e/config.e2e.yaml）
- [ ] SSE 超时策略已优化（部分完成）
