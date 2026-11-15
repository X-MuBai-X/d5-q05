import React from 'react';
import { AlertTriangle } from 'lucide-react';
import { AlertStrip } from '../shell/ui';

export interface ErrorInfo {
  code: string;
  message: string;
}

/**
 * 后端错误展示条。
 *
 * 直接展示后端返回的 `code` 与 `message` —— 前端不解释、不改写、不隐藏，
 * 以便演示时能逐条核对契约里的错误码矩阵。
 */
export const ErrorBanner: React.FC<{ error: ErrorInfo | null; className?: string }> = ({
  error,
  className = ''
}) => {
  if (!error) return null;
  return (
    <div className={className}>
      <AlertStrip tone="rose" icon={AlertTriangle}>
        <div className="flex items-center gap-2 flex-wrap">
          <span className="font-mono font-semibold">{error.code}</span>
          <span>{error.message}</span>
        </div>
      </AlertStrip>
    </div>
  );
};
