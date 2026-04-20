#!/usr/bin/env python3
"""
Apple Mail disk-first reader. Scans ~/Library/Mail/V*/.../*.emlx directly,
bypassing AppleScript so large inboxes don't time out.

Usage:
  mail.py recent [--limit N] [--mailbox PATTERN]
  mail.py search <keyword> [--in subject|from|body|all] [--limit N] [--mailbox PATTERN]
  mail.py read <emlx_path>
  mail.py stats

Output: TSV (mtime\tfrom\tsubject\tpath) for list ops. `read` dumps the message.
"""

import argparse
import email
import email.header
import os
import sys
import time
from pathlib import Path

MAIL_ROOT = Path.home() / "Library" / "Mail"


def find_mail_version_dir():
    for p in sorted(MAIL_ROOT.glob("V*"), reverse=True):
        if p.is_dir():
            return p
    return None


def walk_emlx(mailbox_pattern=None):
    """Yield .emlx paths. If mailbox_pattern is set, filter by substring in the path."""
    root = find_mail_version_dir()
    if not root:
        return
    for dirpath, _, files in os.walk(root):
        if mailbox_pattern and mailbox_pattern.lower() not in dirpath.lower():
            continue
        for f in files:
            if f.endswith(".emlx") and not f.endswith(".partial.emlx"):
                yield os.path.join(dirpath, f)


def decode_header(val):
    if not val:
        return ""
    try:
        parts = email.header.decode_header(val)
        return "".join(
            (t.decode(enc or "utf-8", errors="replace") if isinstance(t, bytes) else t)
            for t, enc in parts
        )
    except Exception:
        return str(val)


def _read_emlx_body(path, max_bytes=None):
    """Read the MIME body of an emlx file (bounded by its first-line byte count).
    If max_bytes is set, truncate the read (for header-only parsing).
    """
    with open(path, "rb") as f:
        first = f.readline()  # "<byte-count>    \n"
        try:
            byte_count = int(first.strip())
        except ValueError:
            byte_count = None
        if max_bytes is not None:
            return f.read(max_bytes)
        if byte_count is not None:
            return f.read(byte_count)
        return f.read()


def parse_headers(path):
    """Parse only headers — 8KB is enough for any reasonable header block."""
    try:
        raw = _read_emlx_body(path, max_bytes=8192)
        return email.message_from_bytes(raw)
    except (OSError, ValueError):
        return None


def parse_full(path):
    """Parse full message including body. Stops at the emlx byte-count boundary,
    so the trailing Mail.app plist does NOT leak into the last MIME part."""
    try:
        raw = _read_emlx_body(path)
        return email.message_from_bytes(raw)
    except (OSError, ValueError):
        return None


def get_body_text(msg):
    """Return plain text body (first text/plain part, fallback to text/html stripped)."""
    if msg.is_multipart():
        for part in msg.walk():
            if part.get_content_type() == "text/plain":
                try:
                    return part.get_payload(decode=True).decode(
                        part.get_content_charset() or "utf-8", errors="replace"
                    )
                except Exception:
                    continue
        for part in msg.walk():
            if part.get_content_type() == "text/html":
                try:
                    html = part.get_payload(decode=True).decode(
                        part.get_content_charset() or "utf-8", errors="replace"
                    )
                    import re
                    return re.sub(r"<[^>]+>", " ", html)
                except Exception:
                    continue
        return ""
    try:
        return msg.get_payload(decode=True).decode(
            msg.get_content_charset() or "utf-8", errors="replace"
        )
    except Exception:
        return str(msg.get_payload())


def fmt_row(path, msg):
    mtime = time.strftime("%Y-%m-%d %H:%M", time.localtime(os.path.getmtime(path)))
    frm = decode_header(msg.get("From", ""))[:50]
    subj = decode_header(msg.get("Subject", ""))[:80]
    return f"{mtime}\t{frm}\t{subj}\t{path}"


def cmd_recent(args):
    paths = list(walk_emlx(args.mailbox))
    paths.sort(key=lambda p: os.path.getmtime(p), reverse=True)
    for p in paths[: args.limit]:
        msg = parse_headers(p)
        if msg:
            print(fmt_row(p, msg))


def cmd_search(args):
    keyword = args.keyword.lower()
    scope = args.scope
    results = []
    for p in walk_emlx(args.mailbox):
        msg = parse_headers(p) if scope != "body" and scope != "all" else parse_full(p)
        if not msg:
            continue
        hit = False
        if scope in ("subject", "all"):
            if keyword in decode_header(msg.get("Subject", "")).lower():
                hit = True
        if not hit and scope in ("from", "all"):
            if keyword in decode_header(msg.get("From", "")).lower():
                hit = True
        if not hit and scope in ("body", "all"):
            body = get_body_text(msg if msg.is_multipart() or msg.get_content_type() else msg)
            if keyword in body.lower():
                hit = True
        if hit:
            results.append((os.path.getmtime(p), p, msg))
            if len(results) >= args.limit:
                break
    results.sort(reverse=True)
    for _, p, msg in results:
        print(fmt_row(p, msg))


def cmd_read(args):
    if not os.path.exists(args.path):
        print(f"Error: file not found: {args.path}", file=sys.stderr)
        sys.exit(1)
    msg = parse_full(args.path)
    if not msg:
        print(f"Error: could not parse {args.path}", file=sys.stderr)
        sys.exit(1)
    print(f"From: {decode_header(msg.get('From', ''))}")
    print(f"To: {decode_header(msg.get('To', ''))}")
    print(f"Date: {msg.get('Date', '')}")
    print(f"Subject: {decode_header(msg.get('Subject', ''))}")
    print("---")
    body = get_body_text(msg)
    print(body.strip())


def cmd_stats(args):
    root = find_mail_version_dir()
    if not root:
        print("No Mail storage found.")
        return
    total = 0
    per_mbox = {}
    for dirpath, _, files in os.walk(root):
        cnt = sum(1 for f in files if f.endswith(".emlx"))
        if cnt == 0:
            continue
        total += cnt
        # Group by innermost .mbox ancestor (Gmail nests: [Gmail].mbox/All Mail.mbox/...)
        parts = Path(dirpath).parts
        mboxes = [p for p in parts if p.endswith(".mbox")]
        mbox = mboxes[-1] if mboxes else "(unknown)"
        per_mbox[mbox] = per_mbox.get(mbox, 0) + cnt
    print(f"Total: {total} messages across {len(per_mbox)} mailboxes")
    print(f"Root: {root}")
    print()
    for mbox, cnt in sorted(per_mbox.items(), key=lambda x: -x[1])[:20]:
        print(f"  {cnt:>6}  {mbox}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    r = sub.add_parser("recent", help="List most recent messages")
    r.add_argument("--limit", type=int, default=20)
    r.add_argument("--mailbox", default=None, help="Substring filter on mailbox path")
    r.set_defaults(func=cmd_recent)

    s = sub.add_parser("search", help="Search by keyword")
    s.add_argument("keyword")
    s.add_argument("--in", dest="scope", choices=["subject", "from", "body", "all"], default="subject")
    s.add_argument("--limit", type=int, default=50)
    s.add_argument("--mailbox", default=None)
    s.set_defaults(func=cmd_search)

    rd = sub.add_parser("read", help="Read a single emlx file")
    rd.add_argument("path")
    rd.set_defaults(func=cmd_read)

    st = sub.add_parser("stats", help="Mailbox message counts")
    st.set_defaults(func=cmd_stats)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
