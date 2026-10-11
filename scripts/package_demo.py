#!/usr/bin/env python3
"""Reproducible Linux deployment archives, without host timestamps/xattrs."""
import gzip
import io
import pathlib
import sys
import tarfile
import zipfile

kind, source_name, output_name = sys.argv[1:]
source, output = pathlib.Path(source_name), pathlib.Path(output_name)
if kind == 'zip':
    info = zipfile.ZipInfo('bootstrap', date_time=(1980, 1, 1, 0, 0, 0))
    info.create_system = 3
    info.external_attr = 0o100755 << 16
    info.compress_type = zipfile.ZIP_DEFLATED
    with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        archive.writestr(info, source.read_bytes())
elif kind == 'tar':
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w', format=tarfile.USTAR_FORMAT) as archive:
        for path in sorted(source.iterdir()):
            data = path.read_bytes()
            info = tarfile.TarInfo(path.name)
            info.size = len(data)
            info.mode = 0o755 if path.name == 'continuum-api' else 0o644
            info.mtime = 0
            archive.addfile(info, io.BytesIO(data))
    with output.open('wb') as raw:
        with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0) as archive:
            archive.write(buffer.getvalue())
else:
    raise SystemExit('Use zip or tar')
