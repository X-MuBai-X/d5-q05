/* ==================================================================
 * 契约兼容层
 *
 * 背景：`docs/19-前后端接口契约.md` 是前后端的唯一约定，但现有 Go 实现
 * 在少数位置与契约文档的字面写法不同（例如：
 *   - 契约 `POST /api/v1/ops/tenants`      ↔ 实现 `POST /api/v1/ops/tenants/open`
 *   - 契约 字段 `client_req_id`            ↔ 实现 字段 `request_no`
 *   - 契约 查询参数 `date`                 ↔ 实现 查询参数 `day`
 *   - 契约 `GET /api/v1/app/request?client_req_id=` ↔ 实现 `?request_no=`
 * ).
 *
 * 为了让前端既能对齐契约、又能直接对接当前实现，每个操作在这里声明
 * 一组「线格式变体」：按顺序尝试，第一个真正匹配（2xx 且响应形状正确）
 * 的变体生效；只有「形状不匹配 / 路由不存在 / 参数被拒」才会继续尝试下一个，
 * 业务性拒绝（额度不足、越权、租户停用…）会原样抛出，不做重试。
 *
 * 该层只解决**报文形状**差异，不涉及任何业务判定。
 * ================================================================== */

import { ApiError, apiRequest, type HttpMethod, type RequestOptions } from './client';

export interface WireVariant {
  /** 变体说明，仅用于排查 */
  label: string;
  path: string;
  method: HttpMethod;
  body?: unknown;
  query?: Record<string, string | number | undefined | null>;
  /** 响应形状校验：返回 false 表示「路由通了但不是我要的响应」，应继续尝试下一变体 */
  accept?: (payload: unknown) => boolean;
}

/** 可以安全尝试下一个变体的状态码：这些响应不产生任何业务副作用。 */
const RETRYABLE_STATUS = new Set([400, 404, 405, 415, 422]);

export async function callVariants<T>(
  variants: WireVariant[],
  options: Omit<RequestOptions, 'method' | 'body' | 'query'> = {}
): Promise<T> {
  let mismatchError: ApiError | null = null;
  const errors: ApiError[] = [];

  for (const variant of variants) {
    try {
      const payload = await apiRequest<unknown>(variant.path, {
        ...options,
        method: variant.method,
        body: variant.body,
        query: variant.query
      });
      if (!variant.accept || variant.accept(payload)) {
        return payload as T;
      }
      // 路由存在但响应形状不符（例如命中了列表接口）：继续尝试下一个变体。
      mismatchError = new ApiError(
        'CONTRACT_MISMATCH',
        `响应结构与预期不符（${variant.label}）`,
        200,
        payload
      );
    } catch (e) {
      if (e instanceof ApiError && isRetryable(e)) {
        errors.push(e);
        continue;
      }
      throw e;
    }
  }

  // 抛错优先级：先给出「真实业务错误」，其次才是「报文形状不匹配」类错误。
  // 典型场景：GET /api/v1/app/request 用错查询参数名时，
  // 第一个变体返回 NOT_FOUND（真实结论），第二个变体可能返回 INVALID_PARAMS；
  // 此时应当把 NOT_FOUND 抛给页面，而不是被后续变体的参数错误覆盖。
  const realError = errors.find(e => !isShapeError(e));
  throw realError ?? errors[errors.length - 1] ?? mismatchError ?? new ApiError('CONTRACT_MISMATCH', '没有可用的接口变体', 0);
}

/** 报文形状类错误：严格 JSON 解码失败 / 未知字段。 */
function isShapeError(err: ApiError): boolean {
  return /解析失败|unknown field|请求体/.test(err.message);
}

/**
 * 判断一次失败是否「可以换一个变体再试」。
 *
 * 正常情况下只有路由不存在 / 参数被拒（400、404、405…）才重试；
 * 另外现有 Go 实现对「请求体字段不匹配」会落到 500（严格 JSON 解码失败），
 * 这类 500 同样属于报文形状问题，也允许重试 —— 它不会产生任何业务副作用。
 */
function isRetryable(err: ApiError): boolean {
  if (RETRYABLE_STATUS.has(err.status)) return true;
  if (err.status === 500 && /解析失败|unknown field|请求体/.test(err.message)) return true;
  return false;
}
