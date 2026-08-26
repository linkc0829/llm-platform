"""Merge several per-source KB bundles into one staging tree that `kbimport` accepts.

Each source directory holds a `kb/` bundle (produced by export_kb.py or
convert_reference_bundle.py) and, for sources with screenshots, a sibling
`modules/` tree. `kbimport` resolves image references against
`filepath.Dir(-from)` (cmd/kbimport/main.go, stageBundle), so `modules/` has to
land beside `kb/` in the staging tree or every illustrated document fails to
stage.

The whole job is deterministic, so this script does all of it -- and refuses
rather than guesses. The rule that matters most: a source listed in the manifest
is never skipped. Dropping one produces a perfectly self-consistent staging tree
that passes `-check`, and `replaceTeam` then deletes those documents from docs/.

Usage:
  python merge_kb_sources.py --report-only
  python merge_kb_sources.py --out C:/tmp/staging
"""
import argparse
import json
import os
import shutil
import sys

# kb/ also holds eng_eval_out*.json run artefacts, so copy by whitelist.
KB_SUBDIRS = ("procedures", "ui_inventory", "playlists", "reference", "engineering_reference", "eval")


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--manifest", default="kb_sources.json",
                        help="JSON listing the sources; roots resolve relative "
                             "to the manifest's own directory")
    parser.add_argument("--source", action="append", default=[],
                        help="source root; repeatable, replaces the manifest")
    parser.add_argument("--team", help="overrides the manifest's team")
    parser.add_argument("--out", help="staging root (omit with --report-only)")
    parser.add_argument("--force", action="store_true",
                        help="delete a non-empty --out first")
    parser.add_argument("--report-only", action="store_true",
                        help="run every check and count; write nothing")
    args = parser.parse_args(argv)
    if not args.report_only and not args.out:
        parser.error("--out is required unless --report-only")
    return args


def die(msg):
    sys.exit("FATAL " + msg)


def long_path(path):
    """Opt out of the 260-character MAX_PATH limit on Windows.

    modules/ trees run deep (module/output/module/img/FLOW/shot.jpg), so a
    staging root more than a few dozen characters long overflows MAX_PATH and
    every copy fails with WinError 3. The prefix only works on a normalised
    absolute path with backslashes.
    """
    if os.name != "nt" or path.startswith("\\\\?\\"):
        return path
    return "\\\\?\\" + os.path.abspath(path).replace("/", os.sep)


def load_sources(args):
    """Resolve the source list from --source or the manifest."""
    if args.source:
        if not args.team:
            die("--source requires --team")
        return args.team, [(os.path.basename(os.path.normpath(s)), os.path.abspath(s))
                           for s in args.source]
    if not os.path.isfile(args.manifest):
        # Nothing generates this file: which directories feed a team is a
        # decision, not a build product. Show the shape rather than scaffold a
        # copy whose paths would be wrong on this machine anyway.
        die("manifest %r not found. Write it (forward slashes; roots are the\n"
            "source directories that CONTAIN kb/, not kb/ itself):\n"
            '  {\n'
            '    "team": "Store.POS",\n'
            '    "sources": [\n'
            '      { "name": "admin-replay", "root": "C:/Protech/admin-replay" }\n'
            '    ]\n'
            '  }' % os.path.abspath(args.manifest))
    with open(args.manifest, encoding="utf-8") as handle:
        manifest = json.load(handle)
    team = args.team or manifest.get("team")
    if not team:
        die("%s: no team" % args.manifest)
    rows = manifest.get("sources") or []
    if not rows:
        die("%s: no sources" % args.manifest)
    # Roots resolve against the manifest, not the cwd: otherwise the same
    # command run from a subdirectory silently points somewhere else.
    base = os.path.dirname(os.path.abspath(args.manifest))
    return team, [(row["name"], os.path.abspath(os.path.join(base, row["root"])))
                  for row in rows]


def check_roots(roots):
    """Every root must resolve to a real bundle. Never skip one."""
    seen = {}
    for name, root in roots:
        # %r, not the bare path: a manifest written "C:\temp\x" is legal JSON
        # whose \t decodes to a tab, and the resulting path looks fine printed.
        if not os.path.isdir(root):
            die("source %s: %r is not a directory" % (name, root))
        if root in seen:
            die("source %s: same root as %s (%r)" % (name, seen[root], root))
        seen[root] = name
        if not os.path.isdir(os.path.join(root, "kb")):
            hint = ""
            if os.path.isfile(os.path.join(root, "kb_index.json")):
                hint = "; that looks like the kb/ directory itself -- point at its parent"
            die("source %s: %r has no kb/ subdirectory%s" % (name, root, hint))
        if not os.path.isfile(os.path.join(root, "kb", "kb_index.json")):
            die("source %s: %r has no kb_index.json" % (name, os.path.join(root, "kb")))


def stage_tree(src, dst, prefix, name, owners):
    """Record every file's owner, copying when dst is set.

    Ownership is tracked so a second source cannot quietly overwrite a file --
    the eval subdirectories happen not to collide today (ADMIN/POS/PM), which is
    luck, and an overwrite would just show up as fewer eval questions.
    """
    for dirpath, _, filenames in os.walk(src):
        for filename in filenames:
            full = os.path.join(dirpath, filename)
            rel = os.path.relpath(full, src)
            key = "%s/%s" % (prefix, rel.replace(os.sep, "/"))
            if key in owners:
                die("%s and %s both provide kb/%s" % (owners[key], name, key))
            owners[key] = name
            if dst:
                target = long_path(os.path.join(dst, rel))
                os.makedirs(os.path.dirname(target), exist_ok=True)
                shutil.copy2(full, target)


def merge_index(out, team, rows):
    """Write the merged kb_index.json, front-running validateEvalIndex's checks.

    kbimport reports an id mismatch without saying which side is short. The same
    checks run here, where the offending source is still known by name.
    """
    seen = {}
    for name, row in rows:
        if row["id"] in seen:
            die("id %s comes from both %s and %s" % (row["id"], seen[row["id"]], name))
        seen[row["id"]] = name
        if row["team"] != team:
            die("%s: %s has team %r, want %r (one staging tree = one team)"
                % (name, row["id"], row["team"], team))
        if out:
            parts = row["path"].replace(os.sep, "/").split("/")
            if not os.path.isfile(os.path.join(out, "kb", *parts)):
                die("%s: %s lists %s but no such file was staged"
                    % (name, row["id"], row["path"]))
    if out:
        with open(os.path.join(out, "kb", "kb_index.json"), "w", encoding="utf-8") as handle:
            json.dump({"docs": [row for _, row in rows]}, handle,
                      ensure_ascii=False, indent=2)


def stage_source(root, out, name, kb_owners, module_owners):
    """Stage one source; returns its module list and index rows."""
    kb = os.path.join(root, "kb")
    modules = os.path.join(root, "modules")

    mods = sorted(os.listdir(modules)) if os.path.isdir(modules) else []
    for mod in mods:
        if mod in module_owners:
            die("modules/%s comes from both %s and %s" % (mod, module_owners[mod], name))
        module_owners[mod] = name
        if out:
            shutil.copytree(os.path.join(modules, mod),
                            long_path(os.path.join(out, "modules", mod)))

    for sub in KB_SUBDIRS:
        src = os.path.join(kb, sub)
        if os.path.isdir(src):
            stage_tree(src, os.path.join(out, "kb", sub) if out else None,
                       sub, name, kb_owners)

    # eng_eval.yaml sits at kb/ root, not in a subdirectory, and is a top-level
    # YAML list -- so sources concatenate onto it instead of overwriting.
    for filename in sorted(os.listdir(kb)):
        if not filename.endswith(".yaml") or not out:
            continue
        with open(os.path.join(kb, filename), encoding="utf-8") as handle:
            body = handle.read()
        os.makedirs(os.path.join(out, "kb"), exist_ok=True)
        dst = os.path.join(out, "kb", filename)
        existing = ""
        if os.path.isfile(dst):
            with open(dst, encoding="utf-8") as handle:
                existing = handle.read()
            # A source whose file lacks a trailing newline would otherwise glue
            # its last list item onto the next source's first one.
            if not existing.endswith("\n"):
                existing += "\n"
        with open(dst, "w", encoding="utf-8") as dest:
            dest.write(existing + body)

    with open(os.path.join(kb, "kb_index.json"), encoding="utf-8") as handle:
        return mods, json.load(handle)["docs"]


def main(argv=None):
    sys.stdout.reconfigure(encoding="utf-8")
    args = parse_args(argv)
    team, roots = load_sources(args)
    check_roots(roots)

    out = os.path.abspath(args.out) if args.out else None
    if out and os.path.isdir(out) and os.listdir(out):
        if not args.force:
            die("%r is not empty; leftovers from an earlier merge are "
                "indistinguishable from this one's output (pass --force)" % out)
        shutil.rmtree(long_path(out))

    kb_owners, module_owners, rows, summary = {}, {}, [], []
    for name, root in roots:
        mods, docs = stage_source(root, out, name, kb_owners, module_owners)
        rows.extend((name, row) for row in docs)
        summary.append((name, root, len(docs), ",".join(mods) or "-"))

    merge_index(out, team, rows)

    print("%d sources -> %s" % (len(roots), out or "(report only, nothing written)"))
    for name, root, count, mods in summary:
        print("  %-14s %4d docs   modules: %-10s %s" % (name, count, mods, root))
    print("%d docs, %d files, 0 collisions, team %s" % (len(rows), len(kb_owners), team))
    if out:
        print("\nnext:\n  go run ./cmd/kbimport -team %s -from %s -check"
              % (team, os.path.join(out, "kb")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
