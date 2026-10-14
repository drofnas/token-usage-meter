#!/usr/bin/env python3
"""Sanitized native firmware contract, state, framing and deadline checks."""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def run(output):
    OUT = output.resolve()
    if not OUT.is_relative_to(ROOT / 'artifacts'):
        raise ValueError('output must be under repository artifacts')
    OUT.mkdir(parents=True, exist_ok=True)
    battery_binary = OUT / 'battery-check'
    subprocess.run(['c++', '-std=c++17', '-Wall', '-Wextra', '-Werror', '-g',
                    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-I', str(ROOT),
                    str(Path(__file__).with_name('battery.cpp')), '-o', str(battery_binary)], check=True)
    subprocess.run([str(battery_binary)], check=True, timeout=15)
    binary = OUT / 'check'
    subprocess.run(['c++', '-std=c++17', '-Wall', '-Wextra', '-Werror', '-g',
                    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-I', str(ROOT),
                    str(Path(__file__).with_name('native.cpp')), '-o', str(binary)], check=True)
    token = 'a' * 64
    # Exercise the real Go server with the device's actual transport and decoder.
    api = OUT.parent / 'meter-api'
    if not api.is_file():
        subprocess.run(['go', 'build', '-o', str(api), './cmd/meter-api'], cwd=ROOT, check=True)
    data = OUT / 'data'
    data.mkdir(exist_ok=True)
    (data / 'usage.json').write_bytes((ROOT / 'contracts/v3/fixtures/normal.json').read_bytes())
    with socket.socket() as reserved:
        reserved.bind(('127.0.0.1', 0))
        port = reserved.getsockname()[1]
    env = {k: v for k, v in os.environ.items() if not k.startswith('METER_')}
    env.update(METER_API_TOKEN=token, METER_TIMEZONE='UTC', METER_DATA_DIR=str(data),
               METER_API_BIND_ADDRESS='127.0.0.1', METER_API_PORT=str(port))
    with subprocess.Popen([str(api)], env=env) as server:
        try:
            deadline = time.monotonic() + 5
            while True:
                try:
                    with socket.create_connection(('127.0.0.1', port), timeout=.2): break
                except OSError:
                    if time.monotonic() >= deadline or server.poll() is not None:
                        raise RuntimeError('synthetic API did not start')
                    time.sleep(.02)
            result = subprocess.check_output([str(binary), 'usage', '127.0.0.1', str(port), token, '1000'], text=True, timeout=3)
            assert result.strip() == 'valid', result
        finally:
            server.terminate()
            try: server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill(); server.wait()
    scenarios = [
        ('post', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}', 'valid'),
        ('length', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}', 'valid'),
        ('chunked', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n1\r\n{\r\n1\r\n}\r\n0\r\n\r\n', 'valid'),
        ('status', b'HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n', 'http_status'),
        ('redirect', b'HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:9\r\nContent-Length: 0\r\n\r\n', 'http_status'),
        ('oversized', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4097\r\n\r\n', 'oversized'),
        ('chunk-overflow', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n1001\r\n', 'oversized'),
        ('truncated', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 3\r\n\r\n{}', 'truncated'),
        ('gzip', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Encoding: gzip\r\nContent-Length: 2\r\n\r\n{}', 'encoding'),
        ('no-framing', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}', 'headers'),
        ('ambiguous-framing', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\n\r\n', 'headers'),
        ('duplicate-length', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\n{}', 'headers'),
        ('header-cap', b'HTTP/1.1 200 OK\r\n' + b'X-Long: ' + b'a'*2048, 'headers'),
        ('slow-headers', b'HTTP/1.1 200 OK\r\n', 'timeout'),
        ('slow-body', b'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 10\r\n\r\n', 'timeout'),
    ]
    durations = []
    for name, response, expected in scenarios:
        with socket.socket() as server:
            server.bind(('127.0.0.1', 0)); server.listen(1); server.settimeout(5)
            port = server.getsockname()[1]
            failures = []
            def serve():
                try:
                    with server.accept()[0] as peer:
                        peer.settimeout(2); request = b''
                        while b'\r\n\r\n' not in request:
                            part = peer.recv(512)
                            if not part: raise RuntimeError('incomplete test request')
                            request += part
                        if name == 'post':
                            assert request.startswith(b'POST /v2/battery HTTP/1.1\r\n')
                            header, body = request.split(b'\r\n\r\n', 1)
                            while len(body) < 13: body += peer.recv(512)
                            assert b'Content-Length: 13' in header and body == b'{"version":2}'
                        else:
                            assert request.startswith(b'GET /v3/usage HTTP/1.1\r\n')
                        assert b'Authorization: Bearer ' + token.encode() + b'\r\n' in request
                        peer.sendall(response)
                        if name.startswith('slow-'):
                            for _ in range(30):
                                time.sleep(.03)
                                try: peer.sendall(b' ')
                                except OSError: break
                except Exception as error: failures.append(type(error).__name__)
            worker = threading.Thread(target=serve); worker.start()
            start = time.monotonic()
            result = subprocess.check_output([str(binary), 'post' if name == 'post' else 'http', '127.0.0.1', str(port), token, '300'], text=True, timeout=3).strip()
            elapsed = time.monotonic() - start; durations.append(elapsed)
            worker.join(5)
            assert not worker.is_alive() and not failures, (name, failures)
            assert result == expected, (name, expected, result)
            assert elapsed < 1.5, (name, elapsed)
    print(json.dumps({'go_api_to_device': 'passed', 'http_cases': len(scenarios),
                      'maximum_http_test_seconds': round(max(durations), 3)}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, help='Keep results under artifacts/')
    args = parser.parse_args()
    if args.output:
        run(args.output)
    else:
        (ROOT / 'artifacts').mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='firmware-', dir=ROOT / 'artifacts') as directory:
            run(Path(directory))
