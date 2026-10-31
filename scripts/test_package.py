import io
import json
from pathlib import Path
import struct
import subprocess
import tarfile
import tempfile
import unittest
from package import PLATFORMS, checksums, name, source_snapshot, validate_version, verify
COMMIT='a'*40
VERSION='v0.1.0'

def make_archive(directory, arch='amd64', version=VERSION, commit=COMMIT, system='linux', extra=None, interpreter=False):
    root=name(version,system,arch)
    path=Path(directory)/(root+'.tar.gz')
    binary=bytearray(120)
    if system=='linux':
        binary[:6]=b'\x7fELF\x02\x01'
        struct.pack_into('<H',binary,18,62 if arch=='amd64' else 183)
        struct.pack_into('<Q',binary,32,64)
        struct.pack_into('<HH',binary,54,56,1)
        struct.pack_into('<I',binary,64,3 if interpreter else 1)
    else:
        binary[:4]=b'\xcf\xfa\xed\xfe'
        struct.pack_into('<I',binary,4,0x1000007 if arch=='amd64' else 0x100000c)
    files={'cloudflare-go':bytes(binary),'build-info.json':json.dumps({'version':version,'commit':commit,'os':system,'arch':arch}).encode()}
    for item in ['LICENSE','NOTICE','README.md','CHANGELOG.md','go.mod','go.sum','docs/CONFIGURATION.md','licenses/cloudflare_LICENSE','licenses/go_LICENSE']:
        files[item]=b'fixture'
    with tarfile.open(path,'w:gz') as archive:
        for item,data in files.items():
            entry=tarfile.TarInfo(root+'/'+item)
            entry.mode=0o755 if item=='cloudflare-go' else 0o644
            entry.size=len(data)
            archive.addfile(entry,io.BytesIO(data))
        if extra:archive.addfile(extra)
    return path

class PackageTest(unittest.TestCase):
    def test_snapshot_excludes_untracked_and_ignored_files(self):
        self.check_snapshot(dirty=False)

    def test_snapshot_rejects_wrong_revision_and_dirty_source(self):
        self.check_snapshot(dirty=True)

    def check_snapshot(self, dirty):
        import os
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as destination:
            source=Path(directory)
            subprocess.run(['git','init','-q',directory],check=True)
            (source/'docs').mkdir()
            (source/'docs'/'README.md').write_text('tracked documentation')
            (source/'.gitignore').write_text('*.token\n')
            subprocess.run(['git','-C',directory,'add','.'],check=True)
            subprocess.run(['git','-C',directory,'-c','user.name=Test','-c','user.email=test@example.invalid','commit','-qm','fixture'],check=True)
            commit=subprocess.check_output(['git','-C',directory,'rev-parse','HEAD'],text=True).strip()
            (source/'docs'/'private.token').write_text('fixture secret')
            (source/'docs'/'untracked.txt').write_text('local document')
            previous=Path.cwd()
            try:
                os.chdir(source)
                with self.assertRaises(ValueError):source_snapshot('f'*40,Path(destination))
                if dirty:
                    (source/'docs'/'README.md').write_text('modified')
                    with self.assertRaises(ValueError):source_snapshot(commit,Path(destination))
                else:
                    source_snapshot(commit,Path(destination))
                    self.assertEqual((Path(destination)/'docs'/'README.md').read_text(),'tracked documentation')
                    self.assertFalse((Path(destination)/'docs'/'private.token').exists())
                    self.assertFalse((Path(destination)/'docs'/'untracked.txt').exists())
            finally:
                os.chdir(previous)

    def test_version_validation(self):
        for tag in ['v0.1.0','v1.2.3-rc.1']:validate_version(tag)
        for tag in ['dev','1.2.3','v01.2.3','v1.2.3-01','v1.2.3\n','../escape']:
            with self.assertRaises(ValueError):validate_version(tag)
    def test_four_platforms_and_static_linux(self):
        with tempfile.TemporaryDirectory() as directory:
            for system,arch in PLATFORMS:
                path=make_archive(directory,arch,system=system)
                verify(path,VERSION,COMMIT,system,arch)
            self.assertEqual(len(checksums(directory,VERSION,COMMIT)),5)
            path=make_archive(directory,interpreter=True)
            with self.assertRaises(ValueError):verify(path,VERSION,COMMIT,'linux','amd64')
    def test_archive_paths_and_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            for item in ['../escape','/absolute',name(VERSION,'linux','amd64')+'/link']:
                entry=tarfile.TarInfo(item);entry.type=tarfile.SYMTYPE
                path=make_archive(directory,extra=entry)
                with self.assertRaises(ValueError):verify(path,VERSION,COMMIT,'linux','amd64')
            path=make_archive(directory)
            with self.assertRaises(ValueError):verify(path,VERSION,'b'*40,'linux','amd64')
            with self.assertRaises((FileNotFoundError,ValueError)):checksums(directory,VERSION,COMMIT)

if __name__=='__main__':unittest.main()
