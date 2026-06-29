# 豆包工具箱 测试覆盖矩阵

> 用途：逐项核对全菜单 / 全页面 / 全接口 / 全主题 / 全分辨率是否被测试覆盖
> 配套文件：`docs/ISSUES-FOUND.md`（问题清单）、`docs/MANUAL-CLICK-CASES.md`（手工点击用例）

---

## 一、菜单与页面覆盖矩阵

| # | 菜单名 | 路由 | 页面文件 | 菜单可见 | 首次进入报错 | Loading | 成功 | 失败 | 空数据 | 重复点击 | 长路径溢出 | 主题可读 | 半屏布局 | 状态 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 首页 | #/home | home.js | ✅ | ✅ | — | ✅ | — | — | — | ✅ | ✅ | ✅ | OK |
| 2 | 日志助手 | #/websphere | websphere.js | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️见FE-005 | ✅ | ✅ | ✅ | OK |
| 3 | FTP文件下载 | #/files | files.js | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️长文件名 | ✅ | ✅ | OK |
| 4 | 报文格式化 | #/formatter | formatter.js | ✅ | ✅ | — | ✅ | ✅ | — | — | — | ✅ | ✅ | OK |
| 5 | HTTP 测试 | #/http | http.js | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️见FE-017 | — | ✅ | ✅ | OK |
| 6 | 常用命令 | #/commands | commands.js | ✅ | ✅ | — | ✅ | — | — | — | — | ✅ | ✅ | OK |
| 7 | 环境自检 | #/diagnostics | diagnostics.js | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — | — | ✅ | ✅ | OK |
| 8 | 系统配置 | #/config | config.js | ✅ | ✅ | ✅ | ✅ | ⚠️见FE-004 | — | ⚠️见FE-009 | — | ✅ | ⚠️见FE-007 | OK |
| 9 | 下载历史 | #/downloads | downloads.js | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️见FE-009 | ✅ | ✅ | ✅ | OK |
| 10 | 操作历史 | #/history | history.js | ❌已隐藏 | ⚠️见FE-001 | — | — | ❌见FE-001 | — | — | — | — | — | **P0** |
| 11 | 时间戳 | #/timestamp | timestamp.js | ✅ | ✅ | — | ✅ | ✅ | — | ⚠️见FE-013 | — | ✅ | ✅ | OK |
| 12 | Cron 解析 | #/cron | cron.js | ✅ | ✅ | — | ✅ | ⚠️见BE-015 | — | ⚠️见FE-013 | — | ✅ | ✅ | OK |
| 13 | JSONPath | #/jsonpath | jsonpath.js | ✅ | ✅ | — | ✅ | ✅ | — | — | — | ✅ | ✅ | OK |
| 14 | 代码比对 | #/compare | compare.js | ✅ | ✅ | ✅ | ✅ | ⚠️见BE-001 | — | — | — | ✅ | ✅ | **P0** |
| 15 | 关于 | #/about | about.js | ✅ | ✅ | — | ✅ | — | — | — | — | ✅ | ✅ | ⚠️见FE-006 |
| 16 | 独立 tail | tail.html | tail.js | 独立窗口 | ✅ | ✅ | ✅ | ✅ | — | ⚠️见FE-005 | — | ✅ | ✅ | OK |
| 17 | 独立 preview | preview.html | preview.html | 独立窗口 | ⚠️见FE-002 | — | ✅ | ⚠️见FE-003 | — | — | — | ⚠️见FE-002 | ✅ | **P1** |

**菜单隐藏问题汇总**：
- 「操作历史」菜单项在 `web/index.html:62` 被注释，但 `web/pages/history.js` 仍在 `index.html:104` 加载，且首页卡片（`home.js:48`）仍可跳转到 `#/history`，形成"僵尸页面"（FE-001）

---

## 二、按钮覆盖矩阵（按页面）

### 2.1 首页（home.js）
| 按钮 | 行为 | 禁用态 | Loading | 重复点击 | 状态 |
|---|---|---|---|---|---|
| 14 张功能卡片 | 跳转到对应路由 | — | — | — | OK（除 history 卡片 FE-020） |

### 2.2 日志助手（websphere.js）
| 按钮 | 行为 | 禁用态 | Loading | 重复点击 | 状态 |
|---|---|---|---|---|---|
| 测试连接 | POST /api/ssh/test | ✅ | ✅ | ✅ | OK |
| 列文件 | POST /api/logs/list/targets | ✅ | ✅ | ✅ | OK |
| 搜索 | POST /api/logs/search/multi | ✅ | ✅ | ✅ | OK |
| 查看上下文 | POST /api/logs/context | ✅ | ✅ | ✅ | OK |
| 下载最新 | POST /api/logs/download-latest | ✅ | ✅ | ✅ | OK |
| 取消下载 | POST /api/logs/download/{id}/cancel | ✅ | — | ✅ | OK |
| 开启 Tail | POST /api/logs/tail/start | ✅ | ✅ | ⚠️见BE-002 | OK |
| 停止 Tail | POST /api/logs/tail/{id}/stop | ✅ | — | ✅ | OK |
| 复制路径 | navigator.clipboard | — | — | — | OK |
| 在文件夹中显示 | POST /api/local/open-folder | — | — | — | OK |
| 用外部程序打开 | POST /api/local/open-with | — | — | — | OK |
| 保存凭据 | POST /api/credentials/save | ✅ | ✅ | ✅ | OK |
| 清除凭据 | POST /api/credentials/clear | ✅ | — | ✅ | OK |

### 2.3 FTP文件下载（files.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 列目录 | POST /api/files/list | OK |
| 预览 | POST /api/files/preview | OK |
| 下载 | POST /api/files/download | OK |
| 取消下载 | POST /api/files/download/{id}/cancel | OK |
| 打开目录 | POST /api/downloads/open-dir | OK |
| 保存凭据 | POST /api/credentials/save | OK |

### 2.4 报文格式化（formatter.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 格式化 | POST /api/format/{json,xml,yaml,url-form} | OK |
| 复制结果 | document.execCommand('copy') | ⚠️ FE-008（应改 navigator.clipboard） |
| 压缩 | 本地处理 | OK |

### 2.5 HTTP 测试（http.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 发送请求 | POST /api/http/request | OK |
| 保存用例 | POST /api/http/cases | OK |
| 删除用例 | DELETE /api/http/cases | OK |
| 保存环境 | POST /api/http/envs | OK |
| 导入 cURL | 本地解析 | OK |
| 复制 cURL | navigator.clipboard | OK |
| 重复 header | kvArrayToMap | ⚠️ FE-017 静默丢弃 |

### 2.6 常用命令（commands.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 复制命令 | navigator.clipboard | OK |
| 分类筛选 | 本地过滤 | OK |
| 收藏 | localStorage | OK |

### 2.7 环境自检（diagnostics.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 运行自检 | GET /api/diagnostics | OK |

### 2.8 系统配置（config.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 添加业务系统 | 本地编辑 | OK |
| 删除业务系统 | confirm() | ⚠️ FE-009（应改 confirmDialog） |
| 添加服务器 | 本地编辑 | OK |
| 添加日志目录 | 本地编辑 | OK |
| 选择目录 | POST /api/choose-dir | OK |
| 添加打开器 | 本地编辑 | OK |
| 保存 | PUT /api/admin/servers | OK |
| 导出配置 | fetch('/api/config/export') | ⚠️ FE-004（裸 fetch） |
| 导入配置 | fetch('/api/config/import') | ⚠️ FE-004（裸 fetch） |
| 重置 | 本地处理 | OK |
| 下载保留配置 | PUT /api/admin/download-retention | OK |

### 2.9 下载历史（downloads.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 刷新 | GET /api/downloads/list | OK |
| 删除单个 | DELETE /api/downloads/{name} | ⚠️ FE-009（confirm） |
| 全部删除 | POST /api/downloads/all | ⚠️ FE-009（confirm） |
| 打开目录 | POST /api/downloads/open-dir | OK |
| 用外部打开 | POST /api/local/open-with | OK |

### 2.10 操作历史（history.js）⚠️ FE-001 僵尸页
| 按钮 | 行为 | 状态 |
|---|---|---|
| 刷新 | GET /api/audit/recent | ❌ 路由可能下线 |
| 导出 CSV | GET /api/audit/export.csv | ❌ 路由可能下线 |
| 导出 JSON | GET /api/audit/export.json | ❌ 路由可能下线 |
| 自动刷新 | setInterval(loadHistory, 3000) | ⚠️ FE-014（路由切换不清理） |

### 2.11 时间戳（timestamp.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 转换 | POST /api/format/timestamp | ⚠️ FE-013（异步竞态） |
| 复制 | navigator.clipboard | OK |

### 2.12 Cron 解析（cron.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 解析 | POST /api/format/cron-parse | ⚠️ BE-015（稀疏表达式漏算）+ FE-013（异步竞态） |
| 复制 | navigator.clipboard | OK |

### 2.13 JSONPath（jsonpath.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 提取 | POST /api/format/jsonpath | OK |
| 复制结果 | document.execCommand('copy') | ⚠️ FE-008 |

### 2.14 代码比对（compare.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| 文本比对 | POST /api/diff/compare | OK |
| 文件夹扫描 | POST /api/compare/folder-scan | ❌ BE-001（任意目录读） |
| 文件 diff | POST /api/compare/file-diff | ❌ BE-001（任意文件读）+ BE-010/011（OOM） |
| 选择目录 | POST /api/choose-dir | OK |
| 展开全部/折叠 | 本地处理 | ⚠️ FE-012（切回页面陈旧数据） |
| 新窗口打开 | document.write | ⚠️ FE-021 |

### 2.15 关于（about.js）
| 按钮 | 行为 | 状态 |
|---|---|---|
| — | 仅展示 | ⚠️ FE-006（版本号硬编码） |

---

## 三、接口覆盖矩阵（57 个路由）

### 3.1 认证类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/auth/status | GET/POST | httpserver.go:138 | auth.js:64,173 | 免鉴权 | OK |
| /api/auth/logout | POST | httpserver.go:142 | auth.js:142 | 免鉴权 | OK |

### 3.2 配置类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/config | GET | httpserver.go:175 | app.js:81 等 | ✅ | OK |
| /api/config/export | GET | httpserver.go:177 | config.js:687 | ✅ | ⚠️ BE-004 脱敏不全 |
| /api/config/import | POST | httpserver.go:179 | config.js:751 | ✅ | ⚠️ BE-003 无 RBAC |
| /api/admin/servers | GET/PUT | httpserver.go:231 | config.js:568 等 | ✅ | ⚠️ BE-003 无 RBAC |
| /api/admin/openers | GET/PUT | httpserver.go:261 | config.js:442 等 | ✅ | ⚠️ BE-003 无 RBAC |
| /api/admin/download-retention | GET/PUT | httpserver.go:263 | config.js:591 等 | ✅ | ⚠️ BE-003 无 RBAC |

### 3.3 SSH/日志类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/ssh/test | POST | httpserver.go:181 | websphere.js:780 | ✅ | OK |
| /api/logs/list | POST | httpserver.go:183 | 无（仅脚本） | ✅ | ⚠️ API-001 死路由 |
| /api/logs/list/targets | POST | httpserver.go:185 | websphere.js:807 等 | ✅ | OK |
| /api/logs/search | POST | httpserver.go:189 | 无（仅脚本） | ✅ | ⚠️ API-002 死路由 |
| /api/logs/search/multi | POST | httpserver.go:191 | websphere.js:1706 | ✅ | OK |
| /api/logs/context | POST | httpserver.go:193 | websphere.js:1816 | ✅ | OK |
| /api/logs/download-latest | POST | httpserver.go:187 | websphere.js:1385 | ✅ | OK |
| /api/logs/download/{id}/events | GET(SSE) | httpserver.go:268 | websphere.js:1395 | ✅ | OK |
| /api/logs/download/{id}/cancel | POST | httpserver.go:268 | websphere.js:1318 | ✅ | OK |
| /api/logs/tail/start | POST | httpserver.go:195 | websphere.js:2616 | ✅ | OK |
| /api/logs/tail/{id}/events | GET(SSE) | httpserver.go:270 | websphere.js:2627 | ✅ | ❌ BE-002 5分钟强断 |
| /api/logs/tail/{id}/stop | POST | httpserver.go:270 | websphere.js:2768 | ✅ | OK |

### 3.4 文件类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/files/list | POST | httpserver.go:239 | files.js:390 | ✅ | ⚠️ BE-007 默认无限制 |
| /api/files/preview | POST | httpserver.go:241 | files.js:424 | ✅ | ⚠️ BE-007 |
| /api/files/download | POST | httpserver.go:243 | files.js:914 | ✅ | ⚠️ BE-007 |
| /api/files/download/{id}/events | GET(SSE) | httpserver.go:265 | files.js:922 | ✅ | ⚠️ BE-009 双重 done |
| /api/files/download/{id}/cancel | POST | httpserver.go:265 | files.js:1080 | ✅ | OK |

### 3.5 下载历史类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/downloads/list | GET | httpserver.go:211 | downloads.js:75 | ✅ | OK |
| /api/downloads/all | POST | httpserver.go:213 | downloads.js:218 | ✅ | OK |
| /api/downloads/{name} | DELETE | httpserver.go:213 | downloads.js:198 | ✅ | OK |
| /api/downloads/{name}/open-dir | POST | httpserver.go:213 | files.js:1175 | ✅ | OK |
| /api/downloads/open-dir | POST | httpserver.go:213 | files.js:1025 | ✅ | OK |

### 3.6 格式化类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/format/json | POST | httpserver.go:215 | formatter.js:182 | ✅ | OK |
| /api/format/xml | POST | httpserver.go:217 | formatter.js:192 | ✅ | OK |
| /api/format/yaml | POST | httpserver.go:219 | formatter.js:199 | ✅ | OK |
| /api/format/url-form | POST | httpserver.go:221 | formatter.js:209 | ✅ | OK |
| /api/format/timestamp | POST | httpserver.go:223 | timestamp.js:98 | ✅ | OK |
| /api/format/cron-parse | POST | httpserver.go:225 | cron.js:88 | ✅ | ⚠️ BE-015 |
| /api/format/jsonpath | POST | httpserver.go:227 | jsonpath.js:104 | ✅ | OK |

### 3.7 比对类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/diff/compare | POST | httpserver.go:229 | compare.js:366 | ✅ | OK |
| /api/compare/folder-scan | POST | httpserver.go:257 | compare.js:673 | ✅ | ❌ BE-001 任意目录读 |
| /api/compare/file-diff | POST | httpserver.go:259 | compare.js:702 | ✅ | ❌ BE-001 任意文件读 |

### 3.8 HTTP 测试类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/http/cases | GET/POST/DELETE | httpserver.go:233 | http.js:235 等 | ✅ | OK |
| /api/http/envs | GET/POST | httpserver.go:235 | http.js:248 | ✅ | OK |
| /api/http/request | POST | httpserver.go:237 | http.js:777 | ✅ | ⚠️ BE-017 不审计 + BE-018 SSRF 轻微 |

### 3.9 审计类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/audit/recent | GET | httpserver.go:197 | history.js:48 | ✅ | ⚠️ BE-014 死路由 |
| /api/audit/export.csv | GET | httpserver.go:199 | history.js | ✅ | ⚠️ BE-014 |
| /api/audit/export.json | GET | httpserver.go:201 | history.js | ✅ | ⚠️ BE-014 |

### 3.10 其他类
| 接口 | 方法 | 后端路由 | 前端调用 | 鉴权 | 状态 |
|---|---|---|---|---|---|
| /api/diagnostics | GET | httpserver.go:203 | diagnostics.js:41 | ✅ | OK |
| /api/credentials/save | POST | httpserver.go:205 | files.js:345 | ✅ | ⚠️ BE-013 AAD 弱化 |
| /api/credentials/has | GET | httpserver.go:207 | files.js:257 | ✅ | OK |
| /api/credentials/clear | POST | httpserver.go:209 | websphere.js:748 | ✅ | ⚠️ BE-003 无 RBAC |
| /api/preferences | GET/PUT | httpserver.go:247 | app.js:94 等 | ✅ | OK |
| /api/local/reveal-file | POST | httpserver.go:245 | websphere.js:1616 | ✅ | OK |
| /api/local/open-folder | POST | httpserver.go:249 | websphere.js:1623 | ✅ | OK |
| /api/local/open-with | POST | httpserver.go:251 | downloads.js:185 | ✅ | ⚠️ BE-008 误拒..文件名 |
| /api/choose-file | POST | httpserver.go:253 | config.js:472 | ✅ | OK |
| /api/choose-dir | POST | httpserver.go:255 | compare.js:922 | ✅ | OK |
| /downloads/{file} | GET | httpserver.go:272 | 浏览器直链 | ✅ | ⚠️ BE-021 误拒..文件名 |

**核对结论**：
- 前端调用但后端没注册（404 风险）：**0 个**
- 后端注册但前端无调用（死路由）：**5 个**（`/api/logs/list`、`/api/logs/search`、`/api/audit/recent`、`/api/audit/export.csv`、`/api/audit/export.json`）
- README 声明但不存在的接口：**1 个**（`POST /api/logs/download`，API-003）

---

## 四、主题覆盖矩阵

| 主题 | data-theme | 首页 | 日志助手 | 文件下载 | 报文格式化 | HTTP测试 | 常用命令 | 环境自检 | 系统配置 | 下载历史 | 时间戳 | Cron | JSONPath | 代码比对 | 关于 | tail.html | preview.html |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| dark | dark | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| light | light | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ FE-002 |
| green | green | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ FE-002 |
| 高对比 hc | hc | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ FE-002 |

**结论**：preview.html 在 light/green/hc 主题下不跟随主窗口（FE-002）

---

## 五、分辨率覆盖矩阵

| 分辨率 | 首页 | 日志助手 | 文件下载 | 系统配置 | 下载历史 | 代码比对 | HTTP测试 | 关于 |
|---|---|---|---|---|---|---|---|---|
| 1366×768 | ✅ | ✅ | ✅ | ⚠️ FE-007 footer | ✅ | ✅ | ✅ | ✅ |
| 1920×1080 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 2K (2560×1440) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 半屏 (960×1080) | ✅ | ✅ | ✅ | ⚠️ FE-007 | ✅ | ⚠️ 长文件名 | ✅ | ✅ |

**结论**：cfg-save-footer-bar 硬编码 240px sidebar 宽度（FE-007），半屏 + 配置页有错位风险

---

## 六、异常场景覆盖矩阵

| 场景 | 首页 | 日志助手 | 文件下载 | 系统配置 | 下载历史 | 代码比对 | HTTP测试 | 关于 |
|---|---|---|---|---|---|---|---|---|
| 401 未授权 | ✅ auth overlay | ✅ | ✅ | ⚠️ FE-004 裸fetch | ✅ | ✅ | ✅ | ✅ |
| 403 禁止 | ✅ toast | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 404 不存在 | ✅ toast | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 500 服务器错 | ✅ toast | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 网络断开 | ✅ toast | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| SSE 断开 | — | ✅ 重连提示 | ✅ | — | — | — | — | — |
| 空值输入 | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ BE-001 无白名单 | ✅ | — |
| 超长输入 | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ BE-010 OOM | ✅ | — |
| 特殊字符 | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | ✅ | — |
| 中文 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

---

## 七、安全边界覆盖矩阵

| 检查项 | 状态 | 说明 |
|---|---|---|
| 仅监听 127.0.0.1 | ✅ | main.go:152 + config.go Validate |
| 跨源校验 | ✅ | httpserver.go:130 allowLocalOrigin |
| X-Content-Type-Options: nosniff | ✅ | httpserver.go:125 |
| X-Frame-Options: DENY | ✅ | httpserver.go:127 |
| Referrer-Policy: no-referrer | ✅ | httpserver.go:126 |
| Bearer token 认证 | ✅ | httpserver.go:147-166 |
| IP 白名单（CIDR） | ✅ | config.go AuthToken.IPAllowed |
| 路径穿越防护（..） | ⚠️ | BE-008/021 子串误伤 + compare 无防护 |
| SSH host key 校验 | ❌ | BE-005 默认 InsecureIgnoreHostKey |
| 配置导出脱敏 | ⚠️ | BE-004 仅单行格式 |
| 凭据 AAD 绑定 | ⚠️ | BE-013 双路径兼容弱化 |
| RBAC 角色分级 | ❌ | BE-003 无角色概念 |
| 文件浏览白名单 | ❌ | BE-007 默认空=无限制 |
| 下载目录白名单 | ❌ | BE-007 默认空=无限制 |
| compare 路径白名单 | ❌ | BE-001 完全无防护 |
| SSRF 防护 | ✅ | handlers_http_request.go isBlockedHTTPIP |
| 命令注入防护 | ✅ | exec.CommandContext 不走 shell |
| 审计日志覆盖 | ⚠️ | BE-017 HTTP/compare 不审计 |
| 本地文件权限 0600 | ⚠️ | BE-016 部分 config.yaml 可能 0644 |
| 0.0.0.0 绑定拒绝 | ❌ | BE-006 auth 启用时放行 |

---

## 八、SSE 长连接覆盖矩阵

| 端点 | 鉴权 | 超时 | 断线重连 | done 事件 | 资源清理 | 状态 |
|---|---|---|---|---|---|---|
| /api/logs/tail/{id}/events | ✅ | ❌ BE-002 5分钟强断 | ✅ 前端提示 | ⚠️ BE-019 双重 | ⚠️ FE-005 interval 不清 | **P0** |
| /api/files/download/{id}/events | ✅ | ⚠️ BE-012 30分钟 IdleGC | ✅ | ⚠️ BE-009 双重 | ✅ | OK |
| /api/logs/download/{id}/events | ✅ | ⚠️ BE-012 | ✅ | ⚠️ BE-009 | ✅ | OK |

---

## 九、测试基线汇总

| 检查项 | 命令 | 结果 |
|---|---|---|
| Go 单测 | `go test -mod=vendor -count=1 ./...` | ✅ 15 包全部通过 |
| 前端单测 | `node web/app.test.js` | ✅ 17 pass / 0 fail |
| JS 语法 | `node --check web/*.js web/pages/*.js` | ✅ 全部通过 |
| gofmt | `gofmt -l` | ❌ 12 文件未格式化（STATIC-001） |
| 离线构建 | tar.gz 解包后 `go test` | ❌ PKG-001 缺 vendor |
| diff2html 加载 | tar.gz 解包后访问 #/compare | ❌ PKG-002 缺 web/vendor |
