# WebService 调试中心（v0.12）

一个轻量 SoapUI，面向老 Java / WebSphere / XFire / SOAP WebService 场景。
支持 WSDL 导入、SOAP 报文生成、接口测试、模板保存、历史回放、Mock 服务端。

---

## 功能入口

浏览器打开工具箱主页 → 左侧导航 **WebService**（或直接访问 `#/webservice`）。

页面布局：

```
┌─────────────────┬──────────────────────────────────────┐
│ 左侧 Sidebar    │ 右侧 Main                            │
│                 │                                      │
│ ┌─ WSDL 项目 ─┐ │ ┌─ WSDL 项目概览 + operation 列表 ─┐ │
│ ├─ 模板      ─┤ │ ├─ 请求编辑器（endpoint/body/...）─┤ │
│ ├─ 历史      ─┤ │ └─ 响应结果区（status/body/关键词）┘ │
│ └─ Mock      ─┘ │                                      │
│                 │                                      │
│ + 搜索框        │                                      │
│ + 列表          │                                      │
│ + 底部操作按钮  │                                      │
└─────────────────┴──────────────────────────────────────┘
```

四个 Tab：
- **WSDL 项目**：已导入的 WSDL 列表，含 operations 数量、namespace
- **模板**：保存的 SOAP 请求模板，按分组管理
- **历史**：最近 500 条请求记录，支持搜索 / 回放
- **Mock**：Mock 服务端配置，保存后立即生效

---

## 1. 导入 WSDL

### 方式 A：从 URL 导入

1. 左侧切到 **WSDL 项目** Tab
2. 点底部 **"+ 导入 URL"**
3. 输入 WSDL URL（如 `http://10.0.0.1:9080/services/Foo?wsdl`），确定
4. 可选输入项目名（留空则用 service 名或 URL 末段作为名字）
5. 工具箱会拉取 WSDL（30s 超时）→ 解析 → 自动保存到本地

### 方式 B：上传 .wsdl / .xsd 文件

1. 左侧切到 **WSDL 项目** Tab
2. 点底部 **"+ 上传文件"**
3. 一次选择本地 `.wsdl` 及它引用的 `.xsd` / `.xml` 文件（单文件 4MB、合计 16MB 上限）
4. 文件内容会按 XML 声明自动识别 UTF-8（含 BOM）、UTF-16LE/BE、GBK、GB2312、GB18030，解析后自动保存

外部 XSD 不在同一目录或不能由 URL 拉取时，建议一次多选 WSDL 和全部 XSD。若缺少附件，页面会明确提示，并只生成当前信息足够的报文骨架。

### 解析后展示

选中一个 WSDL 项目后，主区显示：

- 项目名 / SOAP 版本 / targetNamespace / operations 数量
- **Warnings**（外部 XSD import 未拉取等降级提示）
- **Services / Ports**：service 名、port 名、endpoint、SOAP 版本
- **Operations 列表**：每个 operation 显示 name / SOAPAction / endpoint / 输入字段数 / 输出字段数
- 选中 operation 后展开：namespace / SOAPAction / endpoint / SOAP 版本 + 输入参数树 + 输出参数树

> **降级原则**：复杂 WSDL / 外部 XSD import/include 拉取失败不会导致整个解析崩溃。
> 受影响的 operation 会保留原始片段（`InputRaw` / `OutputRaw`），并在 Warnings 里给出提示。
> 其它能解析的 operation 照常使用。

---

## 2. 生成 SOAP 请求报文

1. 在 WSDL 项目里选中一个 operation
2. 系统自动调用 `POST /api/soap/generate`，把生成的 Envelope 填入请求编辑器
3. 也可手动点 **"生成 Envelope"** 按钮重新生成

生成的报文示例：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:web="http://example.com/demo">
  <soapenv:Header/>
  <soapenv:Body>
    <web:customerQuery>
      <custNo>${custNo}</custNo>
      <serialNo>${serialNo}</serialNo>
    </web:customerQuery>
  </soapenv:Body>
</soapenv:Envelope>
```

- 参数用 `${paramName}` 占位符，方便手动替换
- document/literal wrapped 模式会自动剥掉外层包装元素，避免双层嵌套
- SOAP 1.1 用 `soapenv:` + `http://schemas.xmlsoap.org/soap/envelope/`
- SOAP 1.2 用 `http://www.w3.org/2003/05/soap-envelope`

请求编辑器按钮：
- **生成 Envelope**：按当前 operation 重新生成
- **格式化 / 压缩 / 校验**：XML 辅助操作（保留业务文本中的前后空格、CDATA 和 mixed content）
- **复制**：复制请求体到剪贴板
- **存为模板**：把当前请求保存为模板
- **发送**：发出请求

---

## 3. 发送 SOAP 请求

请求编辑器字段：

| 字段 | 说明 |
| --- | --- |
| endpoint | 目标 URL，必须 `http://` 或 `https://` 开头 |
| SOAPAction | SOAP 1.1 必填（可空字符串）；1.2 走 Content-Type，不需要 |
| 版本 | SOAP 1.1（`text/xml`）/ SOAP 1.2（`application/soap+xml`） |
| 编码 | UTF-8 / GBK / GB2312 / GB18030（请求体、XML declaration、Content-Type charset 同步） |
| 超时(ms) | 默认 30000，硬上限 300000（5 分钟） |
| Headers | 支持逐行 `Key: Value`、JSON 对象、从终端粘贴的 `curl -H` / `--header` |
| 保存本次请求 | 默认开启；临时联调或含敏感报文时可关闭，不写入历史 |
| 请求 XML | SOAP Envelope 正文 |

点 **发送** 后：
- 请求按选定编码序列化发出
- 响应区显示：HTTP 状态码 / 状态文本 / 耗时 / Body 字节数 / 响应 Headers / 响应 Body
- 响应 Body 支持 **格式化 / 压缩 / 复制**
- 勾选“保存本次请求”时写入历史（最多 500 条，超出自动丢弃最旧的）
- 响应 Body 超过 2MB 时截断展示并明确提示，避免大报文卡住页面
- 底部显示 **建议日志搜索关键词**：operation 名、SOAPAction、请求体里的 `serialNo` / `traceNo` / `requestId` / `transId` 等常见 trace 字段值（点击复制）

错误处理：
- 超时：明确提示 "请求超时（Nms）"
- 网络异常：返回原始错误信息
- 非 XML 响应：原样展示 Body
- 5xx：标记 `ok=false`，但仍展示 Body 供排查

---

## 4. 模板库

### 保存模板

1. 在请求编辑器里填好请求后，点 **"存为模板"**
2. 输入模板名（必填）和分组（可空）
3. 模板保存到 `data/soap_templates.json`

### 使用模板

1. 左侧切到 **模板** Tab
2. 点列表里的模板，请求编辑器自动填入 endpoint / SOAPAction / SOAP 版本 / headers / body / encoding / timeout，并保持在模板 Tab
3. 点 **发送** 即可重发

### 模板字段

| 字段 | 说明 |
| --- | --- |
| name | 模板名（必填） |
| group | 分组（可空） |
| endpoint | 目标 URL |
| operation | operation 名 |
| soap_action | SOAPAction |
| soap_version | SOAP 1.1 / 1.2 |
| headers | 自定义 HTTP Header |
| body | 请求 XML |
| encoding | UTF-8 / GBK / GB2312 / GB18030 |
| timeout_ms | 超时毫秒 |
| note | 备注 |
| created_at / updated_at | 时间戳（自动） |

模板按 `id` 或 `(group, name)` 唯一，保存同名模板会覆盖。

---

## 5. 历史与回放

编辑器勾选“保存本次请求”时会记录发送；历史 replay 会记录一条新历史。

### 历史字段

| 字段 | 说明 |
| --- | --- |
| time | 时间（RFC 3339） |
| endpoint | 请求 URL |
| operation | operation 名 |
| soap_action | SOAPAction |
| encoding | 编码 |
| request_body | 请求 XML |
| response_body | 响应 XML |
| status_code | HTTP 状态码 |
| duration_ms | 耗时毫秒 |
| success | 成功/失败 |
| error | 错误信息 |

### 操作

- **搜索**：左侧搜索框支持按 endpoint / operation / SOAPAction / error 关键字过滤
- **查看**：点击历史条目，请求体填入编辑器，响应体展示在响应区
- **回放**：选中历史后调用 `POST /api/soap/history/{id}/replay`，重新发送并记一条新历史
- **清空**：底部 "一键清空" 按钮删除全部历史（不可恢复，确认弹窗）

历史默认上限 500 条，超出自动丢弃最旧的。

---

## 6. Mock WebService 服务端

### 创建 Mock

1. 左侧切到 **Mock** Tab
2. 点底部 **"+ 新 Mock"**
3. 填写表单：

| 字段 | 说明 |
| --- | --- |
| name | Mock 名（必填） |
| path | 路径（必填），如 `/mock/customerQuery` 或 `customerQuery`（自动加 `/mock/` 前缀） |
| operation | operation 名（可选） |
| status_code | HTTP 状态码，默认 200 |
| delay_ms | 延迟毫秒数（用于模拟慢响应 / 超时） |
| enabled | 是否启用 |
| body | 固定响应 XML |

4. 点 **保存 Mock**，立即生效（无需重启）

### 访问 Mock

外部系统直接请求：

```
POST http://<工具箱机器IP>:<端口>/mock/<path>
Content-Type: text/xml

<任意请求体>
```

工具箱会：
1. 记录收到的请求（时间 / path / method / 关键 headers / body）
2. 应用配置的延迟（`delay_ms`）
3. 返回固定 body + 配置的状态码

### Mock 请求记录

- 点 **"查看请求记录"** 按钮，复制最近 20 条记录到剪贴板
- 通过 `GET /api/soap/mocks/records` 可程序化读取全部记录（最多 200 条）
- `DELETE /api/soap/mocks/records` 一键清空

### 模拟场景

| 场景 | 配置 |
| --- | --- |
| 成功响应 | `status_code=200`，body 填正常 SOAP 响应 |
| 失败响应 | `status_code=500`，body 填 SOAP Fault |
| 延迟响应 | `delay_ms=2000` 模拟慢服务 |
| 超时模拟 | `delay_ms` 设置大于客户端超时即可 |

---

## 7. XML 辅助能力

请求编辑器和响应区都支持：

- **格式化**：`POST /api/ws/xml/format`，缩进 2 空格；保留元素文本首尾空格、CDATA、mixed content 的语义
- **压缩**：`POST /api/ws/xml/minify`，只移除结构性缩进，不删除业务文本中的有效空格
- **校验**：`POST /api/ws/xml/validate`，well-formed 检查（标签匹配、未闭合等）

校验只做 well-formed 检查，不做 XSD 校验。代码结构留有扩展空间。

---

## 8. XML 编码兼容说明

老 WebSphere / 老 Java 系统经常用 GBK 编码。工具箱支持：

- **WSDL/XSD 导入**：识别 UTF-8（含 BOM）、UTF-16LE/BE（含 BOM 或字节特征）、GBK、GB2312、GB18030，并统一交给解析器处理
- **请求编码**：编辑器里选 UTF-8、GBK、GB2312 或 GB18030
  - 请求体按选定编码序列化后发送
  - XML declaration 和 Content-Type 的 charset 自动跟随（如 `text/xml; charset=GB18030`）
- **响应解码**：
  - 综合响应 Content-Type 和 XML declaration 判断；两者冲突时避免把合法 UTF-8 错解为 GBK
  - 取不到时用请求编码
  - 再失败回退 UTF-8 / GB18030 自动尝试

常见问题：
- **响应乱码**：检查响应 Content-Type 是否声明了 charset；没声明时工具箱会用请求编码解码，确保两者匹配
- **请求 400**：服务端可能只接受某一种中文编码，依次核对 WSDL、XML declaration 和接口文档要求
- **中文占位符**：`${custNo}` 等占位符是 ASCII，不受编码影响

### 兼容范围

| 场景 | 当前支持情况 |
| --- | --- |
| WSDL | WSDL 1.1；单文件或附带外部 XSD import/include |
| SOAP | SOAP 1.1 / 1.2；同一 WSDL 内混合版本会按 binding/port 分别识别 |
| 消息风格 | document/literal wrapped、bare；rpc/literal 尽力解析并保留原始结构用于降级 |
| XML 编码 | UTF-8/BOM、UTF-16LE/BE、GBK、GB2312、GB18030 |
| XML 内容 | 普通元素、namespace、CDATA、mixed content、具有业务含义的文本空格 |
| 暂未完整覆盖 | WSDL 2.0、SOAP encoded arrays、MTOM/SwA 二进制附件、WS-Security 签名/加密、完整 XSD 约束校验 |

“暂未完整覆盖”的协议不会伪装成已兼容：解析能降级时保留原始片段和 Warning，需要签名、附件或严格 Schema 校验的接口仍应配合专用客户端或后续扩展。

---

## 9. 日志联动

发送请求后，响应区底部会显示 **建议日志搜索关键词**：

- operation 名（如 `customerQuery`）
- SOAPAction（如 `urn:customerQuery`）
- 请求体里识别到的 trace 字段值：
  - `serialNo` / `serialno`
  - `traceNo` / `traceno`
  - `requestId` / `requestid`
  - `transId` / `transid`
  - `tradeNo` / `tradeno`
  - `orderId` / `orderid`
  - `reqSerial`
  - `flowNo` / `flowno`

点击关键词 chip 即可复制到剪贴板，然后去工具箱的「日志助手」页粘贴搜索。

---

## 10. 常见问题

### Q1：SOAPAction 必填吗？

- SOAP 1.1：规范要求必须带 SOAPAction header（即使是空字符串 `""`）。工具箱会自动加引号，未填时发送 `SOAPAction: ""`
- SOAP 1.2：SOAPAction 改放到 Content-Type 的 `action=` 参数里，不需要 header

### Q2：Content-Type 应该填什么？

- SOAP 1.1：`text/xml; charset=UTF-8`（或 `charset=GBK`）
- SOAP 1.2：`application/soap+xml; charset=UTF-8`
- 工具箱按"版本 + 编码"自动生成，留空即可
- 如需自定义（例如服务端要求特殊 Content-Type），可在 Headers 里覆盖

### Q3：请求超时怎么调？

- 编辑器里 "超时(ms)" 字段，默认 30000（30s）
- 硬上限 300000（5 分钟），超过会被截断
- Mock 测试超时场景：把 Mock 的 `delay_ms` 设得比客户端超时大即可

### Q4：WSDL 解析失败怎么办？

- 检查 WSDL 是否是合法 XML（先用「校验」按钮验证）
- 检查根元素是否是 `<wsdl:definitions>`（local name `definitions`）
- URL 导入会在同源/安全策略允许时递归拉取外部 XSD；本地导入请一次多选 WSDL 和所有引用的 XSD
- 解析失败不会崩溃，会在项目详情里显示 `parse_error`，并尽量返回已解析到的部分

### Q5：XSD import 失败怎么办？

- 工具箱会在 Warnings 里提示哪些 XSD import/include 没加载
- 优先方案：点“上传文件”，一次选择 WSDL 和所有引用的 XSD；文件名和相对路径应与 `schemaLocation` 对应
- 仍无法匹配时，再考虑把 XSD 内容合并到 WSDL 的 `<types>` 节点后重新导入

### Q6：Mock 路径必须以 /mock/ 开头吗？

- 是的，所有 Mock 路径都会被规范化为 `/mock/` 前缀
- 输入 `customerQuery` 会自动变成 `/mock/customerQuery`
- 这是出于安全考虑，避免 Mock 路径覆盖工具箱自身的 API

### Q7：Mock 能被外部系统访问吗？

- 可以。`/mock/` 前缀的请求不走工具箱的 `/api/` 鉴权与 license 网关，外部系统直接请求即可
- 工具箱监听 127.0.0.1，外部访问需要把工具箱部署在能被对方访问到的机器上，并修改监听地址为 `0.0.0.0`（生产环境注意防火墙）

### Q8：历史记录有敏感字段怎么办？

- 左侧历史 Tab 底部有 **"一键清空"** 按钮，确认后删除全部历史
- 数据文件 `data/soap_history.json` 权限 0600（仅当前用户可读）

---

## 11. API 列表

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/wsdl/import-url` | 从 URL 导入 WSDL |
| POST | `/api/wsdl/import-file` | 上传 WSDL 文本导入 |
| GET | `/api/wsdl/projects` | 列出所有 WSDL 项目 |
| POST | `/api/wsdl/projects` | 保存 WSDL 项目 |
| GET | `/api/wsdl/projects/{id}` | 取单个 WSDL 项目 |
| DELETE | `/api/wsdl/projects/{id}` | 删除 WSDL 项目 |
| POST | `/api/soap/generate` | 按 operation 生成 Envelope |
| POST | `/api/soap/send` | 发送 SOAP 请求，自动记历史 |
| GET | `/api/soap/templates` | 列出模板 |
| POST | `/api/soap/templates` | 新增/覆盖模板 |
| PUT/POST | `/api/soap/templates/{id}` | 覆盖指定 ID 模板 |
| DELETE | `/api/soap/templates/{id}` | 删除模板 |
| GET | `/api/soap/history` | 列出历史 |
| DELETE | `/api/soap/history` | 清空历史 |
| POST | `/api/soap/history/{id}/replay` | 重放指定历史 |
| GET | `/api/soap/mocks` | 列出 Mock 配置 |
| POST | `/api/soap/mocks` | 新增/覆盖 Mock，保存后立即生效 |
| PUT/POST | `/api/soap/mocks/{id}` | 覆盖指定 ID Mock |
| DELETE | `/api/soap/mocks/{id}` | 删除 Mock |
| GET | `/api/soap/mocks/records` | 列出 Mock 请求记录 |
| DELETE | `/api/soap/mocks/records` | 清空 Mock 请求记录 |
| POST | `/api/ws/xml/format` | XML 格式化 |
| POST | `/api/ws/xml/minify` | XML 压缩 |
| POST | `/api/ws/xml/validate` | XML well-formed 校验 |
| ANY | `/mock/{path}` | Mock 服务端路由（外部系统调用，无需鉴权） |

---

## 12. 数据文件

全部存在 `data/` 目录，JSON 单文件，原子写（temp + rename），权限 0600：

| 文件 | 内容 | 上限 |
| --- | --- | --- |
| `wsdl_projects.json` | WSDL 项目列表 | 无硬上限 |
| `soap_templates.json` | 模板列表 | 无硬上限 |
| `soap_history.json` | 请求历史 | 500 条（超出丢弃最旧） |
| `soap_mocks.json` | Mock 配置 | 无硬上限 |
| `soap_mock_records.json` | Mock 请求记录 | 200 条（超出丢弃最旧） |

所有数据结构带 `version` 字段（当前 = 1），方便以后升级持久化格式。

---

## 13. 安全注意事项

- **URL 导入**：30s 超时，最多拉取 4MB
- **文件导入**：仅接受请求体里的 WSDL/XSD 文本，不直接读服务器磁盘任意路径；单文件 4MB、合计 16MB
- **HTML 转义**：所有 API 响应通过 `json.Encoder` 输出，自动转义，避免 XSS
- **历史清空**：一键清空按钮，敏感数据可随时清除
- **单 exe 部署**：不引入数据库，不依赖公网，不引入重量级服务
- **Windows 兼容**：纯 Go 标准库 + `golang.org/x/text`（GBK/GB2312/GB18030 编码），无平台依赖
- **Mock 路由鉴权**：`/mock/` 前缀不走 `/api/` 鉴权，外部系统可直接调用；但工具箱默认监听 127.0.0.1，外部访问需修改监听地址
