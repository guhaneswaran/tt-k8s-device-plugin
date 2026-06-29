#!/usr/bin/env python3
"""In-pod Tenstorrent hardware verification.

Runs inside an allocated pod (which has /dev/tenstorrent/<id> + /sys mounted by
the device plugin) and asserts the card is healthy: telemetry within limits,
PCIe link not downtrained, firmware present, and tt-smi can talk to the device.

Exit code: 0 if all hard checks pass, 1 otherwise. Soft issues print as warnings
but do not fail. Env knobs:
  EXPECT_FW_BUNDLE   if set, require tt_fw_bundle_ver to equal it (hard fail)
  TEMP_WARN_FRAC     warn when temp >= this fraction of temp_max (default 0.90)
  REQUIRE_FULL_PCIE_SPEED  if "1", a downtrained link speed becomes a hard fail
"""
import glob
import os
import subprocess
import sys

_tty = sys.stdout.isatty()
def _c(code): return code if _tty else ""
G, R, Y, C, D, X = (_c("\033[32m"), _c("\033[31m"), _c("\033[33m"),
                    _c("\033[36m"), _c("\033[2m"), _c("\033[0m"))

fails, warns = [], []
def ok(m):   print(f"  {G}✓{X} {m}")
def bad(m):  print(f"  {R}✗{X} {m}"); fails.append(m)
def warn(m): print(f"  {Y}⚠{X} {m}"); warns.append(m)
def head(m): print(f"\n{C}== {m} =={X}")
def kv(k, v): print(f"    {D}{k:20}{X} {v}")

def read(path):
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return None

def read_int(path):
    v = read(path)
    try:
        return int(v)
    except (TypeError, ValueError):
        return None

# ---- locate the device ------------------------------------------------------
head("device")
classes = sorted(glob.glob("/sys/class/tenstorrent/tenstorrent!*"))
if not classes:
    bad("no /sys/class/tenstorrent/* (is /sys mounted and a device allocated?)")
    sys.exit(1)
S = classes[0]
ok(f"sysfs: {S}")
visible = os.environ.get("TT_VISIBLE_DEVICES", "(unset)")
kv("TT_VISIBLE_DEVICES", visible)
devs = glob.glob("/dev/tenstorrent/*")
ok(f"device node(s): {', '.join(sorted(devs)) or '(none!)'}")
if not devs:
    bad("no /dev/tenstorrent node in the container")

# ---- identity + firmware ----------------------------------------------------
head("identity / firmware")
for a in ("tt_card_type", "tt_serial", "tt_asic_id"):
    kv(a, read(os.path.join(S, a)))
fw_bundle = read(os.path.join(S, "tt_fw_bundle_ver"))
for a in ("tt_fw_bundle_ver", "tt_arc_fw_ver", "tt_eth_fw_ver", "tt_ttflash_ver"):
    kv(a, read(os.path.join(S, a)))
exp_fw = os.environ.get("EXPECT_FW_BUNDLE")
if exp_fw:
    if fw_bundle == exp_fw:
        ok(f"firmware bundle matches expected ({exp_fw})")
    else:
        bad(f"firmware bundle {fw_bundle!r} != expected {exp_fw!r}")
elif not fw_bundle:
    warn("tt_fw_bundle_ver not readable")

# ---- thermal / power telemetry (hwmon, with built-in _max limits) -----------
head("telemetry")
hwmons = glob.glob(os.path.join(S, "device", "hwmon", "hwmon*"))
if not hwmons:
    warn("no hwmon dir — skipping thermal/power checks")
else:
    HW = hwmons[0]
    temp = read_int(os.path.join(HW, "temp1_input"))
    temp_max = read_int(os.path.join(HW, "temp1_max"))
    if temp is not None and temp_max:
        frac = float(os.environ.get("TEMP_WARN_FRAC", "0.90"))
        c, cmax = temp / 1000.0, temp_max / 1000.0
        kv("asic temp", f"{c:.1f} C  (max {cmax:.0f} C)")
        if temp >= temp_max:
            bad(f"temperature {c:.1f}C >= max {cmax:.0f}C")
        elif temp >= temp_max * frac:
            warn(f"temperature {c:.1f}C >= {frac*100:.0f}% of max {cmax:.0f}C")
        else:
            ok(f"temperature OK ({c:.1f}C)")
    else:
        warn("temp1_input/temp1_max not readable")
    # voltage / current / power are informational
    for label, inp in (("vcore", "in0_input"), ("current", "curr1_input"),
                       ("power", "power1_input")):
        v = read_int(os.path.join(HW, inp))
        m = read_int(os.path.join(HW, inp.replace("input", "max")))
        if v is not None:
            kv(label, f"{v} (max {m})")

# ---- PCIe link health -------------------------------------------------------
head("pcie link")
pci = os.path.realpath(os.path.join(S, "device"))
cw = read_int(os.path.join(pci, "current_link_width"))
mw = read_int(os.path.join(pci, "max_link_width"))
cs = read(os.path.join(pci, "current_link_speed"))
ms = read(os.path.join(pci, "max_link_speed"))
kv("link width", f"{cw} (max {mw})")
kv("link speed", f"{cs}  (max {ms})")
if cw and mw:
    if cw < mw:
        bad(f"PCIe link width downtrained: x{cw} < x{mw}")
    else:
        ok(f"PCIe link width full (x{cw})")
if cs and ms:
    if cs != ms:
        msg = f"PCIe link speed below max: {cs} < {ms} (Gen3 slot or downtrain)"
        if os.environ.get("REQUIRE_FULL_PCIE_SPEED") == "1":
            bad(msg)
        else:
            warn(msg)
    else:
        ok(f"PCIe link speed full ({cs})")

# ---- heartbeat advancing (liveness) ----------------------------------------
head("heartbeat")
hb1 = read(os.path.join(S, "tt_heartbeat"))
if hb1 is None:
    warn("tt_heartbeat not readable")
else:
    import time
    time.sleep(2)
    hb2 = read(os.path.join(S, "tt_heartbeat"))
    kv("tt_heartbeat", f"{hb1} -> {hb2}")
    if hb1 == hb2:
        warn("heartbeat did not advance in 2s (ARC may be slow/stuck)")
    else:
        ok("heartbeat advancing")

# ---- tt-smi can talk to the device (functional) -----------------------------
head("tt-smi")
try:
    r = subprocess.run(["tt-smi", "-ls"], capture_output=True, text=True, timeout=60)
    print(D + "\n".join("    " + l for l in r.stdout.splitlines()) + X)
    if r.returncode == 0 and ("n150" in r.stdout.lower() or "board" in r.stdout.lower()):
        ok("tt-smi enumerated the board")
    else:
        bad(f"tt-smi -ls failed (rc={r.returncode})")
        if r.stderr:
            print(D + r.stderr + X)
except Exception as e:  # noqa: BLE001
    bad(f"tt-smi error: {e}")

# ---- summary ----------------------------------------------------------------
print()
if fails:
    print(f"{R}FAIL{X}: {len(fails)} hard issue(s), {len(warns)} warning(s)")
    sys.exit(1)
print(f"{G}PASS{X}: 0 hard issues, {len(warns)} warning(s)")
sys.exit(0)
