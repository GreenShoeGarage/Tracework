#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Package the current source, vendored dependencies, assets and platform binaries."""
import hashlib,json,stat,zipfile,os,datetime
from pathlib import Path
root=Path(__file__).resolve().parents[1]
version=json.loads((root/'package.json').read_text())['version']
exclude={'.git','__pycache__','node_modules','target','tracework-data','.DS_Store'}
files=sorted(p for p in root.rglob('*') if p.is_file() and not any(x in exclude for x in p.relative_to(root).parts) and p.name!='SHA256SUMS' and not p.name.endswith('.pyc'))
(root/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(root).as_posix()+'\n' for p in files))
files.append(root/'SHA256SUMS')
out=root.parent/f'TRACEWORK-v{version}-github-ready.zip'
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
    # Registry archives may have pre-1980 mtimes. Normalize ZIP time and modes,
    # making the archive reproducible for identical inputs and release epoch.
    stamp=datetime.datetime.fromtimestamp(int(os.getenv('SOURCE_DATE_EPOCH','1790553600')),datetime.timezone.utc)
    if stamp.year<1980:raise ValueError('SOURCE_DATE_EPOCH must be 1980 or later')
    for p in files:
        info=zipfile.ZipInfo('tracework/'+p.relative_to(root).as_posix(),stamp.timetuple()[:6])
        info.create_system=3
        info.external_attr=(stat.S_IFREG | (0o755 if p.stat().st_mode & 0o111 else 0o644))<<16
        z.writestr(info,p.read_bytes(),compress_type=zipfile.ZIP_DEFLATED,compresslevel=6)
with zipfile.ZipFile(out) as z:
    assert z.testzip() is None
    for line in z.read('tracework/SHA256SUMS').decode().splitlines():
        expected,name=line.split('  ',1);assert hashlib.sha256(z.read('tracework/'+name)).hexdigest()==expected,name
print(json.dumps({'path':str(out),'files':len(files),'bytes':out.stat().st_size,'sha256':hashlib.sha256(out.read_bytes()).hexdigest(),'zip_integrity':True,'all_file_hashes_verified':True},indent=2))
