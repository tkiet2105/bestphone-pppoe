#!/usr/bin/env python3
"""
reconcile-routing.py — CỨU NGAY: khôi phục policy-routing egress cho các line
PPPoE bị IP-drift, KHÔNG cần restart service.

Mirror đúng logic ApplyReplyRouting (internal/pppoe/routing.go):
  table T = 1000 + ppp_unit
  ip route replace default dev pppN table T
  ip rule add from <liveIP> table T
  iptables -t mangle -A OUTPUT -o pppN -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu

Chỉ áp cho line: iface UP + liveIP là IP public + (IP drift HOẶC routing chưa healthy).
Line chỉ có IP link-local/CGNAT → BỎ QUA (cần redial; binary mới sẽ tự demote/reconnect).

Dùng:
  sudo python3 reconcile-routing.py            # áp thật
  sudo python3 reconcile-routing.py --dry-run  # chỉ in, không đổi gì
"""
import argparse, ipaddress, subprocess, sqlite3, sys

DB = "/var/lib/bestphone-pppoe/data.db"
POLICY_TABLE_BASE = 1000


def sh(*args):
    return subprocess.run(args, capture_output=True, text=True)


def iface_ipv4(iface):
    r = sh("ip", "-4", "-o", "addr", "show", iface)
    for line in r.stdout.split("\n"):
        f = line.split()
        for i, w in enumerate(f):
            if w == "inet" and i + 1 < len(f):
                return f[i + 1].split("/")[0]
    return ""


def iface_up(iface):
    # state UP/UNKNOWN = up (ppp báo UNKNOWN khi chạy). KHÔNG dựa <POINTOPOINT> —
    # cờ đó còn cả khi state DOWN (zombie). state DOWN → coi là DOWN (cần redial).
    r = sh("ip", "-o", "link", "show", iface)
    if "state DOWN" in r.stdout:
        return False
    return ("state UP" in r.stdout) or ("state UNKNOWN" in r.stdout)


def is_blocked(ip):
    if not ip:
        return True
    try:
        a = ipaddress.ip_address(ip)
    except ValueError:
        return True
    return a.is_private or a.is_link_local or a.is_loopback  # RFC1918 / 169.254 / 127 ; 100.64/10 dưới
def is_cgnat(ip):
    try:
        a = ipaddress.ip_address(ip)
    except ValueError:
        return False
    return a in ipaddress.ip_network("100.64.0.0/10")


def rule_source_ips():
    r = sh("ip", "rule", "show")
    res = set()
    for line in r.stdout.split("\n"):
        f = line.split()
        for i, w in enumerate(f):
            if w == "from" and i + 1 < len(f) and f[i + 1] != "all":
                res.add(f[i + 1])
    return res


def tables_with_default_dev():
    r = sh("ip", "route", "show", "table", "all")
    res = {}
    for line in r.stdout.split("\n"):
        f = line.split()
        if len(f) < 4 or f[0] != "default":
            continue
        iface = tbl = None
        for i, w in enumerate(f):
            if w == "dev" and i + 1 < len(f):
                iface = f[i + 1]
            if w == "table" and i + 1 < len(f):
                try:
                    tbl = int(f[i + 1])
                except ValueError:
                    pass
        if iface and tbl is not None:
            res[tbl] = iface
    return res


def mss_rule(action, iface):
    return ["iptables", "-t", "mangle", action, "OUTPUT", "-o", iface,
            "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu"]


def apply_routing(iface, ip, unit, dry):
    t = str(POLICY_TABLE_BASE + unit)
    cmds = []
    # RemoveReplyRouting: dọn rule/table cũ (idempotent)
    for _ in range(16):
        cmds.append(("rule-del", ["ip", "rule", "del", "table", t], True))
    cmds.append(("flush", ["ip", "route", "flush", "table", t], True))
    for _ in range(8):
        cmds.append(("mss-del", mss_rule("-D", iface), True))
    # ApplyReplyRouting
    cmds.append(("route", ["ip", "route", "replace", "default", "dev", iface, "table", t], False))
    cmds.append(("rule-add", ["ip", "rule", "add", "from", ip, "table", t], False))
    cmds.append(("mss-del2", mss_rule("-D", iface), True))
    cmds.append(("mss-add", mss_rule("-A", iface), False))
    if dry:
        return True, ""
    for name, cmd, ignore in cmds:
        r = sh(*cmd)
        if r.returncode != 0 and not ignore:
            return False, f"{name}: {r.stderr.strip()}"
        if name == "rule-del" and r.returncode != 0:
            # hết rule để xoá → dừng vòng del sớm (không lỗi)
            pass
    return True, ""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--db", default=DB)
    args = ap.parse_args()

    con = sqlite3.connect(args.db, timeout=10)
    con.execute("PRAGMA busy_timeout=10000")
    rows = con.execute(
        "SELECT id, iface, ip, public_ip, ppp_unit FROM sessions WHERE status='connected' AND iface<>''"
    ).fetchall()

    rules = rule_source_ips()
    tbls = tables_with_default_dev()

    healed = healthy = down = blocked = drift = failed = 0
    blocked_list = []
    for sid, iface, dbip, pubip, unit in rows:
        if not iface_up(iface):
            down += 1
            continue
        live = iface_ipv4(iface)
        if is_blocked(live) or is_cgnat(live):
            blocked += 1
            blocked_list.append((iface, live or "(no-ip)"))
            continue
        healthy_now = (live in rules) and (tbls.get(POLICY_TABLE_BASE + unit) == iface)
        if live == dbip and healthy_now:
            healthy += 1
            continue
        if live != dbip:
            drift += 1
        ok, err = apply_routing(iface, live, unit, args.dry_run)
        if not ok:
            failed += 1
            print(f"  [FAIL] sess#{sid} {iface} ip={live}: {err}")
            continue
        healed += 1
        tag = "DRY" if args.dry_run else "FIXED"
        note = f"drift {dbip}→{live}" if live != dbip else f"routing missing (ip={live})"
        print(f"  [{tag}] sess#{sid} {iface} unit={unit} table={POLICY_TABLE_BASE+unit}  {note}")
        if not args.dry_run and (live != dbip or pubip != live):
            try:
                con.execute("UPDATE sessions SET ip=?, public_ip=? WHERE id=?", (live, live, sid))
                con.commit()
            except sqlite3.OperationalError as e:
                print(f"      (DB update bỏ qua, app đang giữ lock: {e})")

    con.close()
    print("\n=== TÓM TẮT ===")
    print(f"  Tổng connected      : {len(rows)}")
    print(f"  Đã khỏe sẵn          : {healthy}")
    print(f"  {'Sẽ sửa (dry)' if args.dry_run else 'Đã CỨU (egress phục hồi)':<20}: {healed}  (trong đó IP drift: {drift})")
    print(f"  Bỏ qua iface DOWN    : {down}")
    print(f"  Bỏ qua link-local/CGNAT (cần redial): {blocked}")
    if failed:
        print(f"  LỖI                  : {failed}")
    if blocked_list:
        print("  --- line cần redial (ISP không cấp IP public) ---")
        for iface, ip in blocked_list[:60]:
            print(f"      {iface}: {ip}")
    if args.dry_run:
        print("\n(dry-run — chưa đổi gì. Bỏ --dry-run để áp thật.)")


if __name__ == "__main__":
    if "--dry-run" not in sys.argv:
        print(">>> ÁP THẬT vào kernel routing + DB. Ctrl-C trong 2s để huỷ...")
    main()
