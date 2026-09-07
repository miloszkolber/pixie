export interface ToastOptions {
  title?: string;
  description?: string;
  variant?: 'success' | 'warning' | 'info' | 'destructive';
  duration?: number;
  action?: { label: string; onClick?: () => void };
  onDismiss?: () => void;
}
export interface ToastApi {
  show(options: string | ToastOptions): HTMLDivElement;
  success(options: string | ToastOptions): HTMLDivElement;
  warning(options: string | ToastOptions): HTMLDivElement;
  info(options: string | ToastOptions): HTMLDivElement;
  error(options: string | ToastOptions): HTMLDivElement;
  dismiss(): void;
}
export interface MewaEventMap {
  'tag-input:change': CustomEvent<{ tags: string[]; source: string }>;
  'file-upload:change': CustomEvent<{ files: File[]; source: string }>;
  'checkbox-group:change': CustomEvent<{ values: string[]; selected: number; total: number; source: string }>;
  'input-otp:change': CustomEvent<{ value: string; source: string }>;
  'input-otp:complete': CustomEvent<{ value: string }>;
  'date-picker:select': CustomEvent<{ date: Date }>;
  'tabs:activate': CustomEvent<{ id: string | HTMLElement }>;
}
declare global {
  interface Window { toast?: ToastApi; }
  interface HTMLElementEventMap extends MewaEventMap {}
}
