# 配额治理后台 · 前端（题目 05）

Vite 8 + React 19 + TypeScript 5.7 + Tailwind CSS v4 + lucide-react。
单一 B 端后台：顶栏中间是账号切换器，账号决定角色，角色决定可见页面集合与数据可见范围。

本工程是**自包含**的：源码、依赖锁文件与构建配置都在 `d5/frontend/` 内，
**不再依赖任何外部工程目录**（早期版本曾把 `node_modules` 做成指向
`Downloads\企业模型服务配额管理\node_modules` 的目录联接，现已改为本仓库内真实安装）。

## 目录

```
src/
  api/client.ts      统一 API 客户端（Bearer 令牌、错误码归一）
  api/compat.ts      契约/实现两种线格式的兼容层
  api/endpoints.ts   全部接口端点
  api/normalize.ts   后端报文 → 前端视图模型归一化
  context/SessionContext.tsx  账号 / 会话 / 角色（真实账号口令登录 + 密钥登录）
  components/shell/  AppShell（顶栏 + 账号切换器 + 侧栏）、ui.tsx（UI 原子组件）
  components/pages/  图 01 / 图 02 / 图 03 / 图 04 / 图 05 / 图 06 与登录页
  hooks/useAsyncData.ts
  types/index.ts
```

## 运行

```bash
npm install          # 依赖已由 package-lock.json 锁定
npm run dev          # http://127.0.0.1:8443，/api 代理到 http://127.0.0.1:8080
npm run build        # 产物输出到 frontend/dist，由 Go 以 -web-dir 目录托管
npm run preview
```

### 依赖安装注意事项（当前机器）

本机环境变量里 `HTTP(S)_PROXY` 指向 `127.0.0.1:7890`，而该代理当前**未运行**，
`npm` 会自动使用它并报连接被拒。**直连 registry 是通的**，因此安装/构建前请清掉代理变量：

```bash
# PowerShell
$env:HTTP_PROXY=''; $env:HTTPS_PROXY=''; $env:http_proxy=''; $env:https_proxy=''
npm install
```

> 2026-11-15 实测：清掉代理后 `npm ping` 返回 PONG，`npm install` 成功装入 48 个包，
> 并生成了 `package-lock.json`（49 KB，已入库，锁定依赖版本）。

## 登录页（两种登录方式，可自由切换）

与真实网关场景一致，**默认展示「用户登录」**：

| 方式 | 入口 | 说明 |
|---|---|---|
| 用户登录 | 默认页 | 运营 / 客户管理员输入**账号 + 口令** |
| 访问密钥登录 | 用户登录页**登录按钮正下方**的超链接 | 客户应用输入管理员创建凭证时拿到的 `sk-` 密钥 |

- 两个页面各自在登录按钮下方提供切换超链接（`使用访问密钥登录` / `返回用户登录`）；
- **口令与密钥都交给后端校验**，前端不预置口令、不做本地比对；
- 登录成功后按后端返回的身份确定角色与数据范围，并在顶栏显示当前账号；
  不在预置演示账号表里的账号也会被正确显示（按身份现场生成账号条目）。

> 预置演示账号（`ops` / `admin-a` / `admin-b`）只用于**顶栏的账号切换器**（演示时快速换角色），
> 登录页不预置口令。

## 与接口契约的对齐

语义一律以 `docs/19-前后端接口契约.md` 为准。Go 实现已在下列位置与契约**统一**，
`src/api/compat.ts` 仍保留双写法兼容以兼容旧版后端：

| 场景 | 契约文档 | Go 实现 |
| --- | --- | --- |
| 开通租户 | `POST /api/v1/ops/tenants` | `POST /api/v1/ops/tenants`（另保留 `/open` 别名） |
| 调用编号字段 | `client_req_id` | `client_req_id`（另接受 `request_no`） |
| 单条请求查询参数 | `?client_req_id=` | 两者都接受 |
| 日期查询参数 | `date` | 两者都接受 |

其余路径、请求体、错误码、权限矩阵、角色→页面映射与契约一致。

## 前端不做业务判定

额度是否够、能否越权、能否创建第 3 个凭证，全部由后端裁决：
前端不隐藏、不禁用任何操作来「模拟」越权阻断，只把后端返回的 `code` 与 `message` 原样展示
（`src/api/client.ts` 的 `deriveCode` 仅在后端未返回 `code` 时，按状态码与文案特征回退推导）。
