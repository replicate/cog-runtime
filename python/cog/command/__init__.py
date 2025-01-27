import os
import os.path
import platform
import sys

# Lightweight wrapper from `python3 -m cog.command.openapi_schema` to cog-schema

goos = platform.system().lower()
if goos not in {'linux', 'darwin'}:
    print(f'Unsupported OS: {goos}')
    sys.exit(1)

goarch = platform.machine().lower()
if goarch == 'x86_64':
    goarch = 'amd64'
elif goarch == 'aarch64':
    goarch = 'arm64'
if goarch not in {'amd64', 'arm64'}:
    print(f'Unsupported architecture: {goarch}')
    sys.exit(1)

# Binaries are bundled in the same path as this file
cmd = f'cog-schema-{goos}-{goarch}'
exe = os.path.join(os.path.dirname(__file__), cmd)

args = sys.argv
args[0] = exe

os.execv(exe, args)
