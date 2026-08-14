"""階段一:kb/ 產出的靜態驗證(不需要 KB 服務,純本機讀檔)。

export_kb.py --check 只驗「合規 + 檢索 gate」;這支驗它管不到的東西:
  1. 證據品質 —— trajectory 與 procedure 的證據分布(recorded 佔比決定操作問題答不答得出)
  2. 呈現層退化 —— automation_id 洩漏進動作行、捲軸雜訊、假的「(變化 N)」分裂
  3. 檢索殺手 —— 逐字相同的樣板段落(--check 的零文字 gate 抓不到:它們有字)

用法:填 REPLAY_DIR 後 `python validate_kb_static.py`。exit 0 = 全過;硬錯 exit 1。
"""
import argparse
import collections
import glob
import json
import os
import re
import sys

# ===== 設定 =====
REPLAY_DIR = r"<<REPLAY_DIR>>"     # 模組化 workspace，含 modules/ 與 kb/
# ================

parser = argparse.ArgumentParser(description="Validate a modular WPF replay workspace")
parser.add_argument("--replay-dir", default=REPLAY_DIR,
                    help="workspace root containing modules/ and kb/")
args = parser.parse_args()
REPLAY_DIR = os.path.abspath(args.replay_dir)
KB = os.path.join(REPLAY_DIR, "kb")
errors, warns = [], []


def trajectory_files():
    return sorted(glob.glob(os.path.join(REPLAY_DIR, "modules", "**", "trajectory.json"),
                            recursive=True))


markdown_files = sorted(glob.glob(os.path.join(KB, "**", "*.md"), recursive=True))
procedure_files = [f for f in markdown_files if f.endswith("-procedure.md")]
ui_inventory_files = [f for f in markdown_files if f.endswith("-ui_inventory.md")]

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def section(title):
    print(f"\n===== {title} =====")


# ---------- 1. trajectory 證據分布 ----------
section("trajectory 證據分布")
trajectory_paths = trajectory_files()
if not os.path.isdir(REPLAY_DIR):
    errors.append(f"找不到 replay workspace: {REPLAY_DIR}")
if not os.path.isdir(KB):
    errors.append(f"找不到 KB 目錄: {KB}")
if not trajectory_paths:
    errors.append(f"找不到模組 trajectory: {os.path.join(REPLAY_DIR, 'modules', '**', 'trajectory.json')}")

acts = []
for trajectory_path in trajectory_paths:
    try:
        trajectory = json.load(open(trajectory_path, encoding="utf-8"))
        module_actions = trajectory.get("actions", [])
        if not isinstance(module_actions, list):
            errors.append(f"{trajectory_path}: actions 必須是陣列")
            continue
        acts.extend(module_actions)
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"{trajectory_path}: trajectory 無法讀取 ({exc})")
ev = collections.Counter(a.get("evidence") for a in acts)
n = len(acts)
for k, v in ev.most_common():
    print(f"  {k:22} {v:4}  ({v * 100 // max(n, 1)}%)")
rec = ev.get("recorded", 0) + ev.get("vision_inferred", 0)
print(f"  可指名元件(recorded+vision):{rec}/{n} ({rec * 100 // max(n, 1)}%)")
if rec * 100 // max(n, 1) < 50:
    # 實測:13% 時操作問題全婉拒,59% 後 procedure 才有可用的具名步驟。
    warns.append(f"可指名證據僅 {rec * 100 // max(n, 1)}% —— 操作類問題大多會婉拒;"
                 "先檢查 replay.py 的 UIA 命中,再考慮 ui-visual-action-review 補標")

# ---------- 2. procedure 每區證據 ----------
section("procedure 步驟證據(每 area)")
tot = collections.Counter()
for f in procedure_files:
    t = open(f, encoding="utf-8").read()
    c = collections.Counter(re.findall(r"(?:\*\*)?動作證據(?:\*\*)?[：:]\s*`(\w+)`", t))
    tot += c
    area = os.path.relpath(f, KB)
    print(f"  {area:26} recorded={c['recorded']:3} vision={c['vision_inferred']:3} "
          f"unlabeled={c['recorded_unlabeled']:3} n/a={c['not_attributable']:3} "
          f"inferred={c['inferred']:3}")
    if c and c["recorded"] + c["vision_inferred"] == 0:
        warns.append(f"{area}:沒有任何具名步驟 —— 該區的「如何操作」問題一定婉拒")

# ---------- 3. 呈現層退化 ----------
section("呈現層檢查")
scroll = 0
ascii_actions = []
for f in markdown_files:
    t = open(f, encoding="utf-8").read()
    scroll += len(re.findall(r"Scroll", t))
    for m in re.findall(r"\*\*動作\*\*:點擊「([^」]+)」", t):
        # 純 ASCII 識別字樣的動作名,多半是 automation_id 洩漏(如 Function1)——
        # 中文查詢比對不到。Enter/Clear 這類真實鍵名除外,人工看清單判斷。
        if re.fullmatch(r"[A-Za-z0-9_]+", m):
            ascii_actions.append(m)
print(f"  kb/ 內 'Scroll' 命中:{scroll}(期望 0)")
if scroll:
    errors.append("捲軸雜訊洩漏進 kb/ —— export_kb.py 的 is_noise_control 沒生效")
print(f"  純 ASCII 動作名:{sorted(set(ascii_actions))}")
if ascii_actions:
    warns.append(f"{len(ascii_actions)} 個動作名是純英文識別字 —— 中文查詢比對不到,"
                 "可用視覺覆核補人名")

index_path = os.path.join(KB, "kb_index.json")
if os.path.isfile(index_path):
    idx = json.load(open(index_path, encoding="utf-8"))
else:
    idx = {}
    errors.append(f"缺少索引檔: {index_path}")
nc = idx.get("narration_conflicts", [])
pd = idx.get("possible_duplicates", {})
print(f"  narration_conflicts:{len(nc)}  possible_duplicates:{len(pd)}")
var = sum(len(re.findall(r"變化 \d+", open(f, encoding="utf-8").read()))
          for f in ui_inventory_files)
print(f"  「(變化 N)」畫面:{var}(捲軸濾除後應接近 0;>0 時逐筆確認是真變化)")

# ---------- 4. 檢索殺手:逐字相同的樣板段落 ----------
section("樣板段落(--check 盲區)")
bodies = collections.defaultdict(int)
total_secs = 0
for f in markdown_files:
    t = open(f, encoding="utf-8").read()
    for m in re.finditer(r"^#{2,3} .+?\n(.*?)(?=^#{1,3} |\Z)", t, re.M | re.S):
        total_secs += 1
        body = re.sub(r"!\[.*?\]\(.*?\)", "", m.group(1))
        body = re.sub(r"\s+", "", body)
        if body:
            bodies[body] += 1
dup = {b: c for b, c in bodies.items() if c >= 10}
for b, c in sorted(dup.items(), key=lambda kv: -kv[1])[:3]:
    print(f"  {c:3} 個段落逐字相同:{b[:70]}")
boiler = sum(dup.values())
print(f"  段落總數 {total_secs};大量重複樣板 {boiler} ({boiler * 100 // max(total_secs, 1)}%)")
if boiler * 100 // max(total_secs, 1) > 15:
    # 實測 27% 樣板段照樣佔 top-k 名額,互相不可區分 —— 有字所以零文字 gate 抓不到。
    warns.append("樣板段落 >15% —— 會稀釋 top-k;考慮在 export_kb 對空內容段落不輸出")

# ---------- 結果 ----------
section("結果")
for e in errors:
    print(f"  ✗ {e}")
for w in warns:
    print(f"  ⚠ {w}")
if not errors and not warns:
    print("  全過")
print("\n下一步:通過後跑階段二(run_eval.py 對 KB 服務實測)。靜態全過"
      "不代表答得出問題 —— 檢索與模型是另外兩個瓶頸。")
sys.exit(1 if errors else 0)
