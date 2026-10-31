#!/usr/bin/env python3
"""Build deterministic native/cross-platform archives and validate their contents."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import struct
import subprocess
import tarfile
import tempfile

PLATFORMS = [('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'), ('darwin', 'arm64')]
VERSION = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?')

def validate_version(version, dev=False):
    if dev and version == 'dev':
        return
    match = VERSION.fullmatch(version)
    if not match or (match[4] and any(x.isdigit() and len(x) > 1 and x[0] == '0' for x in match[4].split('.'))):
        raise ValueError('version must be vMAJOR.MINOR.PATCH with optional prerelease')

def commit_valid(commit):
    if not re.fullmatch('[0-9a-f]{40}', commit):
        raise ValueError('commit must be a full Git SHA')

def sha256(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()

def name(version, system, arch):
    validate_version(version, True)
    if (system, arch) not in PLATFORMS:
        raise ValueError('unsupported platform')
    return f'cloudflare-go_{version}_{system}_{arch}'

def verify(path, version, commit, system, arch):
    commit_valid(commit)
    root = name(version, system, arch)
    if Path(path).name != root + '.tar.gz':
        raise ValueError('archive name mismatch')
    required = {'cloudflare-go', 'build-info.json', 'LICENSE', 'NOTICE', 'README.md', 'CHANGELOG.md',
                'go.mod', 'go.sum', 'docs/CONFIGURATION.md', 'licenses/cloudflare_LICENSE', 'licenses/go_LICENSE'}
    with tarfile.open(path, 'r:gz') as archive:
        files = set()
        total = 0
        for member in archive:
            parts = PurePosixPath(member.name)
            if parts.is_absolute() or '..' in parts.parts or not parts.parts or parts.parts[0] != root or str(parts) != member.name or member.name in files:
                raise ValueError('unsafe archive path')
            files.add(member.name)
            if not member.isfile() or member.uid or member.gid or member.mode != (0o755 if member.name == root + '/cloudflare-go' else 0o644):
                raise ValueError('unexpected archive entry or permissions')
            total += member.size
            if total > 128 << 20:
                raise ValueError('archive exceeds size bound')
        if not {root + '/' + x for x in required}.issubset(files):
            raise ValueError('archive missing required files')
        info = json.load(archive.extractfile(root + '/build-info.json'))
        if any(info.get(k) != v for k, v in {'version': version, 'commit': commit, 'os': system, 'arch': arch}.items()):
            raise ValueError('archive metadata mismatch')
        binary = archive.extractfile(root + '/cloudflare-go').read()
        if system == 'linux':
            if len(binary) < 64 or binary[:6] != b'\x7fELF\x02\x01' or struct.unpack_from('<H', binary, 18)[0] != {'amd64':62,'arm64':183}[arch]:
                raise ValueError('ELF architecture mismatch')
            offset = struct.unpack_from('<Q', binary, 32)[0]
            size, count = struct.unpack_from('<HH', binary, 54)
            if size < 56 or offset + size*count > len(binary):
                raise ValueError('invalid ELF headers')
            for index in range(count):
                if struct.unpack_from('<I', binary, offset+index*size)[0] == 3:
                    raise ValueError('Linux binary has a dynamic loader')
        elif len(binary) < 32 or binary[:4] != b'\xcf\xfa\xed\xfe' or struct.unpack_from('<I', binary, 4)[0] != {'amd64':0x1000007,'arm64':0x100000c}[arch]:
            raise ValueError('Mach-O architecture mismatch')

def checksums(directory, version, commit):
    paths = []
    for system, arch in PLATFORMS:
        path = Path(directory) / (name(version, system, arch) + '.tar.gz')
        verify(path, version, commit, system, arch)
        paths.append(path)
    if set(Path(directory).glob('*.tar.gz')) != set(paths):
        raise ValueError('unexpected archives in distribution')
    target = Path(directory) / 'checksums.txt'
    target.write_text(''.join(f'{sha256(p)}  {p.name}\n' for p in sorted(paths)), encoding='utf-8')
    return paths + [target]

def build(version, commit, system, arch, output):
    validate_version(version, True)
    commit_valid(commit)
    root = name(version, system, arch)
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    epoch = int(subprocess.check_output(['git', 'show', '-s', '--format=%ct', commit]))
    env = os.environ | {'CGO_ENABLED':'0', 'GOOS':system, 'GOARCH':arch, 'GOAMD64':'v1', 'GOARM64':'v8.0'}
    with tempfile.TemporaryDirectory(dir=output, prefix='.package-') as work:
        directory = Path(work) / root
        directory.mkdir()
        for item in ['LICENSE','NOTICE','README.md','CHANGELOG.md','go.mod','go.sum']:
            shutil.copyfile(item, directory/item)
        shutil.copytree('docs', directory/'docs')
        shutil.copytree('examples', directory/'examples')
        flags = f'-s -w -X github.com/gatework/cloudflare-go/internal/cli.Version={version} -X github.com/gatework/cloudflare-go/internal/cli.Commit={commit}'
        subprocess.run(['go','build','-mod=readonly','-trimpath','-buildvcs=false','-ldflags',flags,'-o',str(directory/'cloudflare-go'),'./cmd/cloudflare-go'], env=env, check=True)
        licenses = directory/'licenses'
        licenses.mkdir()
        goroot = Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
        shutil.copyfile(goroot/'LICENSE', licenses/'go_LICENSE')
        modules = subprocess.check_output(['go','list','-mod=readonly','-deps','-f','{{with .Module}}{{.Path}} {{.Dir}}{{end}}','./cmd/cloudflare-go'], env=env,text=True)
        for item in sorted(set(modules.splitlines())):
            if not item.strip():
                continue
            module, source = item.split(' ', 1)
            if module == 'github.com/gatework/cloudflare-go':
                continue
            label = 'cloudflare' if module == 'github.com/cloudflare/cloudflare-go/v7' else module.replace('/','_')
            found = False
            for candidate in ['LICENSE','LICENSE.txt','LICENSE.md','COPYING']:
                file = Path(source)/candidate
                if file.is_file():
                    shutil.copyfile(file, licenses/(label+'_'+candidate))
                    found = True
            if not found:
                raise ValueError(f'module license missing: {module}')
        (directory/'build-info.json').write_text(json.dumps({'version':version,'commit':commit,'os':system,'arch':arch,'go':subprocess.check_output(['go','env','GOVERSION'],text=True).strip(),'source_date_epoch':epoch},indent=2)+'\n')
        path = Path(work)/(root+'.tar.gz')
        with path.open('wb') as stream, gzip.GzipFile(filename='',mode='wb',fileobj=stream,mtime=0) as compressed, tarfile.open(fileobj=compressed,mode='w',format=tarfile.GNU_FORMAT) as archive:
            for file in sorted(directory.rglob('*')):
                if not file.is_file():
                    continue
                data = file.read_bytes()
                entry = tarfile.TarInfo(root+'/'+file.relative_to(directory).as_posix())
                entry.mode = 0o755 if file.name == 'cloudflare-go' else 0o644
                entry.mtime = epoch
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
        verify(path,version,commit,system,arch)
        shutil.move(path,output/path.name)
        print(output/path.name)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    sub=parser.add_subparsers(dest='command',required=True)
    for command in ['build','verify']:
        p=sub.add_parser(command)
        if command=='verify':p.add_argument('path')
        p.add_argument('version');p.add_argument('commit');p.add_argument('system');p.add_argument('arch')
        if command=='build':p.add_argument('--output',default='dist')
    p=sub.add_parser('checksums');p.add_argument('directory');p.add_argument('version');p.add_argument('commit')
    args=parser.parse_args()
    if args.command=='build':build(args.version,args.commit,args.system,args.arch,args.output)
    elif args.command=='verify':verify(args.path,args.version,args.commit,args.system,args.arch)
    else:checksums(args.directory,args.version,args.commit)

if __name__=='__main__':main()
