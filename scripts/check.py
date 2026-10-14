#!/usr/bin/env python3
"""Run local checks with synthetic data; never sign in, install services or flash."""
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'artifacts/checks'
OUT.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, GOCACHE=str(ROOT / '.cache/go-build'))


def run(*args):
    subprocess.run(list(map(str, args)), cwd=ROOT, env=env, check=True)


run('go', 'test', '-race', './...')
run('go', 'vet', './...')
for target in ('meter-collector', 'meter-api'):
    run('go', 'build', '-trimpath', '-o', OUT / target, './cmd/' + target)
run(sys.executable, '-m', 'unittest', 'discover', '-s', 'tests/desktop')
run('c++', '-std=c++17', '-Wall', '-Wextra', '-Werror', '-g',
    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-I', ROOT,
    ROOT / 'tests/display/native.cpp', '-o', OUT / 'display-check')
run(OUT / 'display-check', ROOT / 'contracts/v3/fixtures/normal.json', OUT / 'screens')
run(sys.executable, 'tests/firmware/check.py', '--output', OUT / 'firmware')
