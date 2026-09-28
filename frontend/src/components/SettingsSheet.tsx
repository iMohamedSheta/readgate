import { useState } from 'react';
import { Check, Copy, Database, FileText, Layers, Moon, Sun, Palette, Plug2, RefreshCw, RotateCcw, Square, Trash2, Type, SlidersHorizontal, PencilLine, ShieldCheck } from 'lucide-react';
import { ACCENTS, DEFAULT_APPEARANCE, FONT_SIZES, type Appearance } from '../lib/appearance';
import { cn } from '../lib/cn';
import { Badge, Button, IconBtn, Sheet } from './ui';

function Sub({ children }: { children: React.ReactNode }) {
  return <div className="mb-1 mt-3 text-[11px] font-medium uppercase tracking-wider text-zinc-500 first:mt-0">{children}</div>;
}

function Seg<T extends string>({ options, value, onPick }: {
  options: { value: T; label: string; icon?: any }[]; value: string; onPick: (v: T) => void;
}) {
  return (
    <div className="inline-flex flex-wrap gap-1 rounded-xl border border-zinc-800 bg-zinc-950 p-1">
      {options.map((o) => (
        <button key={o.value} type="button" onClick={() => onPick(o.value)}
          className={cn('flex cursor-pointer select-none items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors [&_svg]:pointer-events-none',
            value === o.value ? 'bg-zinc-800 text-zinc-50 shadow-sm' : 'text-zinc-500 hover:text-zinc-200')}>
          {o.icon && <o.icon size={13} />}
          {o.label}
        </button>
      ))}
    </div>
  );
}

function Section({ icon: Icon, title, children }: { icon: any; title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/60 p-3.5">
      <div className="mb-2.5 flex items-center gap-2 text-[13px] font-semibold text-zinc-100">
        <Icon size={15} className="acc-text" />
        {title}
      </div>
      {children}
    </div>
  );
}

function Switch({ on, onToggle }: { on: boolean; onToggle: () => void }) {
  return (
    <button type="button" onClick={onToggle}
      className={cn('relative h-6 w-11 shrink-0 cursor-pointer rounded-full transition-colors', on ? 'acc-bg' : 'bg-zinc-700')}>
      <span className={cn('absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all', on ? 'end-0.5' : 'start-0.5')} />
    </button>
  );
}

export default function SettingsSheet({ open, onClose, appearance, onPatch, storePath, counts, mcpUrl, mcpConfig, logs, logPath, onReloadLogs, onClearLogs, writeMode, onToggleWriteMode, version }: {
  open: boolean;
  onClose: () => void;
  appearance: Appearance;
  onPatch: (p: Partial<Appearance>) => void;
  storePath: string;
  counts: { sources: number; clusters: number; verified: number };
  mcpUrl: string;
  mcpConfig: string;
  logs: string[];
  logPath: string;
  onReloadLogs: () => void;
  onClearLogs: () => void;
  writeMode: boolean;
  onToggleWriteMode: () => void;
  version?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [stab, setStab] = useState<'general' | 'appearance' | 'data' | 'logs' | 'ai'>('appearance');
  const dirty = (Object.keys(DEFAULT_APPEARANCE) as (keyof Appearance)[]).some((k) => appearance[k] !== DEFAULT_APPEARANCE[k]);
  const reset = () => onPatch({ ...DEFAULT_APPEARANCE });

  const tabs = [
    { id: 'general' as const, label: 'General', icon: SlidersHorizontal },
    { id: 'appearance' as const, label: 'Appearance', icon: Palette },
    { id: 'data' as const, label: 'Data', icon: Database },
    { id: 'logs' as const, label: 'Logs', icon: FileText },
    { id: 'ai' as const, label: 'AI access', icon: Plug2 },
  ];

  return (
    <Sheet wide open={open} onClose={onClose} title="Settings" subtitle="General · appearance · data · diagnostics · AI">
      <div className="sticky top-0 z-10 border-b border-zinc-800 bg-zinc-950/95 px-4 py-2 backdrop-blur">
        <div className="flex gap-1">
          {tabs.map((t) => (
            <button key={t.id} type="button" onClick={() => setStab(t.id)}
              className={cn('flex cursor-pointer select-none items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors [&_svg]:pointer-events-none',
                stab === t.id ? 'bg-zinc-800 text-zinc-50' : 'text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200')}>
              <t.icon size={13} /> {t.label}
            </button>
          ))}
          {dirty && (
            <button type="button" onClick={reset} title="Reset appearance to defaults"
              className="ml-auto flex cursor-pointer items-center gap-1 rounded-lg px-2 py-1.5 text-[11px] text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200 [&_svg]:pointer-events-none">
              <RotateCcw size={11} /> Reset
            </button>
          )}
        </div>
      </div>
      <div className="grid gap-3 px-4 py-4">
        {stab === 'general' && (
          <div className="grid items-start gap-3">
            <Section icon={PencilLine} title="Table editing (writes)">
              <div className="flex items-center justify-between gap-3 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5">
                <div className="min-w-0">
                  <div className="text-xs font-medium text-zinc-200">Allow modifications in the app</div>
                  <div className="mt-0.5 text-[11px] leading-relaxed text-zinc-500">Master switch. Off = the app is strictly read-only, like today.</div>
                </div>
                <Switch on={writeMode} onToggle={onToggleWriteMode} />
              </div>
              <div className="mt-2 grid gap-1.5 font-mono text-[11px]">
                {[
                  ['edits', 'Beekeeper-style: edit cells, add rows, delete rows in Browse'],
                  ['confirm', 'every batch shows its exact SQL and asks first'],
                  ['scope', 'row DML only (INSERT / UPDATE / DELETE) — never DDL'],
                  ['login', 'server DBs need a saved write login (postgres / root / sa…)'],
                  ['ai', 'the AI stays read-only: no MCP tool can write, ever'],
                ].map(([k, v]) => (
                  <div key={k} className="flex items-start gap-2 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5">
                    <span className="shrink-0 font-semibold text-emerald-300">{k}</span>
                    <span className="text-zinc-400">{v}</span>
                  </div>
                ))}
              </div>
              <div className="mt-2 flex items-start gap-1.5 text-[11px] leading-relaxed text-zinc-500">
                <ShieldCheck size={12} className="mt-0.5 shrink-0 text-emerald-400" />
                <span>Write logins are AES-GCM encrypted in SQLite (key.bin 0600), used only for live edit connections, and never appear in logs, diagnostics, or AI payloads. Set one per source from Browse → Edit rows.</span>
              </div>
            </Section>
          </div>
        )}
        {stab === 'appearance' && (
          <div className="grid items-start gap-3 xl:grid-cols-2">
            <Section icon={Palette} title="Look & feel">
              <Sub>Theme</Sub>
              <Seg value={appearance.theme} onPick={(v) => onPatch({ theme: v })}
                options={[
                  { value: 'dark', label: 'Dark', icon: Moon },
                  { value: 'light', label: 'Light', icon: Sun },
                ]} />
              <Sub>Accent</Sub>
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(ACCENTS).map(([name, rgb]) => (
                  <button key={name} type="button" title={name} onClick={() => onPatch({ accent: name })}
                    className={cn('flex h-9 w-9 cursor-pointer items-center justify-center rounded-full border-2 transition [&_svg]:pointer-events-none',
                      appearance.accent === name ? 'border-white/80 scale-110' : 'border-transparent hover:scale-105')}
                    style={{ backgroundColor: `rgb(${rgb})` }}>
                    {appearance.accent === name && <Check size={14} className="text-zinc-950" />}
                  </button>
                ))}
              </div>
          <Sub>Font</Sub>
          <Seg value={appearance.font} onPick={(v) => onPatch({ font: v })}
            options={[
              { value: 'system', label: 'System', icon: Type },
              { value: 'inter', label: 'Inter', icon: Type },
              { value: 'manrope', label: 'Manrope', icon: Type },
              { value: 'outfit', label: 'Outfit', icon: Type },
              { value: 'grotesk', label: 'Grotesk', icon: Type },
              { value: 'plex', label: 'Plex', icon: Type },
              { value: 'cairo', label: 'Cairo', icon: Type },
              { value: 'tajawal', label: 'Tajawal', icon: Type },
            ]} />
          <Sub>Text size</Sub>
          <Seg value={appearance.fontSize} onPick={(v) => onPatch({ fontSize: v })}
            options={[{ value: 'sm', label: 'Small' }, { value: 'md', label: 'Default' }, { value: 'lg', label: 'Large' }]} />
          <Sub>Code font</Sub>
          <Seg value={appearance.codeFont} onPick={(v) => onPatch({ codeFont: v })}
            options={[
              { value: 'jetbrains', label: 'JetBrains' },
              { value: 'fira', label: 'Fira Code' },
              { value: 'cascadia', label: 'Cascadia' },
              { value: 'plexmono', label: 'Plex Mono' },
              { value: 'sourcecode', label: 'Source Code' },
              { value: 'robotomono', label: 'Roboto Mono' },
              { value: 'spacemono', label: 'Space Mono' },
              { value: 'inconsolata', label: 'Inconsolata' },
              { value: 'system', label: 'System' },
            ]} />
            </Section>
            <div className="grid gap-3">
              <Section icon={Square} title="Shape & space">
                <Sub>Corner radius</Sub>
                <Seg value={appearance.radius} onPick={(v) => onPatch({ radius: v })}
                  options={[
                    { value: 'none', label: 'None' },
                    { value: 'sharp', label: 'Sharp' },
                    { value: 'balanced', label: 'Balanced' },
                    { value: 'soft', label: 'Soft' },
                    { value: 'round', label: 'Round' },
                  ]} />
                <Sub>Cards</Sub>
                <Seg value={appearance.cardStyle} onPick={(v) => onPatch({ cardStyle: v })}
                  options={[
                    { value: 'elevated', label: 'Elevated', icon: Layers },
                    { value: 'bordered', label: 'Bordered' },
                    { value: 'flat', label: 'Flat' },
                    { value: 'glass', label: 'Glass' },
                  ]} />
                <Sub>Density</Sub>
                <Seg value={appearance.density} onPick={(v) => onPatch({ density: v })}
                  options={[{ value: 'compact', label: 'Compact' }, { value: 'comfortable', label: 'Comfortable' }, { value: 'spacious', label: 'Spacious' }]} />
                <Sub>Effects</Sub>
                <div className="grid gap-1.5">
                  {([
                    ['shadows', 'Shadows', 'Card depth'],
                    ['animations', 'Animations', 'Transitions & motion'],
                    ['glass', 'Frosted glass', 'Blurred sidebar + sheets'],
                  ] as const).map(([k, label, hint]) => (
                    <div key={k} className="flex items-center justify-between rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2">
                      <span className="text-xs text-zinc-300">{label} <span className="text-zinc-600">· {hint}</span></span>
                      <Switch on={appearance[k] === 'on'} onToggle={() => onPatch({ [k]: appearance[k] === 'on' ? 'off' : 'on' } as Partial<Appearance>)} />
                    </div>
                  ))}
                </div>
              </Section>
              <div className="rounded-xl border border-zinc-800 bg-zinc-950 p-3">
                <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-zinc-600">Live preview</div>
                <div className="surf rounded-xl border border-zinc-800 bg-zinc-900/80 p-3">
                  <div className="h-2 w-12 rounded-full acc-soft" />
                  <div className="mt-2 h-2.5 w-full rounded bg-zinc-700/40" />
                  <div className="mt-1.5 h-2.5 w-2/3 rounded bg-zinc-700/25" />
                  <div className="mt-2.5 flex gap-1.5">
                    <span className="acc-bg acc-on grid h-8 flex-1 place-items-center rounded-lg text-xs font-bold">Primary</span>
                    <span className="grid h-8 flex-1 place-items-center rounded-lg border border-zinc-700 text-xs text-zinc-300">Outline</span>
                  </div>
                </div>
                <p className="mt-2 text-center text-zinc-500" style={{ fontSize: FONT_SIZES[appearance.fontSize] }}>
                  Aa — {appearance.accent} · {appearance.font} · {appearance.radius}
                </p>
              </div>
            </div>
          </div>
        )}

        {stab === 'data' && (
          <Section icon={Database} title="Data & storage">
          <div className="grid gap-1.5 font-mono text-[11px]">
            <div className="flex justify-between rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5">
              <span className="text-zinc-500">store</span><span className="truncate text-zinc-300">{storePath || '…'}</span>
            </div>
            <div className="flex gap-1.5">
              <div className="flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5 text-center"><span className="text-zinc-100">{counts.sources}</span> <span className="text-zinc-500">sources</span></div>
              <div className="flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5 text-center"><span className="text-zinc-100">{counts.clusters}</span> <span className="text-zinc-500">clusters</span></div>
              <div className="flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5 text-center"><span className="text-emerald-300">{counts.verified}</span> <span className="text-zinc-500">verified</span></div>
            </div>
          </div>
          <div className="mt-2 text-[11px] text-zinc-600">SQLite (WAL) · passwords AES-GCM · key.bin 0600</div>
        </Section>
        )}

        {stab === 'logs' && (
          <Section icon={FileText} title="Diagnostics log">
          <div className="mb-1.5 flex items-center gap-1.5">
            <span className="truncate font-mono text-[10px] text-zinc-600">{logPath || '…'}</span>
            <span className="ml-auto flex shrink-0 gap-1">
              <IconBtn title="Reload" size="sm" variant="outline" onClick={onReloadLogs}><RefreshCw size={13} /></IconBtn>
              <IconBtn title="Copy" size="sm" variant="outline" onClick={() => { navigator.clipboard.writeText((logs || []).join('\n')); }}><Copy size={13} /></IconBtn>
              <IconBtn title="Clear" size="sm" variant="outline" className="hover:!text-red-300" onClick={onClearLogs}><Trash2 size={13} /></IconBtn>
            </span>
          </div>
          <pre className="max-h-96 min-h-40 overflow-auto rounded-lg codeblock bg-black/60 p-2.5 font-mono text-[10px] leading-relaxed text-zinc-300">{(logs && logs.length ? logs.join('\n') : 'empty — reproduce the table error, then hit reload')}</pre>
          <div className="mt-1.5 text-[11px] text-zinc-600">Backend errors land here (never passwords). Reproduce → reload → paste me the red lines.</div>
        </Section>
        )}

        {stab === 'ai' && (
          <>
            <Section icon={Plug2} title="AI access">
              <div className="mb-1.5 flex items-center gap-1.5">
                <Badge tone="green"><span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> {mcpUrl.replace('http://', '')}/mcp</Badge>
              </div>
              <pre className="max-h-64 min-h-24 overflow-auto rounded-lg codeblock bg-black/60 p-2.5 font-mono text-[11px] text-emerald-200/90">{mcpConfig || 'loading…'}</pre>
              <Button variant="outline" className="mt-2 !py-1 text-[11px]"
                onClick={() => { navigator.clipboard.writeText(mcpConfig); setCopied(true); setTimeout(() => setCopied(false), 1200); }}>
                {copied ? <Check size={12} /> : <Copy size={12} />} {copied ? 'Copied' : 'Copy MCP config'}
              </Button>
            </Section>
            <div className="flex items-center gap-1.5 px-1 text-[11px] text-zinc-600">
              <Type size={11} /> ReadGate {version || 'dev'} · SSH tunnels · verified read-only · MCP 2024-11-05
            </div>
          </>
        )}
      </div>
    </Sheet>
  );
}
