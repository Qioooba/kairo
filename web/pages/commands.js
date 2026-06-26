/* ===== web/pages/commands.js =====
 * 常用命令速查（v0.7 新增）
 *
 * 设计：
 *   - 纯前端静态数据 + 客户端即时搜索，零后端改动
 *   - 6 大分类 tabs + 顶栏搜索 + 收藏 tab（localStorage 持久化）
 *   - 每条命令卡片：标题 / 标签 / syntax（默认显示） / 展开看 example + desc
 *   - 复制按钮：复制 syntax 主体，toast 反馈
 *   - 快捷键："/" 聚焦搜索框，"Esc" 清空
 *   - 主题：复用全局 4 套主题（dark/light/green/hc）
 *
 * 数据 schema：
 *   {
 *     id:        string   // 唯一 ID，用于收藏 localStorage key
 *     title:     string   // 命令名 / 快捷键名
 *     category:  string   // 分类 ID（见 CATEGORIES）
 *     tags:      string[] // 搜索关键词（中文别名 / 用途）
 *     syntax:    string   // 主命令 / 代码
 *     example:   string   // 示例（可选）
 *     desc:      string   // 简短说明
 *     platform?: 'mac' | 'linux' | 'win' | 'all'  // 平台适配，默认 all
 *   }
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast, copyToClipboard, escapeHtml } = OTB.core;

  // ===== 分类定义（顺序就是 tab 顺序） =====
  const CATEGORIES = [
    { id: 'fav',       name: '⭐ 收藏', icon: '★' },
    { id: 'linux',     name: 'Linux',  icon: '🐧' },
    { id: 'git',       name: 'Git',    icon: '⎇' },
    { id: 'docker',    name: 'Docker', icon: '🐳' },
    { id: 'oracle',    name: 'Oracle', icon: '🗄' },
    { id: 'mysql',     name: 'MySQL',  icon: '🐬' },
    { id: 'redis',     name: 'Redis',  icon: '🟥' },
    { id: 'nginx',     name: 'Nginx',  icon: '🌐' },
    { id: 'java',      name: 'Java 后端', icon: '☕' },
    { id: 'frontend',  name: '前端',   icon: '🎨' },
    { id: 'idea',      name: 'IDEA 快捷键', icon: '⌨' },
  ];

  // ===== localStorage 收藏 =====
  const FAV_LS_KEY = 'otb:commands:favs';
  function loadFavs() {
    try {
      const raw = localStorage.getItem(FAV_LS_KEY);
      if (!raw) return new Set();
      const arr = JSON.parse(raw);
      return new Set(Array.isArray(arr) ? arr : []);
    } catch (e) { return new Set(); }
  }
  function saveFavs(set) {
    try { localStorage.setItem(FAV_LS_KEY, JSON.stringify(Array.from(set))); }
    catch (e) { /* ignore */ }
  }
  let favs = loadFavs();

  // ===== 命令数据 =====
  const COMMANDS = [
    // ============ Linux ============
    { id: 'linux-ls', category: 'linux', title: 'ls 列出目录', tags: ['文件','目录','list'],
      syntax: 'ls -lah',
      example: 'ls -lah /var/log/',
      desc: '-l 详细 -a 包含隐藏 -h 人类可读大小' },
    { id: 'linux-cd', category: 'linux', title: 'cd 切换目录', tags: ['目录','切换'],
      syntax: 'cd -',
      example: 'cd /opt/app',
      desc: '"-" 回到上次所在目录；"~" 回 home；".." 上级' },
    { id: 'linux-pwd', category: 'linux', title: 'pwd 当前路径', tags: ['目录','路径'],
      syntax: 'pwd',
      desc: '打印当前工作目录的绝对路径' },
    { id: 'linux-mkdir', category: 'linux', title: 'mkdir 创建目录', tags: ['目录','创建'],
      syntax: 'mkdir -p a/b/c',
      desc: '-p 递归创建多级目录' },
    { id: 'linux-rm', category: 'linux', title: 'rm 删除', tags: ['删除','文件'],
      syntax: 'rm -rf <path>',
      example: 'rm -rf ./node_modules',
      desc: '-r 递归 -f 强制。删除前再三确认，慎用' },
    { id: 'linux-cp', category: 'linux', title: 'cp 复制', tags: ['复制','文件'],
      syntax: 'cp -rp <src> <dst>',
      desc: '-r 递归目录 -p 保留权限/时间戳' },
    { id: 'linux-mv', category: 'linux', title: 'mv 移动/重命名', tags: ['移动','重命名'],
      syntax: 'mv <src> <dst>',
      desc: '同一目录下 = 重命名；不同目录 = 移动' },
    { id: 'linux-cat', category: 'linux', title: 'cat 查看文件', tags: ['查看','文件','读取'],
      syntax: 'cat <file>',
      example: 'cat -n file.txt | tail -50',
      desc: '输出整个文件；大文件用 less/more' },
    { id: 'linux-less', category: 'linux', title: 'less 分页查看', tags: ['查看','分页'],
      syntax: 'less +F <file>',
      desc: '+F 进入 tail -f 模式（按 Ctrl+C 退出 follow）' },
    { id: 'linux-tail', category: 'linux', title: 'tail 看文件末尾', tags: ['日志','查看','尾部'],
      syntax: 'tail -f -n 200 <file>',
      example: 'tail -f /var/log/messages',
      desc: '-f 持续跟踪；-n 指定行数；-F = -f + retry（文件被重建也能跟）' },
    { id: 'linux-head', category: 'linux', title: 'head 看文件头部', tags: ['查看','头部'],
      syntax: 'head -n 50 <file>',
      desc: '看前 N 行' },
    { id: 'linux-grep', category: 'linux', title: 'grep 文本搜索', tags: ['搜索','过滤','正则'],
      syntax: 'grep -rn --include="*.log" "ERROR" /var/log/',
      example: 'grep -C 3 "OutOfMemory" server.log',
      desc: '-r 递归 -n 显示行号 -i 忽略大小写 -C 上下文；--include 控制文件类型' },
    { id: 'linux-grep-v', category: 'linux', title: 'grep 反向匹配', tags: ['grep','排除'],
      syntax: 'grep -v "DEBUG"',
      example: 'grep "ERROR" log.txt | grep -v "心跳"',
      desc: '排除包含关键字的行' },
    { id: 'linux-grep-egrep', category: 'linux', title: 'egrep 扩展正则', tags: ['grep','正则'],
      syntax: 'egrep -n "ERROR|WARN" *.log',
      desc: '默认走 ERE，无需转义 | + ? ()' },
    { id: 'linux-find', category: 'linux', title: 'find 查找文件', tags: ['查找','搜索','文件'],
      syntax: 'find / -name "*.log" -size +100M -mtime -7',
      example: 'find . -name "*.tmp" -mtime +30 -delete',
      desc: '-name 文件名 -size 大小 -mtime 修改时间(天) -type f/d -exec 命令 {} \\; 对结果执行' },
    { id: 'linux-find-exec', category: 'linux', title: 'find -exec 批处理', tags: ['find','批处理'],
      syntax: 'find . -name "*.log" -exec gzip {} \\;',
      desc: '对每个匹配执行命令；性能比 xargs 略差但参数更安全' },
    { id: 'linux-xargs', category: 'linux', title: 'xargs 构造命令行', tags: ['批处理','管道'],
      syntax: 'find . -name "*.log" | xargs grep "ERROR"',
      desc: '把 stdin 拼成命令行参数；-I {} 替换占位符' },
    { id: 'linux-awk', category: 'linux', title: 'awk 文本处理', tags: ['文本','处理','列'],
      syntax: "awk '{print $1, $3}' file.txt",
      example: "awk -F'|' '$3>100 {print $1, $3}' data.txt",
      desc: '按列处理；$1..$N 是字段；-F 指定分隔符；NR 行号；FS/OFS' },
    { id: 'linux-sed', category: 'linux', title: 'sed 流编辑器', tags: ['文本','替换','编辑'],
      syntax: "sed -i 's/old/new/g' file.txt",
      example: "sed -n '100,200p' file.txt",
      desc: 's/old/new/g 全局替换；-i 原地编辑；-n p 打印指定行' },
    { id: 'linux-sort-uniq', category: 'linux', title: 'sort + uniq 去重计数', tags: ['去重','统计','排序'],
      syntax: 'sort file | uniq -c | sort -rn | head',
      desc: '经典组合：排序后去重计数，再按频次降序取 top' },
    { id: 'linux-cut', category: 'linux', title: 'cut 按列切分', tags: ['文本','列','切分'],
      syntax: "cut -d',' -f1,3 file.csv",
      desc: '-d 分隔符 -f 取哪些列（1-based）' },
    { id: 'linux-wc', category: 'linux', title: 'wc 统计行/字/字节', tags: ['统计','行数'],
      syntax: 'wc -l file.txt',
      desc: '-l 行数 -w 字数 -c 字节数' },
    { id: 'linux-ps', category: 'linux', title: 'ps 查看进程', tags: ['进程'],
      syntax: 'ps -ef | grep <name>',
      example: 'ps aux --sort=-%cpu | head -20',
      desc: 'aux 全量 BSD 风格；-ef 全量 SystemV 风格' },
    { id: 'linux-top', category: 'linux', title: 'top 实时进程', tags: ['进程','监控','CPU','内存'],
      syntax: 'top -d 1 -p <pid>',
      desc: 'P 按 CPU 排序，M 按内存排序，1 展开多核，q 退出' },
    { id: 'linux-htop', category: 'linux', title: 'htop 增强 top', tags: ['进程','监控'],
      syntax: 'htop',
      desc: '彩色 + 鼠标 + 树状视图（需安装：yum install htop / apt install htop）' },
    { id: 'linux-lsof', category: 'linux', title: 'lsof 文件/端口占用', tags: ['端口','文件','进程'],
      syntax: 'lsof -i :8080',
      example: 'lsof -p <pid>',
      desc: '查端口被谁占用 / 进程打开了哪些文件' },
    { id: 'linux-netstat', category: 'linux', title: 'netstat 网络状态', tags: ['网络','端口','连接'],
      syntax: 'netstat -tunlp | grep :80',
      desc: '-t tcp -u udp -n 数字端口 -l listen -p 进程；ss 是更现代的替代' },
    { id: 'linux-ss', category: 'linux', title: 'ss 网络状态（替代 netstat）', tags: ['网络','端口'],
      syntax: 'ss -tunlp',
      desc: '更快的网络连接查看工具' },
    { id: 'linux-curl', category: 'linux', title: 'curl HTTP 请求', tags: ['HTTP','接口','请求'],
      syntax: 'curl -X POST -H "Content-Type: application/json" -d \'{"a":1}\' http://host/api',
      example: 'curl -v -o /dev/null -w "%{http_code}\\n" http://host/',
      desc: '-v 详细 -X 方法 -H 头 -d body -o 输出文件 -w 自定义输出 -L 跟随重定向' },
    { id: 'linux-wget', category: 'linux', title: 'wget 下载', tags: ['下载','HTTP'],
      syntax: 'wget -c <url>',
      desc: '-c 断点续传；-O 指定输出文件名' },
    { id: 'linux-scp', category: 'linux', title: 'scp 跨机拷贝', tags: ['复制','远程','SSH'],
      syntax: 'scp -P 22 file.txt user@host:/path/',
      example: 'scp -r dir/ user@host:/path/',
      desc: '-P 端口 -r 递归目录' },
    { id: 'linux-rsync', category: 'linux', title: 'rsync 增量同步', tags: ['同步','远程','备份'],
      syntax: 'rsync -avz --progress src/ user@host:/dst/',
      example: 'rsync -avz --delete src/ dst/',
      desc: '-a 归档 -v 详细 -z 压缩；--delete 镜像删除；适合定时备份' },
    { id: 'linux-ssh', category: 'linux', title: 'ssh 远程登录', tags: ['SSH','远程','登录'],
      syntax: 'ssh -p 22 -i ~/.ssh/id_rsa user@host',
      example: 'ssh -L 8080:localhost:80 user@host',
      desc: '-p 端口 -i 私钥 -L 本地端口转发；-D socks 代理' },
    { id: 'linux-ssh-keygen', category: 'linux', title: 'ssh-keygen 生成密钥', tags: ['SSH','密钥','免密'],
      syntax: 'ssh-keygen -t ed25519 -C "you@example.com"',
      desc: 'ed25519 比 RSA 短且快；-C 加注释；公钥放 ~/.ssh/authorized_keys' },
    { id: 'linux-ssh-config', category: 'linux', title: '~/.ssh/config 别名', tags: ['SSH','配置','别名'],
      syntax: 'Host myhost\n  HostName 10.0.0.1\n  Port 22\n  User admin\n  IdentityFile ~/.ssh/id_rsa',
      desc: '配置后直接 `ssh myhost` 即可登录' },
    { id: 'linux-chmod', category: 'linux', title: 'chmod 改权限', tags: ['权限','文件'],
      syntax: 'chmod 755 file\nchmod -R 644 dir/',
      example: 'chmod u+x script.sh',
      desc: '数字 4=r 2=w 1=x；u/g/o 字母方式更直观' },
    { id: 'linux-chown', category: 'linux', title: 'chown 改属主', tags: ['权限','属主'],
      syntax: 'chown -R user:group /opt/app',
      desc: '-R 递归；常用于部署后改应用文件归属' },
    { id: 'linux-df', category: 'linux', title: 'df 磁盘使用', tags: ['磁盘','空间','监控'],
      syntax: 'df -h',
      desc: '看各挂载点剩余空间' },
    { id: 'linux-du', category: 'linux', title: 'du 目录大小', tags: ['磁盘','大小','目录'],
      syntax: 'du -sh /var/log/* | sort -h | tail',
      desc: '-s 总计 -h 人类可读；常用来找大文件/大目录' },
    { id: 'linux-free', category: 'linux', title: 'free 内存使用', tags: ['内存','监控'],
      syntax: 'free -h',
      desc: '看系统内存 / swap 使用' },
    { id: 'linux-iostat', category: 'linux', title: 'iostat IO 监控', tags: ['IO','磁盘','监控'],
      syntax: 'iostat -xz 1',
      desc: 'await / %util 高表示 IO 瓶颈' },
    { id: 'linux-tar', category: 'linux', title: 'tar 打包/解包', tags: ['压缩','打包','归档'],
      syntax: 'tar -czvf archive.tar.gz dir/\ntar -xzvf archive.tar.gz -C /target/',
      desc: 'c 打包 x 解包 z gzip j bz2 v 详细 f 文件名' },
    { id: 'linux-zip', category: 'linux', title: 'zip / unzip', tags: ['压缩','解压'],
      syntax: 'zip -r a.zip dir/\nunzip -o a.zip -d /target/',
      desc: '-r 递归 -o 覆盖不提示' },
    { id: 'linux-history', category: 'linux', title: 'history 命令历史', tags: ['历史','命令'],
      syntax: 'history | grep "docker"',
      example: '!1023',
      desc: '!N 重新执行第 N 条；Ctrl+R 反向搜索' },
    { id: 'linux-screen', category: 'linux', title: 'screen / tmux 后台会话', tags: ['后台','会话','终端'],
      syntax: 'screen -S name   # 新建\nscreen -r name   # 恢复\nCtrl+A D         # 脱离',
      desc: 'SSH 断开不丢任务；tmux 是更现代的替代' },
    { id: 'linux-crontab', category: 'linux', title: 'crontab 定时任务', tags: ['定时','cron','计划任务'],
      syntax: '* * * * * /path/to/script.sh >> /var/log/cron.log 2>&1',
      example: 'crontab -e',
      desc: '分 时 日 月 周；* 每 ; */5 每 5 ; 0 3 * * * 每天 3 点' },
    { id: 'linux-systemctl', category: 'linux', title: 'systemctl 服务管理', tags: ['服务','systemd','启动'],
      syntax: 'systemctl status nginx\nsystemctl restart nginx\nsystemctl enable nginx',
      desc: 'start/stop/restart/reload/enable/disable/status' },
    { id: 'linux-journal', category: 'linux', title: 'journalctl 系统日志', tags: ['日志','systemd'],
      syntax: 'journalctl -u nginx -f --since "1 hour ago"',
      desc: '-u 指定服务 -f follow --since 时间范围 -p err 仅错误' },
    { id: 'linux-trace-route', category: 'linux', title: 'traceroute 路由跟踪', tags: ['网络','诊断','路由'],
      syntax: 'traceroute -n 8.8.8.8',
      desc: '看包到目标经过的每一跳；-n 不解析域名更快' },
    { id: 'linux-dig', category: 'linux', title: 'dig DNS 查询', tags: ['DNS','网络','诊断'],
      syntax: 'dig +short example.com\ndig @8.8.8.8 example.com',
      desc: '+short 只看结果；@ 指定 DNS server' },
    { id: 'linux-hostname', category: 'linux', title: 'hostname / hostnamectl', tags: ['主机名','系统'],
      syntax: 'hostnamectl set-hostname myhost',
      desc: 'CentOS/RHEL 改主机名后要重启或 logout 才完全生效' },
    { id: 'linux-tee', category: 'linux', title: 'tee 同时输出到文件和屏幕', tags: ['管道','输出'],
      syntax: 'cmd | tee -a /var/log/cmd.log',
      desc: '-a 追加；常用于既要看到结果又要保留日志' },

    // ============ Git ============
    { id: 'git-status', category: 'git', title: 'git status', tags: ['状态','查看'],
      syntax: 'git status',
      example: 'git status -sb   # 简短 + 分支上游',
      desc: '看工作区和暂存区的状态' },
    { id: 'git-log', category: 'git', title: 'git log 提交历史', tags: ['历史','提交','查看'],
      syntax: 'git log --oneline --graph --decorate -20',
      example: 'git log --author="zhangsan" --since="1 month ago"',
      desc: '--oneline 单行 --graph 图形分支 --all 所有分支' },
    { id: 'git-diff', category: 'git', title: 'git diff 对比', tags: ['差异','对比'],
      syntax: 'git diff\n// 暂存区 vs 最新版本\ngit diff --cached\n// 两个分支对比\ngit diff main..feature',
      desc: '默认对比工作区 vs 暂存区' },
    { id: 'git-add', category: 'git', title: 'git add 加入暂存', tags: ['暂存','添加'],
      syntax: 'git add .\ngit add -p   # 交互式按 hunk 暂存',
      desc: '-p 适合"只想 commit 部分改动"的场景' },
    { id: 'git-commit', category: 'git', title: 'git commit 提交', tags: ['提交','commit'],
      syntax: 'git commit -m "feat: 新增XX功能"',
      example: 'git commit --amend   # 改最近一次 commit',
      desc: '推荐 Conventional Commits 规范：feat/fix/chore/docs/...' },
    { id: 'git-push', category: 'git', title: 'git push 推送', tags: ['推送','远程'],
      syntax: 'git push -u origin feature/x',
      example: 'git push --force-with-lease   # 安全强推',
      desc: '-u 首次推送设上游；--force-with-lease 比 --force 安全（防止覆盖别人的新提交）' },
    { id: 'git-pull', category: 'git', title: 'git pull 拉取', tags: ['拉取','同步'],
      syntax: 'git pull --rebase',
      desc: '默认是 merge；rebase 后历史更线性（团队约定为准）' },
    { id: 'git-fetch', category: 'git', title: 'git fetch', tags: ['远程','同步'],
      syntax: 'git fetch --all --prune',
      desc: '同步远程信息但不动本地分支；--prune 清理已删除的远程分支' },
    { id: 'git-branch', category: 'git', title: 'git branch 分支', tags: ['分支'],
      syntax: 'git branch -a           # 列出所有\n// 新建并切换\ngit checkout -b feature/x\ngit switch -c feature/x',
      desc: 'switch 是 Git 2.23+ 的新写法，比 checkout 语义更清晰' },
    { id: 'git-checkout', category: 'git', title: 'git checkout / switch', tags: ['切换','分支','恢复'],
      syntax: 'git switch main\ngit checkout -- file.txt   # 放弃工作区修改',
      desc: 'switch 只管分支；restore（旧版）管文件恢复' },
    { id: 'git-merge', category: 'git', title: 'git merge 合并', tags: ['合并','merge'],
      syntax: 'git merge --no-ff feature/x',
      desc: '--no-ff 保留分支轨迹；fast-forward 时默认看不出合过' },
    { id: 'git-rebase', category: 'git', title: 'git rebase 变基', tags: ['变基','rebase'],
      syntax: 'git rebase main\n// 冲突后\ngit rebase --continue / --abort',
      desc: '让历史更线性；**不要对已推送的公共分支 rebase**' },
    { id: 'git-cherry-pick', category: 'git', title: 'git cherry-pick', tags: ['挑选','移植'],
      syntax: 'git cherry-pick <commit-sha>',
      example: 'git cherry-pick A..B   # 区间',
      desc: '把某次（或某段）commit 移植到当前分支' },
    { id: 'git-stash', category: 'git', title: 'git stash 暂存', tags: ['暂存','stash'],
      syntax: 'git stash\ngit stash list\ngit stash pop\n// 带 untracked\ngit stash -u',
      desc: '临时保存工作区改动；pop 恢复并删除 stash' },
    { id: 'git-reset', category: 'git', title: 'git reset', tags: ['回退','重置'],
      syntax: 'git reset --soft HEAD~1   # 撤销 commit，保留改动到暂存\ngit reset --mixed HEAD~1  # 撤销 commit + 暂存\ngit reset --hard HEAD~1    # 撤销一切（危险）',
      desc: '已推送的 commit 不要 reset，改用 revert' },
    { id: 'git-revert', category: 'git', title: 'git revert 反做', tags: ['回退','反做'],
      syntax: 'git revert <commit-sha>',
      desc: '生成一次"反向 commit"，适合已推送的场景' },
    { id: 'git-tag', category: 'git', title: 'git tag 标签', tags: ['标签','tag','版本'],
      syntax: 'git tag v1.0.0\ngit tag -a v1.0.0 -m "release"\ngit push origin v1.0.0',
      desc: '-a 注释 tag；推送时 tag 不会自动 push' },
    { id: 'git-blame', category: 'git', title: 'git blame', tags: ['追溯','责任'],
      syntax: 'git blame -L 100,120 file.txt',
      desc: '看某行是谁哪个 commit 改的' },
    { id: 'git-bisect', category: 'git', title: 'git bisect 二分定位', tags: ['二分','回归','bug'],
      syntax: 'git bisect start\ngit bisect bad\ngit bisect good v1.0\ngit bisect reset',
      desc: '二分查找引入 bug 的 commit' },
    { id: 'git-config', category: 'git', title: 'git config', tags: ['配置'],
      syntax: 'git config --global user.name "Your Name"\ngit config --global user.email "you@example.com"',
      example: 'git config --global pull.rebase true',
      desc: '--global 全局；项目级用 --local（在 .git/config）' },
    { id: 'git-remote', category: 'git', title: 'git remote 远程仓库', tags: ['远程','remote'],
      syntax: 'git remote -v\ngit remote set-url origin <new-url>',
      desc: '改 remote URL（迁移仓库时常用）' },
    { id: 'git-clean', category: 'git', title: 'git clean 清理未跟踪', tags: ['清理','未跟踪'],
      syntax: 'git clean -fd\ngit clean -nd   # 预览不删',
      desc: '-d 目录 -f 强制 -n 预览；未跟踪文件不会被 .gitignore 保护' },
    { id: 'git-restore', category: 'git', title: 'git restore 恢复文件', tags: ['恢复','还原'],
      syntax: 'git restore file.txt\ngit restore --staged file.txt   # 从暂存区撤出',
      desc: 'Git 2.23+ 推荐写法，语义比 checkout 清晰' },

    // ============ Docker ============
    { id: 'docker-ps', category: 'docker', title: 'docker ps 容器', tags: ['容器','查看','列表'],
      syntax: 'docker ps -a',
      example: 'docker ps -a --format "table {{.Names}}\\t{{.Status}}\\t{{.Ports}}"',
      desc: '-a 含已停止；--format 自定义输出' },
    { id: 'docker-images', category: 'docker', title: 'docker images 镜像', tags: ['镜像','列表'],
      syntax: 'docker images\ndocker images -a   # 含中间层',
      desc: '看本地镜像' },
    { id: 'docker-run', category: 'docker', title: 'docker run 启动容器', tags: ['运行','启动','容器'],
      syntax: 'docker run -d --name web -p 8080:80 -v /host:/container nginx:1.25',
      example: 'docker run -it --rm alpine sh',
      desc: '-d 后台 -it 交互 -p 端口映射 -v 挂载卷 --rm 退出即删 --name 命名' },
    { id: 'docker-exec', category: 'docker', title: 'docker exec 进容器', tags: ['进入','exec','终端'],
      syntax: 'docker exec -it <container> sh',
      example: 'docker exec -it web bash',
      desc: '在运行中的容器内开 shell' },
    { id: 'docker-logs', category: 'docker', title: 'docker logs', tags: ['日志','查看'],
      syntax: 'docker logs -f --tail 200 <container>',
      desc: '-f follow --tail 行数 --since 10m 时间范围' },
    { id: 'docker-stop-rm', category: 'docker', title: 'docker stop / rm', tags: ['停止','删除','容器'],
      syntax: 'docker stop <container>\ndocker rm <container>\n# 一次性\ndocker rm -f <container>',
      desc: 'rm 前需 stop；-f 强删' },
    { id: 'docker-rmi', category: 'docker', title: 'docker rmi / image prune', tags: ['删除','镜像','清理'],
      syntax: 'docker rmi <image>\ndocker image prune -a   # 清所有未用',
      desc: '被容器引用的镜像不能 rmi' },
    { id: 'docker-build', category: 'docker', title: 'docker build', tags: ['构建','镜像','build'],
      syntax: 'docker build -t myapp:1.0 .',
      desc: '-t 标签；. 表示当前目录 Dockerfile' },
    { id: 'docker-pull', category: 'docker', title: 'docker pull / push', tags: ['拉取','推送','镜像'],
      syntax: 'docker pull nginx:1.25\ndocker tag myapp:1.0 registry.example.com/myapp:1.0\ndocker push registry.example.com/myapp:1.0',
      desc: '推私有仓库要先 docker login' },
    { id: 'docker-network', category: 'docker', title: 'docker network', tags: ['网络'],
      syntax: 'docker network ls\ndocker network create mynet\ndocker run --network mynet ...',
      desc: '自定义网络内的容器可通过容器名直接互访' },
    { id: 'docker-volume', category: 'docker', title: 'docker volume 数据卷', tags: ['卷','持久化'],
      syntax: 'docker volume ls\ndocker volume create mydata\ndocker run -v mydata:/data ...',
      desc: '命名卷 = 容器删了数据还在；bind mount 直接挂主机目录' },
    { id: 'docker-compose', category: 'docker', title: 'docker compose', tags: ['compose','编排','多容器'],
      syntax: 'docker compose up -d\ndocker compose down\ndocker compose logs -f\ndocker compose ps',
      desc: 'v2 内置子命令（无需 docker-compose）；老版本是独立命令' },
    { id: 'docker-stats', category: 'docker', title: 'docker stats 资源监控', tags: ['资源','监控','CPU','内存'],
      syntax: 'docker stats',
      desc: '实时看每个容器的 CPU/内存/网络' },
    { id: 'docker-inspect', category: 'docker', title: 'docker inspect', tags: ['详情','配置'],
      syntax: 'docker inspect <container>\n// 看 IP\ndocker inspect -f "{{.NetworkSettings.IPAddress}}" <container>',
      desc: '看容器/镜像的完整配置 JSON' },
    { id: 'docker-cp', category: 'docker', title: 'docker cp 文件互拷', tags: ['复制','文件'],
      syntax: 'docker cp <container>:/path/in/container /local/path\ndocker cp /local/file <container>:/path',
      desc: '主机和容器间拷贝文件' },
    { id: 'docker-system-prune', category: 'docker', title: 'docker system prune', tags: ['清理','释放空间'],
      syntax: 'docker system prune -a --volumes',
      desc: '清所有停止的容器、未用镜像/网络/卷；-a 含未用镜像' },
    { id: 'docker-context', category: 'docker', title: 'docker context 切换 host', tags: ['远程','context'],
      syntax: 'docker context ls\ndocker context use my-remote',
      desc: '在本地 docker CLI 操作远程 dockerd' },

    // ============ Oracle ============
    { id: 'ora-nvl', category: 'oracle', title: 'NVL 空值处理', tags: ['空值','函数','NVL'],
      syntax: 'NVL(expr1, expr2)',
      example: 'SELECT NVL(commission, 0) FROM emp;',
      desc: 'expr1 为 NULL 时返回 expr2；NVL2(a,b,c) 是三元版' },
    { id: 'ora-decode', category: 'oracle', title: 'DECODE 简化 CASE', tags: ['函数','DECODE','CASE'],
      syntax: 'DECODE(col, val1, res1, val2, res2, default_res)',
      example: "DECODE(status, 'A', '活跃', 'I', '停用', '未知')",
      desc: 'Oracle 专属；功能等同 CASE WHEN，可读性看场景' },
    { id: 'ora-case', category: 'oracle', title: 'CASE WHEN 条件分支', tags: ['CASE','条件','函数'],
      syntax: "CASE WHEN sal > 10000 THEN '高' WHEN sal > 5000 THEN '中' ELSE '低' END",
      desc: '标准 SQL，跨数据库' },
    { id: 'ora-to-char-date', category: 'oracle', title: 'TO_CHAR 日期格式化', tags: ['TO_CHAR','日期','格式化'],
      syntax: "TO_CHAR(SYSDATE, 'YYYY-MM-DD HH24:MI:SS')",
      example: "TO_CHAR(hire_date, 'YYYY\"年\"MM\"月\"DD\"日\"')",
      desc: '"YYYY-MM-DD HH24:MI:SS" 24 小时制；HH12 12 小时制' },
    { id: 'ora-to-char-num', category: 'oracle', title: 'TO_CHAR 数字格式化', tags: ['TO_CHAR','数字','格式化'],
      syntax: "TO_CHAR(12345.6, 'FM999,999.00')",
      example: "TO_CHAR(salary, 'FM$999,999.00')",
      desc: 'FM 去前导 0；9 = 数字位；0 = 强制显示' },
    { id: 'ora-to-date', category: 'oracle', title: 'TO_DATE 字符串转日期', tags: ['TO_DATE','日期','解析'],
      syntax: "TO_DATE('2024-06-25 14:30:00', 'YYYY-MM-DD HH24:MI:SS')",
      desc: '第二个参数必须严格匹配输入格式' },
    { id: 'ora-to-number', category: 'oracle', title: 'TO_NUMBER 字符串转数字', tags: ['TO_NUMBER','转换'],
      syntax: "TO_NUMBER('1,234.56', '999,999.99')",
      desc: '按格式解析数字字符串' },
    { id: 'ora-trunc-date', category: 'oracle', title: 'TRUNC 日期截断', tags: ['TRUNC','日期'],
      syntax: "TRUNC(SYSDATE, 'MM')   -- 当月第一天\nTRUNC(SYSDATE, 'YYYY') -- 当年第一天\nTRUNC(SYSDATE, 'DD')   -- 当天 0 点",
      desc: '截断到指定精度（月份/年份/日）' },
    { id: 'ora-add-months', category: 'oracle', title: 'ADD_MONTHS 月份加减', tags: ['日期','月份','函数'],
      syntax: 'ADD_MONTHS(SYSDATE, 3)',
      desc: '加 3 个月；自动处理月末（如 1/31 + 1 月 = 2/28）' },
    { id: 'ora-months-between', category: 'oracle', title: 'MONTHS_BETWEEN 月份差', tags: ['日期','月份差','函数'],
      syntax: 'MONTHS_BETWEEN(d1, d2)',
      desc: '返回 d1-d2 的月份数（小数）' },
    { id: 'ora-last-day', category: 'oracle', title: 'LAST_DAY 月末', tags: ['月末','日期','函数'],
      syntax: 'LAST_DAY(SYSDATE)',
      desc: '返回当月最后一天' },
    { id: 'ora-row-number', category: 'oracle', title: 'ROW_NUMBER 排名', tags: ['分析函数','ROW_NUMBER','排名'],
      syntax: 'ROW_NUMBER() OVER (PARTITION BY dept ORDER BY sal DESC) rn',
      desc: '不并列，1,2,3,4...；RANK() 并列跳跃；DENSE_RANK() 并列不跳跃' },
    { id: 'ora-rank', category: 'oracle', title: 'RANK / DENSE_RANK', tags: ['分析函数','RANK','排名'],
      syntax: 'RANK() OVER (ORDER BY sal DESC)\nDENSE_RANK() OVER (ORDER BY sal DESC)',
      desc: 'RANK：1,1,3,4；DENSE_RANK：1,1,2,3' },
    { id: 'ora-lag-lead', category: 'oracle', title: 'LAG / LEAD 前后行', tags: ['分析函数','LAG','LEAD'],
      syntax: 'LAG(sal, 1, 0) OVER (ORDER BY hire_date) prev_sal',
      desc: 'LAG 取上 N 行；LEAD 取下 N 行；常用于环比' },
    { id: 'ora-sum-over', category: 'oracle', title: 'SUM OVER 累计', tags: ['分析函数','累计','求和'],
      syntax: 'SUM(amount) OVER (ORDER BY dt ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) cum',
      desc: '窗口函数实现累计/移动平均' },
    { id: 'ora-rownum', category: 'oracle', title: 'ROWNUM / ROWID', tags: ['伪列','ROWNUM','分页'],
      syntax: 'SELECT * FROM (SELECT t.*, ROWNUM rn FROM t WHERE ROWNUM <= 20) WHERE rn > 10',
      desc: 'Oracle 老式分页；11g+ 推荐 OFFSET ... FETCH' },
    { id: 'ora-offset-fetch', category: 'oracle', title: 'OFFSET FETCH 分页', tags: ['分页','OFFSET','FETCH'],
      syntax: 'SELECT * FROM emp ORDER BY id OFFSET 10 ROWS FETCH NEXT 20 ROWS ONLY',
      desc: '12c+ 标准 SQL 分页；可读性更好' },
    { id: 'ora-exists', category: 'oracle', title: 'EXISTS / NOT EXISTS', tags: ['EXISTS','子查询'],
      syntax: 'SELECT * FROM a WHERE EXISTS (SELECT 1 FROM b WHERE a.id = b.aid)',
      desc: '比 IN 性能更稳（不会因为 NULL 出错）' },
    { id: 'ora-merge', category: 'oracle', title: 'MERGE 增改合一', tags: ['MERGE','UPSERT'],
      syntax: 'MERGE INTO target t USING source s ON (t.id = s.id)\nWHEN MATCHED THEN UPDATE SET ...\nWHEN NOT MATCHED THEN INSERT ...',
      desc: '一次扫描完成"存在则更新、不存在则插入"' },
    { id: 'ora-connect-by', category: 'oracle', title: 'CONNECT BY 递归查询', tags: ['递归','树','CONNECT BY'],
      syntax: 'SELECT * FROM emp START WITH id = 1 CONNECT BY PRIOR id = manager_id',
      desc: 'Oracle 专属递归语法；11g+ 也支持 CTE WITH ... SEARCH' },
    { id: 'ora-with', category: 'oracle', title: 'CTE 公用表表达式', tags: ['CTE','WITH','子查询'],
      syntax: 'WITH t AS (SELECT ... FROM ...)\nSELECT * FROM t',
      desc: '11g+ 推荐；可读性 + 支持递归' },
    { id: 'ora-explain', category: 'oracle', title: 'EXPLAIN PLAN', tags: ['执行计划','性能'],
      syntax: 'EXPLAIN PLAN FOR <sql>;\nSELECT * FROM TABLE(DBMS_XPLAN.DISPLAY);',
      desc: '看 SQL 执行计划；找全表扫描 / 索引未用' },
    { id: 'ora-index', category: 'oracle', title: '创建索引', tags: ['索引','性能'],
      syntax: 'CREATE INDEX idx_emp_name ON emp(last_name, first_name);\n-- 唯一索引\nCREATE UNIQUE INDEX uq_emp_email ON emp(email);',
      desc: '复合索引遵循最左前缀原则' },
    { id: 'ora-table-full', category: 'oracle', title: '查表/索引占用空间', tags: ['表大小','索引大小','空间'],
      syntax: 'SELECT segment_name, bytes/1024/1024 mb FROM user_segments WHERE segment_type IN (\'TABLE\',\'INDEX\') ORDER BY bytes DESC;',
      desc: '定位大表/大索引' },
    { id: 'ora-lock', category: 'oracle', title: '查锁/杀会话', tags: ['锁','死锁','会话'],
      syntax: '-- 查锁\nSELECT * FROM v$locked_object lo, v$session s WHERE lo.session_id = s.sid;\n-- 杀会话\nALTER SYSTEM KILL SESSION \'sid, serial#\' IMMEDIATE;',
      desc: '死锁排查；v$session + v$locked_object 联合看' },
    { id: 'ora-sql-trace', category: 'oracle', title: 'SQL Trace / 10046', tags: ['跟踪','trace','性能'],
      syntax: 'ALTER SESSION SET EVENTS \'10046 trace name context forever, level 12\';\n-- 跑 SQL\nALTER SESSION SET EVENTS \'10046 trace name context off\';',
      desc: 'level 12 = bind value + wait event；tkprof 格式化' },
    { id: 'ora-awr', category: 'oracle', title: 'AWR 报告', tags: ['AWR','性能','报告'],
      syntax: '@$ORACLE_HOME/rdbms/admin/awrrpt.sql',
      desc: 'DBA 性能诊断基线；快照自动生成' },
    { id: 'ora-export-data', category: 'oracle', title: 'expdp / impdp 数据泵', tags: ['备份','导出','数据泵'],
      syntax: 'expdp user/pwd directory=DATA_PUMP_DIR dumpfile=exp.dmp schemas=scott\nimpdp user/pwd directory=DATA_PUMP_DIR dumpfile=exp.dmp',
      desc: '11g+ 推荐；比老 exp/imp 快、支持并行' },
    { id: 'ora-wallet', category: 'oracle', title: 'tnsnames.ora 连接串', tags: ['tns','连接','配置'],
      syntax: 'ORCL =\n  (DESCRIPTION =\n    (ADDRESS = (PROTOCOL = TCP)(HOST = dbhost)(PORT = 1521))\n    (CONNECT_DATA = (SERVER = DEDICATED)(SERVICE_NAME = orcl))\n  )',
      desc: '客户端 tnsnames.ora 标准格式' },

    // ============ MySQL ============
    { id: 'mysql-ifnull', category: 'mysql', title: 'IFNULL / COALESCE', tags: ['空值','函数','IFNULL'],
      syntax: 'IFNULL(expr, alt)\nCOALESCE(a, b, c, ...) -- 第一个非 NULL',
      desc: 'IFNULL 是 MySQL 专有；COALESCE 是标准 SQL' },
    { id: 'mysql-if', category: 'mysql', title: 'IF 函数', tags: ['IF','条件','函数'],
      syntax: "IF(cond, a, b)",
      example: "SELECT name, IF(score>=60, '及格', '不及格') FROM student;",
      desc: 'MySQL 的三元表达式' },
    { id: 'mysql-date-format', category: 'mysql', title: 'DATE_FORMAT', tags: ['日期','格式化','DATE_FORMAT'],
      syntax: "DATE_FORMAT(dt, '%Y-%m-%d %H:%i:%s')",
      desc: 'MySQL 格式符和 Oracle 不同（%Y/%m/%d vs YYYY/MM/DD）' },
    { id: 'mysql-group-concat', category: 'mysql', title: 'GROUP_CONCAT 行转列', tags: ['GROUP_CONCAT','聚合','行转列'],
      syntax: 'GROUP_CONCAT(name ORDER BY id SEPARATOR \', \')',
      desc: '把多行拼成一个字符串；默认长度 1024，可调 group_concat_max_len' },
    { id: 'mysql-concat', category: 'mysql', title: 'CONCAT 字符串拼接', tags: ['CONCAT','字符串'],
      syntax: "CONCAT(a, b, c)\nCONCAT_WS(',', a, b, c)   -- 带分隔符",
      desc: '任何参数为 NULL 则整体为 NULL；CONCAT_WS 跳过 NULL' },
    { id: 'mysql-on-duplicate', category: 'mysql', title: 'ON DUPLICATE KEY UPDATE', tags: ['UPSERT','重复键','更新'],
      syntax: 'INSERT INTO t (id, cnt) VALUES (1, 1) ON DUPLICATE KEY UPDATE cnt = cnt + 1',
      desc: 'MySQL 版 upsert；VALUES(col) 引用要插入的值' },
    { id: 'mysql-replace', category: 'mysql', title: 'REPLACE INTO', tags: ['替换','插入'],
      syntax: 'REPLACE INTO t (id, name) VALUES (1, "x")',
      desc: '冲突时先删后插；慎用（自增 ID 会变）' },
    { id: 'mysql-explain', category: 'mysql', title: 'EXPLAIN', tags: ['执行计划','性能'],
      syntax: 'EXPLAIN SELECT * FROM t WHERE id = 1;\nEXPLAIN ANALYZE SELECT ... -- 8.0+ 实际执行',
      desc: '看 type / key / rows / Extra 字段' },
    { id: 'mysql-slow', category: 'mysql', title: '慢查询日志', tags: ['慢查询','日志','性能'],
      syntax: 'SET GLOBAL slow_query_log = 1;\nSET GLOBAL long_query_time = 2;\nSET GLOBAL slow_query_log_file = "/var/log/mysql-slow.log";',
      desc: '在线开慢查询；改 my.cnf 持久' },
    { id: 'mysql-show-processlist', category: 'mysql', title: 'SHOW PROCESSLIST', tags: ['进程','锁','会话'],
      syntax: 'SHOW FULL PROCESSLIST;',
      example: 'SELECT * FROM information_schema.PROCESSLIST WHERE COMMAND != "Sleep";',
      desc: '看运行中的 SQL / 锁等待 / 长事务' },
    { id: 'mysql-kill', category: 'mysql', title: 'KILL 查询', tags: ['杀','kill','中断'],
      syntax: 'KILL <id>;\nKILL QUERY <id>;   -- 只杀查询不断连接',
      desc: 'SHOW PROCESSLIST 第一列是 ID' },
    { id: 'mysql-mysqldump', category: 'mysql', title: 'mysqldump 备份', tags: ['备份','导出'],
      syntax: 'mysqldump -uroot -p --single-transaction --routines --triggers dbname > bak.sql',
      example: 'mysqldump -uroot -p --all-databases > all.sql',
      desc: '--single-transaction 适用于 InnoDB 一致性备份' },
    { id: 'mysql-source', category: 'mysql', title: 'source 导入 SQL', tags: ['导入','source'],
      syntax: 'mysql -uroot -p dbname < bak.sql\n-- 或 mysql 客户端内\nsource /path/bak.sql;',
      desc: '大文件推荐 -uroot -p dbname < file.sql 比 source 稳' },
    { id: 'mysql-charset', category: 'mysql', title: 'utf8mb4 字符集', tags: ['字符集','emoji','乱码'],
      syntax: 'CREATE DATABASE db DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;',
      example: "ALTER TABLE t CONVERT TO CHARACTER SET utf8mb4;",
      desc: 'MySQL 的 utf8 是假 utf8（3 字节）；存 emoji 必须 utf8mb4' },
    { id: 'mysql-innodb', category: 'mysql', title: 'InnoDB 事务隔离', tags: ['事务','隔离','InnoDB'],
      syntax: 'SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED;',
      desc: 'RU / RC / RR / Serializable；MySQL 默认 RR' },
    { id: 'mysql-deadlock', category: 'mysql', title: '死锁日志', tags: ['死锁','锁','事务'],
      syntax: 'SHOW ENGINE INNODB STATUS\\G',
      desc: 'LATEST DETECTED DEADLOCK 段；innodb_print_all_deadlocks=ON 全部记到错误日志' },

    // ============ Redis ============
    { id: 'redis-string', category: 'redis', title: 'STRING 字符串', tags: ['string','字符串','基础'],
      syntax: 'SET k v [EX 60] [NX|XX]\nGET k\nINCR k\nMSET k1 v1 k2 v2',
      desc: 'EX 秒 / PX 毫秒过期；NX 不存在才设；XX 存在才设' },
    { id: 'redis-hash', category: 'redis', title: 'HASH 哈希', tags: ['hash','哈希','对象'],
      syntax: 'HSET user:1 name "zhang" age 30\nHGET user:1 name\nHGETALL user:1',
      desc: '适合存对象；HMSET 已弃用，用 HSET 多参数形式' },
    { id: 'redis-list', category: 'redis', title: 'LIST 列表', tags: ['list','列表','队列'],
      syntax: 'LPUSH k v1 v2\nRPUSH k v3\nLPOP k 1\nBRPOP k 0   -- 阻塞',
      desc: 'LPUSH+RPOP = 队列；LPUSH+LPOP = 栈' },
    { id: 'redis-set', category: 'redis', title: 'SET 集合', tags: ['set','集合','去重'],
      syntax: 'SADD tag:1 a b c\nSINTER tag:1 tag:2   -- 交集\nSUNIONSTORE dst k1 k2   -- 并集存',
      desc: '无序 + 去重；常用于标签、共同好友' },
    { id: 'redis-zset', category: 'redis', title: 'ZSET 有序集合', tags: ['zset','有序','排行榜'],
      syntax: 'ZADD rank 100 "alice" 90 "bob"\nZREVRANGE rank 0 9 WITHSCORES\nZRANK k "alice"',
      desc: 'score 默认 double；排行榜/延时队列（score = 时间戳）' },
    { id: 'redis-key', category: 'redis', title: 'KEY 通用命令', tags: ['key','键','通用'],
      syntax: 'KEYS "user:*"           -- 慎用，阻塞\nSCAN 0 MATCH "user:*" COUNT 100\nEXISTS k / DEL k / TYPE k / TTL k',
      desc: '生产用 SCAN 替代 KEYS（不阻塞）' },
    { id: 'redis-ttl', category: 'redis', title: '过期与淘汰', tags: ['TTL','过期','淘汰'],
      syntax: 'EXPIRE k 60\nTTL k\n-- 淘汰策略（配置）\nmaxmemory-policy allkeys-lru',
      desc: 'volatile-ttl / allkeys-lru / noeviction 等' },
    { id: 'redis-persist', category: 'redis', title: 'PERSIST 取消过期', tags: ['过期','持久'],
      syntax: 'PERSIST k',
      desc: '去掉过期时间' },
    { id: 'redis-pubsub', category: 'redis', title: 'Pub/Sub 发布订阅', tags: ['发布','订阅','消息'],
      syntax: 'SUBSCRIBE channel\nPUBLISH channel "msg"',
      desc: '不持久化；断连消息丢失；适合低延迟通知' },
    { id: 'redis-stream', category: 'redis', title: 'Stream 消息流', tags: ['stream','消息流','5.0'],
      syntax: 'XADD stream * k v\nXREAD COUNT 10 BLOCK 0 STREAMS stream >\nXLEN stream',
      desc: '5.0+；可持久、可回溯；类似轻量 Kafka' },
    { id: 'redis-pipeline', category: 'redis', title: 'Pipeline 管道', tags: ['管道','批量','性能'],
      syntax: 'redis-cli ... < commands.txt\n# 编程客户端同理：一次性发一批',
      desc: '减少 RTT；不是事务（不保证原子）' },
    { id: 'redis-multi', category: 'redis', title: 'MULTI/EXEC 事务', tags: ['事务','原子','MULTI'],
      syntax: 'MULTI\nSET k1 v1\nSET k2 v2\nEXEC',
      desc: '原子执行（不保证其他客户端不插队）；不支持回滚' },
    { id: 'redis-lua', category: 'redis', title: 'Lua 脚本', tags: ['lua','脚本','原子'],
      syntax: 'EVAL "return redis.call(\'GET\', KEYS[1])" 1 mykey',
      desc: '原子执行；用 EVALSHA 缓存脚本省带宽' },
    { id: 'redis-rdb-aof', category: 'redis', title: '持久化 RDB / AOF', tags: ['持久化','RDB','AOF'],
      syntax: 'BGSAVE   # 生成 RDB 快照\nBGREWRITEAOF   # 压缩 AOF',
      desc: 'RDB 全量 + AOF 增量；推荐两个都开' },
    { id: 'redis-cluster', category: 'redis', title: 'Cluster 集群', tags: ['cluster','集群','分片'],
      syntax: 'redis-cli --cluster create 127.0.0.1:7000 127.0.0.1:7001 ...',
      desc: '16384 槽位分片；key 用 {} hash tag 保证同 slot' },
    { id: 'redis-monitor', category: 'redis', title: 'MONITOR 调试', tags: ['调试','监控','MONITOR'],
      syntax: 'redis-cli MONITOR',
      desc: '实时看所有命令；生产慎用（性能影响大）' },
    { id: 'redis-info', category: 'redis', title: 'INFO 状态', tags: ['状态','INFO','监控'],
      syntax: 'INFO memory\nINFO stats\nINFO clients',
      desc: '分 section 查看；常用于监控指标采集' },
    { id: 'redis-cli', category: 'redis', title: 'redis-cli 常用', tags: ['cli','客户端'],
      syntax: 'redis-cli -h host -p 6379 -a pwd\nredis-cli --scan --pattern "user:*"',
      desc: '-a 不安全（ps 可见），用 AUTH 命令更稳' },

    // ============ Nginx ============
    { id: 'nginx-reload', category: 'nginx', title: 'reload 重载配置', tags: ['重载','reload','配置'],
      syntax: 'nginx -t            # 测试配置\nnginx -s reload     # 平滑重载',
      desc: '修改配置后先 -t 检查语法，再 -s reload 不中断' },
    { id: 'nginx-location', category: 'nginx', title: 'location 匹配规则', tags: ['location','路由','匹配'],
      syntax: 'location = /exact        # 精确\nlocation ^~ /prefix/    # 前缀优先\nlocation ~ \\.php$       # 正则（区分大小写）\nlocation ~* \\.(jpg|png) # 正则不区分大小写\nlocation /              # 通用',
      desc: '匹配顺序：= > ^~ > ~/~* > /' },
    { id: 'nginx-proxy-pass', category: 'nginx', title: 'proxy_pass 反向代理', tags: ['反向代理','proxy_pass'],
      syntax: 'location /api/ {\n  proxy_pass http://backend:8080/;\n  proxy_set_header Host $host;\n  proxy_set_header X-Real-IP $remote_addr;\n}',
      desc: '注意 proxy_pass 末尾 / 有无会决定是否替换 location 前缀' },
    { id: 'nginx-upstream', category: 'nginx', title: 'upstream 负载均衡', tags: ['负载均衡','upstream'],
      syntax: 'upstream backend {\n  ip_hash;\n  server 10.0.0.1:8080 weight=3;\n  server 10.0.0.2:8080;\n  server 10.0.0.3:8080 backup;\n}',
      desc: '轮询/权重/ip_hash；least_conn 最小连接；backup 备份' },
    { id: 'nginx-https', category: 'nginx', title: 'HTTPS 配置', tags: ['HTTPS','SSL','TLS','证书'],
      syntax: 'server {\n  listen 443 ssl;\n  ssl_certificate /etc/nginx/cert.pem;\n  ssl_certificate_key /etc/nginx/cert.key;\n  ssl_protocols TLSv1.2 TLSv1.3;\n}',
      desc: '推荐 TLSv1.2/1.3；证书路径按实际调整' },
    { id: 'nginx-redirect', category: 'nginx', title: 'rewrite / return 重定向', tags: ['重定向','rewrite','return'],
      syntax: 'return 301 https://$host$request_uri;\n\nlocation /old/ {\n  rewrite ^/old/(.*)$ /new/$1 permanent;\n}',
      desc: 'return 是简单跳转，rewrite 支持正则重写' },
    { id: 'nginx-log', category: 'nginx', title: '日志格式与切割', tags: ['日志','log','access','error'],
      syntax: 'log_format main \'$remote_addr - $request_time $status\';\naccess_log /var/log/nginx/access.log main;',
      example: '# 按天切割（用 logrotate）\n/var/log/nginx/*.log {\n  daily\n  rotate 30\n}',
      desc: 'request_time / upstream_response_time 是性能调优关键字段' },
    { id: 'nginx-try-files', category: 'nginx', title: 'try_files 回退', tags: ['try_files','回退','静态'],
      syntax: 'location / {\n  try_files $uri $uri/ /index.html;\n}',
      example: 'location / {\n  try_files $uri @backend;\n}\nlocation @backend {\n  proxy_pass http://backend;\n}',
      desc: 'SPA 前端常用：找不到静态文件就回 index.html' },
    { id: 'nginx-limit', category: 'nginx', title: 'limit_req 限流', tags: ['限流','limit_req'],
      syntax: 'limit_req_zone $binary_remote_addr zone=one:10m rate=10r/s;\n\nlocation /api/ {\n  limit_req zone=one burst=20 nodelay;\n}',
      desc: 'rate=N r/s 每秒 N 个；burst 桶容量；nodelay 不排队' },
    { id: 'nginx-gzip', category: 'nginx', title: 'gzip 压缩', tags: ['压缩','gzip','性能'],
      syntax: 'gzip on;\ngzip_types text/plain application/json text/css application/javascript;\ngzip_min_length 1024;\ngzip_comp_level 5;',
      desc: '通常压缩等级 5-6 已足够；图/视频类型不要开' },
    { id: 'nginx-cache', category: 'nginx', title: 'proxy_cache 缓存', tags: ['缓存','proxy_cache'],
      syntax: 'proxy_cache_path /var/cache/nginx levels=1:2 keys_zone=mycache:10m max_size=1g;\n\nlocation / {\n  proxy_cache mycache;\n  proxy_cache_valid 200 10m;\n}',
      desc: 'levels 目录层级；keys_zone 内存索引；inactive 闲置清理' },
    { id: 'nginx-cors', category: 'nginx', title: 'CORS 跨域', tags: ['跨域','CORS','header'],
      syntax: 'location /api/ {\n  add_header Access-Control-Allow-Origin *;\n  add_header Access-Control-Allow-Methods "GET,POST,OPTIONS";\n  if ($request_method = OPTIONS) { return 204; }\n}',
      desc: '预检 OPTIONS 要直接 204；带 cookie 不能用 *' },

    // ============ Java 后端 ============
    { id: 'java-mvn-clean', category: 'java', title: 'Maven 常用', tags: ['Maven','构建','依赖'],
      syntax: 'mvn clean package -DskipTests\nmvn dependency:tree | grep conflict\nmvn -pl module-a -am package',
      desc: '-pl 指定模块 -am 同时构建依赖模块' },
    { id: 'java-jstack', category: 'java', title: 'jstack 线程栈', tags: ['线程','jstack','死锁','排查'],
      syntax: 'jstack <pid> > thread.txt',
      example: 'jstack -l <pid>   # -l 看锁信息',
      desc: '死锁日志会有 "Found one Java-level deadlock"；BLOCKED 线程多表示锁竞争' },
    { id: 'java-jmap', category: 'java', title: 'jmap 堆/类', tags: ['内存','jmap','堆','排查'],
      syntax: 'jmap -heap <pid>             # 堆概要\njmap -histo:live <pid> | head   # 类实例数\njmap -dump:live,format=b,file=heap.hprof <pid>',
      desc: '导出 hprof 用 MAT / VisualVM 分析' },
    { id: 'java-jstat', category: 'java', title: 'jstat GC 监控', tags: ['GC','jstat','性能'],
      syntax: 'jstat -gc <pid> 1s 10',
      desc: '每秒一次共 10 次；列：YGC/YGCT/FGC/FGCT 等' },
    { id: 'java-jconsole', category: 'java', title: 'JMX 远程调试端口', tags: ['JMX','远程','监控'],
      syntax: '-Dcom.sun.management.jmxremote.port=9999 \\\n-Dcom.sun.management.jmxremote.ssl=false \\\n-Dcom.sun.management.jmxremote.authenticate=false',
      desc: '生产慎用；建议加认证或放内网' },
    { id: 'java-arthas', category: 'java', title: 'Arthas 在线诊断', tags: ['arthas','诊断','在线','阿里'],
      syntax: 'curl -O https://arthas.aliyun.com/arthas-boot.jar && java -jar arthas-boot.jar\ndashboard\nthread\ntrace com.example.Foo method',
      desc: '不重启 JVM；trace 监控方法调用链路耗时' },
    { id: 'java-lombok', category: 'java', title: 'Lombok 常用注解', tags: ['Lombok','注解','简化'],
      syntax: '@Data                 // @Getter+Setter+ToString+Equals\n@Builder\n@Slf4j                  // log 变量\n@RequiredArgsConstructor // final 字段构造器',
      desc: '省样板代码；IDE 需装 Lombok 插件' },
    { id: 'java-stream', category: 'java', title: 'Stream 常用', tags: ['Stream','集合','Java8','函数式'],
      syntax: 'list.stream()\n  .filter(x -> x.getAge() > 18)\n  .map(User::getName)\n  .distinct().sorted().collect(Collectors.toList());',
      desc: 'filter / map / distinct / sorted / collect / forEach / reduce / groupingBy' },
    { id: 'java-optional', category: 'java', title: 'Optional 防 NPE', tags: ['Optional','空指针','NPE'],
      syntax: 'Optional.ofNullable(user)\n  .map(User::getAddress)\n  .map(Address::getCity)\n  .orElse("未知");',
      desc: 'orElse 默认值 / orElseGet 延迟 / orElseThrow 抛异常' },
    { id: 'java-try-with', category: 'java', title: 'try-with-resources', tags: ['资源','自动关闭','try'],
      syntax: 'try (InputStream in = new FileInputStream("a.txt");\n     BufferedReader br = new BufferedReader(new InputStreamReader(in))) {\n  // use\n}',
      desc: '实现 AutoCloseable 的资源自动关；Java 7+' },
    { id: 'java-spring-jpa', category: 'java', title: 'Spring Data JPA 注解', tags: ['Spring','JPA','ORM','注解'],
      syntax: '@Entity\n@Table(name = "t_user")\npublic class User {\n  @Id @GeneratedValue(strategy = GenerationType.IDENTITY)\n  private Long id;\n  @Column(unique = true, nullable = false)\n  private String username;\n}',
      desc: 'Repository 继承 JpaRepository 即可用 findAll / save / findById 等' },
    { id: 'java-spring-mybatis', category: 'java', title: 'MyBatis 常用', tags: ['MyBatis','ORM','SQL'],
      syntax: '@Mapper\npublic interface UserMapper {\n  @Select("SELECT * FROM user WHERE id = #{id}")\n  User findById(Long id);\n}',
      desc: '复杂 SQL 用 XML mapper；@Param 解决多参数；PageHelper 分页' },
    { id: 'java-spring-rest', category: 'java', title: 'Spring REST 注解', tags: ['Spring','REST','注解','HTTP'],
      syntax: '@RestController\n@RequestMapping("/api/user")\npublic class UserController {\n  @GetMapping("/{id}")\n  public User get(@PathVariable Long id) { ... }\n  @PostMapping\n  public User create(@RequestBody @Valid UserDTO dto) { ... }\n}',
      desc: '@RequestBody / @PathVariable / @RequestParam / @Valid' },
    { id: 'java-spring-config', category: 'java', title: 'Spring Boot 常用配置', tags: ['Spring','Boot','配置','YAML'],
      syntax: 'server:\n  port: 8080\n  servlet:\n    context-path: /api\nspring:\n  datasource:\n    url: jdbc:mysql://localhost:3306/db\n    username: root\n    password: ${DB_PWD:root}\nlogging:\n  level.com.example: DEBUG',
      desc: 'application.yml；profile 用 application-{profile}.yml' },
    { id: 'java-transactional', category: 'java', title: '@Transactional 事务', tags: ['Spring','事务','Transactional'],
      syntax: '@Transactional(rollbackFor = Exception.class, propagation = Propagation.REQUIRED)\npublic void doBusiness() { ... }',
      desc: '默认 RuntimeException 回滚；checked 不回滚要加 rollbackFor；同类自调不生效（代理原因）' },
    { id: 'java-concurrent', category: 'java', title: '线程池参数', tags: ['线程池','ThreadPoolExecutor','并发'],
      syntax: 'new ThreadPoolExecutor(\n  corePoolSize, maxPoolSize, 60, TimeUnit.SECONDS,\n  new ArrayBlockingQueue<>(1000),\n  new ThreadPoolExecutor.CallerRunsPolicy()\n);',
      desc: '拒绝策略：Abort(抛) / CallerRuns(主线程跑) / DiscardOldest(丢最早)' },

    // ============ 前端 ============
    { id: 'fe-vscode-shortcut', category: 'frontend', title: 'VS Code 必备快捷键', tags: ['VSCode','快捷键','编辑器'],
      syntax: 'Ctrl + P           快速打开文件\nCtrl + Shift + P   命令面板\nCtrl + /           行注释\nCtrl + Shift + /   块注释\nAlt + ↑/↓          上下移动行\nShift + Alt + ↑/↓  复制行上下\nCtrl + D           多选同词\nCtrl + B           侧栏显隐\nCtrl + `           内置终端\nCtrl + Shift + F   全局搜索\nCtrl + Shift + K   删除行\nCtrl + Enter       下方插入行\nCtrl + Shift + Enter 上方插入行',
      platform: 'win', desc: 'Windows 键位；Linux 同款' },
    { id: 'fe-js-debounce', category: 'frontend', title: '防抖 debounce', tags: ['JS','防抖','性能'],
      syntax: 'function debounce(fn, wait) {\n  let t;\n  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), wait); };\n}',
      desc: 'n 秒内只执行最后一次；搜索框输入联想' },
    { id: 'fe-js-throttle', category: 'frontend', title: '节流 throttle', tags: ['JS','节流','性能'],
      syntax: 'function throttle(fn, wait) {\n  let last = 0;\n  return (...a) => {\n    const now = Date.now();\n    if (now - last >= wait) { last = now; fn(...a); }\n  };\n}',
      desc: 'n 秒内最多执行一次；scroll / resize 事件' },
    { id: 'fe-js-fetch', category: 'frontend', title: 'fetch 基础', tags: ['JS','fetch','HTTP'],
      syntax: 'const r = await fetch(url, { method: "POST", body: JSON.stringify(d), headers: {"Content-Type": "application/json"} });\nconst json = await r.json();',
      desc: 'fetch 不带 cookie；需要的话 credentials: "include"' },
    { id: 'fe-js-async', category: 'frontend', title: 'async/await 错误处理', tags: ['JS','async','错误'],
      syntax: 'try {\n  const r = await fetch(url);\n  if (!r.ok) throw new Error(r.statusText);\n  return await r.json();\n} catch (e) { console.error(e); }',
      desc: 'try/catch 包住 await；不要忘 throw 4xx/5xx' },
    { id: 'fe-js-copy', category: 'frontend', title: '前端复制到剪贴板', tags: ['JS','复制','剪贴板'],
      syntax: 'await navigator.clipboard.writeText(text);',
      example: 'navigator.clipboard.writeText(text).then(() => toast("ok"))',
      desc: '需要 HTTPS / localhost；否则走 textarea + execCommand fallback' },
    { id: 'fe-js-storage', category: 'frontend', title: 'localStorage / sessionStorage', tags: ['JS','存储','Storage'],
      syntax: 'localStorage.setItem("k", JSON.stringify(v));\nconst v = JSON.parse(localStorage.getItem("k") || "null");',
      desc: '只能存字符串；约 5MB；同步阻塞' },
    { id: 'fe-es6-destruct', category: 'frontend', title: 'ES6 解构', tags: ['ES6','解构','JS'],
      syntax: 'const { name, age = 18 } = user;\nconst [a, b, ...rest] = arr;',
      desc: '默认值 + 重命名 + 剩余运算符' },
    { id: 'fe-es6-spread', category: 'frontend', title: '展开运算符 ...', tags: ['ES6','展开','JS'],
      syntax: 'const merged = { ...a, ...b };\nconst arr2 = [...arr, 4, 5];',
      desc: '浅拷贝；修改新对象不会影响原对象' },
    { id: 'fe-es6-optional', category: 'frontend', title: '可选链 ?.', tags: ['ES6','可选链','JS'],
      syntax: 'const city = user?.address?.city;\nconst len = arr?.[0]?.length;',
      desc: '链上任一环为 null/undefined 就返回 undefined' },
    { id: 'fe-es6-nullish', category: 'frontend', title: '空值合并 ??', tags: ['ES6','空值合并','JS'],
      syntax: 'const v = input ?? "default";',
      desc: '只在 null/undefined 时用默认值；区别于 ||（后者 0/""/false 也会走默认）' },
    { id: 'fe-css-flex', category: 'frontend', title: 'Flex 居中', tags: ['CSS','Flex','布局','居中'],
      syntax: '.box { display: flex; justify-content: center; align-items: center; }',
      desc: '最常用的居中三件套' },
    { id: 'fe-css-grid', category: 'frontend', title: 'Grid 基础', tags: ['CSS','Grid','布局'],
      syntax: '.g { display: grid; grid-template-columns: 1fr 2fr 1fr; gap: 16px; }',
      desc: '二维布局；fr 单位按比例分配剩余空间' },
    { id: 'fe-css-var', category: 'frontend', title: 'CSS 变量', tags: ['CSS','变量','主题'],
      syntax: ':root { --primary: #4f8cff; }\n.btn { background: var(--primary); }',
      desc: '支持运行时改；JS 设 el.style.setProperty("--primary", v)' },
    { id: 'fe-html-meta', category: 'frontend', title: 'HTML 常用 meta', tags: ['HTML','meta','head'],
      syntax: '<meta charset="utf-8">\n<meta name="viewport" content="width=device-width,initial-scale=1">\n<meta http-equiv="X-UA-Compatible" content="IE=edge">',
      desc: '编码 / 移动端 / IE 兼容' },
    { id: 'fe-http-status', category: 'frontend', title: 'HTTP 状态码速记', tags: ['HTTP','状态码','速记'],
      syntax: '2xx 成功  200 OK / 201 Created / 204 No Content\n3xx 重定向 301 永久 / 302 临时 / 304 缓存\n4xx 客户端错 400 参数 / 401 未登录 / 403 拒绝 / 404 没找到 / 429 限流\n5xx 服务端错 500 / 502 网关 / 503 过载 / 504 超时',
      desc: '记忆口诀：2 接 3 转 4 客 5 服' },
    { id: 'fe-restful', category: 'frontend', title: 'RESTful 命名', tags: ['REST','API','命名'],
      syntax: 'GET    /users        列表\nGET    /users/1      详情\nPOST   /users        新建\nPUT    /users/1      全量更新\nPATCH  /users/1      部分更新\nDELETE /users/1      删除',
      desc: '资源用名词复数；动作用 HTTP 方法' },

    // ============ IDEA 快捷键 ============
    { id: 'idea-find-action', category: 'idea', title: '查找动作', tags: ['IDEA','快捷键', 'win', 'find', 'action', '万能'],
      syntax: 'Ctrl + Shift + A',
      desc: '万能命令：按名称搜 IDEA 任何动作（重构、格式化、插件…）；记不住快捷键就用它' },
    { id: 'idea-search', category: 'idea', title: '全局搜索', tags: ['IDEA','快捷键','搜索', 'win', 'find'],
      syntax: 'Ctrl + Shift + F',
      desc: '跨文件搜字符串；支持正则、过滤文件类型/目录' },
    { id: 'idea-class', category: 'idea', title: '搜索类', tags: ['IDEA','快捷键','类', 'win', 'class'],
      syntax: 'Ctrl + N',
      desc: '按类名跳；支持 CamelHump 缩写（如输入 URD 找 UserRepositoryDao）' },
    { id: 'idea-file', category: 'idea', title: '搜索文件', tags: ['IDEA','快捷键','文件', 'win', 'file'],
      syntax: 'Ctrl + Shift + N',
      desc: '按文件名跳（包括配置文件）' },
    { id: 'idea-symbol', category: 'idea', title: '搜索符号', tags: ['IDEA','快捷键','符号', 'win', 'symbol'],
      syntax: 'Ctrl + Alt + Shift + N',
      desc: '跳到方法/字段名' },
    { id: 'idea-goto-line', category: 'idea', title: '跳到行号', tags: ['IDEA','快捷键','行号', 'win'],
      syntax: 'Ctrl + G',
      desc: '输入行号跳转' },
    { id: 'idea-recent', category: 'idea', title: '最近文件', tags: ['IDEA','快捷键','最近', 'win'],
      syntax: 'Ctrl + E',
      desc: '弹出最近打开的文件列表' },
    { id: 'idea-back', category: 'idea', title: '前进/后退', tags: ['IDEA','快捷键','导航', 'win'],
      syntax: 'Ctrl + Alt + ←/→',
      desc: '光标导航历史；跳到上/下一个编辑位置' },
    { id: 'idea-rename', category: 'idea', title: '重命名', tags: ['IDEA','快捷键','重构','rename', 'win'],
      syntax: 'Shift + F6',
      desc: '智能重命名：会改所有引用、文件、注释（比手动改安全太多）' },
    { id: 'idea-extract', category: 'idea', title: '抽取方法/变量', tags: ['IDEA','快捷键','重构','extract', 'win'],
      syntax: 'Ctrl+Alt+M  抽取方法\nCtrl+Alt+V  抽取变量\nCtrl+Alt+C  抽取常量\nCtrl+Alt+F  抽取字段',
      desc: '选中代码块 → 抽方法/变量/常量/字段；配合 Refactor This (Ctrl+Alt+Shift+T) 一键选' },
    { id: 'idea-generate', category: 'idea', title: '生成代码', tags: ['IDEA','快捷键','生成','getter', 'setter', 'win'],
      syntax: 'Alt + Insert',
      desc: '生成 getter/setter/toString/equals/hashCode/constructor/override' },
    { id: 'idea-format', category: 'idea', title: '格式化代码', tags: ['IDEA','快捷键','格式化', 'win', 'format'],
      syntax: 'Ctrl + Alt + L',
      desc: '按项目 code style 格式化' },
    { id: 'idea-optimize-import', category: 'idea', title: '优化 import', tags: ['IDEA','快捷键','import', 'win'],
      syntax: 'Ctrl + Alt + O',
      desc: '去重 import + 删未用 + 按字母序排' },
    { id: 'idea-comment', category: 'idea', title: '注释', tags: ['IDEA','快捷键','注释', 'win', 'comment'],
      syntax: 'Ctrl + /        行注释 Ctrl + Shift + /  块注释',
      desc: '// 单行 / /* */ 块注释切换' },
    { id: 'idea-duplicate', category: 'idea', title: '复制行/块', tags: ['IDEA','快捷键','复制', 'win', 'duplicate'],
      syntax: 'Ctrl + D',
      desc: '没选中 = 复制当前行；选中 = 复制选中块' },
    { id: 'idea-delete-line', category: 'idea', title: '删除行', tags: ['IDEA','快捷键','删除行', 'win'],
      syntax: 'Ctrl + Y',
      desc: '删当前行；Win 注意 Ctrl+Y 是 redo 的别忘了' },
    { id: 'idea-move-line', category: 'idea', title: '移动行', tags: ['IDEA','快捷键','移动', 'win'],
      syntax: 'Alt + Shift + ↑/↓',
      desc: '上下移动当前行/选中块' },
    { id: 'idea-multicursor', category: 'idea', title: '多光标编辑', tags: ['IDEA','快捷键','多光标', 'win'],
      syntax: 'Alt + 点击       添加新光标 Ctrl + Alt + G    选中所有同词 Ctrl + D          逐个选同词',
      desc: '批量改同名字段时神器' },
    { id: 'idea-goto-implementation', category: 'idea', title: '跳到实现/声明', tags: ['IDEA','快捷键','跳转', 'win', 'implementation', 'declaration'],
      syntax: 'Ctrl + B 或 Ctrl + Click  跳到声明 Ctrl + Alt + B              跳到所有实现',
      desc: 'Ctrl+B 跳到定义；Ctrl+Alt+B 跳到所有实现（接口必备）' },
    { id: 'idea-find-usages', category: 'idea', title: '查找所有使用', tags: ['IDEA','快捷键','引用', 'win', 'usage'],
      syntax: 'Alt + F7',
      desc: '在底部窗口列出所有引用' },
    { id: 'idea-hierarchy', category: 'idea', title: '调用层次', tags: ['IDEA','快捷键','层次', 'win', 'hierarchy', 'call'],
      syntax: 'Ctrl + H',
      desc: '类继承层次 / 方法调用层次' },
    { id: 'idea-surround', category: 'idea', title: '包裹代码块', tags: ['IDEA','快捷键','包裹', 'win', 'surround'],
      syntax: 'Ctrl + Alt + T',
      desc: '选中代码 → 包 if/try/for/synchronized 等' },
    { id: 'idea-postfix', category: 'idea', title: '后缀补全', tags: ['IDEA','快捷键','后缀', 'win', 'postfix'],
      syntax: 'str.null        → if (str == null) ...\nlist.fori       → for (int i = 0; i < list.size(); i++)\nlist.for        → for (Object o : list)\nn.nn            → if (n != null) ...',
      desc: '光标在变量名后输入 .xxx + Tab 触发' },
    { id: 'idea-live-template', category: 'idea', title: 'Live Template', tags: ['IDEA','快捷键','模板', 'win', 'template'],
      syntax: 'psvm  → public static void main(String[] args) { ... }\nsout   → System.out.println();\nfori   → for (int i = 0; ...)\niter   → for-each\nctrl+i → 实现接口方法',
      desc: 'Settings → Editor → Live Templates 自定义' },
    { id: 'idea-debug-step', category: 'idea', title: '调试步进', tags: ['IDEA','快捷键','调试', 'win', 'debug'],
      syntax: 'F8          Step Over    跳过本行\nF7          Step Into    进入方法\nShift + F8  Step Out     跳出方法\nF9          Resume       跳到下一个断点\nCtrl + F8   Toggle BP    切换断点Toggle BP    切换断点Ctrl + F8',
      desc: '调试时必备' },
    { id: 'idea-evaluate', category: 'idea', title: '调试时执行表达式', tags: ['IDEA','快捷键','调试', 'win', 'evaluate'],
      syntax: 'Alt + F8',
      desc: '断点处弹出表达式求值；改临时变量、查方法返回值' },
    { id: 'idea-run-recent', category: 'idea', title: '选择 Run 配置', tags: ['IDEA','快捷键','运行', 'win', 'run'],
      syntax: 'Shift + F10             跑 Shift + F9              debug Alt + Shift + F10/F9    选配置',
      desc: '选最近的应用配置启动' },
    { id: 'idea-refactor-this', category: 'idea', title: '重构此处的所有选项', tags: ['IDEA','快捷键','重构', 'win', 'refactor'],
      syntax: 'Ctrl + Alt + Shift + T',
      desc: '弹出所有可用重构动作；不知道快捷键时万能入口' },
    { id: 'idea-quick-fix', category: 'idea', title: '快速修复', tags: ['IDEA','快捷键','修复', 'win', 'fix', 'quickfix'],
      syntax: 'Alt + Enter',
      desc: '灯泡：导包、实现方法、加 try-catch、生成测试…所有 IDE 提示都靠它' },
    { id: 'idea-goto-file', category: 'idea', title: '跳到文件结构', tags: ['IDEA','快捷键','结构', 'win', 'structure'],
      syntax: 'Ctrl + F12',
      desc: '弹出当前文件的成员列表（方法/字段），可输入快速跳' },
    { id: 'idea-toggle-case', category: 'idea', title: '大小写切换', tags: ['IDEA','快捷键','大小写', 'win', 'case'],
      syntax: 'Ctrl + Shift + U',
      desc: '选中文本 → 大小写反转' },
    { id: 'idea-bookmark', category: 'idea', title: '书签', tags: ['IDEA','快捷键','书签', 'win', 'bookmark'],
      syntax: 'F11            切换书签 Shift + F11    书签列表 Ctrl + F11      用助记符',
      desc: 'F11 加书签；Ctrl+数字 快速跳到编号书签' },
    { id: 'idea-version-control', category: 'idea', title: '版本控制面板', tags: ['IDEA','快捷键','Git', 'win', 'vcs'],
      syntax: 'Alt + 9',
      desc: '打开 Git / Local Changes 面板；快捷提交' },
    { id: 'idea-terminal', category: 'idea', title: '内置终端', tags: ['IDEA','快捷键','终端', 'win', 'terminal'],
      syntax: 'Alt + F12',
      desc: '在项目根目录开终端，不用切窗口' },
  ];

  // ===== 渲染函数 =====
  function renderCommands(view) {
    // 顶栏：搜索框 + 分类下拉 + 收藏数提示
    const searchInp = el('input', {
      type: 'text', id: 'cmd-search', placeholder: '搜索命令、关键字、说明…  (按 / 聚焦，Esc 清空)',
      autocomplete: 'off', spellcheck: 'false'
    });
    searchInp.style.flex = '1';
    searchInp.style.minWidth = '240px';

    const categorySel = el('select', { id: 'cmd-category' });
    categorySel.appendChild(el('option', { value: 'all', text: '🗂  全部' }));
    CATEGORIES.forEach(c => {
      const opt = el('option', { value: c.id, text: c.icon + '  ' + c.name });
      categorySel.appendChild(opt);
    });
    categorySel.style.width = '180px';

    const statSpan = el('span', { class: 'muted', id: 'cmd-stat', text: '' });

    const topBar = el('div', { class: 'filter-bar', style: 'display:flex; gap:12px; align-items:center; flex-wrap:wrap; margin-top:12px;' }, [
      el('label', { style: 'display:inline-flex; align-items:center; gap:6px;' }, [
        el('span', { text: '搜索' }),
        searchInp
      ]),
      el('label', { style: 'display:inline-flex; align-items:center; gap:6px;' }, [
        el('span', { text: '分类' }),
        categorySel
      ]),
      el('label', { style: 'display:inline-flex; align-items:center; gap:6px;' }, [
        el('input', { type: 'checkbox', id: 'cmd-favonly' }),
        el('span', { text: '只看收藏' })
      ]),
      statSpan,
    ]);

    // 列表容器
    const list = el('div', { class: 'cmd-list', id: 'cmd-list' });

    // 描述
    const desc = el('div', { class: 'card-desc', text: '速查 250+ 条 Linux / Git / Docker / Oracle / MySQL / Redis / Nginx / Java / 前端 / IDEA 常用命令与快捷键。搜索实时过滤，点 syntax 区右上角「复制」一键复制。收藏 (⭐) 存到 localStorage，下次还在。' });

    const card = el('div', { class: 'card' }, [
      el('h3', { text: '常用命令速查' }),
      desc,
      topBar,
      list,
      el('div', { class: 'muted', text: '提示：收藏的条目会在「⭐ 收藏」tab 集中展示；输入 `/` 立即聚焦搜索框；`Esc` 清空搜索。' })
    ]);
    view.appendChild(card);

    // ===== 事件 =====
    function getCategory() { return categorySel.value; }
    function getQuery() { return searchInp.value.trim().toLowerCase(); }
    function isFavOnly() { return document.getElementById('cmd-favonly').checked; }

    function matchesQuery(cmd, q) {
      if (!q) return true;
      if ((cmd.title || '').toLowerCase().includes(q)) return true;
      if ((cmd.syntax || '').toLowerCase().includes(q)) return true;
      if ((cmd.example || '').toLowerCase().includes(q)) return true;
      if ((cmd.desc || '').toLowerCase().includes(q)) return true;
      if (Array.isArray(cmd.tags)) {
        for (const t of cmd.tags) {
          if (String(t).toLowerCase().includes(q)) return true;
        }
      }
      return false;
    }

    function filterCmds() {
      const cat = getCategory();
      const q = getQuery();
      const favOnly = isFavOnly();
      let pool = COMMANDS;
      if (cat === 'fav') {
        pool = pool.filter(c => favs.has(c.id));
      } else if (cat !== 'all') {
        pool = pool.filter(c => c.category === cat);
      }
      if (favOnly && cat !== 'fav') {
        pool = pool.filter(c => favs.has(c.id));
      }
      if (q) pool = pool.filter(c => matchesQuery(c, q));
      return pool;
    }

    function renderEmpty(msg) {
      list.innerHTML = '';
      list.appendChild(el('div', { class: 'cmd-empty', text: msg }));
    }

    function renderList() {
      const items = filterCmds();
      statSpan.textContent = `共 ${items.length} 条`;
      list.innerHTML = '';
      if (items.length === 0) {
        renderEmpty('没找到匹配的命令。试试搜 `grep`、`partition`、`ALTER`、`Ctrl + B`、`ROW_NUMBER`…');
        return;
      }
      // 按分类分组（在 "all" 模式下有视觉分组感）
      const group = getCategory() === 'all' && !getQuery();
      if (group) {
        const byCat = {};
        items.forEach(it => {
          (byCat[it.category] = byCat[it.category] || []).push(it);
        });
        CATEGORIES.forEach(c => {
          if (c.id === 'fav' || !byCat[c.id]) return;
          list.appendChild(el('div', { class: 'cmd-group-title', text: c.icon + '  ' + c.name + '  ·  ' + byCat[c.id].length + ' 条' }));
          byCat[c.id].forEach(it => list.appendChild(buildCard(it)));
        });
      } else {
        items.forEach(it => list.appendChild(buildCard(it)));
      }
    }

    function buildCard(cmd) {
      const isFav = favs.has(cmd.id);
      // tag chips
      const tagBox = el('div', { class: 'cmd-tags' });
      (cmd.tags || []).slice(0, 6).forEach(t => {
        tagBox.appendChild(el('span', { class: 'cmd-tag', text: t }));
      });
      if (cmd.platform && cmd.platform !== 'all') {
        tagBox.appendChild(el('span', { class: 'cmd-tag cmd-tag-platform', text: cmd.platform }));
      }

      // syntax 块（带复制按钮）
      const syntaxPre = el('pre', { class: 'cmd-syntax' });
      syntaxPre.textContent = cmd.syntax || '';

      const copyBtn = el('button', {
        class: 'btn btn-sm cmd-copy', text: '复制', title: '复制 syntax',
        onclick: (e) => {
          e.stopPropagation();
          copyToClipboard(cmd.syntax || '').then(
            () => toast('已复制 syntax', 'ok'),
            () => toast('复制失败', 'err')
          );
        }
      });
      const syntaxBox = el('div', { class: 'cmd-syntax-box' }, [syntaxPre, copyBtn]);

      // 收藏按钮
      const favBtn = el('button', {
        class: 'btn btn-sm cmd-fav' + (isFav ? ' is-fav' : ''),
        text: isFav ? '★' : '☆',
        title: isFav ? '取消收藏' : '收藏',
        onclick: (e) => {
          e.stopPropagation();
          if (favs.has(cmd.id)) {
            favs.delete(cmd.id);
            toast('已取消收藏', 'ok');
          } else {
            favs.add(cmd.id);
            toast('已收藏', 'ok');
          }
          saveFavs(favs);
          renderList();
        }
      });

      // 展开/折叠：默认展开 syntax，example/desc 在 details 里
      const detailBox = el('div', { class: 'cmd-detail' });
      if (cmd.example) {
        detailBox.appendChild(el('div', { class: 'cmd-section-title', text: '示例' }));
        const ex = el('pre', { class: 'cmd-example' });
        ex.textContent = cmd.example;
        detailBox.appendChild(ex);
      }
      if (cmd.desc) {
        detailBox.appendChild(el('div', { class: 'cmd-section-title', text: '说明' }));
        detailBox.appendChild(el('div', { class: 'cmd-desc', text: cmd.desc }));
      }

      const cardEl = el('div', { class: 'cmd-card' + (isFav ? ' is-fav' : '') }, [
        el('div', { class: 'cmd-head' }, [
          el('div', { class: 'cmd-title', text: cmd.title }),
          el('div', { class: 'cmd-head-right' }, [favBtn])
        ]),
        tagBox,
        syntaxBox,
        detailBox
      ]);
      return cardEl;
    }

    // 绑定事件
    let timer = null;
    searchInp.addEventListener('input', () => {
      clearTimeout(timer);
      timer = setTimeout(renderList, 80);
    });
    categorySel.addEventListener('change', renderList);
    document.getElementById('cmd-favonly').addEventListener('change', renderList);

    // 快捷键
    function onKey(e) {
      // "/" 聚焦搜索框（仅当焦点不在 input/textarea 且不是 contenteditable）
      const tag = (e.target && e.target.tagName) || '';
      const inEditable = tag === 'INPUT' || tag === 'TEXTAREA' || (e.target && e.target.isContentEditable);
      if (e.key === '/' && !inEditable) {
        e.preventDefault();
        searchInp.focus();
        searchInp.select();
      } else if (e.key === 'Escape') {
        if (document.activeElement === searchInp) {
          searchInp.value = '';
          renderList();
          searchInp.blur();
        } else if (getQuery()) {
          searchInp.value = '';
          renderList();
        }
      }
    }
    view.addEventListener('keydown', onKey);
    document.addEventListener('keydown', onKey);

    // 卸载时清理全局监听（避免 hashchange 后污染别的页面）
    const cleanup = () => document.removeEventListener('keydown', onKey);
    window.addEventListener('hashchange', cleanup, { once: true });

    // 首屏
    renderList();
  }

  OTB.pages.commands = renderCommands;
  OTB.state.routes.commands = renderCommands;
  OTB.state.routeNames.commands = '常用命令';
  OTB.state.routeSubs.commands = 'Linux / Git / Docker / Oracle / MySQL / Redis / Nginx / Java / 前端 / IDEA 速查';
})();
