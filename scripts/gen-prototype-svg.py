#!/usr/bin/env python3
"""按真实页面模板生成原型线框图（SVG）。

为什么要有这个脚本：原型本体是**服务端渲染的真实页面**
（`internal/httpapi/web/templates/*.html`），线框图只是**同一结构的静态副本**，
用于在文档、PPT 或视频里快速展示页面构成。为避免线框与实现脱节，
本脚本**从模板的真实区块、字段与文案出发**绘制，并在图上标注对应的模板文件与行号。

输出：`docs/原型线框图/*.svg`（4 个页面 × 正常态/异常态 = 8 张）
"""
from __future__ import annotations

import argparse
import io
from pathlib import Path
from xml.sax.saxutils import escape

# 与 internal/httpapi/web/templates/layout.html 的 CSS 同色
BG, CARD, BD, FG, MUT = "#f5f7fb", "#ffffff", "#d7dce5", "#1c2333", "#5a6478"
ACC, BAD, OK = "#2f5bd7", "#b4232b", "#1d7a4c"
TH_BG, CODE_BG, MSG_BG, ERR_BG = "#f0f3f9", "#eef1f7", "#eaf5ee", "#fdecec"
FONT = "'Microsoft YaHei','Segoe UI',sans-serif"

W = 1280
MARGIN = 24
HEADER_H = 52


def tw(text: str, size: float) -> float:
    """粗略估算文本宽度（CJK 按 1em，ASCII 按 0.55em）。"""
    w = 0.0
    for ch in text:
        w += size * (1.0 if ord(ch) > 0x2E80 else 0.55)
    return w


def wrap(text: str, size: float, max_w: float) -> list[str]:
    lines, cur = [], ""
    for ch in text:
        if tw(cur + ch, size) > max_w:
            lines.append(cur)
            cur = ch
        else:
            cur += ch
    if cur:
        lines.append(cur)
    return lines


class Svg:
    def __init__(self, title: str, subtitle: str = "") -> None:
        self.parts: list[str] = []
        self._buf: list[list[str]] = [[]]
        self.y = HEADER_H + 20
        self.title = title
        self.subtitle = subtitle

    def _out(self) -> list[str]:
        """当前绘制目标（section 打开时指向该 section 的缓冲区）。"""
        return self._buf[-1]

    # ---------- 基础图元 ----------
    def rect(self, x, y, w, h, fill=CARD, stroke=BD, rx=6, sw=1, opacity=None):
        op = f' opacity="{opacity}"' if opacity else ""
        self._out().append(
            f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="{fill}" '
            f'stroke="{stroke}" stroke-width="{sw}"{op}/>')

    def line(self, x1, y1, x2, y2, stroke=BD, sw=1, dash=None):
        d = f' stroke-dasharray="{dash}"' if dash else ""
        self._out().append(
            f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{stroke}" stroke-width="{sw}"{d}/>')

    def text(self, x, y, s, size=14, fill=FG, weight="normal", anchor="start", family=FONT):
        s = escape(s)
        self._out().append(
            f'<text x="{x}" y="{y}" font-family="{family}" font-size="{size}" fill="{fill}" '
            f'font-weight="{weight}" text-anchor="{anchor}">{s}</text>')

    def code(self, x, y, s, size=12):
        w = tw(s, size) + 10
        self.rect(x, y - size + 2, w, size + 6, fill=CODE_BG, stroke=CODE_BG, rx=3)
        self.text(x + 5, y, s, size=size, fill=ACC)

    def tag(self, x, y, s, color=OK):
        w = tw(s, 12) + 14
        self.rect(x, y - 12, w, 18, fill=CARD, stroke=color, rx=9, sw=1)
        self.text(x + 7, y + 1, s, size=12, fill=color)
        return x + w

    # ---------- 组件 ----------
    def header(self, user: str):
        self.rect(0, 0, W, HEADER_H, fill=CARD, stroke=CARD, rx=0)
        self.line(0, HEADER_H, W, HEADER_H)
        self.text(MARGIN, 33, "企业模型服务配额管理", size=16, weight="600")
        nx = MARGIN + 190
        for label in ("运营台", "租户管理台", "应用调用台"):
            self.text(nx, 32, label, size=14, fill=ACC)
            nx += tw(label, 14) + 20
        if user:
            self.text(W - MARGIN, 32, user, size=13, fill=MUT, anchor="end")
        else:
            self.text(W - MARGIN, 32, "未登录", size=13, fill=MUT, anchor="end")

    def banner(self, kind: str, msg: str):
        h = 20 + 18 * len(wrap(msg, 13, W - 2 * MARGIN - 40))
        bg, bd, fg = (MSG_BG, OK, OK) if kind == "msg" else (ERR_BG, BAD, BAD)
        self.rect(MARGIN, self.y, W - 2 * MARGIN, h, fill=bg, stroke=bd, rx=4)
        for i, ln in enumerate(wrap(msg, 13, W - 2 * MARGIN - 40)):
            self.text(MARGIN + 12, self.y + 20 + i * 18, ln, size=13, fill=fg)
        self.y += h + 14

    def section(self, h2: str, desc: str = "", source: str = "") -> tuple[float, float]:
        """开启一个 section：内容先写进缓冲区，收尾时再把卡片底板垫到最底层。"""
        x, w = MARGIN, W - 2 * MARGIN
        top = self.y
        self._buf.append([])
        self.y += 14
        self.text(x + 18, self.y, h2, size=15, weight="600")
        self.y += 22
        if desc:
            for ln in wrap(desc, 13, w - 36):
                self.text(x + 18, self.y, ln, size=13, fill=MUT)
                self.y += 18
            self.y += 6
        if source:
            self.text(x + w - 18, top + 20, source, size=11, fill=MUT, anchor="end")
        return top, x

    def close_section(self, top: float, x: float, w: int):
        """收尾：先画白底卡片，再把该 section 的内容盖在上面。"""
        self.y += 10
        buf = self._buf.pop()
        card = (f'<rect x="{x}" y="{top}" width="{w}" height="{self.y - top}" fill="{CARD}" '
                f'stroke="{BD}" stroke-width="1" rx="6"/>')
        self._out().append(card)
        self._out().extend(buf)
        self.y += 18

    def form_row(self, items, x: int = MARGIN + 18, gap: int = 12) -> int:
        """一行表单：items 为 ('label',文本) / ('input',宽,占位) / ('select',宽,选项,选中)
        / ('button',文案,是否主按钮) 的序列。整行统一基线，不会压住上一块内容。"""
        ry = self.y + 26
        cur = x
        for it in items:
            kind = it[0]
            if kind == "label":
                cur = self.label(cur, ry, it[1])
            elif kind == "input":
                cur = self.input(cur, ry - 18, it[1], it[2]) + gap
            elif kind == "select":
                cur = self.select(cur, ry - 18, it[1], it[2], it[3]) + gap
            elif kind == "button":
                cur = self.button(cur, ry - 18, it[1], it[2]) + gap
        self.y = ry + 14
        return cur

    def input(self, x, y, w, placeholder, h=26):
        self.rect(x, y, w, h, fill=CARD, stroke=BD, rx=4)
        self.text(x + 7, y + h - 8, placeholder, size=12, fill=MUT)
        return x + w

    def button(self, x, y, label, primary=True, h=26):
        w = tw(label, 13) + 28
        self.rect(x, y, w, h, fill=(ACC if primary else CARD), stroke=ACC, rx=4)
        self.text(x + w / 2, y + h - 8, label, size=13, fill=("#fff" if primary else ACC), anchor="middle")
        return x + w

    def select(self, x, y, w, options, chosen=0, h=26):
        self.rect(x, y, w, h, fill=CARD, stroke=BD, rx=4)
        self.text(x + 7, y + h - 8, options[chosen], size=12, fill=FG)
        self.text(x + w - 12, y + h - 8, "▾", size=11, fill=MUT, anchor="end")
        return x + w

    def label(self, x, y, s):
        self.text(x, y, s, size=13, fill=FG)
        return x + tw(s, 13) + 6

    def table(self, x, y, headers, rows, widths, tag_cols=None, code_cols=None):
        tag_cols = tag_cols or {}
        code_cols = code_cols or {}
        rowh = 24
        self.rect(x, y, sum(widths), rowh, fill=TH_BG, stroke=BD, rx=0)
        cx = x
        for hd, cw in zip(headers, widths):
            self.text(cx + 7, y + 16, hd, size=12, weight="600")
            cx += cw
        y += rowh
        for row in rows:
            self.rect(x, y, sum(widths), rowh, fill=CARD, stroke=BD, rx=0)
            cx = x
            for cell, cw in zip(row, widths):
                if cx in code_cols:
                    self.code(cx + 5, y + 16, str(cell))
                elif cx in tag_cols:
                    self.tag(cx + 7, y + 16, str(cell), tag_cols[cx])
                else:
                    self.text(cx + 7, y + 16, str(cell), size=12)
                cx += cw
            y += rowh
        self.y = y
        return y

    def kpi(self, x, y, value, label):
        self.text(x, y, value, size=20, weight="600")
        self.text(x, y + 16, label, size=12, fill=MUT)
        return x + max(tw(value, 20), tw(label, 12)) + 26

    def render(self, height: int) -> str:
        head = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{height}" '
            f'viewBox="0 0 {W} {height}" role="img" aria-label="{escape(self.title)}">',
            f'<rect width="{W}" height="{height}" fill="{BG}"/>',
        ]
        return "\n".join(head + list(self._buf[0]) + ["</svg>", ""])


def page_login(state: str) -> Svg:
    s = Svg("登录页原型线框图")
    s.header("")
    if state == "error":
        s.banner("err", "用户名或口令不正确（或凭证密钥无效）")
    top = s.y
    s.section("登录", "平台运营与租户管理员使用预置账号口令登录；客户应用使用凭证密钥登录。"
                     "身份与租户归属由服务端签发，请求参数无法指定。",
              "templates/login.html:4")
    s.form_row([("label", "用户名"), ("input", 190, "ops / admin-a / admin-b"),
                ("label", "口令"), ("input", 150, "预置口令"),
                ("button", "管理员登录", True)])
    s.form_row([("label", "凭证密钥"), ("input", 230, "sk_..."),
                ("button", "应用登录", False)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("预置账号", "口令不在页面上明文展示，初始口令见 docs/04-部署与使用说明.md。",
              "templates/login.html:17")
    s.table(MARGIN + 18, s.y,
            ["账号", "角色", "租户", "口令"],
            [["ops", "平台运营", "全局", "••••••"],
             ["admin-a", "客户管理员", "tenant-a", "••••••"],
             ["admin-b", "客户管理员", "tenant-b", "••••••"]],
            [140, 200, 160, 160], code_cols={MARGIN + 18})
    s.close_section(top, MARGIN, W - 2 * MARGIN)
    return s


def page_ops(state: str) -> Svg:
    s = Svg("运营台原型线框图")
    s.header("ops（ops_admin / 全局） · 2026-11-13 11:40 UTC")
    if state == "error":
        s.banner("err", "降低额度不得低于当日已成功数量加处理中数量: 当日已成功 3 + 处理中 0 = 3")
    else:
        s.banner("msg", "操作成功：租户 tenant-a 的日额度已调整为 20")
    top = s.y
    s.section("核查日期", "缺省为 UTC 今日（2026-11-13）。当前服务时间：2026-11-13 11:40 UTC。",
              "templates/ops.html:3")
    s.form_row([("label", "UTC 自然日"), ("input", 130, "2026-11-13"),
                ("button", "查看该日核查", False)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("租户与配额", "配额是租户级属性，租户下所有凭证共享同一份日额度（R2）。"
                            "降低额度不得低于当日已成功数量加处理中数量（R5）。", "templates/ops.html:12")
    s.table(MARGIN + 18, s.y, ["租户", "名称", "状态", "日额度", "操作"],
            [["tenant-a", "企业甲（演示活动客户）", "启用", "10", "调整额度 / 停用租户"],
             ["tenant-b", "企业乙（凭证泄露客户）", "启用", "10", "调整额度 / 停用租户"]],
            [120, 250, 90, 90, 380], tag_cols={MARGIN + 18 + 370: OK},
            code_cols={MARGIN + 18: OK})
    s.form_row([("label", "新租户名称"), ("input", 170, "企业名称"),
                ("label", "日额度"), ("input", 80, "10"),
                ("input", 170, "开通原因"), ("button", "开通租户", True)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("日终核查（2026-11-13）", "按租户与日期分列成功、失败、超时、处理中与被拒绝请求，"
                                        "并核对「成功记录数 == 计入用量次数」（R8）。", "templates/ops.html:47")
    s.table(MARGIN + 18, s.y,
            ["租户", "成功", "失败", "超时", "处理中", "被拒绝", "额度", "剩余", "一致性"],
            [["tenant-a", "5", "1", "2", "0", "10", "20", "15", "一致"],
             ["tenant-b", "0", "0", "0", "0", "3", "10", "10", "一致"]],
            [120, 70, 70, 70, 80, 90, 70, 70, 110],
            tag_cols={MARGIN + 18 + 640: OK}, code_cols={MARGIN + 18: OK})
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("配额变更与停用影响", "每次额度变更记录操作人、原因与生效时间，不改变既有请求编号与日期归属。",
              "templates/ops.html:71")
    s.table(MARGIN + 18, s.y, ["租户", "变更", "操作人", "原因", "生效时间"],
            [["tenant-a", "0 → 10", "ops", "预置租户并设置初始日额度", "2026-11-11 20:37:05"],
             ["tenant-a", "10 → 20", "ops", "活动增额", "2026-11-13 11:38:12"]],
            [120, 120, 110, 300, 200], code_cols={MARGIN + 18: OK})
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("诊断记录（迟到结果）", "迟到结果只作诊断保留，不改变请求终态、不追加扣额、不计入成功量（R4）。",
              "templates/ops.html:100")
    s.table(MARGIN + 18, s.y, ["租户", "类型", "详情", "记录时间"],
            [["tenant-a", "late_result", "上游在 2 秒后返回，已按 timeout 结案", "2026-11-13 11:12:40"]],
            [120, 160, 520, 200], code_cols={MARGIN + 18: OK})
    s.close_section(top, MARGIN, W - 2 * MARGIN)
    return s


def page_admin(state: str) -> Svg:
    s = Svg("租户管理台原型线框图")
    s.header("admin-a（tenant_admin / 租户 tenant-a） · 2026-11-13 11:40 UTC")
    if state == "error":
        s.banner("err", "凭证数量已达上限: 每个租户最多 2 个凭证")
    else:
        s.banner("msg", "凭证 cred-1 的密钥：sk-**** （仅此一次，刷新页面后不再显示）")
    top = s.y
    s.section("核查日期", "缺省为 UTC 今日（2026-11-13）。", "templates/admin.html:3")
    s.form_row([("label", "UTC 自然日"), ("input", 130, "2026-11-13"),
                ("button", "查看该日记录", False)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("租户概况", "管理员可查看本租户全部调用、成功量、处理中量和剩余额度（R7）。"
                          "凭证所属租户由系统确认（R1）。", "templates/admin.html:12")
    kx = MARGIN + 18
    for v, l in (("20", "日额度"), ("5", "成功量"), ("0", "处理中"), ("15", "剩余额度"), ("tenant-a", "租户编号")):
        kx = s.kpi(kx, s.y + 40, v, l)
    s.y += 60
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("应用凭证", "每个租户最多 2 个凭证（R1 与题面预置条件）。密钥只在创建时返回一次，请立即交给应用。",
              "templates/admin.html:22")
    s.table(MARGIN + 18, s.y,
            ["凭证", "名称", "状态", "创建人", "停用时间", "停用原因", "操作"],
            [["cred-a1", "app-1", "启用", "admin-a", "—", "—", "停用凭证"],
             ["cred-a2", "app-2", "已停用", "admin-a", "2026-11-13 09:02:11", "凭证泄露演练", "—"]],
            [110, 110, 90, 100, 170, 200, 150],
            tag_cols={MARGIN + 18 + 220: OK}, code_cols={MARGIN + 18: OK})
    s.form_row([("label", "凭证名称"), ("input", 180, "应用名，如 app-1"),
                ("button", "创建凭证", True)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("本租户调用记录（2026-11-13）", "被拒绝的尝试不占用请求编号，因此额度提升后应用仍可用同一编号重试（Q7）。",
              "templates/admin.html:59")
    s.table(MARGIN + 18, s.y,
            ["请求编号", "凭证", "状态", "模型", "提示词", "结果 / 失败原因", "幂等命中", "受理时间"],
            [["req-001", "cred-a1", "成功", "text-1", "你好", "模型输出", "0", "11:02:03"],
             ["req-002", "cred-a1", "超时", "text-1", "讲个笑话", "上游超过 2 秒未返回", "0", "11:05:40"],
             ["req-003", "cred-a2", "失败", "text-1", "再试一次", "上游明确失败", "1", "11:09:12"]],
            [130, 110, 90, 90, 160, 300, 100, 110],
            tag_cols={MARGIN + 18 + 240: OK}, code_cols={MARGIN + 18: OK})
    s.close_section(top, MARGIN, W - 2 * MARGIN)
    return s


def page_app(state: str) -> Svg:
    s = Svg("应用调用台原型线框图")
    s.header("cred-a1（client_app / 租户 tenant-a） · 2026-11-13 11:40 UTC")
    if state == "error":
        s.banner("err", "当日额度已用尽")
    else:
        s.banner("msg", "调用 req-001 终态：success")
    top = s.y
    s.section("核查日期", "缺省为 UTC 今日（2026-11-13）。", "templates/app.html:3")
    s.form_row([("label", "UTC 自然日"), ("input", 130, "2026-11-13"),
                ("button", "查看该日记录", False)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("发起模型调用", "请求必须携带本租户全局唯一编号；相同编号与相同参数重复提交不会再次调用上游、"
                             "也不会重复计量（R3）。上游超过 2 秒按超时处理并归还额度（R4）。",
              "templates/app.html:12")
    s.form_row([("label", "请求编号"), ("input", 130, "req-001"),
                ("label", "模型"), ("input", 80, "text-1"),
                ("label", "提示词"), ("input", 170, "你好"),
                ("label", "上游场景"),
                ("select", 150, ["成功", "上游明确失败", "上游超时（>2 秒）", "重复通知"],
                 1 if state == "error" else 0),
                ("button", "发起调用", True)])
    s.close_section(top, MARGIN, W - 2 * MARGIN)

    top = s.y
    s.section("本凭证的调用记录（2026-11-13）", "应用仅可查询由本凭证创建的请求（R7），看不到其他凭证或其他租户的数据。",
              "templates/app.html:35")
    s.table(MARGIN + 18, s.y,
            ["请求编号", "状态", "模型", "提示词", "结果 / 失败原因", "幂等命中", "受理时间", "完成时间"],
            [["req-001", "成功", "text-1", "你好", "模型输出", "0", "11:02:03", "11:02:03"],
             ["req-002", "超时", "text-1", "讲个笑话", "上游超过 2 秒未返回", "0", "11:05:40", "11:05:42"],
             ["req-003", "被拒绝", "text-1", "再来一次", "当日额度已用尽", "0", "11:09:12", "—"]],
            [130, 100, 90, 160, 300, 100, 110, 110],
            tag_cols={MARGIN + 18 + 130: OK}, code_cols={MARGIN + 18: OK})
    s.close_section(top, MARGIN, W - 2 * MARGIN)
    return s


PAGES = {
    "01-login": ("登录页", page_login),
    "02-ops": ("运营台", page_ops),
    "03-admin": ("租户管理台", page_admin),
    "04-app": ("应用调用台", page_app),
}


def main() -> int:
    ap = argparse.ArgumentParser(description="生成原型线框图（SVG）")
    ap.add_argument("--out", default="docs/原型线框图")
    ap.add_argument("--repo", default=".")
    args = ap.parse_args()
    out = Path(args.repo) / args.out
    out.mkdir(parents=True, exist_ok=True)
    made = []
    for stem, (title, fn) in PAGES.items():
        for state, label in (("normal", "正常态"), ("error", "异常态")):
            svg = fn(state)
            h = int(svg.y + 24)
            path = out / f"{stem}-{state}.svg"
            path.write_text(svg.render(h), encoding="utf-8", newline="\n")
            made.append((path.name, h))
    for name, h in made:
        print(f"生成 {name}（{W}x{h}）")
    print(f"输出目录：{out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
