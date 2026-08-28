"""Turn a directory of hand-written markdown into an exporter-shaped `reference`
bundle that `kbimport` accepts.

Hand-written docs (PM knowledge, UIUX notes, RFCs) carry none of the eight
fields ValidateCorpus requires (internal/kb/domain.go), and their section sizes
are unbounded. This script does the deterministic half: metadata, ids, index,
and refusing malformed input. It deliberately does NOT split oversized
sections -- how to split depends on the shape of the content, which is a
judgement call the caller makes after reading this script's report.

Usage:
  python convert_reference_bundle.py --src <dir> --report-only
  python convert_reference_bundle.py --src <dir> --out <bundle> \
      --team Store.POS --product POS --owner pm-reference --id-prefix pm

Re-running is idempotent: rows whose id starts with "<team>--<prefix>-" are
dropped from kb_index.json before the new ones are appended.
"""
import argparse
import datetime
import hashlib
import json
import os
import re
import sys

FRONT_MATTER = re.compile(r"^---\n(.*?)\n---\n", re.S)
H1 = re.compile(r"(?m)^# .+$")
TITLE = re.compile(r"(?m)^#*\s*title:\s*(.+)$")
# Mirrors MarkdownRepo.Parse: any heading level starts a new section.
HEADING = re.compile(r"(?m)^(#{1,6})\s+(.*)$")


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--src", required=True, help="directory of hand-written *.md")
    parser.add_argument("--out", help="bundle root to write (omit with --report-only)")
    parser.add_argument("--team", default="Store.POS")
    parser.add_argument("--product", default="POS")
    parser.add_argument("--owner", default="reference")
    parser.add_argument("--id-prefix", default="ref",
                        help="id becomes <team>--<prefix>-<n>-<hash>")
    parser.add_argument("--doc-type", default="reference",
                        help="must be accepted by ClassifySection (internal/kb/domain.go)")
    parser.add_argument("--access-level", default="internal")
    parser.add_argument("--version", default=None, help="ISO8601; defaults to today")
    parser.add_argument("--max-section-chars", type=int, default=8000,
                        help="sections above this are reported, never split")
    parser.add_argument("--overrides",
                        help="directory of restructured replacements; a file here "
                             "with the same name is read instead of the one in --src")
    parser.add_argument("--report-only", action="store_true",
                        help="measure and report; write nothing")
    args = parser.parse_args(argv)
    if not args.report_only and not args.out:
        parser.error("--out is required unless --report-only")
    return args


def slug(name, prefix):
    """Stable id stem.

    Slugging a Chinese filename leaves almost nothing -- "07_次世代架構現況-菜單體系"
    reduces to "07" -- so a whole directory collapses onto a handful of stems.
    Uniqueness therefore rides on a content hash, with the leading number kept
    only so a human can still recognise the file.
    """
    base = re.sub(r"\.md$", "", name)
    lead = re.match(r"^(\d+)_", base)
    stem = re.sub(r"[^0-9a-z]+", "-", base.lower()).strip("-")
    digest = hashlib.sha256(base.encode("utf-8")).hexdigest()[:8]
    head = lead.group(1) if lead else (stem[:6] or "doc")
    return "%s-%s-%s" % (prefix, head, digest)


def split_front_matter(name, raw):
    """Return (front_matter_text, body, warning), refusing input it cannot read.

    Three shapes occur in practice. A well-formed fence is stripped normally.
    A file with no fence at all is all body. The dangerous one is a fence that
    is opened and never closed -- seen in the wild alongside "## date:" instead
    of "date:" -- because falling through to body=raw silently indexes
    transcript paths and related-doc lists as knowledge, and turns a metadata
    line into a section heading. Recover the boundary at the first level-1
    heading, and refuse outright when there is none rather than guess.
    """
    match = FRONT_MATTER.match(raw)
    if match:
        return match.group(1), raw[match.end():], None
    if not raw.startswith("---"):
        return "", raw, None
    heading = H1.search(raw)
    if not heading:
        sys.exit("FATAL %s: front matter fence is not closed and there is no "
                 "level-1 heading to recover the boundary from" % name)
    return (raw[:heading.start()], raw[heading.start():],
            "unterminated front matter, recovered at first h1")


def measure(body):
    """Section sizes as MarkdownRepo.Parse would see them."""
    parts = HEADING.split(body)
    return [(parts[i + 1].strip(), len(parts[i + 2].strip()))
            for i in range(1, len(parts), 3)]


def front_matter_for(args, doc_id, name, title, version):
    # Keep the source directory in source_bundle ("knowledge/foo.md", not
    # "foo.md"): the bundle ends up merged with other sources, and the bare
    # filename stops saying which tree it came from.
    name = "%s/%s" % (os.path.basename(os.path.normpath(args.src)), name)
    return (
        "---\n"
        'id: "%s"\n' % doc_id +
        'team: "%s"\n' % args.team +
        'product: "%s"\n' % args.product +
        'doc_type: "%s"\n' % args.doc_type +
        'version: "%s"\n' % version +
        'access_level: "%s"\n' % args.access_level +
        'owner: "%s"\n' % args.owner +
        'last_reviewed: "%s"\n' % version[:10] +
        'source_bundle: "%s"\n' % name +
        'title: "%s"\n' % title +
        "---\n\n"
    )


def write_index(args, docs):
    idx_path = os.path.join(args.out, "kb_index.json")
    index = {"docs": []}
    if os.path.isfile(idx_path):
        with open(idx_path, encoding="utf-8") as handle:
            index = json.load(handle)
    mine = "%s--%s-" % (args.team, args.id_prefix)
    index["docs"] = [d for d in index.get("docs", []) if not d["id"].startswith(mine)]
    have = {d["id"] for d in index["docs"]}
    for doc in docs:
        if doc["id"] in have:
            sys.exit("FATAL id collision with an existing bundle row: %s" % doc["id"])
    index["docs"].extend(docs)
    with open(idx_path, "w", encoding="utf-8") as out:
        json.dump(index, out, ensure_ascii=False, indent=2)
    return len(index["docs"])


def main(argv=None):
    sys.stdout.reconfigure(encoding="utf-8")
    args = parse_args(argv)
    version = args.version or (datetime.date.today().isoformat() + "T00:00:00+08:00")

    names = sorted(n for n in os.listdir(args.src) if n.endswith(".md"))
    if not names:
        sys.exit("FATAL %s: no *.md files" % args.src)
    if not args.report_only:
        os.makedirs(os.path.join(args.out, args.doc_type), exist_ok=True)

    docs, warnings, oversized, total, overridden = [], [], [], 0, 0
    for name in names:
        # Splitting an oversized section is hand work, and regenerating from
        # --src would silently throw it away. Keeping the restructured file in
        # --overrides makes every later run reproduce it instead.
        path = os.path.join(args.src, name)
        if args.overrides and os.path.isfile(os.path.join(args.overrides, name)):
            path = os.path.join(args.overrides, name)
            overridden += 1
        with open(path, encoding="utf-8") as handle:
            raw = handle.read().replace("\r\n", "\n")
        front_text, body, warning = split_front_matter(name, raw)
        if warning:
            warnings.append("%s: %s" % (name, warning))
        title_hit = TITLE.search(front_text)
        title = title_hit.group(1).strip() if title_hit else ""

        sizes = measure(body)
        if not sizes:
            sys.exit("FATAL %s: no headings, so it parses to zero sections and "
                     "kbimport rejects files that yield none" % name)
        total += len(sizes)
        biggest = max(sizes, key=lambda pair: pair[1])
        if biggest[1] > args.max_section_chars:
            oversized.append((name, biggest[0], biggest[1]))

        stem = slug(name, args.id_prefix)
        doc_id = "%s--%s" % (args.team, stem)
        rel = "%s/%s-%s.md" % (args.doc_type, stem, args.doc_type)
        if not args.report_only:
            with open(os.path.join(args.out, *rel.split("/")), "w", encoding="utf-8") as out:
                out.write(front_matter_for(args, doc_id, name, title, version)
                          + body.lstrip())
        docs.append({"id": doc_id, "doc_type": args.doc_type, "path": rel,
                     "team": args.team, "product": args.product, "version": version})

    print("%d files, %d sections%s"
          % (len(names), total,
             " (%d from --overrides)" % overridden if overridden else ""))
    for line in warnings:
        print("  ! %s" % line)
    if oversized:
        print("\n  %d section(s) over --max-section-chars=%d. Decide how to split by "
              "looking at the content, then rerun:" % (len(oversized), args.max_section_chars))
        for name, heading, size in oversized:
            print("    %-46s %7d chars  under %r" % (name, size, heading[:40]))
        print("    (a table splits per row; prose needs intermediate headings; a list "
              "splits by meaning -- there is no one right answer)")
    if args.report_only:
        return 0

    rows = write_index(args, docs)
    print("\nwrote %d docs to %s; kb_index.json now %d rows" % (len(docs), args.out, rows))
    return 0


if __name__ == "__main__":
    sys.exit(main())
