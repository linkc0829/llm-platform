"""run_eval 計分的回歸檢查。跑法:python selfcheck_fixes.py

service 婉拒時會清空 sources(service.go:「A refusal carries no usable sources」),
舊版 src_ok 直接把空清單判成檢索失敗 —— 實測 3 題 D 明明命中了期待文件、答案內文
也引了路徑,卻全被記進 hit@1 分母當 miss(553/557 應為 556/557)。

run_eval.py 是一支直線腳本(沒有 __main__ guard,import 就會開始打 API),所以這裡
用文字切片把 answer_cites 取出來測,不 import 整支。產生器那半邊的檢查在
export_kb.py 的 _selftest() 裡(python export_kb.py --selftest)。
"""
import pathlib


def load_answer_cites():
    src = pathlib.Path(__file__).with_name("run_eval.py").read_text(encoding="utf-8")
    start = src.index("def answer_cites(")
    ns = {}
    exec(src[start:src.index("\ndef kind(", start)], ns)
    return ns["answer_cites"]


def main():
    cites = load_answer_cites()
    paths = {"Store.POS--POS_Login-Login_Initial-procedure":
             "procedures/POS_Login/Login_Initial-procedure.md"}
    expected = "Store.POS--POS_Login-Login_Initial-procedure"
    hit = ("根據提供的上下文,`Store.POS/procedures/POS_Login/Login_Initial-procedure.md"
           "#登入-初始化-完整步驟總覽` 說明...")
    # 婉拒清空了 sources,但答案內文引到期待文件 —— 這是檢索命中,不是 miss。
    assert cites(expected, hit, paths)
    # 連內文都沒引到才是真的判不出來(呼叫端會記 None,退出 hit@1 分母)。
    assert not cites(expected, "提供的文件中沒有相關資訊。", paths)
    # 沒有 kb_index 對應時退回 doc id 尾段,不要無條件放行。
    assert not cites(expected, hit, {})
    assert cites(expected, "…POS_Login-Login_Initial-procedure…", {})
    # 題庫沒填 expected src 的題目不判失敗。
    assert cites("", "隨便什麼答案", paths)
    print("ok  answer_cites:婉拒時改看答案內文,引到期待文件就算命中")


if __name__ == "__main__":
    main()
