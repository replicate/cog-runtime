import os
import tempfile

from cog import Path

BASE_UID = 9000
NOGROUP_GID = 65534


def predict(p: Path) -> Path:
    uid = os.getuid()
    gid = os.getgid()
    print(f'UID={uid}')
    print(f'GID={gid}')
    assert uid >= BASE_UID
    assert gid == NOGROUP_GID

    # CWD is a copy of the procedure source
    # where all directories and files are owned by UID/GID
    cwd = os.getcwd()
    print(f'CWD={cwd}')
    stat = os.stat(cwd)
    assert stat.st_uid == uid
    assert stat.st_gid == gid

    with open('out.txt', 'w') as f:
        print(f'writing to file: {f.name}')
        f.write('out')

    tmpdir = os.environ.get('TMPDIR')
    print(f'TMPDIR={tmpdir}')
    assert tmpdir is not None
    assert tmpdir.startswith('/tmp/cog-runner-tmp-')
    stat = os.stat(tmpdir)
    assert stat.st_uid == uid
    assert stat.st_gid == gid

    with tempfile.NamedTemporaryFile(mode='w+') as f:
        print(f'writing to file: {f.name}')
        f.write('out')

    with p.open() as fin:
        with open('out.txt', 'w') as fout:
            fout.write(fin.read())

    return Path('out.txt')
