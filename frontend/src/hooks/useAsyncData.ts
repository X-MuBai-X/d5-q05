import { useCallback, useEffect, useRef, useState } from 'react';
import { describeError } from '@/api/client';

export interface AsyncState<T> {
  data: T | null;
  error: { code: string; message: string } | null;
  loading: boolean;
  reload: () => void;
}

/**
 * 轻量数据加载 hook。
 *
 * 只负责「调后端 → 存结果 → 展示后端返回的错误码」，
 * 不在本地做任何业务判定（额度、权限等一律以后端裁决为准）。
 */
export function useAsyncData<T>(
  loader: () => Promise<T>,
  deps: React.DependencyList,
  enabled = true
): AsyncState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<{ code: string; message: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);
  const loaderRef = useRef(loader);
  loaderRef.current = loader;

  useEffect(() => {
    if (!enabled) {
      setData(null);
      setError(null);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    loaderRef
      .current()
      .then(result => {
        if (cancelled) return;
        setData(result);
        setError(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(describeError(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, enabled, tick]);

  const reload = useCallback(() => setTick(t => t + 1), []);

  return { data, error, loading, reload };
}
