#!/usr/bin/env python3
"""全量接口能力测试：覆盖题目 05 服务的全部 HTTP 接口与页面。

为什么需要这个脚本：
  · `scripts/smoke-test.sh` 只跑主干业务的冒烟断言；
    本脚本按「接口清单」逐个覆盖 **每一个** HTTP 端点，并对每个端点同时验证
    正向路径、越权路径、参数缺失路径与边界路径（题面 R1–R8 的接口级口径）。
  · 只输出断言，不依赖第三方库（标准库 urllib），保证在干净环境可直接执行。

用法：
    python scripts/api-full-test.py [--base-url http://127.0.0.1:8080]
                                  [--out evidence/api-full-test.log]

退出码：0 = 全部通过；1 = 存在失败项；2 = 执行错误（服务不可达等）。
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone, timedelta

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

CST = timezone(timedelta(hours=8))
REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

BASE = "http://127.0.0.1:8080"
PASSED: list[str] = []
FAILED: list[str] = []


# ---------- 断言与请求辅助 ----------

def check(name: str, cond: bool, detail: str = "") -> bool:
    if cond:
        PASSED.append(name)
        print(f"[PASS] {name}")
    else:
        FAILED.append(f"{name} :: {detail}")
        print(f"[FAIL] {name} :: {detail}")
    return cond


class Resp:
    """一次 HTTP 响应的轻量封装。"""

    def __init__(self, status: int, headers: dict, body: str):
        self.status = status
        self.headers = headers
        self.body = body

    @property
    def json(self):
        try:
            return json.loads(self.body)
        except Exception:
            return None

    def has(self, needle: str) -> bool:
        return needle in self.body


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """禁止自动跟随重定向，以便断言 303 与 Location 头。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


OPENER = urllib.request.build_opener(NoRedirect)


def req(method: str, path: str, body=None, token: str = None, form=None,
        raw_body: str = None, content_type: str = None) -> Resp:
    """发起一次请求，返回状态码、响应头与响应体。"""
    url = BASE + path
    data = None
    headers = {}
    if form is not None:
        data = urllib.parse.urlencode(form).encode("utf-8")
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif raw_body is not None:
        data = raw_body.encode("utf-8")
        if content_type:
            headers["Content-Type"] = content_type
    elif body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with OPENER.open(request, timeout=60) as resp:
            return Resp(resp.status, dict(resp.headers), resp.read().decode("utf-8", "replace"))
    except urllib.error.HTTPError as e:
        return Resp(e.code, dict(e.headers), e.read().decode("utf-8", "replace"))
    except Exception as e:  # 网络层异常
        return Resp(0, {}, f"请求异常: {e}")


def login_admin(username: str, password: str) -> tuple[int, Resp]:
    r = req("POST", "/api/v1/auth/login", body={"username": username, "password": password})
    return r.status, r


def login_secret(secret: str) -> tuple[int, Resp]:
    r = req("POST", "/api/v1/auth/login", body={"secret": secret})
    return r.status, r


def err_of(r: Resp) -> str:
    j = r.json
    if isinstance(j, dict):
        return str(j.get("error", ""))
    return r.body[:200]


# ---------- 各阶段测试 ----------

def phase_health() -> None:
    r = req("GET", "/healthz")
    check("GET /healthz 返回 200 且 status=ok",
          r.status == 200 and isinstance(r.json, dict) and r.json.get("status") == "ok",
          f"status={r.status} body={r.body[:120]}")
    r = req("GET", "/readyz")
    check("GET /readyz 返回 200", r.status == 200, f"status={r.status} body={r.body[:120]}")


def phase_auth() -> dict:
    tokens = {}
    st, r = login_admin("ops", "ops-secret")
    if check("POST /api/v1/auth/login 运营登录成功", st == 200 and "token" in (r.json or {}),
             f"status={st} body={r.body[:150]}"):
        tokens["ops"] = r.json["token"]
        check("运营登录返回 role=ops", r.json.get("identity", {}).get("role") == "ops",
              str(r.json.get("identity")))

    st, r = login_admin("admin-a", "admin-a-secret")
    if check("POST /api/v1/auth/login 客户管理员登录成功", st == 200 and "token" in (r.json or {}),
             f"status={st} body={r.body[:150]}"):
        tokens["admin_a"] = r.json["token"]
        ident = r.json.get("identity", {})
        check("管理员登录返回 tenant_id=tenant-a", ident.get("tenant_id") == "tenant-a", str(ident))
        check("管理员登录返回 role=tenant_admin", ident.get("role") == "tenant_admin", str(ident))

    st, r = login_admin("admin-b", "admin-b-secret")
    if check("POST /api/v1/auth/login 另一租户管理员登录成功", st == 200, f"status={st}"):
        tokens["admin_b"] = r.json["token"]

    st, r = login_admin("admin-a", "wrong-password")
    check("错误口令登录返回 401", st == 401, f"status={st} body={r.body[:120]}")

    st, r = login_admin("no-such-user", "x")
    check("不存在账号登录返回 401", st == 401, f"status={st}")

    r = req("GET", "/api/v1/auth/whoami", token=tokens.get("admin_a"))
    check("GET /api/v1/auth/whoami 返回本角色身份",
          r.status == 200 and r.json.get("role") == "tenant_admin" and r.json.get("tenant_id") == "tenant-a",
          f"status={r.status} body={r.body[:150]}")

    r = req("GET", "/api/v1/auth/whoami")
    check("未带令牌访问 whoami 返回 401", r.status == 401, f"status={r.status}")

    r = req("GET", "/api/v1/auth/whoami", token="not-a-real-token")
    check("无效令牌访问 whoami 返回 401", r.status == 401, f"status={r.status}")

    r = req("GET", "/api/v1/auth/login", token=tokens.get("ops"))
    check("GET 方式访问 login 返回 405", r.status == 405, f"status={r.status}")

    r = req("POST", "/api/v1/auth/login", raw_body="{不是合法 JSON", content_type="application/json")
    check("非法 JSON 登录体返回 400", r.status == 400, f"status={r.status} body={r.body[:120]}")

    # 登出：用一次性令牌验证会话失效
    st, r = login_admin("admin-b", "admin-b-secret")
    tmp = r.json["token"]
    r = req("POST", "/api/v1/auth/logout", token=tmp)
    check("POST /api/v1/auth/logout 返回 200", r.status == 200, f"status={r.status}")
    r = req("GET", "/api/v1/auth/whoami", token=tmp)
    check("登出后令牌失效返回 401", r.status == 401, f"status={r.status}")
    return tokens


def phase_ops(tokens: dict) -> dict:
    ops = tokens["ops"]
    admin_a = tokens["admin_a"]
    out = {}

    r = req("GET", "/api/v1/ops/tenants", token=ops)
    tenants = (r.json or {}).get("tenants", [])
    ids = [t.get("id") for t in tenants] if isinstance(tenants, list) else []
    check("GET /api/v1/ops/tenants 列出预置租户",
          r.status == 200 and "tenant-a" in ids and "tenant-b" in ids, f"status={r.status} ids={ids}")
    check("租户列表返回数组而非 null", isinstance(tenants, list), f"type={type(tenants).__name__}")

    r = req("GET", "/api/v1/ops/tenants", token=admin_a)
    check("客户管理员访问运营接口返回 403", r.status == 403, f"status={r.status}")

    r = req("GET", "/api/v1/ops/tenants")
    check("未认证访问运营接口返回 401", r.status == 401, f"status={r.status}")

    # 开通新租户
    r = req("POST", "/api/v1/ops/tenants/open", token=ops,
            body={"tenant_id": "", "name": "接口测试租户", "daily_quota": 5, "reason": "接口能力测试"})
    ok = check("POST /api/v1/ops/tenants/open 开通新租户",
               r.status == 200 and (r.json or {}).get("tenant", {}).get("id"),
               f"status={r.status} body={r.body[:200]}")
    if ok:
        out["new_tenant"] = r.json["tenant"]["id"]

    r = req("POST", "/api/v1/ops/tenants/open", token=ops,
            body={"tenant_id": "", "name": "", "daily_quota": 5, "reason": "x"})
    check("开通租户缺名称返回 400", r.status == 400 and "名称" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/ops/tenants/open", token=ops,
            body={"tenant_id": "", "name": "越界租户", "daily_quota": 100001, "reason": "x"})
    check("开通租户额度超过上限返回 400", r.status == 400 and "范围" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/ops/tenants/open", token=ops,
            body={"tenant_id": "", "name": "负额度租户", "daily_quota": -1, "reason": "x"})
    check("开通租户负额度返回 400", r.status == 400, f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/ops/tenants/open", token=ops,
            body={"tenant_id": "", "name": "未知字段租户", "daily_quota": 5, "reason": "x", "who": "me"})
    check("JSON 未知字段被拒绝返回 400", r.status == 400, f"status={r.status} body={r.body[:120]}")

    # 调整额度
    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-a", "daily_quota": 12, "reason": "接口能力测试调整额度"})
    check("POST /api/v1/ops/tenants/quota 调整额度成功",
          r.status == 200 and (r.json or {}).get("change", {}).get("new_quota") == 12,
          f"status={r.status} body={r.body[:200]}")

    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-a", "daily_quota": 12, "reason": ""})
    check("调整额度缺原因返回 400", r.status == 400 and "原因" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-a", "daily_quota": 999999, "reason": "越界"})
    check("调整额度越界返回 400", r.status == 400, f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-not-exist", "daily_quota": 5, "reason": "x"})
    check("调整不存在的租户额度返回 404", r.status == 404, f"status={r.status} err={err_of(r)}")

    # 日终报表
    r = req("GET", "/api/v1/ops/report/daily", token=ops)
    check("运营日报未指定 tenant_id 返回 400", r.status == 400 and "tenant_id" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("GET", "/api/v1/ops/report/daily?tenant_id=tenant-a", token=ops)
    rep = (r.json or {}).get("report", {})
    counts = rep.get("counts", {}) if isinstance(rep, dict) else {}
    check("GET /api/v1/ops/report/daily 返回日报", r.status == 200 and rep.get("tenant_id") == "tenant-a",
          f"status={r.status} body={r.body[:200]}")
    check("日报列出五类终态（成功/失败/超时/处理中/被拒绝）",
          all(k in counts for k in ("success", "failed", "timeout", "processing", "rejected")),
          f"counts={list(counts.keys())}")
    check("日报一致性标记为一致", rep.get("consistent") is True, f"note={rep.get('mismatch_note')}")
    check("日报包含额度与剩余额度字段",
          isinstance(rep.get("quota_limit"), int) and isinstance(rep.get("remaining"), int),
          f"quota_limit={rep.get('quota_limit')} remaining={rep.get('remaining')}")

    r = req("GET", "/api/v1/ops/report/daily?tenant_id=tenant-a", token=admin_a)
    check("客户管理员访问运营专属日报接口返回 403", r.status == 403,
          f"status={r.status} err={err_of(r)}")

    r = req("GET", "/api/v1/ops/report/daily?tenant_id=tenant-b", token=admin_a)
    check("客户管理员指定其他租户访问运营接口返回 403（角色拦截优先于租户范围）",
          r.status == 403, f"status={r.status} err={err_of(r)}")

    # 诊断记录
    r = req("GET", "/api/v1/ops/diagnostics?tenant_id=tenant-a", token=ops)
    diags = (r.json or {}).get("diagnostics")
    check("GET /api/v1/ops/diagnostics 返回数组", r.status == 200 and isinstance(diags, list),
          f"status={r.status} type={type(diags).__name__}")

    r = req("GET", "/api/v1/ops/diagnostics", token=ops)
    check("运营诊断未指定 tenant_id 返回 400", r.status == 400, f"status={r.status}")

    # 教学时钟
    r = req("POST", "/api/v1/ops/clock", token=ops, body={"offset_seconds": 3600})
    check("POST /api/v1/ops/clock 调整时钟偏移成功",
          r.status == 200 and (r.json or {}).get("offset_seconds") == 3600,
          f"status={r.status} body={r.body[:150]}")
    r = req("POST", "/api/v1/ops/clock", token=ops, body={"offset_seconds": 0})
    check("POST /api/v1/ops/clock 复位时钟成功",
          r.status == 200 and (r.json or {}).get("offset_seconds") == 0,
          f"status={r.status} body={r.body[:150]}")
    r = req("POST", "/api/v1/ops/clock", token=admin_a, body={"offset_seconds": 60})
    check("客户管理员调整时钟返回 403", r.status == 403, f"status={r.status}")
    return out


def phase_admin(tokens: dict) -> dict:
    admin_a = tokens["admin_a"]
    admin_b = tokens["admin_b"]
    ops = tokens["ops"]
    out = {"secrets": {}, "credential_ids": {}}

    r = req("GET", "/api/v1/admin/credentials", token=admin_a)
    creds = (r.json or {}).get("credentials")
    check("GET /api/v1/admin/credentials 返回本租户凭证数组",
          r.status == 200 and isinstance(creds, list), f"status={r.status} type={type(creds).__name__}")

    r = req("POST", "/api/v1/admin/credentials/create", token=admin_a, body={"name": "应用一"})
    ok = check("POST /api/v1/admin/credentials/create 创建凭证一",
               r.status == 200 and bool((r.json or {}).get("secret")),
               f"status={r.status} body={r.body[:200]}")
    if ok:
        out["secrets"]["cred1"] = r.json["secret"]
        out["credential_ids"]["cred1"] = r.json["credential"]["id"]
        check("凭证一归属 tenant-a", r.json["credential"].get("tenant_id") == "tenant-a",
              str(r.json["credential"]))
        check("凭证响应不含密钥摘要字段", "key_hash" not in r.json["credential"], str(r.json["credential"]))

    r = req("POST", "/api/v1/admin/credentials/create", token=admin_a, body={"name": "应用二"})
    ok = check("POST /api/v1/admin/credentials/create 创建凭证二", r.status == 200,
               f"status={r.status} body={r.body[:200]}")
    if ok:
        out["secrets"]["cred2"] = r.json["secret"]
        out["credential_ids"]["cred2"] = r.json["credential"]["id"]

    r = req("POST", "/api/v1/admin/credentials/create", token=admin_a, body={"name": "应用三"})
    check("超过租户凭证上限（2 个）返回 400", r.status == 400 and "上限" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/admin/credentials/create", token=admin_a,
            body={"name": "越权凭证", "tenant_id": "tenant-b"})
    check("创建凭证指定其他租户返回 400", r.status == 400 and "租户" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/admin/credentials/create", token=ops, body={"name": "运营代建凭证"})
    check("运营代客户创建凭证返回 403", r.status == 403, f"status={r.status} err={err_of(r)}")

    r = req("GET", "/api/v1/admin/usage", token=admin_a)
    usage = (r.json or {}).get("usage", {})
    check("GET /api/v1/admin/usage 返回本租户用量口径",
          r.status == 200 and usage.get("tenant_id") == "tenant-a",
          f"status={r.status} body={r.body[:200]}")
    check("用量返回额度/成功/处理中/占用/剩余五项",
          all(k in usage for k in ("quota_limit", "success", "in_flight", "occupied", "remaining")),
          f"keys={list(usage.keys())}")

    r = req("GET", "/api/v1/admin/usage?tenant_id=tenant-b", token=admin_a)
    check("管理员查看其他租户用量返回 400", r.status == 400, f"status={r.status} err={err_of(r)}")

    r = req("GET", "/api/v1/admin/requests", token=admin_a)
    body = r.json or {}
    check("GET /api/v1/admin/requests 返回请求与被拒绝两个数组",
          r.status == 200 and isinstance(body.get("requests"), list) and isinstance(body.get("rejected"), list),
          f"status={r.status} body={r.body[:200]}")

    # 表单通道：HTML 表单创建凭证（同一业务逻辑的另一条通道）
    r = req("POST", "/api/v1/admin/credentials/create", token=admin_b,
            form={"name": "表单通道凭证"})
    check("表单通道创建凭证返回 200 且页面含一次性密钥",
          r.status == 200 and "sk_" in r.body, f"status={r.status} body={r.body[:200]}")
    check("表单通道返回 HTML 页面", "text/html" in r.headers.get("Content-Type", ""),
          r.headers.get("Content-Type", ""))

    # 停用凭证（tenant-b 的表单凭证需要先拿到 ID）
    r = req("GET", "/api/v1/admin/credentials", token=admin_b)
    b_creds = (r.json or {}).get("credentials", [])
    b_cred_id = b_creds[0]["id"] if b_creds else ""
    if b_cred_id:
        r = req("POST", "/api/v1/admin/credentials/disable", token=admin_b,
                body={"credential_id": b_cred_id, "reason": "接口能力测试停用"})
        check("POST /api/v1/admin/credentials/disable 停用本租户凭证成功",
              r.status == 200, f"status={r.status} body={r.body[:200]}")
        r = req("POST", "/api/v1/admin/credentials/disable", token=admin_a,
                body={"credential_id": b_cred_id, "reason": "跨租户停用尝试"})
        check("停用其他租户凭证返回 400", r.status == 400, f"status={r.status} err={err_of(r)}")
        r = req("POST", "/api/v1/admin/credentials/disable", token=admin_a,
                body={"credential_id": "cred-not-exist", "reason": "x"})
        check("停用不存在的凭证返回 404", r.status == 404, f"status={r.status} err={err_of(r)}")
    else:
        check("获取 tenant-b 凭证用于停用测试", False, "未取得凭证列表")
    return out


def phase_app(tokens: dict, admin: dict) -> None:
    secrets = admin["secrets"]
    if "cred1" not in secrets:
        check("应用阶段前置条件（凭证密钥）", False, "未创建出凭证密钥")
        return

    st, r = login_secret(secrets["cred1"])
    if not check("客户应用以凭证密钥登录成功", st == 200 and "token" in (r.json or {}),
                 f"status={st} body={r.body[:150]}"):
        return
    tok1 = r.json["token"]
    check("凭证登录返回 subject_type=credential",
          (r.json.get("identity") or {}).get("subject_type") == "credential",
          str(r.json.get("identity")))

    st, r = login_secret("sk_not_exist")
    check("无效密钥登录返回 401", st == 401, f"status={st}")

    # 成功调用
    r = req("POST", "/api/v1/app/invoke", token=tok1,
            body={"request_no": "req-ok-1", "model": "m1", "prompt": "hello"})
    ok = check("POST /api/v1/app/invoke 成功受理调用", r.status == 200,
               f"status={r.status} body={r.body[:250]}")
    if ok:
        check("成功调用终态为 success",
              (r.json.get("request") or {}).get("status") == "success",
              str(r.json.get("request")))
        check("成功调用不标记为幂等重放", r.json.get("replayed") is False, str(r.json.get("replayed")))
        check("成功调用返回用量口径", "usage" in r.json, str(list(r.json.keys())))

    # 幂等命中
    r2 = req("POST", "/api/v1/app/invoke", token=tok1,
             body={"request_no": "req-ok-1", "model": "m1", "prompt": "hello"})
    check("同编号同参数再次调用按幂等处理（replayed=true）",
          r2.status == 200 and r2.json.get("replayed") is True,
          f"status={r2.status} body={r2.body[:250]}")
    if r2.status == 200 and r.json.get("usage"):
        check("幂等命中不重复计量（成功数不增加）",
              r2.json["usage"]["success"] == r.json["usage"]["success"],
              f"before={r.json['usage']} after={r2.json['usage']}")

    # 同编号异参数
    r3 = req("POST", "/api/v1/app/invoke", token=tok1,
             body={"request_no": "req-ok-1", "model": "m1", "prompt": "different"})
    check("同编号异参数返回 409", r3.status == 409, f"status={r3.status} err={err_of(r3)}")

    # 参数缺失与非法
    r = req("POST", "/api/v1/app/invoke", token=tok1, body={"model": "m1", "prompt": "x"})
    check("调用缺请求编号返回 400", r.status == 400 and "编号" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/app/invoke", token=tok1,
            body={"request_no": "非法 编号!", "model": "m1", "prompt": "x"})
    check("非法请求编号返回 400", r.status == 400 and "格式" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/app/invoke", token=tokens["admin_a"],
            body={"request_no": "req-admin-1", "model": "m1", "prompt": "x"})
    check("管理员令牌调用应用接口返回 403", r.status == 403, f"status={r.status}")

    # 上游明确失败
    r = req("POST", "/api/v1/app/invoke", token=tok1,
            body={"request_no": "req-fail-1", "model": "m1", "prompt": "x", "scenario": "fail"})
    check("上游失败场景终态为 failed",
          r.status == 200 and (r.json.get("request") or {}).get("status") == "failed",
          f"status={r.status} body={r.body[:250]}")
    check("失败不计入成功量", (r.json.get("usage") or {}).get("success") == 1,
          str(r.json.get("usage")))

    # 上游超时（2 秒判定）
    t0 = time.time()
    r = req("POST", "/api/v1/app/invoke", token=tok1,
            body={"request_no": "req-timeout-1", "model": "m1", "prompt": "x", "scenario": "timeout"})
    elapsed = time.time() - t0
    check("上游超时场景终态为 timeout",
          r.status == 200 and (r.json.get("request") or {}).get("status") == "timeout",
          f"status={r.status} body={r.body[:250]}")
    check("超时判定在 2 秒后返回（实测 %.1f 秒）" % elapsed, 1.5 <= elapsed <= 10, f"elapsed={elapsed:.2f}")
    check("超时释放额度（处理中为 0）", (r.json.get("usage") or {}).get("in_flight") == 0,
          str(r.json.get("usage")))

    # 迟到结果（上游 3 秒后才返回）
    r = req("POST", "/api/v1/app/invoke", token=tok1,
            body={"request_no": "req-late-1", "model": "m1", "prompt": "x", "scenario": "slow_success"})
    check("迟到结果场景终态仍为 timeout",
          r.status == 200 and (r.json.get("request") or {}).get("status") == "timeout",
          f"status={r.status} body={r.body[:250]}")
    time.sleep(2.5)
    r = req("GET", "/api/v1/app/request?request_no=req-late-1", token=tok1)
    diags = (r.json or {}).get("diagnostics")
    check("迟到结果只写入诊断记录（不改终态、不追加扣额）",
          isinstance(diags, list) and len(diags) >= 1 and diags[0].get("kind") == "late_result",
          f"status={r.status} diags={diags}")
    check("迟到诊断后请求终态仍是 timeout",
          ((r.json or {}).get("request") or {}).get("status") == "timeout",
          str((r.json or {}).get("request")))

    # 应用侧查询
    r = req("GET", "/api/v1/app/requests", token=tok1)
    reqs = (r.json or {}).get("requests")
    check("GET /api/v1/app/requests 返回本凭证请求数组",
          r.status == 200 and isinstance(reqs, list) and len(reqs) >= 1,
          f"status={r.status} type={type(reqs).__name__}")
    check("应用侧请求列表不含其他凭证的请求",
          all(x.get("credential_id") == admin["credential_ids"].get("cred1") for x in reqs),
          str([x.get("credential_id") for x in reqs]) if isinstance(reqs, list) else "n/a")

    r = req("GET", "/api/v1/app/request?request_no=req-ok-1", token=tok1)
    check("GET /api/v1/app/request 查询本凭证请求成功", r.status == 200, f"status={r.status}")

    r = req("GET", "/api/v1/app/request", token=tok1)
    check("查询请求缺 request_no 返回 400", r.status == 400, f"status={r.status} err={err_of(r)}")

    r = req("GET", "/api/v1/app/request?request_no=not-exist", token=tok1)
    check("查询不存在的请求返回 404", r.status == 404, f"status={r.status} err={err_of(r)}")

    # 同租户另一凭证越权查询
    st, r = login_secret(secrets.get("cred2", ""))
    if st == 200:
        tok2 = r.json["token"]
        r = req("GET", "/api/v1/app/request?request_no=req-ok-1", token=tok2)
        check("同租户其他凭证查询他人请求返回 403", r.status == 403,
              f"status={r.status} err={err_of(r)}")
        r = req("GET", "/api/v1/admin/usage", token=tok2)
        check("应用凭证访问租户级用量接口返回 403", r.status == 403, f"status={r.status}")
        r = req("GET", "/api/v1/ops/report/daily?tenant_id=tenant-a", token=tok2)
        check("应用凭证访问运营日报返回 403", r.status == 403, f"status={r.status}")
    else:
        check("获取第二个凭证令牌", False, f"status={st}")


def phase_quota_exhaust(tokens: dict, admin: dict) -> None:
    """额度用尽 → 429；租户停用 → 409；凭证停用 → 409。"""
    ops = tokens["ops"]
    admin_a = tokens["admin_a"]
    secret = admin["secrets"]["cred1"]
    st, r = login_secret(secret)
    if st != 200:
        check("额度阶段重新登录凭证", False, f"status={st}")
        return
    tok = r.json["token"]

    # 降额到当前占用（合法），再触发额度用尽
    r = req("GET", "/api/v1/admin/usage", token=admin_a)
    occupied = (r.json or {}).get("usage", {}).get("occupied", 0)
    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-a", "daily_quota": occupied, "reason": "压到当前占用以验证额度用尽"})
    check("降额到当前占用值成功", r.status == 200, f"status={r.status} body={r.body[:200]}")

    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-a", "daily_quota": occupied - 1 if occupied > 0 else 0,
                  "reason": "低于占用应被拒绝"})
    check("降额低于当日占用返回 400（下限约束）", r.status == 400 and "不得低于" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("POST", "/api/v1/app/invoke", token=tok,
            body={"request_no": "req-over-quota", "model": "m1", "prompt": "x"})
    check("额度用尽返回 429", r.status == 429 and "额度" in err_of(r),
          f"status={r.status} err={err_of(r)}")

    r = req("GET", "/api/v1/admin/requests", token=admin_a)
    rejected = (r.json or {}).get("rejected", [])
    check("被拒绝的尝试写入独立记录（rejected_attempts）",
          isinstance(rejected, list) and any(x.get("request_no") == "req-over-quota" for x in rejected),
          f"count={len(rejected) if isinstance(rejected, list) else 'n/a'}")

    # 恢复额度后，同一编号可重试（R8/Q7）
    r = req("POST", "/api/v1/ops/tenants/quota", token=ops,
            body={"tenant_id": "tenant-a", "daily_quota": 10, "reason": "恢复额度"})
    check("恢复额度成功", r.status == 200, f"status={r.status}")

    r = req("POST", "/api/v1/app/invoke", token=tok,
            body={"request_no": "req-over-quota", "model": "m1", "prompt": "x"})
    check("增额后可用同一编号重试（拒绝不占用编号唯一性）", r.status == 200,
          f"status={r.status} err={err_of(r)}")

    # 停用凭证 → 不再受理新调用
    cred_id = admin["credential_ids"]["cred1"]
    r = req("POST", "/api/v1/admin/credentials/disable", token=admin_a,
            body={"credential_id": cred_id, "reason": "接口能力测试停用凭证"})
    check("停用凭证成功", r.status == 200, f"status={r.status}")

    st, r = login_secret(admin["secrets"]["cred1"])
    if st == 200:
        tok = r.json["token"]
        r = req("POST", "/api/v1/app/invoke", token=tok,
                body={"request_no": "req-after-cred-disable", "model": "m1", "prompt": "x"})
        check("凭证停用后调用返回 409", r.status == 409 and "凭证" in err_of(r),
              f"status={r.status} err={err_of(r)}")
    else:
        check("停用后重新登录凭证应被拒绝", st == 401, f"status={st}")

    # 停用租户 → 不再受理新调用（用 tenant-b 的凭据验证）
    st, r = login_admin("admin-b", "admin-b-secret")
    if st == 200:
        tb = r.json["token"]
        r = req("POST", "/api/v1/admin/credentials/create", token=tb, body={"name": "停用前凭证"})
        if r.status == 200:
            sec = r.json["secret"]
            cid = r.json["credential"]["id"]
            r = req("POST", "/api/v1/ops/tenants/disable", token=tokens["ops"],
                    body={"tenant_id": "tenant-b", "reason": "接口能力测试停用租户"})
            check("POST /api/v1/ops/tenants/disable 停用租户成功", r.status == 200,
                  f"status={r.status} body={r.body[:200]}")
            r = req("POST", "/api/v1/ops/tenants/disable", token=tokens["ops"],
                    body={"tenant_id": "tenant-b", "reason": ""})
            check("停用租户缺原因返回 400", r.status == 400 and "原因" in err_of(r),
                  f"status={r.status} err={err_of(r)}")

            st, r = login_secret(sec)
            if st == 200:
                tok = r.json["token"]
                r = req("POST", "/api/v1/app/invoke", token=tok,
                        body={"request_no": "req-after-tenant-disable", "model": "m1", "prompt": "x"})
                check("租户停用后调用返回 409", r.status == 409 and "租户" in err_of(r),
                      f"status={r.status} err={err_of(r)}")
            else:
                check("租户停用后凭证登录应被拒绝", st == 401, f"status={st}")

            # 重新开通租户后可再次受理
            r = req("POST", "/api/v1/ops/tenants/open", token=tokens["ops"],
                    body={"tenant_id": "tenant-b", "name": "企业乙（凭证泄露客户）", "daily_quota": 10})
            check("重新开通已停用租户成功", r.status == 200, f"status={r.status} body={r.body[:200]}")
            st, r = login_secret(sec)
            if st == 200:
                tok = r.json["token"]
                r = req("POST", "/api/v1/app/invoke", token=tok,
                        body={"request_no": "req-after-reopen", "model": "m1", "prompt": "x"})
                check("租户重新开通后可受理调用", r.status == 200, f"status={r.status} err={err_of(r)}")
            else:
                check("租户重新开通后凭证可登录", st == 200, f"status={st}")
            _ = cid
        else:
            check("为 tenant-b 创建凭证", False, f"status={r.status}")


def phase_pages(tokens: dict) -> None:
    ops = tokens["ops"]
    admin_a = tokens["admin_a"]

    r = req("GET", "/login")
    check("GET /login 返回登录页", r.status == 200 and "登录" in r.body,
          f"status={r.status}")
    check("登录页不回显口令明文", "ops-secret" not in r.body and "admin-a-secret" not in r.body,
          "登录页出现明文口令")

    r = req("GET", "/")
    check("未登录访问 / 重定向到 /login",
          r.status == 303 and "/login" in r.headers.get("Location", ""),
          f"status={r.status} loc={r.headers.get('Location')}")

    r = req("GET", "/ops", token=ops)
    check("GET /ops 运营工作台返回 200", r.status == 200 and "运营" in r.body,
          f"status={r.status}")

    r = req("GET", "/ops", token=admin_a)
    check("客户管理员访问 /ops 重定向离开", r.status == 303, f"status={r.status}")

    r = req("GET", "/admin", token=admin_a)
    check("GET /admin 客户管理员工作台返回 200", r.status == 200, f"status={r.status}")

    r = req("GET", "/app", token=admin_a)
    check("管理员访问 /app 重定向离开", r.status == 303, f"status={r.status}")

    r = req("GET", "/no-such-page")
    check("访问不存在的页面返回 404", r.status == 404, f"status={r.status}")

    r = req("GET", "/")
    check("未登录访问根路径返回 303", r.status == 303, f"status={r.status}")


def phase_cross_role() -> None:
    """越权矩阵：无令牌 / 错误角色访问各角色专属接口。"""
    r = req("GET", "/api/v1/admin/credentials")
    check("未认证访问管理员接口返回 401", r.status == 401, f"status={r.status}")
    r = req("GET", "/api/v1/app/requests")
    check("未认证访问应用接口返回 401", r.status == 401, f"status={r.status}")
    r = req("POST", "/api/v1/ops/tenants/open", body={"name": "x", "daily_quota": 1})
    check("未认证开通租户返回 401", r.status == 401, f"status={r.status}")


def main() -> int:
    global BASE
    ap = argparse.ArgumentParser(description="题目 05 全量接口能力测试")
    ap.add_argument("--base-url", default=os.environ.get("BASE_URL", BASE))
    ap.add_argument("--out", default="evidence/api-full-test.log")
    args = ap.parse_args()
    BASE = args.base_url.rstrip("/")

    print(f"[api-full-test] 被测地址：{BASE}")
    try:
        probe = req("GET", "/healthz")
    except Exception as e:
        print(f"[api-full-test] 服务不可达: {e}", file=sys.stderr)
        return 2
    if probe.status != 200:
        print(f"[api-full-test] 健康检查失败: status={probe.status}", file=sys.stderr)
        return 2

    phase_health()
    tokens = phase_auth()
    phase_cross_role()
    phase_ops(tokens)
    admin = phase_admin(tokens)
    phase_app(tokens, admin)
    phase_quota_exhaust(tokens, admin)
    phase_pages(tokens)

    started = datetime.now(CST)
    total = len(PASSED) + len(FAILED)
    print("=" * 78)
    print(f"[api-full-test] 断言总数：{total}，通过 {len(PASSED)}，失败 {len(FAILED)}")

    out_path = args.out
    if not os.path.isabs(out_path):
        out_path = os.path.join(REPO, out_path)
    os.makedirs(os.path.dirname(out_path), exist_ok=True)
    header = [
        "题目 05 全量接口能力测试归档",
        f"执行时间：{started.isoformat()}",
        f"被测地址：{BASE}",
        "执行命令：python scripts/api-full-test.py",
        f"断言总数：{total}，通过 {len(PASSED)}，失败 {len(FAILED)}",
        "-" * 78,
        "",
    ]
    body = "\n".join(header) + "\n".join(
        [f"[PASS] {n}" for n in PASSED] + [f"[FAIL] {n}" for n in FAILED]
    ) + "\n"
    with open(out_path, "w", encoding="utf-8", newline="\n") as f:
        f.write(body)
    print(f"[api-full-test] 归档：{out_path}")
    return 1 if FAILED else 0


if __name__ == "__main__":
    raise SystemExit(main())
