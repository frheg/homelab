"""Downloads the models listed in models.txt into a directory and checks each file's sha256 (build step)."""
import hashlib
import os
import sys
import urllib.request

listing, out = sys.argv[1], sys.argv[2]
os.makedirs(out, exist_ok=True)
for line in open(listing, encoding="utf-8"):
    if not line.strip() or line.startswith("#"):
        continue
    name, want, url = line.split()
    data = urllib.request.urlopen(url, timeout=300).read()
    got = hashlib.sha256(data).hexdigest()
    if got != want:
        sys.exit(f"{name}: sha256 {got}, want {want}")
    with open(os.path.join(out, name), "wb") as f:
        f.write(data)
    print(f"{name}: {len(data)} bytes, ok")
