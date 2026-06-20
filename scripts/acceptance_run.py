#!/usr/bin/env python3
"""OpsToolbox Phase 1 模拟验收驱动"""
import json, os, signal, socket, subprocess, sys, time, urllib.request, urllib.error, contextlib

ROOT = "/Users/qi/Documents/spaces/ops-toolbox"
MOCK_PORT = int(os.environ.get("MOCK_SSHD_PORT", "2225"))
APP_PORT = int(os.environ.get("OPS_APP_PORT", "18090"))
LOG_DIR = os.path.join(ROOT, "logs")
os.makedirs(LOG_DIR, exist_ok=True)

def wait_port(host, port, timeout=8.0):
    end = time.time() + timeout
    while time.time() < end:
        with contextlib.suppress(Exception):
            s = socket.socket(); s.settimeout(0.3); s.connect((host, port)); s.close(); return True
        time.sleep(0.1)
    return False

def http_post(path, body):
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        f"http://127.0.0.1:{APP_PORT}{path}",
        data=data, headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, json.loads(r.read().decode("utf-8") or "{}")
    except urllib.error.HTTPError as e:
        try:    payload = json.loads(e.read().decode("utf-8") or "{}")
        except: payload = {"error": f"HTTP {e.code}"}
        return e.code, payload
    except Exception as e:
        return 0, {"error": f"{type(e).__name__}: {e}"}

def run(label, body, path):
    code, payload = http_post(path, body)
    print(f"===== {label} =====")
    print(f"HTTP {code} -> {json.dumps(payload, ensure_ascii=False)[:900]}")
    print()
    return code, payload

def main():
    # 启动 mock
    mock = subprocess.Popen(
        [sys.executable, os.path.join(ROOT, "scripts/mock_sshd.py")],
        env={**os.environ, "MOCK_SSHD_PORT": str(MOCK_PORT), "MOCK_SSHD_HOST": "127.0.0.1"},
        stdout=open(os.path.join(LOG_DIR, "mock_sshd.out"), "w"),
        stderr=subprocess.STDOUT, cwd=ROOT)
    print(f"mock pid={mock.pid}")
    if not wait_port("127.0.0.1", MOCK_PORT, 6):
        print("MOCK NOT READY"); mock.terminate(); return
    print(f"mock listening on 127.0.0.1:{MOCK_PORT}")

    # 启动 OpsToolbox
    app = subprocess.Popen(
        [os.path.join(ROOT, "OpsToolbox_mac")],
        stdout=open(os.path.join(LOG_DIR, "ops_toolbox.out"), "w"),
        stderr=subprocess.STDOUT, cwd=ROOT, preexec_fn=os.setsid)
    print(f"app  pid={app.pid}")
    if not wait_port("127.0.0.1", APP_PORT, 6):
        print("APP NOT READY"); mock.terminate(); app.terminate(); return
    print(f"app  listening on 127.0.0.1:{APP_PORT}")
    print()

    CRED = {
        "system": "信贷生产（模拟）",
        "server": "mock-node-1",
        "dir": "/opt/IBM/WebSphere/AppServer/profiles/AppSrv01/logs/server1",
        "username": "test",
        "password": "ops",
    }

    # 基础接口
    run("T01 SSH test ok", CRED, "/api/ssh/test")
    run("T02 SSH test 错密码", dict(CRED, password="WRONG"), "/api/ssh/test")
    nopass = dict(CRED); nopass.pop("password")
    run("T03 SSH test 缺密码", nopass, "/api/ssh/test")
    run("T04 logs.list", CRED, "/api/logs/list")
    run("T05 search Exception", dict(CRED, query="Exception"), "/api/logs/search")
    run("T06 search Exception && userinfo", dict(CRED, query="Exception && userinfo"), "/api/logs/search")
    run("T07 search ORA-00060 || deadlock", dict(CRED, query="ORA-00060 || deadlock"), "/api/logs/search")
    run("T08 search Exception && !DEBUG", dict(CRED, query="Exception && !DEBUG"), "/api/logs/search")
    run("T09 search !DEBUG", dict(CRED, query="!DEBUG"), "/api/logs/search")
    run("T10 search 危险 ;", dict(CRED, query="Exception; cat /etc/passwd"), "/api/logs/search")
    run("T11 search 危险 $", dict(CRED, query="$(id)"), "/api/logs/search")
    run("T12 search 危险 反引号", dict(CRED, query="`whoami`"), "/api/logs/search")
    run("T13 list 目录白名单 /etc", dict(CRED, dir="/etc"), "/api/logs/list")
    run("T14 list 目录白名单 /", dict(CRED, dir="/"), "/api/logs/list")
    run("T15 download latest=1", dict(CRED, latest=1), "/api/logs/download-latest")
    run("T16 context line=13", dict(CRED, file="SystemOut.log", line=13, before=5, after=5), "/api/logs/context")

    # 报文
    run("T17 JSON format", {"input": '{"a":1,"b":[1,2,3]}', "mode": "format", "indent": "  "}, "/api/format/json")
    run("T18 JSON minify", {"input": "{\n  \"a\": 1\n}", "mode": "minify"}, "/api/format/json")
    run("T19 JSON validate bad", {"input": "not json", "mode": "validate"}, "/api/format/json")
    run("T20 XML format", {"input": "<a><b>1</b><b>2</b></a>", "mode": "format", "indent": "  "}, "/api/format/xml")
    run("T21 XML minify", {"input": "<a><b>1</b></a>", "mode": "minify"}, "/api/format/xml")

    # GBK 验证
    print("===== T22 GBK 目录 list =====")
    code, payload = http_post("/api/logs/list", CRED)
    files = payload.get("files", []) or []
    gbk_files = [f for f in files if f.get("name","").startswith("gbk_")]
    print(f"HTTP {code} -> gbk files: {[f['name'] for f in gbk_files]}")
    print()

    print("===== T23 GBK 搜索 信贷系统 =====")
    code, payload = http_post("/api/logs/search", dict(CRED, query="信贷系统"))
    hits = payload.get("hits", []) or []
    for h in hits[:5]:
        print(f"  hit file={h['file']} line={h['line_no']} content={h['content']!r}")
    print(f"  total: {len(hits)} hits")
    print()

    print("===== T24 GBK 下载 =====")
    code, payload = http_post("/api/logs/download-latest", dict(CRED, latest=1))
    print(f"HTTP {code} -> {json.dumps(payload, ensure_ascii=False)[:600]}")
    print()

    time.sleep(0.5)
    print("===== 审计日志 ====")
    af = os.path.join(LOG_DIR, "audit.log")
    if os.path.exists(af):
        with open(af) as f: text = f.read()
        n = text.lower().count("password")
        print(f"  audit.log: {len(text.splitlines())} 行, password 出现 {n} 次")
        for line in text.splitlines()[-30:]:
            print("  " + line)
    else:
        print("  审计日志不存在")

    print()
    print("===== downloads 目录 ====")
    subprocess.run(["ls", "-la", os.path.join(ROOT, "downloads")])

    print("=== 关闭 mock + OpsToolbox ===")
    try: os.killpg(os.getpgid(app.pid), signal.SIGTERM)
    except: pass
    mock.terminate()
    try: mock.wait(timeout=4)
    except: mock.kill()
    try: app.wait(timeout=4)
    except: app.kill()
    print("done")

if __name__ == "__main__":
    main()
