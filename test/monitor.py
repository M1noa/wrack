#!/usr/bin/env python3
# live monitor via curl (python's TLS fp gets 403'd by cloudflare)
import json, subprocess, sys, time

TOKEN = open("tokens.txt").read().strip()
GUILD = sys.argv[1] if len(sys.argv) > 1 else "1314639419973828709"
BASE = f"https://discord.com/api/v10/guilds/{GUILD}"

def get(path):
    out = subprocess.run(
        ["curl", "-s", "-m", "5", "-H", f"Authorization: Bot {TOKEN}", BASE + path],
        capture_output=True, text=True).stdout
    try:
        return json.loads(out)
    except Exception:
        return None

def snap():
    s = {}
    for key, path in [("chan", "/channels"), ("role", "/roles"), ("emoji", "/emojis"),
                      ("stkr", "/stickers")]:
        d = get(path)
        s[key] = len(d) if isinstance(d, list) else -1
    d = get("/soundboard-sounds")
    s["snd"] = len(d.get("items", [])) if isinstance(d, dict) else -1
    return s

print(f"monitoring guild {GUILD} (ctrl-c to stop)", flush=True)
print(f"{'time':>8}  {'chan':>5}  {'role':>5}  {'emoji':>5}  {'stkr':>4}  {'snd':>3}", flush=True)
start = time.time()
try:
    while True:
        s = snap()
        el = round(time.time() - start, 1)
        print(f"{el:>8}  {s['chan']:>5}  {s['role']:>5}  {s['emoji']:>5}  {s['stkr']:>4}  {s['snd']:>3}", flush=True)
        time.sleep(0.5)
except KeyboardInterrupt:
    pass
