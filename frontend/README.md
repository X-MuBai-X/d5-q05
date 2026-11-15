# 配额治理后台 · 前端（题目 05）

Vite + React 19 + TypeScript 5.7 + Tailwind CSS v4 + lucide-react。
单一 B 端后台：顶栏中间是账号切换器，账号决定角色，角色决定可见页面集合与数据可见范围。

## 目录

```
src/
  api/client.ts      统一 API 客户端（Bearer 令牌、错误码归一）
  api/compat.ts      契约/实现两种线格式的兼容层
  api/endpoints.ts   全部接口端点
  api/normalize.ts   后端报文 → 前端视图模型归一化
  context/SessionContext.tsx  账号 / 会话 / 角色
  components/shell/  AppShell（顶栏 + 账号切换器 + 侧栏）、ui.tsx（UI 原子组件）
  components/pages/  图 01 / 图 02 / 图 03 / 图 04 / 图 05 / 图 06 与登录页
  hooks/useAsyncData.ts
  types/index.ts
```

## 运行

```bash
npm install          # 若本机无法访问 registry，见下方「依赖说明」
npm run dev          # http://127.0.0.1:8443，/api 代理到 http://127.0.0.1:8080
npm run build        # 产物输出到 frontend/dist，由 Go 以 -web-dir 目录托管
npm run preview
```

## 依赖说明（当前机器）

本机无法访问 npm registry（`npm ping` 超时），因此 `node_modules` 是指向参考工程
`C:\Users\26765\Downloads\企业模型服务配额管理\node_modules` 的**目录联接（junction）**，
不是复制品，也已被 `frontend/.gitignore` 忽略。网络恢复后请执行：

```bash
rm node_modules        # Windows 下可用 mavis-trash / rm -- 回收
npm install
```

版本与参考工程完全一致：react 19 / vite 8 / tailwindcss 4 / typescript 5.7 / lucide-react 1.73。

## 与接口契约的对齐

语义一律以 `docs/19-前后端接口契约.md` 为准。现有 Go 实现在少数位置与契约文档字面不同，
`src/api/compat.ts` 用「线格式变体」同时兼容两种写法（当前实现写法优先，契约写法兜底）：

| 场景 | 契约文档 | 现有 Go 实现 |
| --- | --- | --- |
| 开通租户 | `POST /api/v1/ops/tenants` | `POST /api/v1/ops/tenants/open` |
| 调用编号字段 | `client_req_id` | `request_no` |
| 单条请求查询参数 | `?client_req_id=` | `?request_no=` |
| 日期查询参数 | `date` | `day`（前端两个都发） |

其余路径、请求体、错误码、权限矩阵、角色→页面映射与契约一致。

## 前端不做业务判定

额度是否够、能否越权、能否创建第 3 个凭证，全部由后端裁决：
前端不隐藏、不禁用任何操作来「模拟」越权阻断，只把后端返回的 `code` 与 `message` 原样展示
（`src/api/client.ts` 的 `deriveCode` 仅在后端未返回 `code` 时，按状态码与文案特征回退推导）。
