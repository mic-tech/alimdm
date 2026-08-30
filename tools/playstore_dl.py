#!/usr/bin/env python3
"""
Download a free Play Store APK by package ID using the Play Store
batchexecute API (the same method apkeep/sugarcane use; only needs requests).

Usage: playstore_dl.py <package_id> <output.apk>
"""
import sys, json, re
import requests

UA = ("Mozilla/5.0 (Linux; Android 13; Pixel 6) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36")
MAGIC = ")]}'"

def batchexecute(payload):
    url = "https://play.google.com/_/PlayStoreUi/data/batchexecute"
    body = MAGIC + "\n" + json.dumps([payload])
    r = requests.post(url, data=body, headers={
        "User-Agent": UA,
        "Content-Type": "application/x-www-form-urlencoded;charset=utf-8",
        "X-Goog-Page-Client": "gaea/20240101.01.00",
    }, timeout=30)
    txt = r.text
    idx = txt.find(MAGIC)
    if idx != -1:
        txt = txt[idx + len(MAGIC):].lstrip("\n")
    return json.loads(txt)

def get_apk_url(package_id):
    # Mirror the Play Store web client's install-data request.
    inner = {
        "f": 1, "i": package_id, "a": "", "c": 1, "d": 0, "e": 0,
        "g": 0, "h": 0, "j": 0, "k": 0, "l": 0, "m": 0, "n": 0,
        "o": 0, "p": 0, "q": 0, "r": 0, "s": 0, "t": 0, "u": 0,
        "v": 0, "w": 0, "x": 0, "y": 0, "z": 0,
    }
    payload = [None, None, None, None, None, None, None, None, None, json.dumps(inner)]
    res = batchexecute(payload)
    blob = json.dumps(res)
    m = re.search(r'(https://[^"]*\.apk[^"]*)', blob)
    if m:
        return m.group(1)
    m = re.search(r'"(https://play\.google\.com/install[^"]*)"', blob)
    if m:
        return m.group(1)
    return None

def main():
    if len(sys.argv) < 3:
        print("usage: playstore_dl.py <package_id> <output.apk>", file=sys.stderr)
        sys.exit(2)
    pkg, out = sys.argv[1], sys.argv[2]
    print("[*] requesting install data for " + pkg + " ...")
    url = get_apk_url(pkg)
    if not url:
        print("[!] no APK URL found (app may be paid / region-locked / removed)", file=sys.stderr)
        sys.exit(1)
    print("[*] APK URL: " + url[:120] + "...")
    r = requests.get(url, headers={"User-Agent": UA}, stream=True, timeout=180)
    print("[*] HTTP " + str(r.status_code) + " type=" + str(r.headers.get("Content-Type"))
          + " size=" + str(r.headers.get("Content-Length")))
    if r.status_code != 200:
        print("[!] download failed", file=sys.stderr)
        sys.exit(1)
    with open(out, "wb") as f:
        for chunk in r.iter_content(1024 * 1024):
            f.write(chunk)
    print("[+] saved " + out)

if __name__ == "__main__":
    main()
