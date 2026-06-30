'use strict';

const menuList = [
  { route: 'home', name: '首页', hash: '#/home' },
  { route: 'websphere', name: '日志助手', hash: '#/websphere' },
  { route: 'files', name: '文件下载', hash: '#/files' },
  { route: 'formatter', name: '报文格式化', hash: '#/formatter' },
  { route: 'http', name: 'HTTP 测试', hash: '#/http' },
  { route: 'commands', name: '常用命令', hash: '#/commands' },
  { route: 'diagnostics', name: '环境自检', hash: '#/diagnostics' },
  { route: 'config', name: '系统配置', hash: '#/config' },
  { route: 'downloads', name: '下载历史', hash: '#/downloads' },
  { route: 'timestamp', name: '时间戳', hash: '#/timestamp' },
  { route: 'cron', name: 'Cron 解析', hash: '#/cron' },
  { route: 'jsonpath', name: 'JSONPath', hash: '#/jsonpath' },
  { route: 'compare', name: '代码比对', hash: '#/compare' },
  { route: 'about', name: '关于', hash: '#/about' },
];

const pageMeta = {
  home: { name: '首页', expectedCrumb: '首页', expectedTitle: '首页', hasDangerButtons: false },
  websphere: { name: '日志助手', expectedCrumb: 'WebSphere 日志助手', expectedTitle: '日志助手', hasDangerButtons: true },
  files: { name: '文件下载', expectedCrumb: '文件下载', expectedTitle: '文件下载', hasDangerButtons: true },
  formatter: { name: '报文格式化', expectedCrumb: '报文格式化', expectedTitle: '报文格式化', hasDangerButtons: false },
  http: { name: 'HTTP 测试', expectedCrumb: 'HTTP 测试', expectedTitle: 'HTTP 测试', hasDangerButtons: false },
  commands: { name: '常用命令', expectedCrumb: '常用命令', expectedTitle: '常用命令', hasDangerButtons: false },
  diagnostics: { name: '环境自检', expectedCrumb: '环境自检', expectedTitle: '环境自检', hasDangerButtons: false },
  config: { name: '系统配置', expectedCrumb: '系统配置', expectedTitle: '系统配置', hasDangerButtons: true },
  downloads: { name: '下载历史', expectedCrumb: '下载历史', expectedTitle: '下载历史', hasDangerButtons: true },
  timestamp: { name: '时间戳', expectedCrumb: '时间戳转换', expectedTitle: '时间戳', hasDangerButtons: false },
  cron: { name: 'Cron 解析', expectedCrumb: 'Cron 解析', expectedTitle: 'Cron 解析', hasDangerButtons: false },
  jsonpath: { name: 'JSONPath', expectedCrumb: 'JSONPath 查询', expectedTitle: 'JSONPath 查询', hasDangerButtons: false },
  compare: { name: '代码比对', expectedCrumb: '代码比对', expectedTitle: '文本比对', hasDangerButtons: false },
  about: { name: '关于', expectedCrumb: '关于', expectedTitle: '关于', hasDangerButtons: false },
};

const apiList = [
  '/api/config',
  '/api/auth/status',
  '/api/ssh/test',
  '/api/logs/list/targets',
  '/api/logs/search/multi',
  '/api/logs/context',
  '/api/logs/download-latest',
  '/api/logs/download/{id}/events',
  '/api/logs/download/{id}/cancel',
  '/api/logs/tail/start',
  '/api/logs/tail/{id}/events',
  '/api/logs/tail/{id}/stop',
  '/api/files/list',
  '/api/files/preview',
  '/api/files/download',
  '/api/files/download/{id}/events',
  '/api/files/download/{id}/cancel',
  '/api/downloads/list',
  '/api/downloads/open-dir',
  '/api/format/json',
  '/api/format/xml',
  '/api/format/yaml',
  '/api/format/url-form',
  '/api/format/timestamp',
  '/api/format/cron-parse',
  '/api/format/jsonpath',
  '/api/http/cases',
  '/api/http/envs',
  '/api/http/request',
  '/api/diagnostics',
  '/api/diff/compare',
  '/api/compare/folder-scan',
  '/api/compare/file-diff',
  '/api/config/export',
  '/api/config/import',
  '/api/admin/servers',
  '/api/admin/openers',
  '/api/admin/download-retention',
  '/api/credentials/save',
  '/api/credentials/has',
  '/api/credentials/clear',
  '/api/preferences',
  '/api/local/open-folder',
  '/api/local/open-with',
  '/api/choose-file',
  '/api/choose-dir',
];

const themes = ['dark', 'light', 'green', 'hc'];

const viewports = [
  { name: 'desktop-1366', width: 1366, height: 900 },
  { name: 'desktop-1920', width: 1920, height: 1080 },
  { name: 'half-960', width: 960, height: 1080 },
];

const DANGEROUS_BUTTON_PATTERNS = [
  '删除',
  '清空',
  '重置',
  '停止',
  '取消',
  '放弃',
  '关闭',
  '取消下载',
  '清空全部',
  '放弃改动',
  '取消修改',
];

const MOCK_SSH = {
  host: '127.0.0.1',
  port: 2222,
  username: 'test',
  password: 'test',
};

const homeCards = [
  { name: '日志助手', route: 'websphere' },
  { name: '文件下载', route: 'files' },
  { name: '报文格式化', route: 'formatter' },
  { name: 'HTTP 接口测试', route: 'http' },
  { name: '常用命令速查', route: 'commands' },
  { name: '环境自检', route: 'diagnostics' },
  { name: '下载历史', route: 'downloads' },
  { name: '系统配置', route: 'config' },
  { name: '时间戳转换', route: 'timestamp' },
  { name: 'Cron 解析', route: 'cron' },
  { name: 'JSONPath 查询', route: 'jsonpath' },
  { name: '文本比对', route: 'compare' },
];

module.exports = {
  menuList,
  pageMeta,
  apiList,
  themes,
  viewports,
  DANGEROUS_BUTTON_PATTERNS,
  MOCK_SSH,
  homeCards,
};
