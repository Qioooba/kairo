#!/usr/bin/env python3
"""
Mock SSH server for Kairo 模拟验收。

行为：
- 监听 127.0.0.1:2222
- 接受任何用户名 + 密码 ops
- 把每个命令 chdir 到 FAKE_ROOT 后用 /bin/sh 执行
- 返回真实 stdout / stderr / exit code
- 不修改任何文件，只读
- 支持同一连接多次 exec_channel

GNU→BSD 兼容：把 `find -printf 'fmt'` 翻译成 `find -exec stat -f 'fmt' {} +`
路径映射：把 Kairo 配置里的"远程绝对路径"替换为 FAKE_ROOT 下的相对路径

依赖：pip install paramiko
"""

import os
import re
import socket
import subprocess
import sys
import threading

import paramiko
try:
    from paramiko import SFTPServer, SFTPServerInterface, SFTPAttributes, SFTPHandle, SFTP_OK, SFTP_PERMISSION_DENIED, SFTP_NO_SUCH_FILE
except Exception:  # 旧 paramiko 没有这些常量时降级
    SFTPServer = None
    SFTPServerInterface = object
    SFTPAttributes = None
    SFTPHandle = None
    SFTP_OK = 0
    SFTP_PERMISSION_DENIED = 3
    SFTP_NO_SUCH_FILE = 2

HOST = os.environ.get("MOCK_SSHD_HOST", "127.0.0.1")
PORT = int(os.environ.get("MOCK_SSHD_PORT", "2225"))
PASSWORD = os.environ.get("MOCK_SSHD_PASSWORD", "test")
FAKE_ROOT = os.environ.get("FAKE_ROOT", os.path.abspath(os.path.join(os.path.dirname(__file__), "fake-websphere")))
FAKE_FILES_ROOT = os.environ.get("FAKE_FILES_ROOT", os.path.abspath(os.path.join(os.path.dirname(__file__), "fake-files")))

# 把 Kairo 配置里的远程绝对路径映射到 FAKE_ROOT 下的相对路径。
PATH_MAP = {
    "/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1":
        "opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1",
    "/opt/IBM/WebSphere/AppServer/profiles/AppSrv02/logs/server1":
        "opt/IBM/WebSphere/AppServer/profiles/AppSrv02/logs/server1",
}

PRINTF_RE = re.compile(r"""-printf\s+(['"])([^'"]+)\1""")


def gnu_to_bsd_find(cmd):
    """如果命令里有 GNU find -printf，转成 BSD find -exec stat。"""
    if "-printf" not in cmd:
        return cmd

    def repl(m):
        quote = m.group(1)
        fmt = m.group(2)
        # GNU printf → BSD stat -f
        bsd = fmt
        bsd = bsd.replace(r"%s", r"%z")     # size
        bsd = bsd.replace(r"%T@", r"%m")    # mtime unix seconds
        bsd = bsd.replace(r"%p", r"%N")     # full path
        bsd = bsd.replace(r"%n", r"%N")     # file name
        # 字面 \t \n 转成真 tab / newline
        bsd = bsd.replace(r"\t", "\t")
        bsd = bsd.replace(r"\n", "\n")
        return "-exec stat -f " + quote + bsd + quote + " {} +"

    return PRINTF_RE.sub(repl, cmd)


def translate_for_mock(command):
    """路径映射 + GNU→BSD 转换。"""
    # WebSphere 日志路径
    for fake_abs, real_rel in PATH_MAP.items():
        command = command.replace(fake_abs, real_rel)

    # Files/FTP 路径：对于以 / 开头但不是 WebSphere 路径的命令，
    # 把 / 替换为 FAKE_FILES_ROOT（安全限制：只替换字面值 /）
    # 这样 "ls /" 变成 "ls /path/to/fake-files"
    # 但 "cat /opt/IBM/..." 不会被影响（已经被上面的循环处理）
    if not command.startswith('/opt/IBM') and not command.startswith('"') and not command.startswith("'"):
        # 简单情况：命令以 ls / 或 cat / 等开头
        import shlex
        try:
            parts = shlex.split(command)
            new_parts = []
            for i, part in enumerate(parts):
                if part == '/' and i == len(parts) - 1:
                    # 单独的 / 参数，替换为 FAKE_FILES_ROOT
                    new_parts.append(FAKE_FILES_ROOT)
                elif part.startswith('/') and not part.startswith(FAKE_FILES_ROOT):
                    # 绝对路径参数，检查是否是 WebSphere 路径
                    if '/opt/IBM' not in part:
                        # 非 WebSphere 路径，当作 Files 路径
                        rel = part.lstrip('/')
                        new_parts.append(os.path.join(FAKE_FILES_ROOT, rel))
                    else:
                        new_parts.append(part)
                else:
                    new_parts.append(part)
            command = ' '.join(shlex.quote(p) for p in new_parts)
        except Exception:
            # 如果解析失败，用简单的字符串替换
            if command.startswith('ls /') or command.startswith('cat /') or command.startswith('stat /'):
                command = command.replace('/ ', FAKE_FILES_ROOT + '/', 1)
                command = command.replace('/\'', FAKE_FILES_ROOT + '/\'')
                command = command.replace('"/', FAKE_FILES_ROOT + '/"')

    command = gnu_to_bsd_find(command)
    return command


def run_command_streaming(command, on_stdout, on_stderr, timeout=600):
    """在 FAKE_ROOT 流式跑命令：每收到 stdout/stderr 一段就回调。

    返回 exit code。客户端断连/关闭 channel 后，调用方应主动取消线程或终止进程。
    """
    try:
        proc = subprocess.Popen(
            ["/bin/sh", "-c", command],
            cwd=FAKE_ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
        )
    except Exception as e:
        on_stderr(f"mock ssh: exec error: {e}\n".encode("utf-8", errors="replace"))
        return 1

    def pump(stream, cb):
        try:
            while True:
                chunk = stream.readline()
                if not chunk:
                    return
                try:
                    cb(chunk)
                except Exception:
                    # channel 已关，杀进程走人
                    try:
                        proc.kill()
                    except Exception:
                        pass
                    return
        except Exception:
            return

    t_out = threading.Thread(target=pump, args=(proc.stdout, on_stdout), daemon=True)
    t_err = threading.Thread(target=pump, args=(proc.stderr, on_stderr), daemon=True)
    t_out.start()
    t_err.start()

    try:
        code = proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        try:
            proc.kill()
        except Exception:
            pass
        t_out.join(1)
        t_err.join(1)
        on_stderr(b"mock ssh: command timeout\n")
        return 124
    # 等读取线程收尾（process 退出会让 readline 拿到 EOF）
    t_out.join(2)
    t_err.join(2)
    return code


class MockServer(paramiko.ServerInterface):
    def check_channel_request(self, kind, chanid):
        if kind == "session":
            return paramiko.OPEN_SUCCEEDED
        return paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

    # 注意：不要 override check_channel_subsystem_request！
    # ServerInterface 默认实现会查 transport.subsystem_table，
    # 如果有 set_subsystem_handler 注册的 handler，会自动起 SFTPServer 线程处理。
    # 之前 mock 自己 override 只返回 OPEN_SUCCEEDED 而没启动 SFTP server，
    # 导致客户端 invoke_subsystem("sftp") 后等不到 SFTP 协议响应、channel 关闭。

    def check_channel_shell_request(self, channel):
        # 模拟环境不开交互 shell，exec channel 就够用。
        return paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

    def check_auth_password(self, username, password):
        if password == PASSWORD:
            return paramiko.AUTH_SUCCESSFUL
        return paramiko.AUTH_FAILED

    def get_allowed_auths(self, username):
        return "password"

    def check_channel_exec_request(self, channel, command):
        # 异步执行
        t = threading.Thread(target=self._run_one, args=(channel, command), daemon=True)
        t.start()
        return True

    def _run_one(self, channel, command):
        if isinstance(command, bytes):
            try:
                command = command.decode("utf-8")
            except UnicodeDecodeError:
                command = command.decode("utf-8", errors="replace")
        print(f"  exec_in : {command}", file=sys.stderr)
        translated = translate_for_mock(command)
        print(f"  exec_run: {translated}", file=sys.stderr)

        def on_stdout(chunk):
            channel.sendall(chunk)

        def on_stderr(chunk):
            try:
                channel.sendall_stderr(chunk)
            except Exception:
                pass

        try:
            code = run_command_streaming(translated, on_stdout, on_stderr)
            channel.send_exit_status(code)
        except Exception as e:
            import traceback
            traceback.print_exc(file=sys.stderr)
            try:
                msg = ("mock ssh: " + str(e) + "\n").encode("utf-8")
                channel.sendall(msg)
                channel.send_exit_status(1)
            except Exception:
                pass
        finally:
            try:
                channel.shutdown(2)
            except Exception:
                pass
            try:
                channel.close()
            except Exception:
                pass


class ReadOnlySFTPServer(SFTPServerInterface):
    """只读 SFTP 服务：把客户端请求的"远程绝对路径"翻译到 FAKE_ROOT 下。"""

    def __init__(self, server, *args, **kwargs):
        super().__init__(server, *args, **kwargs)

    def _resolve(self, path):
        """把客户端传入的"远程路径"翻译到 FAKE_ROOT 或 FAKE_FILES_ROOT 下。"""
        if path is None:
            path = "/"
        if not path.startswith("/"):
            path = "/" + path

        # 先查 PATH_MAP（WebSphere 日志路径）
        for fake_abs, real_rel in PATH_MAP.items():
            if path == fake_abs:
                return os.path.join(FAKE_ROOT, real_rel)
            prefix = fake_abs.rstrip("/") + "/"
            if path.startswith(prefix):
                rel = path[len(prefix):]
                return os.path.join(FAKE_ROOT, real_rel, rel)

        # Files / FTP 场景：根路径 "/" 映射到 FAKE_FILES_ROOT
        # FTP 服务器的 root 是 "/"，所以 /file.txt → FAKE_FILES_ROOT/file.txt
        if path == "/" or not path.startswith("/opt/IBM"):
            # 如果 path 是文件或目录的绝对路径（如 /README.md），
            # 直接拼接到 FAKE_FILES_ROOT
            rel_path = path.lstrip("/")
            fake_path = os.path.join(FAKE_FILES_ROOT, rel_path) if rel_path else FAKE_FILES_ROOT
            # 安全检查：确保结果在 FAKE_FILES_ROOT 下
            real = os.path.abspath(fake_path)
            root = os.path.abspath(FAKE_FILES_ROOT)
            if real.startswith(root):
                return real
            return None

        # 不在白名单里：拒绝
        return None

    def list_folder(self, path):
        real = self._resolve(path)
        if real is None or not os.path.isdir(real):
            return SFTP_PERMISSION_DENIED
        try:
            entries = []
            for name in os.listdir(real):
                full = os.path.join(real, name)
                if SFTPAttributes is None:
                    continue
                attr = SFTPAttributes.from_stat(os.stat(full))
                # paramiko 5.x 严格要求纯 SFTPAttributes 列表，且每个要有 .filename
                attr.filename = name
                entries.append(attr)
            return entries
        except Exception as e:
            print(f"  sftp list_folder {path} err: {e}", file=sys.stderr)
            return SFTP_PERMISSION_DENIED

    def stat(self, path):
        real = self._resolve(path)
        if real is None or not os.path.exists(real):
            return SFTP_PERMISSION_DENIED
        try:
            return SFTPAttributes.from_stat(os.stat(real))
        except Exception:
            return SFTP_PERMISSION_DENIED

    lstat = stat

    def fstat(self, handle):
        # 实际不被 paramiko 调用（它调 handle.stat()），但留着保持完整性。
        return handle.stat()

    def open(self, path, flags, attr):
        real = self._resolve(path)
        if real is None or not os.path.isfile(real):
            return SFTP_NO_SUCH_FILE
        # 只允许读。flags 是 paramiko 内部 SFTP_FLAG_* 位掩码；
        # 旧版 paramiko 有 paramiko.sftp.O_WRONLY 等 os.O_* 常量，新版（5.x）已删除，只能用 SFTP_FLAG_*。
        flag_mask = (
            getattr(paramiko.sftp, "SFTP_FLAG_WRITE", 0x2)
            | getattr(paramiko.sftp, "SFTP_FLAG_CREATE", 0x4)
            | getattr(paramiko.sftp, "SFTP_FLAG_TRUNC", 0x8)
            | getattr(paramiko.sftp, "SFTP_FLAG_APPEND", 0x10)
        )
        if flags & flag_mask:
            return SFTP_PERMISSION_DENIED
        try:
            f = open(real, "rb")
            handle = SFTPHandle(flags)
            handle.filename = real
            handle.readfile = f
            # paramiko 处理 SSH_FXP_FSTAT 时调 handle.stat()，默认返回 OP_UNSUPPORTED。
            # 我们 override 成查 .filename 对应的实际 stat。
            def _stat(self=handle):
                real_path = getattr(self, "filename", None)
                if not real_path or not os.path.exists(real_path):
                    return SFTP_PERMISSION_DENIED
                try:
                    return SFTPAttributes.from_stat(os.stat(real_path))
                except Exception:
                    return SFTP_PERMISSION_DENIED
            handle.stat = _stat
            return handle
        except Exception as e:
            print(f"  sftp open {path} err: {e}", file=sys.stderr)
            return SFTP_NO_SUCH_FILE

    def remove(self, path):
        return SFTP_PERMISSION_DENIED

    def rename(self, src, dst):
        return SFTP_PERMISSION_DENIED

    def mkdir(self, path, attr):
        return SFTP_PERMISSION_DENIED

    def rmdir(self, path):
        return SFTP_PERMISSION_DENIED


def handle_client(client_socket):
    transport = paramiko.Transport(client_socket)
    key = paramiko.RSAKey.generate(2048)
    transport.add_server_key(key)
    # 提前注册 sftp subsystem handler：客户端请求 sftp 时 paramiko 会自动起 SFTPServer
    if SFTPServer is not None:
        try:
            # 关键：必须用 sftp_si= 关键字参数，否则 ReadOnlySFTPServer 被当成 *args 传进去，
            # paramiko 在构造时拿不到正确的 SFTPServerInterface，所有 SFTP 调用都会返回 SSH_FX_FAILURE。
            transport.set_subsystem_handler("sftp", SFTPServer, sftp_si=ReadOnlySFTPServer)
        except Exception as e:
            print(f"  set_subsystem_handler 失败: {e}", file=sys.stderr)
    server = MockServer()
    try:
        transport.start_server(server=server)
    except paramiko.SSHException:
        return
    while True:
        chan = transport.accept(60)
        if chan is None:
            if not transport.is_active():
                break
            continue
        # exec channel：客户端会随后发 exec_request；_run_one 在 MockServer 里处理。
        # sftp channel：由 set_subsystem_handler 自动驱动，这里啥也不用做。
        # paramiko 会在内部开新线程跑 sftp server，本循环继续 accept 下一个 channel。
        # 注：transport._channels 在新版 paramiko 是 ChannelMap 对象，不能用 `in` 直接判断。
        # paramiko 1.7+ 已通过 set_subsystem_handler 自动起 SFTP server，这里只需要 keep-alive 即可。
        # 留个 channel 引用防止被 GC
        _ = chan


def main():
    if not os.path.isdir(FAKE_ROOT):
        print(f"FAKE_ROOT 不存在: {FAKE_ROOT}", file=sys.stderr)
        sys.exit(1)
    if not os.path.isdir(FAKE_FILES_ROOT):
        print(f"FAKE_FILES_ROOT 不存在: {FAKE_FILES_ROOT}", file=sys.stderr)
        sys.exit(1)

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind((HOST, PORT))
    sock.listen(50)
    print(f"Mock SSH listening on {HOST}:{PORT}", file=sys.stderr)
    print(f"  FAKE_ROOT (WebSphere): {FAKE_ROOT}", file=sys.stderr)
    print(f"  FAKE_FILES_ROOT (Files/FTP): {FAKE_FILES_ROOT}", file=sys.stderr)

    while True:
        client, addr = sock.accept()
        print(f"client connected from {addr}", file=sys.stderr)
        t = threading.Thread(target=handle_client, args=(client,), daemon=True)
        t.start()


if __name__ == "__main__":
    main()
