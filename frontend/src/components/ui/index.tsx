import * as DialogPrimitive from "@radix-ui/react-dialog";
import * as SelectPrimitive from "@radix-ui/react-select";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { clsx } from "clsx";
import {
  AlertTriangle, Check, ChevronDown, CircleAlert, Eye, EyeOff, Info, LoaderCircle,
  Menu, X, type LucideIcon,
} from "lucide-react";
import {
  Component, forwardRef, useState, type ButtonHTMLAttributes, type ErrorInfo,
  type HTMLAttributes, type InputHTMLAttributes, type ReactNode,
} from "react";
import { NavLink } from "react-router-dom";
import { BrandMark } from "../../BrandMark";

export function cn(...values: Array<string | false | null | undefined>) {
  return clsx(values);
}

export function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}

const buttonVariants = cva("ui-button", {
  defaultVariants: { size: "md", tone: "primary", variant: "solid" },
  variants: {
    size: { sm: "ui-button--sm", md: "ui-button--md", lg: "ui-button--lg", icon: "ui-button--icon" },
    tone: { default: "ui-button--default", primary: "ui-button--primary", danger: "ui-button--danger" },
    variant: { solid: "ui-button--solid", outline: "ui-button--outline", ghost: "ui-button--ghost" },
    fullWidth: { true: "ui-button--full" },
  },
});

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean;
  loading?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { asChild, children, className, disabled, fullWidth, loading, size, tone, type = "button", variant, ...props }, ref,
) {
  const styles = buttonVariants({ className, fullWidth, size, tone, variant });
  if (asChild) return <Slot className={styles} ref={ref} {...props}>{children}</Slot>;
  return <button className={styles} disabled={disabled || loading} ref={ref} type={type} {...props}>{loading && <LoaderCircle className="spin" size={16} aria-hidden="true" />}{children}</button>;
});

export function IconButton({ label, children, ...props }: Omit<ButtonProps, "aria-label" | "size"> & { label: string; children: ReactNode }) {
  return <Tooltip content={label}><Button aria-label={label} size="icon" variant="ghost" {...props}>{children}</Button></Tooltip>;
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...props }, ref) {
  return <input className={cn("ui-input", className)} ref={ref} {...props} />;
});

export const PasswordInput = forwardRef<HTMLInputElement, Omit<InputHTMLAttributes<HTMLInputElement>, "type">>(function PasswordInput(props, ref) {
  const [visible, setVisible] = useState(false);
  return <span className="ui-password"><Input {...props} ref={ref} type={visible ? "text" : "password"} /><IconButton label={visible ? "隐藏密码" : "显示密码"} onClick={() => setVisible((value) => !value)} tabIndex={-1}>{visible ? <EyeOff size={16} /> : <Eye size={16} />}</IconButton></span>;
});

export interface SelectOption { label: string; value: string }
export function Select({ ariaLabel, disabled, onValueChange, options, placeholder = "请选择", value }: { ariaLabel: string; disabled?: boolean; onValueChange: (value: string) => void; options: SelectOption[]; placeholder?: string; value?: string }) {
  return <SelectPrimitive.Root disabled={disabled} onValueChange={onValueChange} value={value}>
    <SelectPrimitive.Trigger aria-label={ariaLabel} className="ui-select"><SelectPrimitive.Value placeholder={placeholder} /><SelectPrimitive.Icon><ChevronDown size={15} /></SelectPrimitive.Icon></SelectPrimitive.Trigger>
    <SelectPrimitive.Portal><SelectPrimitive.Content className="ui-select-content" position="popper" sideOffset={6}><SelectPrimitive.Viewport>{options.map((option) => <SelectPrimitive.Item className="ui-select-item" key={option.value} value={option.value}><SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText><SelectPrimitive.ItemIndicator><Check size={14} /></SelectPrimitive.ItemIndicator></SelectPrimitive.Item>)}</SelectPrimitive.Viewport></SelectPrimitive.Content></SelectPrimitive.Portal>
  </SelectPrimitive.Root>;
}

export function SegmentedControl({ ariaLabel, onChange, options, value }: { ariaLabel: string; onChange: (value: string) => void; options: SelectOption[]; value: string }) {
  return <div aria-label={ariaLabel} className="ui-segmented" role="group">{options.map((option) => <Button aria-pressed={value === option.value} key={option.value} onClick={() => onChange(option.value)} size="sm" variant="ghost">{option.label}</Button>)}</div>;
}

export type Tone = "neutral" | "success" | "warning" | "danger" | "info";
export function Badge({ children, tone = "neutral" }: { children: ReactNode; tone?: Tone }) {
  return <span className={`ui-badge ui-badge--${tone}`}>{children}</span>;
}

const noticeIcons: Record<Tone, LucideIcon> = { neutral: Info, success: Check, warning: AlertTriangle, danger: CircleAlert, info: Info };
export function InlineNotice({ action, children, title, tone = "info" }: { action?: ReactNode; children?: ReactNode; title?: ReactNode; tone?: Tone }) {
  const Icon = noticeIcons[tone];
  return <div className={`ui-notice ui-notice--${tone}`} role={tone === "danger" ? "alert" : "status"}><Icon size={18} aria-hidden="true" /><div>{title && <strong>{title}</strong>}{children && <span>{children}</span>}</div>{action && <div className="ui-notice__action">{action}</div>}</div>;
}

export function Loading({ compact, label = "加载中" }: { compact?: boolean; label?: string }) {
  return <div className={cn("ui-loading", compact && "ui-loading--compact")} role="status"><LoaderCircle className="spin" size={20} /><span>{label}</span></div>;
}

export function Skeleton({ className }: { className?: string }) { return <span aria-hidden="true" className={cn("ui-skeleton", className)} />; }

export function Progress({ className, indeterminate, label, value = 0 }: { className?: string; indeterminate?: boolean; label?: string; value?: number }) {
  const normalized = Math.max(0, Math.min(100, value));
  return <div aria-label={label} aria-valuemax={100} aria-valuemin={0} aria-valuenow={indeterminate ? undefined : normalized} className={cn("ui-progress", indeterminate && "is-indeterminate indeterminate", className)} role="progressbar"><span style={indeterminate ? undefined : { width: `${normalized}%` }} /></div>;
}

export function EmptyState({ action, description, icon, title }: { action?: ReactNode; description?: ReactNode; icon?: ReactNode; title: ReactNode }) {
  return <div className="ui-empty">{icon && <span className="ui-empty__icon">{icon}</span>}<strong>{title}</strong>{description && <span>{description}</span>}{action}</div>;
}

export function Dialog({ children, description, onOpenChange, open, title }: { children: ReactNode; description?: ReactNode; onOpenChange: (open: boolean) => void; open: boolean; title: ReactNode }) {
  return <DialogPrimitive.Root onOpenChange={onOpenChange} open={open}><DialogPrimitive.Portal><DialogPrimitive.Overlay className="ui-dialog-overlay" /><DialogPrimitive.Content {...(description ? {} : { "aria-describedby": undefined })} className="ui-dialog-content"><header><div><DialogPrimitive.Title>{title}</DialogPrimitive.Title>{description && <DialogPrimitive.Description>{description}</DialogPrimitive.Description>}</div><DialogPrimitive.Close asChild><IconButton label="关闭"><X size={17} /></IconButton></DialogPrimitive.Close></header>{children}</DialogPrimitive.Content></DialogPrimitive.Portal></DialogPrimitive.Root>;
}

export function ConfirmDialog({ cancelLabel = "取消", confirmLabel = "确认", description, onConfirm, onOpenChange, open, title, tone = "danger" }: { cancelLabel?: string; confirmLabel?: string; description: ReactNode; onConfirm: () => void | Promise<void>; onOpenChange: (open: boolean) => void; open: boolean; title: ReactNode; tone?: "primary" | "danger" }) {
  return <Dialog description={description} onOpenChange={onOpenChange} open={open} title={title}><div className="ui-dialog-actions"><Button onClick={() => onOpenChange(false)} tone="default" variant="outline">{cancelLabel}</Button><Button onClick={() => void onConfirm()} tone={tone}>{confirmLabel}</Button></div></Dialog>;
}

export function Tooltip({ children, content }: { children: ReactNode; content: ReactNode }) {
  return <TooltipPrimitive.Provider delayDuration={450}><TooltipPrimitive.Root><TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger><TooltipPrimitive.Portal><TooltipPrimitive.Content className="ui-tooltip" sideOffset={7}>{content}<TooltipPrimitive.Arrow /></TooltipPrimitive.Content></TooltipPrimitive.Portal></TooltipPrimitive.Root></TooltipPrimitive.Provider>;
}

export function ScrollArea({ children, className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("ui-scroll-area", className)} {...props}>{children}</div>;
}

export function PageHeader({ action, description, title }: { action?: ReactNode; description?: ReactNode; title: ReactNode }) {
  return <header className="ui-page-header"><div><h1>{title}</h1>{description && <p>{description}</p>}</div>{action}</header>;
}

export function Toolbar({ children }: { children: ReactNode }) { return <div className="ui-toolbar">{children}</div>; }

export interface AppNavItem { icon: LucideIcon; label: string; to: string; end?: boolean }
export function AppShell({ actions, brand, children, nav, product }: { actions?: ReactNode; brand?: string; children: ReactNode; nav: AppNavItem[]; product: string }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const navigation = <nav className="ui-app-nav">{nav.map((item) => <NavLink end={item.end} key={item.to} onClick={() => setMobileOpen(false)} to={item.to}><item.icon size={17} /><span>{item.label}</span></NavLink>)}</nav>;
  return <div className="ui-app-shell"><aside><AppBrand brand={brand} product={product} />{navigation}{actions && <div className="ui-app-actions">{actions}</div>}</aside><header className="ui-mobile-bar"><IconButton label="打开菜单" onClick={() => setMobileOpen(true)}><Menu size={19} /></IconButton><AppBrand brand={brand} product={product} />{actions && <div className="ui-mobile-actions">{actions}</div>}</header><main>{children}</main><Dialog onOpenChange={setMobileOpen} open={mobileOpen} title="导航"><div className="ui-mobile-drawer"><AppBrand brand={brand} product={product} />{navigation}</div></Dialog></div>;
}

function AppBrand({ brand = "HomeStack", product }: { brand?: string; product: string }) {
  return <div className="ui-app-brand"><BrandMark className="brand-mark" /><div><strong>{brand}</strong><small>{product}</small></div></div>;
}

export function AuthLayout({ children, description, title }: { children: ReactNode; description?: ReactNode; title: ReactNode }) {
  return <main className="ui-auth"><div className="ui-auth__brand"><BrandMark className="login-mark" /></div><h1>{title}</h1>{description && <p>{description}</p>}<div className="ui-auth__content">{children}</div></main>;
}

interface ErrorBoundaryState { error?: Error }
export class ErrorBoundary extends Component<{ children: ReactNode }, ErrorBoundaryState> {
  state: ErrorBoundaryState = {};
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error("HomeStack 界面渲染失败", error, info.componentStack); }
  render() {
    if (!this.state.error) return this.props.children;
    return <AuthLayout title="页面加载失败" description="界面渲染遇到错误，请重试。"><InlineNotice tone="danger">{this.state.error.message}</InlineNotice><Button onClick={() => location.reload()}><LoaderCircle size={16} />重新加载</Button></AuthLayout>;
  }
}
