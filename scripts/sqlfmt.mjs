#!/usr/bin/env node
// scripts/sqlfmt.js
//
// OpsToolbox 的 SQL 格式化胶水脚本：接收 stdin 上的 JSON 请求，调 sql-formatter 库，
// 把结果 / 错误写回 stdout（也是 JSON）。
//
// 为什么需要这一层？
//   Go 生态里没有覆盖多方言（MySQL/PostgreSQL/Oracle/SQLite/BigQuery/Snowflake/...）
//   且活跃维护的 SQL formatter 库。sql-formatter-org/sql-formatter 是这个领域
//   事实标准（24k+ star，20+ 方言），用 Node 实现 → 通过本地 node 子进程调用，
//   完全本地，不出网络。
//
// 输入（stdin，UTF-8 JSON）：
//   {
//     "sql": "SELECT * FROM t WHERE x=1",
//     "language": "mysql",            // 必填，方言名
//     "keyword_case": "upper",         // upper | lower | preserve，默认 upper
//     "tab_width": 2,                  // 缩进宽度，默认 2
//     "indent_style": "standard",      // standard | tabularLeft | tabularRight，默认 standard
//     "logical_operator_newline": "before", // before | after，默认 before
//     "lines_between_queries": 2,      // 多语句间隔，默认 2
//     "max_column_length": 50          // 单行最大长度，默认 50
//   }
//
// 输出（stdout，UTF-8 JSON）：
//   { "ok": true,  "output": "SELECT\n  *\nFROM\n  t\n..." }
//   { "ok": false, "error":  "...", "stage": "parse|format|input" }
//
// 退出码：始终 0；调用方按 ok 字段判断。
//
// 注意：必须用 ESM（"type":"module"）才能 import sql-formatter 的 ESM 入口。
//       用 .mjs 后缀避免对仓库其他 .js 的 module 规则产生影响。

import { format } from 'sql-formatter';

function readStdin() {
  return new Promise((resolve, reject) => {
    let data = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (c) => (data += c));
    process.stdin.on('end', () => resolve(data));
    process.stdin.on('error', reject);
  });
}

function emit(obj) {
  process.stdout.write(JSON.stringify(obj));
}

const SUPPORTED_LANGS = new Set([
  'sql', 'bigquery', 'db2', 'duckdb', 'mariadb', 'mysql', 'n1ql',
  'plsql', 'postgresql', 'redshift', 'snowflake', 'spark', 'sqlite',
  'tsql', 'trino', 'transact',
]);
const SUPPORTED_CASE = new Set(['upper', 'lower', 'preserve']);
const SUPPORTED_INDENT_STYLE = new Set(['standard', 'tabularLeft', 'tabularRight']);
const SUPPORTED_OP_NEWLINE = new Set(['before', 'after']);

(async () => {
  let raw;
  try {
    raw = await readStdin();
  } catch (e) {
    emit({ ok: false, stage: 'input', error: '读取 stdin 失败: ' + e.message });
    return;
  }
  if (!raw || !raw.trim()) {
    emit({ ok: false, stage: 'input', error: 'stdin 为空' });
    return;
  }
  let req;
  try {
    req = JSON.parse(raw);
  } catch (e) {
    emit({ ok: false, stage: 'input', error: 'JSON 解析失败: ' + e.message });
    return;
  }
  const sql = typeof req.sql === 'string' ? req.sql : '';
  if (!sql) {
    emit({ ok: false, stage: 'input', error: 'sql 不能为空' });
    return;
  }
  const language = String(req.language || 'sql').toLowerCase();
  if (!SUPPORTED_LANGS.has(language)) {
    emit({
      ok: false,
      stage: 'input',
      error: '不支持的 language: ' + language + '（支持的见文档）',
    });
    return;
  }
  const keywordCase = String(req.keyword_case || 'upper').toLowerCase();
  if (!SUPPORTED_CASE.has(keywordCase)) {
    emit({ ok: false, stage: 'input', error: '不支持的 keyword_case: ' + keywordCase });
    return;
  }
  const indentStyle = String(req.indent_style || 'standard').toLowerCase();
  if (!SUPPORTED_INDENT_STYLE.has(indentStyle)) {
    emit({ ok: false, stage: 'input', error: '不支持的 indent_style: ' + indentStyle });
    return;
  }
  const logicalOperatorNewline = String(req.logical_operator_newline || 'before').toLowerCase();
  if (!SUPPORTED_OP_NEWLINE.has(logicalOperatorNewline)) {
    emit({ ok: false, stage: 'input', error: '不支持的 logical_operator_newline: ' + logicalOperatorNewline });
    return;
  }
  const tabWidth = Number.isFinite(req.tab_width) ? Number(req.tab_width) : 2;
  const linesBetweenQueries = Number.isFinite(req.lines_between_queries)
    ? Number(req.lines_between_queries)
    : 2;
  const maxColumnLength = Number.isFinite(req.max_column_length)
    ? Number(req.max_column_length)
    : 50;

  let out;
  try {
    out = format(sql, {
      language,
      keywordCase,
      tabWidth,
      indentStyle,
      logicalOperatorNewline,
      linesBetweenQueries,
      maxColumnLength,
    });
  } catch (e) {
    // sql-formatter 不会因为非法 SQL 抛错——它对未识别 token 走的是原样保留策略。
    // 这里仍然兜底，万一未来版本改了行为不会让进程崩。
    emit({ ok: false, stage: 'format', error: e.message || String(e) });
    return;
  }
  emit({ ok: true, output: out });
})();