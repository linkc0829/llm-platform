"""run_eval.py vectors health gatekeeper 自我檢驗。

測試場景:
1. vectors 為 "stale" -> exit != 0, 0 /chat, no output file
2. 缺少 vectors 欄位 -> exit != 0, 0 /chat, no output file
3. /health 回傳 HTTP 500 -> exit != 0, 0 /chat, no output file
4. /health 回傳 non-JSON -> exit != 0, 0 /chat, no output file
5. 連線被拒絕 (connection refused) -> exit != 0, 0 /chat, no output file
6. (正向對照) vectors 為 "ok" -> 正常執行 /chat, exit == 0, 產出 output file

跑法:
    python selfcheck_health.py
"""
import http.server
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading


class MockKBHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass  # 靜音標準 log

    def do_GET(self):
        self.server.requests_log.append(("GET", self.path))
        if self.path.endswith("/health"):
            mode = getattr(self.server, "mode", "ok")
            if mode == "stale":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"status": "ok", "vectors": "stale"}).encode("utf-8"))
            elif mode == "missing_vectors":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"status": "ok"}).encode("utf-8"))
            elif mode == "http_500":
                self.send_response(500)
                self.send_header("Content-Type", "text/plain")
                self.end_headers()
                self.wfile.write(b"Internal Server Error")
            elif mode == "non_json":
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                self.wfile.write(b"<html>not json</html>")
            elif mode == "ok":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"status": "ok", "vectors": "ok"}).encode("utf-8"))
            else:
                self.send_response(404)
                self.end_headers()
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        self.server.requests_log.append(("POST", self.path))
        if self.path.endswith("/chat"):
            # 讀取 request body 避免客戶端 BrokenPipe
            content_len = int(self.headers.get("Content-Length", 0))
            if content_len > 0:
                self.rfile.read(content_len)
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            resp = {
                "answer": "根據 docs/doc1.md 的說明",
                "grounded": True,
                "sources": ["docs/doc1.md#步驟-1"],
                "strategy": "hybrid",
            }
            self.wfile.write(json.dumps(resp).encode("utf-8"))
        else:
            self.send_response(404)
            self.end_headers()


def get_unused_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def run():
    run_eval_script = str(pathlib.Path(__file__).with_name("run_eval.py"))

    with tempfile.TemporaryDirectory() as tmpdir:
        eval_dir = os.path.join(tmpdir, "eval")
        area_dir = os.path.join(eval_dir, "TestArea")
        os.makedirs(area_dir, exist_ok=True)

        # 準備 eval yaml
        yaml_content = (
            '- question: "測試問題"\n'
            '  expect_source_id: "doc1"\n'
            '  must_not_infer: false\n'
            '  pass_criteria:\n'
            '    - "dummy"\n'
        )
        with open(os.path.join(area_dir, "test-eval.yaml"), "w", encoding="utf-8") as f:
            f.write(yaml_content)

        # 準備 kb_index.json
        index_content = {"docs": [{"id": "doc1", "path": "docs/doc1.md"}]}
        with open(os.path.join(eval_dir, "kb_index.json"), "w", encoding="utf-8") as f:
            json.dump(index_content, f)

        # 啟動 mock server
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), MockKBHandler)
        server.requests_log = []
        server_port = server.server_address[1]

        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()

        failure_cases = [
            ("stale", f"http://127.0.0.1:{server_port}/chat", "stale"),
            ("missing_vectors", f"http://127.0.0.1:{server_port}/chat", "missing_vectors"),
            ("http_500", f"http://127.0.0.1:{server_port}/chat", "http_500"),
            ("non_json", f"http://127.0.0.1:{server_port}/chat", "non_json"),
            ("connection_refused", f"http://127.0.0.1:{get_unused_port()}/chat", "none"),
        ]

        try:
            for name, kb_url, mode in failure_cases:
                server.mode = mode
                server.requests_log.clear()
                out_file = os.path.join(tmpdir, f"out_{name}.json")
                sentinel = json.dumps([{"checkpoint_preserved": True, "case": name}])
                with open(out_file, "w", encoding="utf-8") as f:
                    f.write(sentinel)

                env = os.environ.copy()
                env["KB_URL"] = kb_url
                env["KB_EVAL_DIR"] = eval_dir
                env["KB_EVAL_OUT"] = out_file
                env["KB_EVAL_SKIP_BUILD_CHECK"] = "1"
                env["KB_EVAL_REQUESTS_PER_MINUTE"] = "6000"

                proc = subprocess.run(
                    [sys.executable, run_eval_script],
                    env=env,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    timeout=15,
                )

                assert proc.returncode != 0, f"Case {name} expected non-zero exit code, got 0"
                chat_calls = [path for cmd, path in server.requests_log if path.endswith("/chat")]
                assert len(chat_calls) == 0, f"Case {name} expected 0 calls to /chat, got {chat_calls}"
                assert os.path.exists(out_file), f"Case {name} expected {out_file} to exist"
                with open(out_file, "r", encoding="utf-8") as f:
                    content = f.read()
                assert content == sentinel, f"Case {name} checkpoint was modified or overwritten"
                print(f"ok  case {name}: blocked before eval, 0 calls to /chat, checkpoint untouched")

            # 正向對照 (positive control)
            server.mode = "ok"
            server.requests_log.clear()
            out_file = os.path.join(tmpdir, "out_positive.json")
            if os.path.exists(out_file):
                os.remove(out_file)

            env = os.environ.copy()
            env["KB_URL"] = f"http://127.0.0.1:{server_port}/chat"
            env["KB_EVAL_DIR"] = eval_dir
            env["KB_EVAL_OUT"] = out_file
            env["KB_EVAL_SKIP_BUILD_CHECK"] = "1"
            env["KB_EVAL_REQUESTS_PER_MINUTE"] = "6000"

            proc = subprocess.run(
                [sys.executable, run_eval_script],
                env=env,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=15,
            )

            assert proc.returncode == 0, f"Positive case expected 0 exit code, got {proc.returncode}, stderr: {proc.stderr}"
            chat_calls = [path for cmd, path in server.requests_log if path.endswith("/chat")]
            assert len(chat_calls) > 0, "Positive case expected calls to /chat"
            assert os.path.exists(out_file), f"Positive case expected {out_file} created"
            with open(out_file, encoding="utf-8") as f:
                data = json.load(f)
            assert len(data) > 0, "Positive case expected eval results in output file"
            print("ok  positive control: vectors: ok allows evaluation and records /chat")

        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    run()
    print("ALL HEALTH GATEKEEPER CHECKS PASSED")
