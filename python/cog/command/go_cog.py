import os
import os.path
import platform
import sys
from pathlib import Path
from typing import List


def run(subcmd: str, args: List[str]) -> None:
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

    # Binaries are bundled in python/cog
    cmd = f'cog-{goos}-{goarch}'
    exe = os.path.join(Path(__file__).parent.parent, cmd)
    args = [exe, subcmd] + args

    env = os.environ.copy()

    # Replicate Go logger logs to stdout in production mode
    # Use stderr instead to be consistent with legacy Cog
    if 'LOG_FILE' not in env:
        env['LOG_FILE'] = 'stderr'

    # NOTE: Secrets are explicitly *not* expected in the default environment and are
    # instead loaded from an 'envdir'-style path so that there is slightly less risk of
    # accidental secrets leakage into child processes. This loading process must take
    # place here in the wrapper level because there are `go` libraries that expect to take
    # action based on the environment at process load time.
    secrets_env_path = env.get('COG_RUNTIME_SECRETS_ENV')
    if secrets_env_path is not None:
        env |= load_envdir(secrets_env_path)

    os.execve(exe, args, env)


def load_envdir(env_path: str) -> dict[str, str]:
    """This is a mini / more brittle version of https://github.com/jezdez/envdir"""
    return {
        str(e.name).strip(): str(e.read_text(encoding='utf-8', errors='ignore')).strip()
        for e in Path(env_path).absolute().glob("*")
        if e.is_file() or e.is_symlink()
    }
