#!/usr/bin/env python3
"""
Mock SSH server for OpsToolbox 模拟验收。

行为：
- 监听 127.0.0.1:2222
- 接受任何用户名 + 密码 ops
- 把每个命令 chdir 到 FAKE_ROOT 后用 /bin/sh 执行
- 返回真实 stdout / stderr / exit code
- 不修改任何文件，只读
- 支持同一连接多次 exec_channel

GNU→BSD 兼容：把 `find -printf 'fmt'` 翻译成 `find -exec stat -f 'fmt' {} +`
路径映射：把 OpsToolbox 配置里的"远程绝对路径"替换为 FAKE_ROOT 下的相对路径

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
PORT = int(os.environ.get("MOCK_SSHD_PORT", "2222"))
PASSWORD = os.environ.get("MOCK_SSHD_PASSWORD", "ops")
FAKE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "fake-websphere"))

# 把 OpsToolbox 配置里的远程绝对路径映射到 FAKE_ROOT 下的相对路径。
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
    for fake_abs, real_rel in PATH_MAP.items():
        command = command.replace(fake_abs, real_rel)
    command = gnu_to_bsd_find(command)
    return command


def run_command(command):
    """在 FAKE_ROOT 跑命令，返回 (stdout_bytes, stderr_bytes, exit_code)"""
    try:
        proc = subprocess.run(
            ["/bin/sh", "-c", command],
            cwd=FAKE_ROOT,
            capture_output=True,
            timeout=60,
        )
        return proc.stdout, proc.stderr, proc.returncode
    except subprocess.TimeoutExpired:
        return b"", b"mock ssh: command timeout (60s)\n", 124
    except Exception as e:
        return b"", f"mock ssh: exec error: {e}\n".encode(), 1


class MockServer(paramiko.ServerInterface):
    def check_channel_request(self, kind, chanid):
        if kind == "session":
            return paramiko.OPEN_SUCCEEDED
        return paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

    def check_channel_subsystem_request(self, channel, name):
        # 只放行 sftp，其它 subsystem 一律拒。
        if name == "sftp" and SFTPServer is not None:
            return paramiko.OPEN_SUCCEEDED
        return paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

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
        try:
            stdout, stderr, code = run_command(translated)
            if isinstance(stdout, str):
                stdout = stdout.encode("utf-8", errors="replace")
            if isinstance(stderr, str):
                stderr = stderr.encode("utf-8", errors="replace")
            channel.sendall(stdout)
            if stderr:
                try:
                    channel.sendall_stderr(stderr)
                except Exception:
                    pass
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
        """把客户端传入的"远程路径"翻译成 FAKE_ROOT 下的真实路径。"""
        if path is None:
            path = "/"
        if not path.startswith("/"):
            path = "/" + path
        for fake_abs, real_rel in PATH_MAP.items():
            if path == fake_abs:
                return os.path.join(FAKE_ROOT, real_rel)
            prefix = fake_abs.rstrip("/") + "/"
            if path.startswith(prefix):
                rel = path[len(prefix):]
                return os.path.join(FAKE_ROOT, real_rel, rel)
        # 不在白名单里：拒绝
        return None

    def list_folder(self, path):
        real = self._resolve(path)
        if real is None or not os.path.isdir(real):
            return SFTP_PERMISSION_DENIED, []
        try:
            entries = []
            for name in os.listdir(real):
                full = os.path.join(real, name)
                attr = SFTPAttributes.from_stat(os.stat(full)) if SFTPAttributes else None
                entries.append((name, attr) if attr else name)
            return SFTP_OK, entries
        except Exception as e:
            print(f"  sftp list_folder {path} err: {e}", file=sys.stderr)
            return SFTP_PERMISSION_DENIED, []

    def stat(self, path):
        real = self._resolve(path)
        if real is None or not os.path.exists(real):
            return SFTP_PERMISSION_DENIED, SFTPAttributes()
        try:
            return SFTP_OK, SFTPAttributes.from_stat(os.stat(real))
        except Exception:
            return SFTP_PERMISSION_DENIED, SFTPAttributes()

    lstat = stat

    def open(self, path, flags, attr):
        real = self._resolve(path)
        if real is None or not os.path.isfile(real):
            return SFTP_NO_SUCH_FILE, SFTPHandle(None)
        # 只允许读。flags 是 paramiko 内部位运算位掩码；含 0x1 表示 O_WRONLY，0x2 表示 O_RDWR，含其一就拒。
        is_write = bool(flags & (paramiko.sftp.O_WRONLY | paramiko.sftp.O_RDWR | paramiko.sftp.O_CREAT | paramiko.sftp.O_TRUNC))
        if is_write:
            return SFTP_PERMISSION_DENIED, SFTPHandle(None)
        try:
            f = open(real, "rb")
            handle = SFTPHandle(flags)
            handle.filename = real
            handle.readfile = f
            return SFTP_OK, handle
        except Exception as e:
            print(f"  sftp open {path} err: {e}", file=sys.stderr)
            return SFTP_NO_SUCH_FILE, SFTPHandle(None)

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
            transport.set_subsystem_handler("sftp", SFTPServer, ReadOnlySFTPServer)
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

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind((HOST, PORT))
    sock.listen(50)
    print(f"Mock SSH listening on {HOST}:{PORT} (root={FAKE_ROOT})", file=sys.stderr)

    while True:
        client, addr = sock.accept()
        print(f"client connected from {addr}", file=sys.stderr)
        t = threading.Thread(target=handle_client, args=(client,), daemon=True)
        t.start()


if __name__ == "__main__":
    main()
