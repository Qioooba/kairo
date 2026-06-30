#!/usr/bin/env python3
"""
Mock SSH server with interactive shell support for SSH terminal testing.
Supports PTY allocation and shell channel.
"""

import os
import pty
import socket
import subprocess
import sys
import threading
import select
import signal
import struct
import fcntl
import termios

import paramiko

HOST = os.environ.get("MOCK_SSHD_HOST", "127.0.0.1")
PORT = int(os.environ.get("MOCK_SSHD_PORT", "2225"))
PASSWORD = os.environ.get("MOCK_SSHD_PASSWORD", "test")

_shell_sessions = {}


def handle_shell_channel(channel):
    """Handle an interactive shell channel with PTY."""
    master_fd, slave_fd = pty.openpty()

    try:
        env = os.environ.copy()
        env["TERM"] = "xterm-256color"
        env["PS1"] = "[mock] \\u@\\h:\\w\\$ "

        proc = subprocess.Popen(
            ["/bin/bash", "--noprofile", "--norc", "-i"],
            stdin=slave_fd,
            stdout=slave_fd,
            stderr=slave_fd,
            preexec_fn=os.setsid,
            env=env,
            close_fds=True
        )

        os.close(slave_fd)

        _shell_sessions[id(channel)] = master_fd
        
        def set_winsize(fd, rows, cols):
            try:
                fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
            except Exception:
                pass
        
        set_winsize(master_fd, 24, 80)
        
        stop_event = threading.Event()
        
        def pump_to_ws():
            try:
                while not stop_event.is_set():
                    r, _, _ = select.select([master_fd], [], [], 0.2)
                    if master_fd in r:
                        try:
                            data = os.read(master_fd, 4096)
                            if not data:
                                break
                            channel.send(data)
                        except OSError:
                            break
            except Exception:
                pass
        
        pump_thread = threading.Thread(target=pump_to_ws, daemon=True)
        pump_thread.start()
        
        try:
            while not stop_event.is_set():
                r, _, _ = select.select([channel], [], [], 0.2)
                if channel in r:
                    try:
                        data = channel.recv(4096)
                        if not data:
                            break
                        os.write(master_fd, data)
                    except Exception:
                        break
        except Exception:
            pass
        finally:
            stop_event.set()
            _shell_sessions.pop(id(channel), None)
            try:
                os.killpg(os.getpgid(proc.pid), signal.SIGHUP)
            except Exception:
                pass
            try:
                os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
            except Exception:
                pass
            try:
                proc.wait(timeout=2)
            except Exception:
                pass
            try:
                os.close(master_fd)
            except Exception:
                pass
            try:
                channel.close()
            except Exception:
                pass
    except Exception as e:
        print(f"  shell channel error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc(file=sys.stderr)


class MockShellServer(paramiko.ServerInterface):
    def __init__(self):
        self.chan_event = threading.Event()
        
    def check_channel_request(self, kind, chanid):
        if kind == "session":
            return paramiko.OPEN_SUCCEEDED
        return paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

    def check_channel_shell_request(self, channel):
        t = threading.Thread(target=handle_shell_channel, args=(channel,), daemon=True)
        t.start()
        return True

    def check_channel_pty_request(self, channel, term, width, height, pixelwidth, pixelheight, modes):
        return True

    def check_channel_window_change_request(self, channel, width, height, pixelwidth, pixelheight):
        master_fd = _shell_sessions.get(id(channel))
        if master_fd is not None:
            try:
                fcntl.ioctl(master_fd, termios.TIOCSWINSZ,
                           struct.pack('HHHH', height, width, pixelwidth, pixelheight))
            except Exception:
                pass
        return True

    def check_auth_password(self, username, password):
        if password == PASSWORD:
            return paramiko.AUTH_SUCCESSFUL
        return paramiko.AUTH_FAILED

    def get_allowed_auths(self, username):
        return "password"


def handle_client(client_socket):
    transport = paramiko.Transport(client_socket)
    key = paramiko.RSAKey.generate(2048)
    transport.add_server_key(key)
    server = MockShellServer()
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


def main():
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind((HOST, PORT))
    sock.listen(50)
    print(f"Mock SSH Shell server listening on {HOST}:{PORT}", file=sys.stderr)

    while True:
        client, addr = sock.accept()
        print(f"client connected from {addr}", file=sys.stderr)
        t = threading.Thread(target=handle_client, args=(client,), daemon=True)
        t.start()


if __name__ == "__main__":
    main()
