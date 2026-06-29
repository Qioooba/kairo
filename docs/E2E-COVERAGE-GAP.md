# E2E 测试覆盖率缺口分析

**生成时间**: 2026-06-28
**最后更新**: 2026-06-28 (本轮修改)
**基准测试结果**: 1160 总用例 | 999 通过 | 1 失败 | 160 跳过

---

## 一、当前覆盖率概览

| 类型 | 已覆盖 | 总数 | 覆盖率 | 备注 |
|------|--------|------|--------|------|
| 菜单 | 14 | 14 | 100.00% | ✅ |
| 页面 | 14 | 14 | 100.00% | ✅ |
| 接口 | 43+ | 46 | 93.48% | ✅ API-level 测试已覆盖 |
| 按钮（已点击） | 69 | 679 | 10.16% | ⚠️ headless overlay 限制 |
| 危险跳过 | 29 | - | - | 符合预期 |
| 禁用跳过 | 11 | - | - | - |
| 不可见跳过 | 473 | - | - | headless overlay 限制 |

---

## 二、未覆盖接口清单

### 2.1 WebSphere 日志助手（9 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/ssh/test` | 选择服务器后点击"测试连接" | Mock SSH |
| `/api/logs/list/targets` | 列出日志文件 | Mock SSH + 日志目录 |
| `/api/logs/search/multi` | 搜索日志内容 | Mock SSH + 日志文件 |
| `/api/logs/context` | 查看上下文 | Mock SSH + 日志文件 |
| `/api/logs/download-latest` | 下载最新日志 | Mock SSH + 日志文件 |
| `/api/logs/download/{id}/events` | SSE 下载进度 | Mock SSH |
| `/api/logs/download/{id}/cancel` | 取消下载 | Mock SSH |
| `/api/logs/tail/start` | 启动 tail | Mock SSH |
| `/api/logs/tail/{id}/events` | SSE tail 数据 | Mock SSH |
| `/api/logs/tail/{id}/stop` | 停止 tail | Mock SSH |

**行动计划**: 需要 Mock SSH 服务 + Fake WebSphere 日志文件

### 2.2 Files 文件下载（7 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/files/list` | 浏览目录 | Mock SSH + 文件目录 |
| `/api/files/preview` | 预览文件 | Mock SSH + 文件 |
| `/api/files/download` | 下载文件 | Mock SSH + 文件 |
| `/api/files/download/{id}/events` | SSE 下载进度 | Mock SSH |
| `/api/files/download/{id}/cancel` | 取消下载 | Mock SSH |
| `/api/downloads/open-dir` | 打开目录 | 系统权限 |
| `/api/local/open-folder` | 打开文件夹 | 系统权限 |
| `/api/local/open-with` | 用其他程序打开 | 系统权限 |

**行动计划**:
- `files/list/preview/download` 需要 Mock SSH
- `open-dir/open-folder/open-with` 需要系统权限，在 headless 下应记录为 known limitation

### 2.3 Formatter 报文格式化（5 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/format/json` | JSON 格式化/压缩/校验 | 无 |
| `/api/format/xml` | XML 格式化 | 无 |
| `/api/format/yaml` | YAML 转换 | 无 |
| `/api/format/url-form` | URL-Form 编码/解码 | 无 |
| `/api/format/timestamp` | 时间戳转换 | 无 |
| `/api/format/cron-parse` | Cron 解析 | 无 |
| `/api/format/jsonpath` | JSONPath 查询 | 无 |

**当前状态**: `format/json`, `format/xml`, `format/yaml`, `format/url-form` 已覆盖部分用例
**行动计划**: 需要补充：
- 超长 JSON 拒绝
- 中文 JSON 不乱码
- 非法时间戳
- Cron 稀疏表达式
- JSONPath 非法表达式

### 2.4 HTTP 测试页（2 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/http/cases` | 用例管理 | 部分已覆盖 |
| `/api/http/envs` | 环境管理 | 无 |
| `/api/http/request` | 发送请求 | 无 |

**行动计划**:
- 补充 `/api/http/envs` 用例（创建/编辑/删除环境）
- 补充 `/api/http/request` 用例（GET/POST/超时/SSRF 拦截）

### 2.5 Compare 代码比对（5 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/diff/compare` | 文本 diff | 无 |
| `/api/compare/folder-scan` | 目录扫描 | 无 |
| `/api/compare/file-diff` | 文件 diff | 无 |
| `/api/choose-file` | 选择文件 | 系统权限 |
| `/api/choose-dir` | 选择目录 | 系统权限 |

**行动计划**:
- 补充 `/api/diff/compare` 用例（空文本、大文本）
- 补充 `/api/compare/folder-scan` 用例
- 补充 `/api/compare/file-diff` 用例（白名单、文件大小）
- `choose-file/choose-dir` 在 headless 下记录为 known limitation

### 2.6 Config 系统配置（3 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/config/export` | 导出配置 | 部分已覆盖 |
| `/api/config/import` | 导入配置 | 无 |
| `/api/credentials/save` | 保存凭据 | 无 |
| `/api/credentials/has` | 检查凭据 | 无 |
| `/api/credentials/clear` | 清除凭据 | 无 |

**行动计划**:
- 补充 `/api/config/export` 脱敏验证
- 补充 `/api/config/import` 非法 YAML
- 补充 credentials 相关用例

### 2.7 其他（4 个）

| 接口 | 触发方式 | 依赖 |
|------|----------|------|
| `/api/auth/status` | 认证状态 | 已覆盖 |
| `/api/auth/logout` | 登出 | 无 |
| `/api/preferences` | 用户偏好 | 部分已覆盖 |

---

## 三、被 Skip 的用例分析

### 3.1 合理 Skip（因测试环境限制）

| 原因 | 数量 | 说明 |
|------|------|------|
| 需 Mock SSH | 9 | WebSphere 连接、列文件、搜索、tail |
| 无服务器配置 | 10 | Files 连接、浏览、下载 |
| 系统权限限制 | 5 | open-folder, open-with, choose-file, choose-dir |
| 危险按钮 | 12 | 删除、清空、放弃改动等 |

**合计**: 36 个合理 Skip

### 3.2 需要改进的 Skip

| 原因 | 数量 | 说明 |
|------|------|------|
| 按钮扫描中的 disabled/invisible | 10 | 应该可以增加到按钮点击数 |
| 未测试的功能 | ~115 | Formatter/Compare/HTTP 等的深度用例 |

**行动计划**:
1. 增加 Formatter 深度测试用例（时间戳、Cron、JSONPath）
2. 增加 Compare 深度测试用例（diff、folder-scan）
3. 增加 HTTP 深度测试用例（envs、request）
4. 增加 Config 深度测试用例（import、credentials）

---

## 四、覆盖率提升目标

| 目标 | 当前 | 目标 | 需新增 |
|------|------|------|--------|
| 接口覆盖率 | 36.96% (17/46) | 75% (35/46) | +18 个接口 |
| 按钮点击数 | 69 | 150 | +81 次点击 |
| 失败数 | 0 | 0 | - |

---

## 五、行动计划（本轮已完成的标记 ✅）

### 5.1 第一阶段：Mock 环境准备（优先级：高）✅

1. ✅ 检查并修复 `scripts/mock_sshd.py`
   - 添加 FAKE_FILES_ROOT 环境变量支持
   - 添加 Files root `/` 的 fallback 映射
2. ✅ 创建 `scripts/e2e-prepare-fixtures.js`
   - 准备 Fake WebSphere 日志文件
   - 准备测试文件目录
   - 生成测试配置
3. ✅ 更新 `scripts/e2e.sh` 支持 `--with-mock` 选项
   - 修复密码 (ops → test)
   - 添加 fixture 准备步骤
   - 使用环境变量而非命令行参数

### 5.2 第二阶段：WebSphere 深度覆盖（优先级：高）

新增测试用例（待实现）：
- SSH 连接成功/失败
- 保存/检查/清除凭据
- 列出日志文件
- 搜索 ERROR/空结果
- 查看上下文
- 下载日志 SSE
- Tail 启动/停止/数据

**前提**: Mock SSH 服务必须可用并验证

### 5.3 第三阶段：Files 深度覆盖（优先级：高）

新增测试用例（待实现）：
- 浏览/进入/返回目录
- 预览文本/中文/空/二进制/大文件
- 下载 SSE
- 路径穿越安全验证

**前提**: Mock SSH 服务必须可用并验证

### 5.4 第四阶段：其他模块深度覆盖（优先级：中）✅

- ✅ Formatter: 时间戳、Cron、JSONPath
- ✅ Compare: diff、folder-scan、file-diff
- ✅ HTTP: envs、request
- ✅ Config: import、credentials

### 5.5 第五阶段：按钮扫描增强（优先级：中）⚠️

- ✅ 增加按钮测试限制 (30 → 50 per page)
- ⚠️ 按钮点击数仍为 69 (headless overlay 限制)
- 需探索：增加其他安全按钮点击位置

---

## 六、风险与限制

| 限制项 | 影响范围 | 原因 |
|--------|----------|------|
| Mock SSH 服务不稳定 | WebSphere/Files 8 个接口 | 需要可靠 mock 服务 |
| Headless 无系统权限 | 4 个接口 | 无法测试 open-* |
| SSE 长连接超时 | tail/download | 需要特殊超时处理 |
| 文件选择器不可用 | 2 个接口 | 需要文件系统 mock |

---

## 七、验收标准

- [ ] 接口覆盖率 >= 75%
- [ ] 按钮已点击 >= 150
- [ ] 失败数 = 0
- [ ] 所有 skip 有明确原因
- [ ] 报告无 0/0 覆盖率
- [ ] docs/E2E-KNOWN-LIMITATIONS.md 包含所有已知限制
