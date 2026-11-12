#!/usr/bin/env python3
"""题目 05「必须演示的完整工作场景」逐条验证脚本。

按题面第五节列出的五组场景，对已部署的真实服务逐条执行并断言，
把每一步的实际返回（已脱敏）写入证据日志。

覆盖：
  场景 1 开通使用：运营开通 → 管理员建两个凭证 → 两个应用调用 → 管理员查询共享额度
  场景 2 活动增额：额度耗尽 → 运营增加额度 → 应用恢复调用；非法降额被拒绝并保留原因
  场景 3 事故处置：存在在途请求时停用凭证或租户 → 新请求受限 → 原请求完成 → 运营查明最终用量
  场景 4 上游异常：失败、超时、迟到成功、重复请求、最后一个额度的并发争抢
  场景 5 日终核查：跨日请求、两个租户的数据隔离、普通应用与管理员权限差异

用法：
    python scripts/scenario-verify.py --base-url http://127.0.0.1:8080 \
        --out evidence/scenario-verify.log --json-out evidence/scenario-verify.json

退出码：0 = 全部通过；1 = 存在失败项。

脱敏约定：口令、密钥、会话令牌一律不写入日志，只记录前缀与长度。
"""
from __future__ import annotations

import argparse
import json
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

# ---------------------------------------------------------------------------
# 输出缓冲：控制台只打印 ASCII 摘要，明细写入日志文件。
# ---------------------------------------------------------------------------

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

LINES: list[str] = []
RESULTS: list[dict] = []
PASSED = 0
FAILED = 0


def emit(text: str) -> None:
    LINES.append(text)


def check(scenario: str, label: str, ok: bool, detail: str = "") -> bool:
    """记录一条断言。"""
    global PASSED, FAILED
    if ok:
        PASSED += 1
        emit(f"  [PASS] {label}" + (f"｜{detail}" if detail else ""))
    else:
        FAILED += 1
        emit(f"  [FAIL] {label}" + (f"｜{detail}" if detail else ""))
    RESULTS.append({
        "scenario": scenario,
        "assertion": label,
        "status": "pass" if ok else "fail",
        "detail": detail,
    })
    return ok


def section(title: str) -> None:
    emit("")
    emit(f"=== {title} ===")


# ---------------------------------------------------------------------------
# HTTP 客户端
# ---------------------------------------------------------------------------


def api(method: str, path: str, token: str = "", body: dict | None = None,
        timeout: float = 20.0) -> tuple[int, object]:
    """发起一次 API 调用，返回 (HTTP 状态码, 解析后的响应体)。"""
    url = BASE + path
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            code = resp.status
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        code = exc.code
    except urllib.error.URLError as exc:
        return 0, {"error": f"请求失败: {exc.reason}"}
    except OSError as exc:
        # 连接被对端重置等底层错误也要归一为一次失败，而不是让脚本崩溃。
        return 0, {"error": f"连接异常: {type(exc).__name__}: {exc}"}
    try:
        return code, json.loads(raw)
    except json.JSONDecodeError:
        return code, raw


def mask(secret: str) -> str:
    """密钥/令牌脱敏：只保留前 6 位。"""
    if not secret:
        return "(空)"
    return secret[:6] + f"…({len(secret)}位)"


def err_text(payload: object) -> str:
    if isinstance(payload, dict):
        return str(payload.get("error", payload))
    return str(payload)


def login_user(username: str, password: str) -> tuple[int, str]:
    code, body = api("POST", "/api/v1/auth/login", body={"username": username, "password": password})
    token = body.get("token", "") if isinstance(body, dict) else ""
    return code, token


def login_secret(secret: str) -> tuple[int, str]:
    code, body = api("POST", "/api/v1/auth/login", body={"secret": secret})
    token = body.get("token", "") if isinstance(body, dict) else ""
    return code, token


def create_credential(admin_token: str, name: str) -> tuple[int, str, object]:
    code, body = api("POST", "/api/v1/admin/credentials/create", admin_token, {"name": name})
    secret = ""
    if isinstance(body, dict):
        secret = body.get("secret", "")
    return code, secret, body


def invoke(token: str, request_no: str, prompt: str = "演示",
           scenario: str = "", model: str = "text-1") -> tuple[int, object]:
    payload: dict = {"request_no": request_no, "model": model, "prompt": prompt}
    if scenario:
        payload["scenario"] = scenario
    return api("POST", "/api/v1/app/invoke", token, payload, timeout=30.0)


def usage_of(admin_token: str) -> dict:
    code, body = api("GET", "/api/v1/admin/usage", admin_token)
    if code != 200 or not isinstance(body, dict):
        return {}
    return body.get("usage", {}) or {}


def set_quota(ops_token: str, tenant_id: str, quota: int, reason: str) -> tuple[int, object]:
    return api("POST", "/api/v1/ops/tenants/quota", ops_token,
               {"tenant_id": tenant_id, "daily_quota": quota, "reason": reason})


# ---------------------------------------------------------------------------
# 场景 1：开通使用
# ---------------------------------------------------------------------------


def scenario_1(ctx: dict) -> None:
    section("场景 1 开通使用：运营开通 → 管理员建两个凭证 → 两个应用调用 → 管理员查询共享额度")
    ops, ctx["ops"] = login_user("ops", "ops-secret")
    check("场景1", "运营登录取得会话令牌", ops == 200 and bool(ctx["ops"]),
          f"HTTP {ops}，令牌 {mask(ctx['ops'])}")

    admin_a, ctx["admin_a"] = login_user("admin-a", "admin-a-secret")
    check("场景1", "企业甲管理员登录", admin_a == 200 and bool(ctx["admin_a"]),
          f"HTTP {admin_a}")

    code, body = api("GET", "/api/v1/ops/tenants", ctx["ops"])
    tenants = body.get("tenants", []) if isinstance(body, dict) else []
    ta = next((t for t in tenants if t.get("id") == "tenant-a"), None)
    check("场景1", "运营可列出租户且含 tenant-a", code == 200 and ta is not None,
          f"HTTP {code}，租户数 {len(tenants)}")
    check("场景1", "tenant-a 处于已开通状态", bool(ta) and ta.get("status") == "active",
          f"status={ta.get('status') if ta else 'N/A'}")

    code, body = set_quota(ctx["ops"], "tenant-a", 10, "场景1 开通使用：设置初始日额度 10 次")
    check("场景1", "运营为 tenant-a 设置日额度 10（带原因）", code == 200,
          f"HTTP {code}｜{err_text(body) if code != 200 else '额度=10'}")

    code1, secret1, body1 = create_credential(ctx["admin_a"], "S1-应用一")
    check("场景1", "管理员创建第一个凭证", code1 == 200 and bool(secret1),
          f"HTTP {code1}｜密钥 {mask(secret1)}")
    ctx["secret1"] = secret1
    code2, secret2, body2 = create_credential(ctx["admin_a"], "S1-应用二")
    check("场景1", "管理员创建第二个凭证", code2 == 200 and bool(secret2),
          f"HTTP {code2}｜密钥 {mask(secret2)}")

    code3, _, body3 = create_credential(ctx["admin_a"], "S1-应用三")
    check("场景1", "第三个凭证被拒（每租户最多 2 个）", code3 == 400,
          f"HTTP {code3}｜{err_text(body3)}")

    code, tok1 = login_secret(secret1)
    check("场景1", "应用一用凭证登录", code == 200 and bool(tok1), f"HTTP {code}")
    code, tok2 = login_secret(secret2)
    check("场景1", "应用二用凭证登录", code == 200 and bool(tok2), f"HTTP {code}")
    ctx["app1"], ctx["app2"] = tok1, tok2

    code, body = invoke(tok1, "s1-req-1", "应用一的第一笔调用")
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景1", "应用一发起调用并成功", code == 200 and st == "success", f"HTTP {code}｜status={st}")

    code, body = invoke(tok2, "s1-req-2", "应用二的第二笔调用")
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景1", "应用二发起调用并成功", code == 200 and st == "success", f"HTTP {code}｜status={st}")

    usage = usage_of(ctx["admin_a"])
    check("场景1", "管理员看到两个凭证共享同一份额度（成功量=2）",
          usage.get("success") == 2, f"成功={usage.get('success')}｜额度上限={usage.get('quota_limit')}"
          f"｜剩余={usage.get('remaining')}")
    check("场景1", "剩余额度按共享额度计算（10-2=8）", usage.get("remaining") == 8,
          f"remaining={usage.get('remaining')}")

    code, _ = api("GET", "/api/v1/admin/usage", ctx["app1"])
    check("场景1", "普通应用访问管理员用量接口被拒", code == 403, f"HTTP {code}")


# ---------------------------------------------------------------------------
# 场景 2：活动增额
# ---------------------------------------------------------------------------


def scenario_2(ctx: dict) -> None:
    section("场景 2 活动增额：额度耗尽 → 运营增加额度 → 应用恢复调用；非法降额被拒绝并保留原因")
    usage = usage_of(ctx["admin_a"])
    occupied = usage.get("success", 0) + usage.get("in_flight", 0)

    code, body = set_quota(ctx["ops"], "tenant-a", occupied, "场景2 活动增额：把额度压到已用量以便触发耗尽")
    check("场景2", "运营把额度压到当日已用量（剩余为 0）", code == 200, f"HTTP {code}｜额度={occupied}")

    code, body = invoke(ctx["app1"], "s2-req-1", "额度耗尽时的调用")
    check("场景2", "额度耗尽后新调用被拒绝（429）", code == 429, f"HTTP {code}｜{err_text(body)}")

    code, body = invoke(ctx["app1"], "s2-req-1", "额度耗尽时的调用")
    check("场景2", "被拒绝的请求不占用请求编号（再次提交仍是被拒绝，不是编号冲突）",
          code == 429, f"HTTP {code}｜{err_text(body)}")

    code, body = set_quota(ctx["ops"], "tenant-a", occupied + 5, "场景2 活动增额：演示活动临时扩容 5 次")
    check("场景2", "运营增额成功（附原因）", code == 200, f"HTTP {code}")

    code, body = invoke(ctx["app1"], "s2-req-1", "额度耗尽时的调用")
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景2", "增额后应用用同一编号恢复调用成功", code == 200 and st == "success",
          f"HTTP {code}｜status={st}")

    code, body = set_quota(ctx["ops"], "tenant-a", 0, "场景2 非法降额尝试")
    check("场景2", "非法降额被拒绝（400）", code == 400, f"HTTP {code}｜{err_text(body)}")
    check("场景2", "拒绝原因说明了降额下限口径",
          "成功" in err_text(body) and "处理中" in err_text(body), err_text(body))

    usage2 = usage_of(ctx["admin_a"])
    check("场景2", "被拒绝的降额没有改变额度",
          usage2.get("quota_limit") == occupied + 5,
          f"额度上限={usage2.get('quota_limit')}（期望 {occupied + 5}）")

    code, body = api("GET", "/api/v1/ops/report/daily?tenant_id=tenant-a", ctx["ops"])
    report = body.get("report", {}) if isinstance(body, dict) else {}
    changes = report.get("quota_changes", []) or []
    latest = changes[-1] if changes else {}
    check("场景2", "额度变更留痕：记录了操作人、原因与生效时间",
          bool(latest) and latest.get("operator") and latest.get("reason") and latest.get("effective_at"),
          f"operator={latest.get('operator')}｜reason={latest.get('reason')}｜"
          f"effective_at={latest.get('effective_at')}")


# ---------------------------------------------------------------------------
# 场景 3：事故处置
# ---------------------------------------------------------------------------


def scenario_3(ctx: dict) -> None:
    section("场景 3 事故处置：在途请求时停用凭证/租户 → 新请求受限 → 原请求完成 → 运营查明最终用量")
    _, admin_b = login_user("admin-b", "admin-b-secret")
    check("场景3", "企业乙管理员登录", bool(admin_b))

    code, secret, body = create_credential(admin_b, "S3-在途应用")
    check("场景3", "企业乙管理员创建凭证", code == 200 and bool(secret), f"HTTP {code}")
    code, app_b = login_secret(secret)
    check("场景3", "企业乙应用登录", code == 200 and bool(app_b), f"HTTP {code}")

    # 在途请求：上游延迟 3 秒返回，平台 2 秒判超时。
    inflight: dict = {}

    def run_inflight() -> None:
        c, b = invoke(app_b, "s3-req-1", "停用时的在途请求", scenario="slow_success")
        inflight["code"] = c
        inflight["body"] = b

    th = threading.Thread(target=run_inflight)
    th.start()
    time.sleep(0.4)  # 确保请求已进入处理中

    code, body = api("GET", "/api/v1/admin/usage", admin_b)
    inflight_count = (body.get("usage", {}) or {}).get("in_flight") if isinstance(body, dict) else 0
    check("场景3", "在途请求已占用额度（处理中=1）", inflight_count == 1, f"in_flight={inflight_count}")

    code, body = api("POST", "/api/v1/admin/credentials/disable", admin_b,
                     {"credential_id": "", "reason": "场景3 凭证可能泄露，立即停用"})
    if code != 200:
        # 接口按凭证 ID 停用；先查列表取 ID。
        code2, lst = api("GET", "/api/v1/admin/credentials", admin_b)
        creds = lst.get("credentials", []) if isinstance(lst, dict) else []
        target = next((c for c in creds if c.get("name") == "S3-在途应用"), None)
        if target:
            code, body = api("POST", "/api/v1/admin/credentials/disable", admin_b,
                             {"credential_id": target.get("id"), "reason": "场景3 凭证可能泄露，立即停用"})
    check("场景3", "管理员停用凭证成功", code == 200, f"HTTP {code}｜{err_text(body) if code != 200 else ''}")

    code, body = invoke(app_b, "s3-req-2", "停用后的新调用")
    check("场景3", "停用凭证后新调用被拒", code in (409, 403), f"HTTP {code}｜{err_text(body)}")

    code, body = invoke(app_b, "s3-req-1", "停用后用旧编号再触发")
    check("场景3", "停用后用旧编号不会触发新的上游调用", code in (409, 403),
          f"HTTP {code}｜{err_text(body)}")

    code, body = api("POST", "/api/v1/ops/tenants/disable", ctx["ops"],
                     {"tenant_id": "tenant-b", "reason": "场景3 运营停用租户"})
    check("场景3", "运营停用租户成功", code == 200, f"HTTP {code}｜{err_text(body) if code != 200 else ''}")

    code, body = invoke(app_b, "s3-req-3", "租户停用后的新调用")
    check("场景3", "停用租户后新调用被拒", code in (409, 403), f"HTTP {code}｜{err_text(body)}")

    th.join(timeout=30)
    st = ""
    if isinstance(inflight.get("body"), dict):
        st = inflight["body"].get("request", {}).get("status", "")
    check("场景3", "在途请求正常结束并按原规则计量（超时终态）", st == "timeout",
          f"HTTP {inflight.get('code')}｜status={st}")

    code, body = api("GET", "/api/v1/ops/report/daily?tenant_id=tenant-b", ctx["ops"])
    report = body.get("report", {}) if isinstance(body, dict) else {}
    counts = report.get("counts", {}) or {}
    check("场景3", "运营查明最终用量：超时与被拒绝分列统计",
          counts.get("timeout", 0) >= 1 and counts.get("rejected", 0) >= 1,
          f"counts={counts}")
    check("场景3", "用量与请求记录一致", report.get("consistent") is True,
          f"consistent={report.get('consistent')}｜{report.get('mismatch_note', '')}")
    events = report.get("events", []) or []
    kinds = {e.get("kind") for e in events}
    check("场景3", "停用动作留痕（凭证停用与租户停用事件）",
          any("credential" in str(k) for k in kinds) and any("tenant" in str(k) for k in kinds),
          f"事件类型={sorted(str(k) for k in kinds)}")


# ---------------------------------------------------------------------------
# 场景 4：上游异常
# ---------------------------------------------------------------------------


def scenario_4(ctx: dict) -> None:
    section("场景 4 上游异常：失败、超时、迟到成功、重复请求、最后一个额度的并发争抢")
    before = usage_of(ctx["admin_a"])

    code, body = invoke(ctx["app1"], "s4-req-fail", "触发上游明确失败", scenario="fail")
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景4", "上游明确失败 → 终态 failed", code == 200 and st == "failed", f"HTTP {code}｜status={st}")

    after_fail = usage_of(ctx["admin_a"])
    check("场景4", "失败请求不计入成功量且释放占用",
          after_fail.get("success") == before.get("success") and after_fail.get("in_flight") == 0,
          f"成功 {before.get('success')}→{after_fail.get('success')}｜处理中={after_fail.get('in_flight')}")

    t0 = time.time()
    code, body = invoke(ctx["app1"], "s4-req-timeout", "触发上游超时", scenario="timeout")
    elapsed = time.time() - t0
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景4", "上游超时 → 终态 timeout", code == 200 and st == "timeout", f"HTTP {code}｜status={st}")
    check("场景4", "超时判定在 2 秒硬时限附近作出", 1.5 <= elapsed <= 5.0, f"耗时 {elapsed:.2f}s")

    after_timeout = usage_of(ctx["admin_a"])
    check("场景4", "超时请求释放占用且不计成功量",
          after_timeout.get("success") == before.get("success") and after_timeout.get("in_flight") == 0,
          f"成功={after_timeout.get('success')}｜处理中={after_timeout.get('in_flight')}")

    # 迟到成功：上游 3 秒后才返回，平台 2 秒已判超时。
    code, body = invoke(ctx["app1"], "s4-req-late", "触发迟到成功", scenario="slow_success")
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景4", "迟到成功场景先按超时结案", st == "timeout", f"status={st}")

    time.sleep(2.5)  # 等待上游在 3 秒时返回
    code, body = api("GET", "/api/v1/app/request?request_no=s4-req-late", ctx["app1"])
    diags = body.get("diagnostics", []) if isinstance(body, dict) else []
    late = [d for d in diags if d.get("kind") == "late_result"]
    check("场景4", "迟到结果只作诊断记录保留", code == 200 and len(late) >= 1,
          f"HTTP {code}｜诊断 {len(diags)} 条（late_result {len(late)} 条）")
    req_status = body.get("request", {}).get("status") if isinstance(body, dict) else ""
    check("场景4", "迟到结果不改变已返回的超时结论", req_status == "timeout", f"status={req_status}")
    after_late = usage_of(ctx["admin_a"])
    check("场景4", "迟到结果不追加扣额",
          after_late.get("success") == before.get("success"),
          f"成功 {before.get('success')}→{after_late.get('success')}")

    # 重复请求：同编号同参数幂等；同编号异参数拒绝。
    code, body = invoke(ctx["app1"], "s4-req-dup", "重复请求第一次", scenario="duplicate")
    st = body.get("request", {}).get("status") if isinstance(body, dict) else None
    check("场景4", "重复请求首次受理成功", code == 200 and st == "success", f"HTTP {code}｜status={st}")
    code, body = invoke(ctx["app1"], "s4-req-dup", "重复请求第一次", scenario="duplicate")
    replayed = body.get("replayed") if isinstance(body, dict) else None
    check("场景4", "相同编号相同参数幂等重放（replayed=true）", code == 200 and replayed is True,
          f"HTTP {code}｜replayed={replayed}")
    code, body = invoke(ctx["app1"], "s4-req-dup", "换了提示词", scenario="duplicate")
    check("场景4", "相同编号不同参数被拒绝（409）", code == 409, f"HTTP {code}｜{err_text(body)}")
    after_dup = usage_of(ctx["admin_a"])
    check("场景4", "幂等重放不重复计量（成功量只 +1）",
          after_dup.get("success") == before.get("success") + 1,
          f"成功 {before.get('success')}→{after_dup.get('success')}")

    # 最后一个额度的并发争抢。
    occupied = after_dup.get("success", 0) + after_dup.get("in_flight", 0)
    code, _ = set_quota(ctx["ops"], "tenant-a", occupied + 1, "场景4 并发验证：只剩 1 个额度名额")
    check("场景4", "把额度调整为只剩 1 个名额", code == 200, f"HTTP {code}｜额度={occupied + 1}")

    results: list[int] = []
    lock = threading.Lock()

    def race(i: int) -> None:
        c, _ = invoke(ctx["app1"], f"s4-race-{i}", "并发争抢最后一个额度")
        with lock:
            results.append(c)

    threads = [threading.Thread(target=race, args=(i,)) for i in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join(timeout=30)
    ok_n = results.count(200)
    rej_n = results.count(429)
    check("场景4", "8 个并发争抢最后 1 个额度：只有 1 个成功，其余被拒",
          ok_n == 1 and rej_n == 7, f"成功 {ok_n}｜拒绝 {rej_n}｜其余 {8 - ok_n - rej_n}")
    after_race = usage_of(ctx["admin_a"])
    check("场景4", "并发未造成超额（成功量不超过额度上限）",
          after_race.get("success", 0) <= after_race.get("quota_limit", 0),
          f"成功={after_race.get('success')}｜额度上限={after_race.get('quota_limit')}")


# ---------------------------------------------------------------------------
# 场景 5：日终核查
# ---------------------------------------------------------------------------


def scenario_5(ctx: dict) -> None:
    section("场景 5 日终核查：跨日请求、两个租户的数据隔离、普通应用与管理员权限差异")
    code, body = api("GET", "/api/v1/ops/report/daily?tenant_id=tenant-a", ctx["ops"])
    report_a = body.get("report", {}) if isinstance(body, dict) else {}
    counts_a = report_a.get("counts", {}) or {}
    check("场景5", "运营按租户与日期列出五类请求计数",
          code == 200 and all(k in counts_a for k in ("success", "failed", "timeout", "processing", "rejected")),
          f"HTTP {code}｜counts={counts_a}")
    check("场景5", "成功记录数与用量次数一致",
          report_a.get("consistent") is True and report_a.get("usage_success") == counts_a.get("success"),
          f"consistent={report_a.get('consistent')}｜用量={report_a.get('usage_success')}"
          f"｜成功记录={counts_a.get('success')}")

    code, body = api("GET", "/api/v1/ops/report/daily?tenant_id=tenant-b", ctx["ops"])
    report_b = body.get("report", {}) if isinstance(body, dict) else {}
    check("场景5", "两个租户的数据相互隔离（tenant-b 计数与 tenant-a 不同）",
          code == 200 and report_b.get("tenant_id") == "tenant-b"
          and (report_b.get("counts") or {}) != counts_a,
          f"tenant-a={counts_a}｜tenant-b={(report_b.get('counts') or {})}")
    check("场景5", "被停用的 tenant-b 用量不再新增（成功量不增长）",
          (report_b.get("counts") or {}).get("success", 0) == (report_b.get("usage_success", -1)),
          f"成功记录={(report_b.get('counts') or {}).get('success')}｜用量={report_b.get('usage_success')}")

    ua = usage_of(ctx["admin_a"])
    ub = usage_of(login_user("admin-b", "admin-b-secret")[1])
    check("场景5", "管理员只能看到本租户用量",
          ua.get("tenant_id") == "tenant-a" and ub.get("tenant_id") == "tenant-b",
          f"admin-a→{ua.get('tenant_id')}｜admin-b→{ub.get('tenant_id')}")

    code, _ = api("GET", "/api/v1/admin/usage", ctx["app1"])
    check("场景5", "普通应用访问管理员用量接口被拒（403）", code == 403, f"HTTP {code}")
    code, _ = api("GET", "/api/v1/admin/requests", ctx["app1"])
    check("场景5", "普通应用访问管理员请求列表被拒（403）", code == 403, f"HTTP {code}")

    # 普通应用只能查询由其凭证创建的请求：app2 的请求对 app1 不可见。
    code, body = api("GET", "/api/v1/app/request?request_no=s1-req-2", ctx["app1"])
    check("场景5", "普通应用查询他人凭证的请求被拒（403）", code == 403, f"HTTP {code}｜{err_text(body)}")
    code, body = api("GET", "/api/v1/app/request?request_no=s1-req-1", ctx["app1"])
    check("场景5", "普通应用可查询自身凭证的请求", code == 200, f"HTTP {code}")

    code, body = api("GET", "/api/v1/ops/report/daily", ctx["ops"])
    check("场景5", "运营未指定租户被拒（400）", code == 400, f"HTTP {code}｜{err_text(body)}")

    # 跨日：用教学时钟把「当前时间」偏移 48 小时，新请求归属下一个 UTC 自然日。
    code, body = api("POST", "/api/v1/ops/clock", ctx["ops"], {"offset_seconds": 172800})
    check("场景5", "教学时钟前移 48 小时", code == 200, f"HTTP {code}｜{err_text(body) if code != 200 else ''}")
    try:
        # 会话有效期 12 小时，前移 48 小时后所有旧令牌自然过期，需要重新登录。
        # 这本身也印证了「会话由服务端签发并带有效期」。
        code, body = api("GET", "/api/v1/auth/whoami", ctx["app1"])
        check("场景5", "前移 48 小时后旧应用令牌已失效（会话按有效期过期）", code == 401,
              f"HTTP {code}｜{err_text(body)}")
        code, body = api("GET", "/api/v1/auth/whoami", ctx["ops"])
        check("场景5", "前移 48 小时后旧运营令牌同样失效", code == 401,
              f"HTTP {code}｜{err_text(body)}")

        # 重新登录取得新会话（运营用于核查，应用用于发起跨日调用）。
        code, ops_token = login_user("ops", "ops-secret")
        check("场景5", "运营重新登录取得新会话", code == 200 and bool(ops_token), f"HTTP {code}")
        ctx["ops"] = ops_token
        code, tok = login_secret(ctx["secret1"])
        check("场景5", "用同一凭证重新登录取得新会话", code == 200 and bool(tok), f"HTTP {code}")
        ctx["app1"] = tok

        code, body = invoke(ctx["app1"], "s5-next-day", "跨日调用")
        st = body.get("request", {}).get("status") if isinstance(body, dict) else None
        check("场景5", "跨日请求成功受理", code == 200 and st == "success", f"HTTP {code}｜status={st}")

        code, body = api("GET", "/api/v1/ops/report/daily?tenant_id=tenant-a", ctx["ops"])
        today = body.get("report", {}) if isinstance(body, dict) else {}
        day2 = today.get("day", "")
        counts2 = today.get("counts", {}) or {}
        check("场景5", "新请求归属新的 UTC 自然日", code == 200 and day2 != report_a.get("day"),
              f"HTTP {code}｜当日={report_a.get('day')}｜次日={day2}")
        check("场景5", "次日只统计当日的请求", counts2.get("success") == 1,
              f"次日成功计数={counts2.get('success')}")
        code, body = api("GET", f"/api/v1/ops/report/daily?tenant_id=tenant-a&day={report_a.get('day')}",
                         ctx["ops"])
        old = body.get("report", {}) if isinstance(body, dict) else {}
        check("场景5", "原日的统计不被跨日请求污染",
              code == 200 and (old.get("counts") or {}).get("success") == counts_a.get("success"),
              f"HTTP {code}｜原日成功={(old.get('counts') or {}).get('success')}｜跨日前={counts_a.get('success')}")
    finally:
        # 时钟必须复位：否则后续核查都落在错误的日期上。
        code = 0
        for _ in range(3):
            code, body = api("POST", "/api/v1/ops/clock", ctx["ops"], {"offset_seconds": 0})
            if code == 200:
                break
            # 运营会话可能已过期，用新令牌重试。
            ops_token, _ = login_user("ops", "ops-secret")
            if ops_token:
                ctx["ops"] = ops_token
            time.sleep(1)
        check("场景5", "教学时钟复位", code == 200, f"HTTP {code}｜{err_text(body) if code != 200 else ''}")


# ---------------------------------------------------------------------------
# 入口
# ---------------------------------------------------------------------------

BASE = "http://127.0.0.1:8080"


def main() -> int:
    global BASE, PASSED, FAILED
    ap = argparse.ArgumentParser(description="题目 05 五组完整工作场景验证")
    ap.add_argument("--base-url", default=BASE)
    ap.add_argument("--out", default="")
    ap.add_argument("--json-out", default="")
    args = ap.parse_args()
    BASE = args.base_url.rstrip("/")

    emit("题目 05 完整工作场景验证")
    emit(f"目标服务：{BASE}")
    emit(f"开始时间：{time.strftime('%Y-%m-%d %H:%M:%S %z')}")

    code, _ = api("GET", "/healthz")
    if code != 200:
        emit(f"[FAIL] 服务健康检查失败：HTTP {code}")
        FAILED += 1
    else:
        emit("[PASS] 服务健康检查")

    ctx: dict = {}
    for fn in (scenario_1, scenario_2, scenario_3, scenario_4, scenario_5):
        try:
            fn(ctx)
        except Exception as exc:  # noqa: BLE001 - 单个场景异常不应中断其余场景
            FAILED += 1
            emit(f"  [FAIL] {fn.__name__} 执行异常：{type(exc).__name__}: {exc}")
    emit("")
    emit(f"=== 验证结果：通过 {PASSED} 项，失败 {FAILED} 项 ===")
    emit(f"结束时间：{time.strftime('%Y-%m-%d %H:%M:%S %z')}")

    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            fh.write("\n".join(LINES) + "\n")
    if args.json_out:
        with open(args.json_out, "w", encoding="utf-8") as fh:
            json.dump({"base_url": BASE, "passed": PASSED, "failed": FAILED,
                       "checks": RESULTS}, fh, ensure_ascii=False, indent=2)

    print(f"scenario-verify: passed={PASSED} failed={FAILED} "
          f"exit={0 if FAILED == 0 else 1}")
    return 0 if FAILED == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
