import { cn } from '../lib/cn';
import React, { useEffect, useState } from 'react';
import { Eye, EyeOff, TriangleAlert, X } from 'lucide-react';

export function Button({ className, variant = 'default', type, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'default' | 'outline' | 'ghost' | 'emerald' | 'danger' }) {
  const styles = {
    default: 'bg-zinc-100 text-zinc-900 hover:bg-white',
    emerald: 'acc-bg acc-on font-semibold hover:brightness-110',
    outline: 'border border-zinc-700 bg-transparent hover:bg-zinc-800 text-zinc-200',
    ghost: 'bg-transparent hover:bg-zinc-800 text-zinc-300',
    danger: 'bg-red-500 text-white hover:bg-red-400 font-medium',
  }[variant];
  return <button type={type ?? 'button'} {...props} className={cn('inline-flex cursor-pointer select-none items-center justify-center gap-2 rounded-lg px-4 py-2 text-sm transition active:scale-[0.98] disabled:cursor-not-allowed disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0', styles, className)} />;
}

export function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div {...props} className={cn('surf rounded-xl border border-zinc-800 bg-zinc-900/80', className)} />;
}

export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={cn('w-full rounded-lg border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 placeholder:text-zinc-600 outline-none focus:border-emerald-500/60 focus:ring-2 focus:ring-emerald-500/20', props.className)} />;
}

// Masked secret input with reveal toggle. Secrets are AES-GCM encrypted
// in SQLite — masking only keeps them off shoulders/screenshares.
export function PasswordInput({ value, onChange, placeholder, className, mono }: {
  value: string; onChange: (v: string) => void; placeholder?: string; className?: string; mono?: boolean;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="relative">
      <Input type={show ? 'text' : 'password'} value={value}
        onChange={(e) => onChange(e.target.value)} placeholder={placeholder}
        className={cn(mono && 'font-mono', '!pe-9', className)} />
      <button type="button" onClick={() => setShow((v) => !v)} title={show ? 'Hide' : 'Reveal'}
        className="absolute end-1 top-1/2 grid h-7 w-7 -translate-y-1/2 cursor-pointer place-items-center rounded-md text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200 [&_svg]:pointer-events-none">
        {show ? <EyeOff size={14} /> : <Eye size={14} />}
      </button>
    </div>
  );
}

export function Label({ children, icon }: { children: React.ReactNode; icon?: React.ReactNode }) {
  return (
    <div className="mb-1 flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wider text-zinc-500">
      {icon && <span className="text-zinc-600">{icon}</span>}{children}
    </div>
  );
}

export function Badge({ tone = 'zinc', children }: { tone?: 'zinc' | 'green' | 'amber' | 'red' | 'indigo'; children: React.ReactNode }) {
  return <span className={cn('bdg', `bdg-${tone}`)}>{children}</span>;
}

export function Field({ label, children, hint }: { label: string; children: React.ReactNode; hint?: string }) {
  return (
    <div>
      <Label>{label}</Label>
      {children}
      {hint && <div className="mt-1 text-xs text-zinc-500">{hint}</div>}
    </div>
  );
}

export function Empty({ icon, title, hint }: { icon: React.ReactNode; title: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-zinc-800 bg-zinc-900/40 px-6 py-14 text-center">
      <div className="text-zinc-600">{icon}</div>
      <div className="text-sm font-medium text-zinc-300">{title}</div>
      {hint && <div className="max-w-sm text-xs text-zinc-500">{hint}</div>}
    </div>
  );
}

// Icon button with a real 32px hit target (28px in sm). Use for every
// icon-only control so nobody has to snipe a 12px glyph.
export function IconBtn({ title, onClick, disabled, variant = 'ghost', size = 'md', className, children }: {
  title: string; onClick?: (e: React.MouseEvent) => void; disabled?: boolean;
  variant?: 'ghost' | 'outline'; size?: 'md' | 'sm'; className?: string; children: React.ReactNode;
}) {
  return (
    <button type="button" title={title} aria-label={title} onClick={onClick} disabled={disabled}
      onMouseDown={(e) => e.stopPropagation()}
      className={cn('grid shrink-0 cursor-pointer select-none place-items-center rounded-lg transition active:scale-95 disabled:cursor-not-allowed disabled:opacity-40 [&_svg]:pointer-events-none',
        size === 'md' ? 'h-8 w-8' : 'h-7 w-7',
        variant === 'ghost' ? 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100' : 'border border-zinc-700 text-zinc-300 hover:bg-zinc-800',
        className)}>
      {children}
    </button>
  );
}
export function Sheet({ open, onClose, title, subtitle, children, wide }: {
  open: boolean; onClose: () => void; title: string; subtitle?: string; children: React.ReactNode; wide?: boolean;
}) {
  useEffect(() => {
    if (!open) return;
    const fn = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50">
      <div className="sheet-dim absolute inset-0 bg-black/60 backdrop-blur-[2px]" onClick={onClose} />
      <aside className={`sheet-glass sheet-in absolute right-0 top-0 flex h-full w-full ${wide ? 'max-w-5xl' : 'max-w-3xl'} flex-col border-l border-zinc-800 sheet-bg shadow-2xl`}>
        <div className="flex items-center justify-between border-b border-zinc-800 px-4 py-3">
          <div>
            <div className="text-sm font-semibold text-zinc-100">{title}</div>
            {subtitle && <div className="text-[11px] text-zinc-500">{subtitle}</div>}
          </div>
          <button type="button" onClick={onClose} className="grid h-8 w-8 shrink-0 cursor-pointer place-items-center rounded-lg text-zinc-500 transition active:scale-95 hover:bg-zinc-800 hover:text-zinc-200 [&_svg]:pointer-events-none"><X size={16} /></button>
        </div>
        <div className="flex-1 overflow-y-auto">{children}</div>
      </aside>
    </div>
  );
}

// Small centered modal — reserved for destructive confirmations.
export function ConfirmModal({ open, title, body, confirmLabel = 'Confirm', busy, onCancel, onConfirm }: {
  open: boolean; title: string; body: string; confirmLabel?: string; busy?: boolean;
  onCancel: () => void; onConfirm: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    const fn = (e: KeyboardEvent) => { if (e.key === 'Escape') onCancel(); };
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [open, onCancel]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-4">
      <div className="sheet-dim absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={onCancel} />
      <div className="pop-in sheet-bg relative w-full max-w-sm rounded-xl border border-zinc-700 p-4 shadow-2xl">
        <div className="flex items-center gap-2 text-sm font-semibold text-zinc-100">
          <span className="flex h-7 w-7 items-center justify-center rounded-full bg-red-500/15 text-red-300"><TriangleAlert size={14} /></span>
          {title}
        </div>
        <div className="mt-2 text-xs leading-relaxed text-zinc-400">{body}</div>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>
          <Button variant="danger" onClick={onConfirm} disabled={busy}>{busy ? 'Working…' : confirmLabel}</Button>
        </div>
      </div>
    </div>
  );
}
