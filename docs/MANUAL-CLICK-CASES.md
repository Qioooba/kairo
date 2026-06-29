# 豆包工具箱 手工点击测试用例

> 用途：按菜单顺序逐个点击验证，每个按钮都要点，每个功能都要看请求、响应、页面状态和错误提示
> 配套文件：`docs/ISSUES-FOUND.md`（问题清单）、`docs/TEST-MATRIX.md`（覆盖矩阵）
> 执行前置：服务已启动在 http://127.0.0.1:18080，config.yaml 已配置至少一台可连通的 mock SSH server（可用 `scripts/mock_sshd.py` + `scripts/fake-websphere/`）

---

## 通用前置准备

1. 启动 mock SSH 服务：`python3 scripts/mock_sshd.py`（默认 127.0.0.1:2225）
2. 启动主服务：`go run .` 或 `./DoubaoToolbox`（默认 127.0.0.1:18080，会自动开浏览器）
3. 准备测试数据：`scripts/fake-websphere/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1/` 下放几个 `SystemOut*.log` 文件
4. 浏览器开 DevTools Network 面板，勾选 "Preserve log" + "Disable cache"
5. 准备 4 套主题切换：dark / light / green / hc（点右上角 🌙 按钮循环）
6. 准备 4 种窗口尺寸：1366×768 / 1920×1080 / 2K / 半屏（960×1080）

---

## 用例 1：首页（#/home）

### TC-HOME-001 首次进入
1. 浏览器访问 http://127.0.0.1:18080/
2. 期望：自动跳转到 #/home，左侧菜单高亮"首页"，右侧显示 14 张功能卡片
3. 检查：Network 面板应有 `GET /api/config` 200 响应；footer 显示 `v0.8 · 下载管理 + 工具集 + 全功能完善`

### TC-HOME-002 逐个点击卡片
1. 依次点击 14 张卡片
2. 期望：每张卡片跳转到对应路由，左侧菜单对应项高亮
3. **重点关注**：第 8 张卡片「操作历史」（⏱ 图标，标签"已就绪"）→ 跳转 #/history
   - 期望：进入页面后调用 `GET /api/audit/recent`
   - 实际（FE-001）：菜单已被注释，但从卡片可进入；若后端 audit 路由已下线则页面报错；若路由还在则显示历史记录
   - 记录：是"已就绪"还是"已下线"？是否应该删除该卡片？

### TC-HOME-003 主题切换
1. 在首页点右上角 🌙 按钮
2. 依次切换 dark → light → green → hc
3. 期望：每套主题下卡片颜色、文字、图标都清晰可读
4. 检查：切换后刷新页面，主题是否持久化（localStorage `dtb_theme`）

### TC-HOME-004 窗口缩放
1. 依次调整窗口到 1366×768 / 1920×1080 / 2K / 半屏
2. 期望：卡片网格自适应，不溢出不留大片空白

---

## 用例 2：日志助手（#/websphere）

### TC-WS-001 首次进入
1. 点左侧菜单「日志助手」
2. 期望：显示业务系统选择区，左侧树形结构，右侧搜索面板
3. 检查：Network 应有 `GET /api/config` 200；凭据状态显示「未保存」或「已保存」

### TC-WS-002 测试 SSH 连接
1. 选择业务系统 → 服务器 → 输入密码（mock_sshd 用任意密码）
2. 点「测试连接」按钮
3. 期望：按钮显示 loading → 成功 toast「连接成功」
4. 检查：Network 应有 `POST /api/ssh/test` 200
5. **重复点击测试**：快速连点 3 次「测试连接」
   - 期望：按钮禁用，不产生重复请求
   - 实际：当前实现是否有禁用态？

### TC-WS-003 列文件
1. 测试连接成功后，点「列文件」
2. 期望：显示 `SystemOut*.log` 等文件列表，含大小、修改时间
3. 检查：Network 应有 `POST /api/logs/list/targets` 200
4. **超长文件名测试**：mock 数据放一个 200 字符长的文件名
   - 期望：表格列宽自适应或截断 + tooltip
   - 记录：是否溢出？

### TC-WS-004 搜索日志
1. 输入关键字「ERROR」
2. 点「搜索」
3. 期望：显示匹配行 + 上下文，高亮关键字
4. 检查：Network 应有 `POST /api/logs/search/multi` 200
5. **空结果测试**：搜一个不存在的关键字「ZZZZNOTEXIST」
   - 期望：显示"未找到匹配"，不是空白
6. **超长关键字测试**：输入 1000 字符
   - 期望：不卡顿，后端校验拒绝或正常处理

### TC-WS-005 下载最新日志
1. 列文件后，点某文件「下载最新」
2. 期望：显示下载进度条，完成后 toast「下载完成」
3. 检查：Network 应有 `POST /api/logs/download-latest` → `GET /api/logs/download/{id}/events`（SSE）200
4. **取消下载测试**：下载中点「取消」
   - 期望：立即停止，toast「已取消」
   - 检查：Network 应有 `POST /api/logs/download/{id}/cancel` 200

### TC-WS-006 开启 Tail（**重点：BE-002 5 分钟强断 bug**）
1. 列文件后，点某文件「Tail」
2. 期望：打开 tail 视图（嵌入或独立窗口），实时滚动日志行
3. 检查：Network 应有 `POST /api/logs/tail/start` → `GET /api/logs/tail/{id}/events`（SSE）200
4. **5 分钟稳定性测试**：保持 tail 连接不关，持续观看 6 分钟
   - 期望（修复后）：连接稳定，持续推送日志
   - 实际（BE-002）：5 分钟后连接被强制断开，需重新点 tail
   - 记录：断开时间、是否提示用户
5. **停止 Tail**：点「停止」按钮
   - 期望：SSE 关闭，按钮恢复
   - 检查：Network 应有 `POST /api/logs/tail/{id}/stop` 200

### TC-WS-007 凭据管理
1. 输入密码后点「保存凭据」
2. 期望：toast「已保存」，凭据状态变为「已保存」
3. 检查：Network 应有 `POST /api/credentials/save` 200
4. **凭据持久化测试**：刷新页面
   - 期望：凭据状态仍为「已保存」，无需重新输入密码
5. **清除凭据**：点「清除凭据」
   - 期望：toast「已清除」，状态变「未保存」
   - 检查：Network 应有 `POST /api/credentials/clear` 200

### TC-WS-008 复制路径
1. 搜索结果中点「复制路径」
2. 期望：toast「已复制」，剪贴板含完整路径
3. 检查：是否调用 `DTB.core.copyToClipboard`（FE-010：websphere.js 有局部重复实现）

### TC-WS-009 在文件夹中显示
1. 搜索结果中点「在文件夹中显示」
2. 期望：调用系统文件管理器打开远端目录的本地缓存
3. 检查：Network 应有 `POST /api/local/open-folder` 200

### TC-WS-010 用外部程序打开
1. 下载完成的文件，点「用外部程序打开」
2. 期望：调用系统默认程序打开文件（Windows: 默认关联程序；Mac: `open`；Linux: `xdg-open`）
3. 检查：Network 应有 `POST /api/local/open-with` 200，响应中不应回显文件绝对路径
4. **未下载文件测试**：在下载历史中对一个已被手动删除的文件点「用外部程序打开」
   - 期望：toast「文件不存在」，不崩溃
   - 实际（BE-015）：handler 直接 os.Open 失败返回 500，前端是否友好提示？

### TC-WS-011 关键字搜索上下文
1. 搜索结果中点某行的「查看上下文」
2. 期望：弹出上下文窗口，显示该行前后 N 行
3. 检查：Network 应有 `POST /api/logs/context` 200
4. **超长上下文测试**：设置 context_lines=10000
   - 期望：后端按 max_matches 截断，前端分页或虚拟滚动
   - 记录：是否卡顿

### TC-WS-012 多服务器搜索
1. 选多个服务器同时搜索「ERROR」
2. 期望：并行搜索，结果按服务器分组
3. 检查：Network 应有 `POST /api/logs/search/multi` 200
4. **超时测试**：选 3 个服务器，其中一个 mock 故意慢响应（>30s）
   - 期望：timeout_seconds=30 生效，慢服务器显示「超时」，其他正常返回
   - 实际（BE-008）：是否所有请求都被外层 ctx 取消？

---

## 用例 3：FTP 文件下载（#/files）

### TC-FILES-001 首次进入
1. 点左侧菜单「FTP 文件下载」
2. 期望：显示路径输入框、浏览区、下载按钮
3. 检查：Network 应有 `GET /api/config` 200；`free_file_browser` 状态决定路径输入是否可用
4. **关键**：若 `enable_free_file_browser=false`，期望页面提示「文件浏览器已禁用」
   - 实际（CFG-005）：示例配置默认 true 不限制，需验证 false 分支

### TC-FILES-002 浏览目录
1. 在路径输入框输入 `/tmp`（或 Windows `C:\`）
2. 点「浏览」
3. 期望：显示目录列表（文件名、大小、修改时间）
4. 检查：Network 应有 `POST /api/files/list` 200
5. **路径穿越测试**：输入 `/tmp/../etc/passwd`
   - 期望：后端拒绝（400 或 403）
   - 实际（BE-007）：`free_file_roots` 为空时无白名单校验，`..` 检查依赖 `strings.Contains`，可能漏判
6. **超长路径测试**：输入 4096 字符路径
   - 期望：后端拒绝（路径过长）
   - 记录：是否引发 panic

### TC-FILES-003 预览文件
1. 浏览目录后，点某文本文件「预览」
2. 期望：弹出预览窗口，显示文件内容
3. 检查：Network 应有 `POST /api/files/preview` 200
4. **大文件测试**：预览一个 100MB 文件
   - 期望：后端截断到 max_preview_size，前端显示「已截断」
   - 实际（BE-010）：是否一次性读入内存导致 OOM？
5. **二进制文件测试**：预览一个 .exe 文件
   - 期望：检测到二进制后显示「不支持预览二进制文件」
   - 记录：是否乱码或崩溃

### TC-FILES-004 下载文件
1. 浏览目录后，点某文件「下载」
2. 期望：显示下载进度条，完成后 toast「下载完成」
3. 检查：Network 应有 `POST /api/files/download` → `GET /api/files/download/{id}/events`（SSE）200
4. **取消下载测试**：下载中点「取消」
   - 期望：立即停止
   - 检查：Network 应有 `POST /api/files/download/{id}/cancel` 200
5. **下载到指定目录测试**：勾选「下载到指定目录」，输入 `/tmp/dtb-test`
   - 期望：文件下载到该目录
   - 实际（CFG-006）：`allowed_download_roots` 为空时不限制，可下到任意路径（含 `C:\Windows`）

### TC-FILES-005 在文件夹中显示
1. 下载完成后，点「在文件夹中显示」
2. 期望：调用系统文件管理器定位到下载文件
3. 检查：Network 应有 `POST /api/local/open-folder` 200
4. **Windows 路径测试**：下载到 `D:\test`，点「在文件夹中显示」
   - 期望：打开 Explorer 并选中文件
   - 实际（已修复）：使用 `filepath.Clean()` + `cmd /c explorer`，应正常工作

### TC-FILES-006 复制文件路径
1. 浏览目录后，点某文件「复制路径」
2. 期望：toast「已复制」，剪贴板含完整路径
3. 检查：是否调用 `DTB.core.copyToClipboard`（FE-010：websphere.js 有局部重复实现，files.js 应统一）

---

## 用例 4：报文格式化（#/formatter）

### TC-FMT-001 首次进入
1. 点左侧菜单「报文格式化」
2. 期望：显示左侧输入框、右侧输出框、顶部格式选择（JSON/XML/YAML/URL-Form）
3. 检查：首次进入不应报错（无 Network 请求，纯前端页面）

### TC-FMT-002 JSON 格式化
1. 输入 `{"a":1,"b":[2,3]}`
2. 点「格式化」
3. 期望：右侧显示格式化后的 JSON
4. 检查：Network 应有 `POST /api/format/json` 200
5. **非法 JSON 测试**：输入 `{a:1}`（无引号）
   - 期望：toast「JSON 解析失败」，不崩溃
6. **超长输入测试**：输入 10MB JSON
   - 期望：后端拒绝（超过 MaxBytesReader）或前端分块处理
   - 记录：是否卡顿
7. **中文测试**：输入 `{"name":"中文测试"}`
   - 期望：正确显示中文，不乱码

### TC-FMT-003 XML 格式化
1. 输入 `<root><a>1</a></root>`
2. 点「格式化」
3. 期望：右侧显示格式化后的 XML
4. 检查：Network 应有 `POST /api/format/xml` 200
5. **XXE 测试**：输入 `<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><root>&xxe;</root>`
   - 期望：后端禁用外部实体（DOCTYPES 解析被拒），不泄露 /etc/passwd
   - 实际（BE-017）：需确认是否使用安全解析器

### TC-FMT-004 YAML 格式化
1. 输入 `a: 1\nb:\n  - 2\n  - 3`
2. 点「格式化」
3. 期望：右侧显示格式化后的 YAML
4. 检查：Network 应有 `POST /api/format/yaml` 200

### TC-FMT-005 URL-Form 格式化
1. 输入 `a=1&b=2&c=hello%20world`
2. 点「格式化」
3. 期望：右侧显示格式化后的键值对
4. 检查：Network 应有 `POST /api/format/url-form` 200

### TC-FMT-006 主题可读性
1. 在 4 套主题下分别查看格式化结果
2. 期望：代码块背景色、文字色对比足够，可读
3. **重点检查 hc（高对比）主题**：语法高亮颜色是否被覆盖

---

## 用例 5：HTTP 测试（#/http）

### TC-HTTP-001 首次进入
1. 点左侧菜单「HTTP 测试」
2. 期望：显示用例列表、环境列表、请求构造器
3. 检查：Network 应有 `GET /api/http/cases` 200 和 `GET /api/http/envs` 200

### TC-HTTP-002 发送请求
1. 构造一个 GET 请求到 `http://127.0.0.1:18080/api/config`
2. 点「发送」
3. 期望：右侧显示响应状态、Header、Body
4. 检查：Network 应有 `POST /api/http/request` 200
5. **超时测试**：构造请求到 `http://10.255.255.1`（不可达 IP）
   - 期望：超时后显示「请求超时」
   - 实际（BE-013）：是否设置合理超时？

### TC-HTTP-003 用例管理
1. 点「新建用例」，填写名称、URL、Method、Headers、Body
2. 点「保存」
3. 期望：用例出现在列表
4. 检查：Network 应有 `POST /api/http/cases` 200
5. **重复用例名测试**：新建同名用例
   - 期望：后端拒绝或自动加后缀
   - 记录：当前行为？

### TC-HTTP-004 环境变量
1. 新建环境 `dev`，添加变量 `BASE_URL=http://127.0.0.1:18080`
2. 在用例 URL 中使用 `{{BASE_URL}}/api/config`
3. 选环境 `dev`，点「发送」
4. 期望：变量被替换，请求成功
5. 检查：Network 请求 URL 应为 `http://127.0.0.1:18080/api/config`

### TC-HTTP-005 长响应测试
1. 构造请求到一个返回 10MB JSON 的接口
2. 期望：响应不卡顿，可滚动查看
3. 记录：是否前端虚拟滚动？是否一次性渲染导致卡顿（FE-017）？

---

## 用例 6：常用命令（#/commands）

### TC-CMD-001 首次进入
1. 点左侧菜单「常用命令」
2. 期望：显示分类标签 + 命令列表
3. 检查：首次进入不应报错（命令数据嵌入前端或 /api/config）

### TC-CMD-002 搜索命令
1. 在搜索框输入「git」
2. 期望：实时过滤显示 git 相关命令
3. **空结果测试**：输入「ZZZZNOTEXIST」
   - 期望：显示「未找到匹配命令」
4. **超长搜索测试**：输入 1000 字符
   - 期望：不卡顿

### TC-CMD-003 收藏命令
1. 点某命令的「收藏」按钮
2. 期望：命令加入收藏列表，按钮变「已收藏」
3. 刷新页面
4. 期望：收藏状态持久化（localStorage）
5. **取消收藏测试**：再点「已收藏」
   - 期望：从收藏列表移除

### TC-CMD-004 复制命令
1. 点某命令的「复制」按钮
2. 期望：toast「已复制」，剪贴板含完整命令
3. 检查：命令中的占位符（如 `<file>`）是否原样复制

### TC-CMD-005 分类切换
1. 依次点击所有分类标签
2. 期望：每个分类下显示对应命令
3. **重点关注**：是否有重复分类（已修复：git restore 描述错误、shell 注释符 `//` 改为 `#`、chmod -R 644 危险示例）

---

## 用例 7：环境自检（#/diagnostics）

### TC-DIAG-001 首次进入
1. 点左侧菜单「环境自检」
2. 期望：自动开始检测，显示进度
3. 检查：Network 应有 `GET /api/diagnostics` 200

### TC-DIAG-002 检测项展示
1. 等待检测完成
2. 期望：显示各项检测结果（绿/黄/红），含详细说明
3. **重点关注**：
   - 下载目录可写性
   - SSH 客户端版本
   - 端口占用
   - 配置文件路径

### TC-DIAG-003 失败项处理
1. 故意让某项失败（如下载目录设为不可写）
2. 期望：该项标红，显示失败原因
3. 检查：是否提供「修复建议」

### TC-DIAG-004 重复检测
1. 检测完成后点「重新检测」
2. 期望：重新发起请求，更新结果
3. **重复点击测试**：快速连点 3 次「重新检测」
   - 期望：按钮禁用，不产生重复请求
   - 记录：当前实现是否有禁用态？

---

## 用例 8：系统配置（#/config）

### TC-CFG-001 首次进入
1. 点左侧菜单「系统配置」
2. 期望：显示配置编辑器、顶部 Tab（业务系统/SSH/下载/外观）
3. 检查：Network 应有 `GET /api/config` 200

### TC-CFG-002 编辑业务系统
1. 在「业务系统」Tab 下，点「新增系统」
2. 填写名称、描述
3. 点「保存」
4. 期望：toast「已保存」，列表出现新系统
5. 检查：Network 应有 `POST /api/config/import` 200（整体替换式保存）

### TC-CFG-003 编辑 SSH 服务器
1. 展开某业务系统，点「新增服务器」
2. 填写 host、port、username、auth_type
3. 点「保存」
4. 期望：toast「已保存」
5. **危险输入测试**：host 填 `10.10.10.11; rm -rf /`
   - 期望：后端拒绝（shell 注入校验）
   - 实际（CFG-008）：是否有 host 格式校验？

### TC-CFG-004 导出配置
1. 点「导出配置」
2. 期望：下载 config.yaml 文件
3. 检查：Network 应有 `GET /api/config/export` 200
4. **关键**：导出的 yaml 不应包含 SSH 密码（密码走 keyring/file，不落 yaml）

### TC-CFG-005 导入配置
1. 点「导入配置」，选择一个合法 config.yaml
2. 期望：toast「导入成功」，配置刷新
3. 检查：Network 应有 `POST /api/config/import` 200
4. **非法文件测试**：导入一个非 yaml 文件
   - 期望：toast「导入失败：格式错误」
5. **危险配置测试**：导入一个 host=`0.0.0.0` 的配置
   - 期望：后端拒绝或警告
   - 实际（CFG-004）：是否校验 host 不为 0.0.0.0？

### TC-CFG-006 Sticky 保存栏
1. 修改配置但不保存
2. 期望：底部出现 sticky 保存栏，显示「有未保存修改」
3. 滚动页面
4. 期望：保存栏始终可见，不遮挡内容（FE-007：检查是否遮挡表格末行）

### TC-CFG-007 下载保留策略
1. 在「下载」Tab 设置 `download_retention_days=7`、`download_max_count=100`
2. 点「保存」
3. 期望：toast「已保存」
4. 检查：Network 应有 `POST /api/admin/download-retention` 200
5. **触发清理测试**：手动放一个 8 天前的文件到 downloads/
6. 点「立即清理」
7. 期望：旧文件被删除，audit 日志记录
8. 检查：Network 应有 `POST /api/admin/download-retention` 200

---

## 用例 9：下载历史（#/downloads）

### TC-DL-001 首次进入
1. 点左侧菜单「下载历史」
2. 期望：显示下载列表（文件名、大小、来源、时间、状态）
3. 检查：Network 应有 `GET /api/downloads/list` 200

### TC-DL-002 空数据状态
1. 删除所有下载文件
2. 刷新页面
3. 期望：显示「暂无下载记录」空状态，不是空白
4. 检查：Network 应有 `GET /api/downloads/list` 200，响应 `[]`

### TC-DL-003 查看全部
1. 点「查看全部」
2. 期望：显示所有下载记录（含已清理的）
3. 检查：Network 应有 `GET /api/downloads/all` 200

### TC-DL-004 打开目录
1. 点某下载记录的「打开目录」
2. 期望：调用系统文件管理器打开下载目录
3. 检查：Network 应有 `GET /api/downloads/open-dir?name=...` 200
4. **特殊字符测试**：文件名含中文、空格、特殊字符
   - 期望：URL 编码正确，后端正确解析
   - 实际（API-005）：URL 参数 `name` 是否正确解码？

### TC-DL-005 删除下载记录
1. 点某下载记录的「删除」
2. 期望：toast「已删除」，列表移除该项
3. **重复删除测试**：删除一个已被手动删除的文件
   - 期望：toast「文件不存在」，列表状态更新
4. **批量删除测试**：勾选多个，点「批量删除」
   - 期望：全部删除，进度提示

### TC-DL-006 重复通知测试
1. 删除一个下载记录
2. 期望：只弹一次 toast「已删除」
3. 实际（已修复）：websphere.js 之前每组下载完成都弹 toast，已改为汇总单条

---

## 用例 10：操作历史（#/history，菜单已隐藏）

### TC-HIST-001 通过 hash 直接进入
1. 浏览器地址栏输入 `http://127.0.0.1:18080/#/history`
2. 期望：进入操作历史页面
3. 实际（FE-001）：菜单已被注释（index.html:62），但 history.js 仍被加载（index.html:104），路由仍注册
4. 检查：页面是否报错？是否调用 `GET /api/audit/recent`？
5. **关键**：后端 audit 路由是否仍注册？
   - 实际（已核对 httpserver.go:197-202）：audit 路由仍在，返回数据
   - 但项目记忆显示「已移除 audit handler 文件」，需现场确认

### TC-HIST-002 查看操作历史
1. 进入 #/history 后
2. 期望：显示操作历史列表（时间、用户、操作、详情）
3. 检查：Network 应有 `GET /api/audit/recent` 200
4. **空数据测试**：清空 audit.log 后访问
   - 期望：显示「暂无操作记录」空状态

### TC-HIST-003 导出 CSV
1. 点「导出 CSV」
2. 期望：下载 audit.csv 文件
3. 检查：Network 应有 `GET /api/audit/export.csv` 200
4. **大数据量测试**：audit.log 有 10万条记录
   - 期望：流式导出，不 OOM
   - 实际（BE-019）：是否一次性加载到内存？

### TC-HIST-004 导出 JSON
1. 点「导出 JSON」
2. 期望：下载 audit.json 文件
3. 检查：Network 应有 `GET /api/audit/export.json` 200

### TC-HIST-005 菜单可见性决策
1. 检查 index.html:62 行
2. **决策**：
   - 选项 A：恢复菜单（取消注释），保留功能
   - 选项 B：彻底移除（删 history.js、删 audit 路由、删 audit handler 文件）
3. 推荐：选项 B（已与项目记忆一致：曾移除 audit 路由）

---

## 用例 11：时间戳（#/timestamp）

### TC-TS-001 首次进入
1. 点左侧菜单「时间戳」
2. 期望：显示当前时间戳（秒/毫秒）、可读时间、转换输入框
3. 检查：首次进入不应报错（纯前端，无 Network 请求）

### TC-TS-002 时间戳转可读
1. 输入 `1719500000`
2. 期望：显示对应可读时间（UTC + 本地时区）
3. 检查：Network 应有 `POST /api/format/timestamp` 200
4. **非法输入测试**：输入 `abc`
   - 期望：toast「无效时间戳」
5. **超大时间戳测试**：输入 `99999999999999999999`
   - 期望：后端拒绝（int64 溢出）

### TC-TS-003 可读转时间戳
1. 输入 `2026-06-27 12:00:00`
2. 期望：显示对应时间戳
3. **时区测试**：切换时区（UTC/本地/自定义）
4. 期望：时间戳随时区变化

### TC-TS-004 当前时间戳
1. 点「复制当前时间戳」
2. 期望：剪贴板含当前时间戳
3. 等待 1 秒，再点「复制当前时间戳」
4. 期望：时间戳递增

---

## 用例 12：Cron 解析（#/cron）

### TC-CRON-001 首次进入
1. 点左侧菜单「Cron 解析」
2. 期望：显示 Cron 输入框、解析结果区、常用示例
3. 检查：首次进入不应报错

### TC-CRON-002 解析 Cron
1. 输入 `0 2 * * 1-5`
2. 点「解析」
3. 期望：显示「每个工作日 02:00 执行」+ 接下来 5 次执行时间
4. 检查：Network 应有 `POST /api/format/cron-parse` 200
5. **非法 Cron 测试**：输入 `* * * * * * * *`（8 段）
   - 期望：toast「无效 Cron 表达式」
6. **特殊字符测试**：输入 `0 0 1 1 0`（每年 1 月 1 日）
   - 期望：正确解析

### TC-CRON-003 常用示例
1. 点击每个常用示例（如「每小时」「每天 0 点」）
2. 期望：自动填入输入框并解析
3. 检查：示例是否准确

### TC-CRON-004 下次执行时间
1. 输入 `*/5 * * * *`
2. 期望：显示接下来 5 次执行时间
3. **时区测试**：切换时区，执行时间应相应变化

---

## 用例 13：JSONPath（#/jsonpath）

### TC-JP-001 首次进入
1. 点左侧菜单「JSONPath」
2. 期望：显示 JSON 输入框、JSONPath 表达式输入框、结果区
3. 检查：首次进入不应报错

### TC-JP-002 查询 JSONPath
1. JSON 输入：`{"store":{"book":[{"title":"A"},{"title":"B"}]}}`
2. JSONPath 输入：`$.store.book[*].title`
3. 点「查询」
4. 期望：结果显示 `["A","B"]`
5. 检查：Network 应有 `POST /api/format/jsonpath` 200
6. **非法 JSONPath 测试**：输入 `$$invalid$$`
   - 期望：toast「无效 JSONPath 表达式」
7. **非法 JSON 测试**：JSON 输入 `{a:1}`
   - 期望：toast「JSON 解析失败」
8. **超长 JSON 测试**：输入 10MB JSON
   - 期望：后端拒绝或前端分块
   - 记录：是否卡顿

### TC-JP-003 复杂查询
1. JSON 输入大型嵌套结构
2. 使用过滤器 `$.store.book[?(@.price < 10)]`
3. 期望：正确过滤
4. 记录：是否支持完整 JSONPath 语法？

---

## 用例 14：代码比对（#/compare）

### TC-CFG-CMP-001 首次进入
1. 点左侧菜单「代码比对」
2. 期望：显示左右路径输入框、扫描按钮、比对结果区
3. 检查：首次进入不应报错

### TC-CMP-002 目录扫描
1. 左路径输入 `/tmp/dirA`，右路径输入 `/tmp/dirB`（提前准备两个目录）
2. 点「扫描」
3. 期望：显示两目录文件树对比（相同/不同/仅左/仅右）
4. 检查：Network 应有 `POST /api/compare/folder-scan` 200
5. **超长路径测试**：输入 4096 字符路径
   - 期望：后端拒绝
6. **大目录测试**：扫描含 10万文件的目录
   - 期望：流式返回或分页
   - 实际（BE-011）：scanDir 一次性 WalkDir，可能 OOM

### TC-CMP-003 文件 diff（**重点：BE-001 任意文件读漏洞**）
1. 扫描完成后，点某文件「查看 diff」
2. 期望：显示 unified diff
3. 检查：Network 应有 `POST /api/compare/file-diff` 200
4. **安全测试 - 任意文件读**：
   - left_path 输入 `/etc/passwd`
   - right_path 输入 `/etc/passwd`
   - 期望：后端拒绝（路径不在白名单）
   - 实际（BE-001）：handler 仅校验非空，直接 `exec.Command("diff","-u",left,right)`，可读任意文件！
5. **安全测试 - 命令注入**：
   - left_path 输入 `file1; rm -rf /`
   - 期望：exec.Command 不走 shell，应安全
   - 记录：确认无 shell 注入
6. **大文件 diff 测试**：两个 100MB 文件 diff
   - 期望：流式返回或截断
   - 实际（BE-010）：stdout.Buffer 一次性加载，可能 OOM

### TC-CMP-004 diff2html 渲染（**重点：PKG-002 缺失静态资源**）
1. 文件 diff 返回后
2. 期望：diff2html 渲染高亮 diff
3. 实际（PKG-002）：tar.gz 包缺 `web/vendor/diff2html.min.css` 和 `web/vendor/diff2html.min.js`，本地源码有但打包漏了
4. 检查：DevTools Console 是否报 404？
5. **本地源码验证**：`ls web/vendor/diff2html.*` 应存在

### TC-CMP-005 主题可读性
1. 在 4 套主题下查看 diff 渲染
2. 期望：diff 高亮（红/绿）在所有主题下可读
3. **重点检查 hc 主题**：是否覆盖 diff2html 默认色

---

## 用例 15：关于页（#/about）

### TC-ABOUT-001 首次进入
1. 点左侧菜单「关于」
2. 期望：显示版本号、构建时间、功能列表、开源协议
3. 检查：Network 应有 `GET /api/config` 200
4. **版本一致性测试**：
   - 期望：页面显示的版本与 footer 一致
   - 实际（已修复）：about.js 从 `/api/config` 响应读取 Version/BuildTime

### TC-ABOUT-002 功能列表
1. 检查功能列表是否与实际一致
2. **重点**：是否有「操作历史」功能被列出但实际菜单已隐藏？
3. 期望：功能列表与实际可用功能一致（FE-001 关联）

### TC-ABOUT-003 链接测试
1. 点击页面所有外链（如 GitHub 仓库、文档）
2. 期望：在新标签页打开，不替换工具箱页面
3. 检查：链接是否 `target="_blank"` + `rel="noopener noreferrer"`

---

## 用例 16：独立 tail.html

### TC-TAIL-001 直接访问
1. 浏览器访问 `http://127.0.0.1:18080/tail.html`
2. 期望：显示独立 tail 页面（无侧边栏）
3. 检查：页面是否需要登录？是否可被未授权用户访问？
4. **安全测试**：未登录状态下直接访问
   - 期望：若 auth 开启，重定向到登录页
   - 实际（FE-002）：独立页面是否走 auth 中间件？

### TC-TAIL-002 通过 tail_id 接入
1. 在主页面 #/websphere 开启一个 tail 会话
2. 复制 tail_id
3. 在 tail.html 中输入 tail_id，点「接入」
4. 期望：实时显示该 tail 的日志流
5. 检查：Network 应有 `GET /api/logs/tail/{id}/events`（SSE）200

### TC-TAIL-003 主题切换
1. 在 tail.html 点主题切换
2. 期望：4 套主题都生效
3. 实际（FE-003）：独立页面是否复用主题逻辑？是否持久化？

### TC-TAIL-004 断线重连
1. tail 进行中，断开网络 5 秒
2. 重新联网
3. 期望：SSE 自动重连，显示断线期间的日志
4. 实际：当前是否实现 EventSource 自动重连？

---

## 用例 17：独立 preview.html

### TC-PREVIEW-001 直接访问
1. 浏览器访问 `http://127.0.0.1:18080/preview.html`
2. 期望：显示独立预览页面
3. 检查：是否需要登录？
4. **安全测试**：未登录访问
   - 期望：若 auth 开启，重定向到登录页
   - 实际（FE-002）：独立页面是否走 auth 中间件？

### TC-PREVIEW-002 预览文件
1. 通过 URL 参数 `?path=/tmp/test.log` 预览
2. 期望：显示文件内容
3. 检查：Network 应有 `POST /api/files/preview` 200
4. **路径穿越测试**：`?path=/etc/passwd`
   - 期望：后端拒绝
   - 实际（BE-007）：白名单为空时不限制

### TC-PREVIEW-003 主题切换
1. 在 preview.html 点主题切换
2. 期望：4 套主题都生效
3. 实际（FE-003）：独立页面是否复用主题逻辑？

### TC-PREVIEW-004 大文件预览
1. 预览一个 100MB 文件
2. 期望：截断到 max_preview_size，显示「已截断」
3. 实际（BE-010）：是否一次性读入内存？

---

## 附录 A：跨页面通用检查清单

执行每个用例时，对照本清单逐项确认：

| # | 检查项 | 通过条件 |
|---|---|---|
| 1 | 首次进入报错 | 无 console error，无 500 响应 |
| 2 | Loading 状态 | 有 loading 占位，不闪屏 |
| 3 | 成功状态 | 数据正确渲染 |
| 4 | 失败状态 | 有 toast/inline 错误提示 |
| 5 | 空数据状态 | 显示「暂无数据」占位 |
| 6 | 重复点击禁用 | 按钮有 disabled 态，无重复请求 |
| 7 | 长路径溢出 | 表格列宽自适应或截断 + tooltip |
| 8 | 中文显示 | 无乱码 |
| 9 | 超长输入 | 后端拒绝或前端截断 |
| 10 | 特殊字符 | 正确转义，不破坏布局 |
| 11 | dark 主题 | 背景深色，文字浅色 |
| 12 | light 主题 | 背景浅色，文字深色 |
| 13 | green 主题 | 背景墨绿，文字可读 |
| 14 | hc 主题 | 高对比，无低对比度元素 |
| 15 | 1366×768 | 布局不溢出 |
| 16 | 1920×1080 | 布局不拉伸过度 |
| 17 | 2K (2560×1440) | 布局自适应 |
| 18 | 半屏 (960×1080) | 布局合理，不挤压 |
| 19 | 401 响应 | 跳转登录页 |
| 20 | 403 响应 | 显示「无权限」 |
| 21 | 404 响应 | 显示「未找到」 |
| 22 | 500 响应 | 显示「服务器错误」，不崩页 |
| 23 | 网络断开 | 显示「网络异常」，可重试 |
| 24 | SSE 断开 | 显示「连接断开」，自动重连 |
| 25 | 无密码泄露 | Network 响应、DOM、console 无明文密码 |
| 26 | 无日志泄露 | 错误提示不回显日志正文 |
| 27 | 无 token 泄露 | Network 响应、URL 不含 token（除 query 参数 fallback） |

---

## 附录 B：异常场景专项测试

### TC-EXC-001 401 全局处理
1. 启用 auth（config.yaml 配置 token）
2. 不带 token 访问任意 /api/* 接口
3. 期望：返回 401 + `WWW-Authenticate: Bearer`
4. 前端应跳转登录页

### TC-EXC-002 403 全局处理
1. 配置 IP CIDR 白名单
2. 从非白名单 IP 访问
3. 期望：返回 403「IP 不在允许列表」

### TC-EXC-003 跨源请求
1. 从 `http://evil.com` 发起 fetch 到 `http://127.0.0.1:18080/api/config`
2. 期望：返回 403「拒绝跨源请求」
3. 检查：allowLocalOrigin 是否正确判断

### TC-EXC-004 SSE 长连接稳定性
1. 开启 tail，保持 10 分钟
2. 期望：连接稳定
3. 实际（BE-002）：5 分钟后被 idleGC 强制 killSSH
4. 记录断开时间、是否提示

### TC-EXC-005 配置热替换
1. 修改 config.yaml，保存
2. 点「重新加载配置」（若有）
3. 期望：新配置生效，旧 session 不受影响
4. 检查：COW 快照是否正确切换

### TC-EXC-006 进程重启后状态
1. 下载进行中，强制 kill 进程
2. 重启服务
3. 期望：下载列表显示「已中断」，可清理
4. tail 会话应全部清除（内存态）

---

## 附录 C：安全边界专项测试

### TC-SEC-001 路径穿越
| # | 输入 | 期望 | 关联问题 |
|---|---|---|---|
| 1 | `/tmp/../etc/passwd` | 400 路径非法 | BE-007 |
| 2 | `/tmp/./../../etc/shadow` | 400 | BE-007 |
| 3 | `/tmp/%2e%2e/etc/passwd` | 400（URL 解码后再校验） | BE-007 |
| 4 | `C:\..\..\Windows\System32` | 400 | BE-007 |

### TC-SEC-002 命令注入
| # | 输入 | 期望 | 关联问题 |
|---|---|---|---|
| 1 | host=`10.10.10.11; rm -rf /` | 400 | CFG-008 |
| 2 | file=`; cat /etc/passwd` | 400 | — |
| 3 | compare left_path=`$(whoami)` | exec.Command 不走 shell，安全 | BE-001 |

### TC-SEC-003 敏感信息泄露
| # | 检查项 | 期望 | 关联问题 |
|---|---|---|---|
| 1 | /api/config 响应 | 不含 SSH 密码 | — |
| 2 | /api/config/export 下载 | 不含 SSH 密码 | CFG-007 |
| 3 | audit.log | 不含 SSH 密码、token | BE-016 |
| 4 | 错误响应 | 不回显日志正文、文件内容 | BE-015 |
| 5 | DevTools Network | 不含明文密码 | — |
| 6 | URL 参数 | 不含 token（除 SSE 必要 fallback） | API-004 |

### TC-SEC-004 任意文件读（BE-001 专项）
1. 用 curl 直接发请求：
   ```bash
   curl -X POST http://127.0.0.1:18080/api/compare/file-diff \
     -H "Content-Type: application/json" \
     -d '{"left_path":"/etc/passwd","right_path":"/etc/passwd"}'
   ```
2. 期望：返回 400 或 403
3. 实际（BE-001）：返回 200，unified 字段含 /etc/passwd 内容！
4. 影响：任意文件读取，泄露敏感配置

### TC-SEC-005 SSH host key 校验
1. 配置 SSH 连接到一台新服务器
2. 期望：首次连接提示 host key 指纹，需用户确认
3. 实际（BE-005）：默认 `InsecureIgnoreHostKey=true`，跳过校验，存在 MITM 风险

---

## 附录 D：测试基线执行清单

执行手工测试前，必须先跑通以下基线：

| # | 命令 | 期望 | 关联问题 |
|---|---|---|---|
| 1 | `tar -tzf doubao-toolbox-src-review.tar.gz \| grep vendor/` | 有输出 | PKG-001 |
| 2 | `tar -tzf doubao-toolbox-src-review.tar.gz \| grep web/vendor/diff2html` | 有输出 | PKG-002 |
| 3 | `gofmt -l $(find . -name '*.go' -not -path './vendor/*')` | 无输出 | STATIC-001 |
| 4 | `go test -mod=vendor -count=1 ./...` | all pass | — |
| 5 | `node web/app.test.js` | 17 pass / 0 fail | — |
| 6 | `for f in web/*.js web/pages/*.js; do node --check "$f"; done` | 全部通过 | — |

**重要**：基线 1、2 当前失败（PKG-001/002），需先修复打包脚本再继续手工测试。

---

## 附录 E：执行结果记录模板

每个用例执行后，填写本表：

| 用例编号 | 执行时间 | 执行人 | 结果 | 实际现象 | 关联问题 |
|---|---|---|---|---|---|
| TC-HOME-001 | 2026-06-27 | ___ | ⬜ 通过 / ❌ 失败 / ⚠️ 部分 | ___ | ___ |
| TC-HOME-002 | | | | | |
| ... | | | | | |

---

**文档结束**

> 本用例集覆盖 17 个菜单/页面、57 个 /api/* 接口、4 套主题、4 种分辨率、10 类异常场景、6 类安全边界。
> 全部执行后，对照 `docs/ISSUES-FOUND.md` 核对每个问题的复现性，并更新问题状态。