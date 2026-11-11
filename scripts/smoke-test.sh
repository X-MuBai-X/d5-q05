#!/usr/bin/env bash
# 业务冒烟脚本：跑通题目 05 的核心业务闭环。
#
# 覆盖：
#   1. 健康检查
#   2. 运营：列租户、调整配额（原因必填）、停用租户
#   3. 客户管理员：创建凭证、查看用量
#   4. 应用凭证：发起调用、幂等重放、参数冲突、编号非法
#   5. 并发争抢最后一个额度名额：只能有一个成功
#   6. 日终核查报表：五类分列与一致性核对
#
# 用法：BASE_URL=http://127.0.0.1:8080 bash scripts/smoke-test.sh
# 退出码：0 = 全部通过，1 = 存在失败项。

set -u

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
OPS_USER="${OPS_USER:-ops}"
OPS_PASS="${OPS_PASS:-ops-secret}"
ADMIN_USER="${ADMIN_USER:-admin-a}"
ADMIN_PASS="${ADMIN_PASS:-admin-a-secret}"
TENANT_ID="${TENANT_ID:-tenant-a}"

# JSON 解析用的解释器：CI(ubuntu) 用 python3；本地 Windows 可能只有 python，
# 且 python3 可能是微软商店的占位符（存在但无法执行），因此要实际探测一次。
PY="python3"
if ! python3 -c "" >/dev/null 2>&1; then PY="python"; fi

SMK_DIR="$(mktemp -d)"
trap 'rm -rf "$SMK_DIR"' EXIT

PASSED=0
FAILED=0
API_CODE=""
API_BODY="$SMK_DIR/body"

ok()  { PASSED=$((PASSED + 1)); printf '  [PASS] %s\n' "$1"; }
bad() { FAILED=$((FAILED + 1)); printf '  [FAIL] %s\n' "$1"; if [ $# -gt 1 ]; then printf '         %s\n' "$2"; fi; }

# api <方法> <路径> [令牌] [请求体]
# 结果写入全局 API_CODE（HTTP 状态码）与 API_BODY（响应体文件路径）。
api() {
  local method="$1" path="$2" token="${3:-}" body="${4:-}"
  if [ -n "$token" ]; then
    if [ -n "$body" ]; then
      API_CODE=$(curl -sS -o "$API_BODY" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
        -H "Authorization: Bearer $token" -H 'Content-Type: application/json' --data-raw "$body")
    else
      API_CODE=$(curl -sS -o "$API_BODY" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
        -H "Authorization: Bearer $token" -H 'Content-Type: application/json')
    fi
  else
    if [ -n "$body" ]; then
      API_CODE=$(curl -sS -o "$API_BODY" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
        -H 'Content-Type: application/json' --data-raw "$body")
    else
      API_CODE=$(curl -sS -o "$API_BODY" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
        -H 'Content-Type: application/json')
    fi
  fi
}

# jsonq <json文件> <python表达式>  从 JSON 文件中取值（避免依赖 jq）。
# 注意两点：
#   1. 这里用文件而不是管道，因为下面的 heredoc 会占用标准输入；
#   2. 在 Git Bash(Windows) 下 /tmp/... 这类路径 Windows 版 Python 无法解析，
#      因此先用 cygpath 转换成 Windows 可识别的路径。
jsonq() {
  local path="$1" expr="$2"
  if command -v cygpath >/dev/null 2>&1; then
    path="$(cygpath -m "$path")"
  fi
  "$PY" - "$path" "$expr" <<'PYEOF'
import json, sys
path, expr = sys.argv[1], sys.argv[2]
try:
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
except Exception:  # noqa: BLE001
    print("")
    sys.exit(0)
try:
    print(eval(expr, {"d": data, "json": json}))  # noqa: S307 - 受控表达式
except Exception:  # noqa: BLE001
    print("")
PYEOF
}

# expect <标签> <期望码> <实际码>
expect() {
  if [ "$2" = "$3" ]; then ok "$1（HTTP $3）"; else bad "$1" "期望 HTTP $2，实际 $3"; fi
}

echo "=== 业务冒烟：$BASE_URL ==="

# ---------- 1. 健康检查 ----------
echo "[1] 健康检查"
API_CODE=$(curl -sS -o "$SMK_DIR/h" -w '%{http_code}' "$BASE_URL/healthz")
expect "服务健康检查" 200 "$API_CODE"
API_CODE=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE_URL/readyz")
expect "服务就绪检查" 200 "$API_CODE"

# ---------- 2. 登录 ----------
echo "[2] 登录与会话"
api POST /api/v1/auth/login "" "{\"username\":\"$OPS_USER\",\"password\":\"$OPS_PASS\"}"
expect "运营登录" 200 "$API_CODE"
OPS_TOKEN=$(jsonq "$API_BODY" 'd["token"]')
[ -n "$OPS_TOKEN" ] && ok "运营取得会话令牌" || bad "运营取得会话令牌" "token 为空"

api POST /api/v1/auth/login "" "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}"
expect "客户管理员登录" 200 "$API_CODE"
ADMIN_TOKEN=$(jsonq "$API_BODY" 'd["token"]')
[ -n "$ADMIN_TOKEN" ] && ok "客户管理员取得会话令牌" || bad "客户管理员取得会话令牌" "token 为空"

API_CODE=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE_URL/api/v1/ops/tenants")
expect "未认证访问被拒绝" 401 "$API_CODE"

api GET /api/v1/ops/tenants "$ADMIN_TOKEN"
expect "租户管理员访问运营接口被拒" 403 "$API_CODE"

# ---------- 3. 运营：租户与配额 ----------
echo "[3] 运营：租户与配额"
api GET /api/v1/ops/tenants "$OPS_TOKEN"
expect "列出租户" 200 "$API_CODE"
cp "$API_BODY" "$SMK_DIR/tenants.json"
COUNT=$(jsonq "$SMK_DIR/tenants.json" 'len(d["tenants"])')
if [ "${COUNT:-0}" -ge 2 ] 2>/dev/null; then ok "租户数量为 $COUNT"; else bad "租户数量" "实际 $COUNT"; fi

api POST /api/v1/ops/tenants/quota "$OPS_TOKEN" "{\"tenant_id\":\"$TENANT_ID\",\"daily_quota\":20,\"reason\":\"冒烟扩容\"}"
expect "调整额度（带原因）" 200 "$API_CODE"

api POST /api/v1/ops/tenants/quota "$OPS_TOKEN" "{\"tenant_id\":\"$TENANT_ID\",\"daily_quota\":20}"
expect "调整额度缺原因被拒" 400 "$API_CODE"

api POST /api/v1/ops/tenants/quota "$OPS_TOKEN" "{\"tenant_id\":\"$TENANT_ID\",\"daily_quota\":999999,\"reason\":\"越界\"}"
expect "额度越界被拒" 400 "$API_CODE"

# ---------- 4. 客户管理员：凭证 ----------
echo "[4] 客户管理员：凭证"
api POST /api/v1/admin/credentials/create "$ADMIN_TOKEN" '{"name":"smoke-app"}'
expect "创建凭证" 200 "$API_CODE"
cp "$API_BODY" "$SMK_DIR/cred.json"
SECRET=$(jsonq "$SMK_DIR/cred.json" 'd.get("secret","")')
[ -n "$SECRET" ] && ok "返回一次性密钥" || bad "返回一次性密钥" "secret 为空"

api POST /api/v1/admin/credentials/create "$ADMIN_TOKEN" '{"name":"越权","tenant_id":"tenant-b"}'
expect "指定其他租户创建凭证被拒" 400 "$API_CODE"

api POST /api/v1/admin/credentials/create "$OPS_TOKEN" '{"name":"运营代建"}'
expect "运营代建凭证被拒" 403 "$API_CODE"

# ---------- 5. 应用凭证：调用与计量 ----------
echo "[5] 应用凭证：调用与计量"
api POST /api/v1/auth/login "" "{\"secret\":\"$SECRET\"}"
expect "应用凭证登录" 200 "$API_CODE"
APP_TOKEN=$(jsonq "$API_BODY" 'd["token"]')
[ -n "$APP_TOKEN" ] && ok "应用取得会话令牌" || bad "应用取得会话令牌" "token 为空"

api POST /api/v1/app/invoke "$APP_TOKEN" '{"request_no":"smoke-1","model":"text-1","prompt":"你好"}'
expect "首次调用成功" 200 "$API_CODE"
cp "$API_BODY" "$SMK_DIR/invoke1.json"
ST=$(jsonq "$SMK_DIR/invoke1.json" 'd["request"]["status"]')
[ "$ST" = "success" ] && ok "终态为 success" || bad "终态" "实际 $ST"

api POST /api/v1/app/invoke "$APP_TOKEN" '{"request_no":"smoke-1","model":"text-1","prompt":"你好"}'
expect "相同编号相同参数幂等重放" 200 "$API_CODE"
cp "$API_BODY" "$SMK_DIR/invoke2.json"
REPLAYED=$(jsonq "$SMK_DIR/invoke2.json" 'd.get("replayed")')
[ "$REPLAYED" = "True" ] && ok "幂等命中标记为真" || bad "幂等命中标记" "实际 $REPLAYED"

api POST /api/v1/app/invoke "$APP_TOKEN" '{"request_no":"smoke-1","model":"text-1","prompt":"不同的提示词"}'
expect "相同编号不同参数被拒" 409 "$API_CODE"

api POST /api/v1/app/invoke "$APP_TOKEN" '{"model":"text-1","prompt":"缺少编号"}'
expect "缺少请求编号被拒" 400 "$API_CODE"

api POST /api/v1/app/invoke "$APP_TOKEN" '{"request_no":"非法 编号!","model":"text-1","prompt":"x"}'
expect "请求编号格式非法被拒" 400 "$API_CODE"

api GET "/api/v1/app/request?request_no=smoke-1" "$APP_TOKEN"
expect "应用查询本凭证请求" 200 "$API_CODE"

api GET /api/v1/admin/usage "$APP_TOKEN"
expect "应用访问管理员接口被拒" 403 "$API_CODE"

# ---------- 6. 用量与日终核查 ----------
echo "[6] 用量与日终核查"
api GET /api/v1/admin/usage "$ADMIN_TOKEN"
expect "查询本租户用量" 200 "$API_CODE"
cp "$API_BODY" "$SMK_DIR/usage.json"
SUCCESS=$(jsonq "$SMK_DIR/usage.json" 'd["usage"]["success"]')
if [ "${SUCCESS:-0}" -ge 1 ] 2>/dev/null; then ok "成功量已计数（$SUCCESS）"; else bad "成功量" "实际 $SUCCESS"; fi

api GET "/api/v1/ops/report/daily?tenant_id=$TENANT_ID" "$OPS_TOKEN"
expect "运营跨租户日终核查" 200 "$API_CODE"
cp "$API_BODY" "$SMK_DIR/report.json"
CONSISTENT=$(jsonq "$SMK_DIR/report.json" 'd["report"].get("consistent")')
[ "$CONSISTENT" = "True" ] && ok "成功记录数与用量次数一致" || bad "一致性核对" "实际 $CONSISTENT"
REJECTED=$(jsonq "$SMK_DIR/report.json" 'd["report"]["counts"].get("rejected", 0)')
if [ "${REJECTED:-0}" -ge 1 ] 2>/dev/null; then ok "被拒绝请求已分列统计（$REJECTED）"; else bad "被拒绝分列" "实际 $REJECTED"; fi

api GET /api/v1/ops/report/daily "$OPS_TOKEN"
expect "运营未指定租户被拒" 500 "$API_CODE"

# ---------- 7. 并发争抢最后一个额度 ----------
# 用另一个租户（tenant-b）做，避免受 tenant-a 已有用量影响；
# 这样并发请求面对的是一个「额度为 1、已用 0」的新租户。
echo "[7] 并发争抢最后一个额度名额"
TENANT_B="${TENANT_B:-tenant-b}"
api POST /api/v1/ops/tenants/quota "$OPS_TOKEN" "{\"tenant_id\":\"$TENANT_B\",\"daily_quota\":1,\"reason\":\"并发验证\"}"
api POST /api/v1/auth/login "" "{\"username\":\"${ADMIN_B_USER:-admin-b}\",\"password\":\"${ADMIN_B_PASS:-admin-b-secret}\"}"
ADMIN_B_TOKEN=$(jsonq "$API_BODY" 'd["token"]')
api POST /api/v1/admin/credentials/create "$ADMIN_B_TOKEN" '{"name":"smoke-conc-app"}'
expect "为并发验证创建凭证" 200 "$API_CODE"
CONC_SECRET=$(jsonq "$API_BODY" 'd.get("secret","")')
api POST /api/v1/auth/login "" "{\"secret\":\"$CONC_SECRET\"}"
CONC_TOKEN=$(jsonq "$API_BODY" 'd["token"]')

CONC_DIR="$SMK_DIR/conc"
mkdir -p "$CONC_DIR"
for i in $(seq 1 8); do
  (
    code=$(curl -sS -o "$CONC_DIR/$i.out" -w '%{http_code}' -X POST "$BASE_URL/api/v1/app/invoke" \
      -H "Authorization: Bearer $CONC_TOKEN" -H 'Content-Type: application/json' \
      --data-raw "{\"request_no\":\"conc-$i\",\"model\":\"text-1\",\"prompt\":\"并发\"}")
    echo "$code" > "$CONC_DIR/$i.code"
  ) &
done
wait
OK_N=$(cat "$CONC_DIR"/*.code | grep -c '^200$' || true)
REJ_N=$(cat "$CONC_DIR"/*.code | grep -c '^429$' || true)
if [ "$OK_N" = "1" ] && [ "$REJ_N" = "7" ]; then
  ok "额度为 1 时并发只有 1 个成功（成功 $OK_N，拒绝 $REJ_N）"
else
  bad "并发额度控制" "成功 $OK_N，拒绝 $REJ_N（期望 1/7）"
fi

# ---------- 8. 停用后不再受理 ----------
echo "[8] 停用后不再受理新调用"
api POST /api/v1/ops/tenants/disable "$OPS_TOKEN" "{\"tenant_id\":\"$TENANT_ID\",\"reason\":\"冒烟停用\"}"
expect "停用租户" 200 "$API_CODE"
api POST /api/v1/app/invoke "$APP_TOKEN" '{"request_no":"after-disable","model":"text-1","prompt":"x"}'
expect "停用后调用被拒" 409 "$API_CODE"
api POST /api/v1/ops/tenants/disable "$OPS_TOKEN" "{\"tenant_id\":\"$TENANT_ID\"}"
expect "停用缺原因被拒" 400 "$API_CODE"

echo
echo "=== 冒烟结果：通过 $PASSED 项，失败 $FAILED 项 ==="
[ "$FAILED" -eq 0 ] || exit 1
exit 0
