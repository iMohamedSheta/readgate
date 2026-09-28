import { useEffect, useState } from 'react';
import {
  Plus, FolderPlus, Copy, Check, LayoutGrid, Table2, SquareTerminal,
  Stethoscope, Plug2, Palette, Database, Settings2, FlaskConical,
  Minus, Square, X,
} from 'lucide-react';
import { WindowMinimise, WindowToggleMaximise, Quit } from '../../wailsjs/runtime/runtime';
import { cn } from '../lib/cn';
import type { Tab } from './menuTypes';

export const DRAG = { ['--wails-draggable' as any]: 'drag' };
export const NODRAG = { ['--wails-draggable' as any]: 'no-drag' };

/** Small custom caption buttons (frameless window). */
function WindowControls() {
  const base = 'flex h-9 w-11 items-center justify-center text-zinc-500 transition-colors';
  return (
    <div className="flex items-stretch" style={NODRAG}>
      <button title="Minimize" onClick={() => { try { WindowMinimise(); } catch {} }} className={cn(base, 'hover:bg-zinc-800 hover:text-zinc-100')}>
        <Minus size={14} />
      </button>
      <button title="Maximize / restore" onClick={() => { try { WindowToggleMaximise(); } catch {} }} className={cn(base, 'hover:bg-zinc-800 hover:text-zinc-100')}>
        <Square size={11} />
      </button>
      <button title="Close" onClick={() => { try { Quit(); } catch {} }} className={cn(base, 'hover:bg-[#E81123] hover:text-white')}>
        <X size={15} />
      </button>
    </div>
  );
}

function Item({ icon: Icon, label, shortcut, checked, onClick }: {
  icon?: any; label: string; shortcut?: string; checked?: boolean; onClick?: () => void;
}) {
  return (
    <button type="button" onClick={onClick}
      className="dyp flex w-full cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-[7px] text-[13px] text-zinc-200/90 transition-colors hover:bg-zinc-800 hover:text-zinc-50">
      <span className="flex w-4 justify-center text-zinc-500">
        {checked ? <Check size={14} className="acc-text" /> : Icon ? <Icon size={14} /> : null}
      </span>
      <span className="flex-1 text-start font-medium">{label}</span>
      {shortcut && (
        <kbd className="rounded border border-zinc-700 bg-zinc-950 px-1.5 py-0.5 font-mono text-[10px] text-zinc-500">{shortcut}</kbd>
      )}
    </button>
  );
}

function Sep() {
  return <div className="mx-2 my-1 h-px bg-zinc-800" />;
}

export default function Menubar({ tab, onTab, onAdd, onNewCluster, onCopyMCP, onOpenSettings, onToggleSidebar }: {
  tab: Tab;
  onTab: (t: Tab) => void;
  onAdd: () => void;
  onNewCluster: () => void;
  onCopyMCP: () => void;
  onOpenSettings: () => void;
  onToggleSidebar: () => void;
}) {
  const [open, setOpen] = useState<string | null>(null);
  const close = () => setOpen(null);

  useEffect(() => {
    if (!open) return;
    const fn = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    document.addEventListener('keydown', fn);
    return () => document.removeEventListener('keydown', fn);
  }, [open ]);

  const run = (fn?: () => void) => () => { close(); fn?.(); };

  const tabs: { id: Tab; label: string; icon: any }[] = [
    { id: 'fleet', label: 'Fleet', icon: LayoutGrid },
    { id: 'browse', label: 'Browse', icon: Table2 },
    { id: 'query', label: 'Query', icon: SquareTerminal },
    { id: 'doctor', label: 'Doctor', icon: Stethoscope },
    { id: 'mcp', label: 'MCP', icon: Plug2 },
  ];

  const menus = [
    {
      key: 'fleet', label: 'Fleet',
      items: [
        { icon: Plus, label: 'Add Database…', shortcut: 'Ctrl+N', onClick: run(onAdd) },
        { icon: FolderPlus, label: 'New Cluster', onClick: run(onNewCluster) },
        { type: 'sep' },
        { icon: Copy, label: 'Copy MCP config', onClick: run(onCopyMCP) },
      ],
    },
    {
      key: 'view', label: 'View',
      items: tabs.map((t) => ({ icon: t.icon, label: t.label, checked: tab === t.id, onClick: run(() => onTab(t.id)) })),
    },
    {
      key: 'settings', label: 'Settings',
      items: [
        { icon: Palette, label: 'Appearance…', onClick: run(onOpenSettings) },
        { icon: Database, label: 'Data & storage…', onClick: run(onOpenSettings) },
        { type: 'sep' },
        { icon: FlaskConical, label: 'Connection proof…', onClick: run(() => onTab('doctor')) },
      ],
    },
  ];

  return (
    <div className="bar-bg relative z-30 flex h-9 shrink-0 select-none items-center gap-0.5 border-b border-zinc-800 ps-2"
      style={DRAG}
      onDoubleClick={() => { try { WindowToggleMaximise(); } catch {} }}
    >
      {open && <div className="fixed inset-0 z-40 cursor-default" style={NODRAG} onClick={close} />}
      <button type="button" onClick={onToggleSidebar} title="Toggle sidebar" style={NODRAG}
        className="grid h-8 w-8 shrink-0 cursor-pointer place-items-center rounded-md text-zinc-500 transition active:scale-95 hover:bg-zinc-800 hover:text-zinc-200">
        <LayoutGrid size={14} />
      </button>
      <div className="mx-1 h-4 w-px bg-zinc-800" />
      {menus.map((m) => (
        <div key={m.key} className="relative" style={NODRAG}>
          <button type="button" onClick={() => setOpen(open === m.key ? null : m.key)}
            onMouseEnter={() => open && open !== m.key && setOpen(m.key)}
            className={cn('cursor-pointer rounded-md px-3 py-1.5 text-[13px] font-medium transition-colors',
              open === m.key ? 'bg-zinc-800 text-zinc-50' : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100')}>
            {m.label}
          </button>
          {open === m.key && (
            <div className="pop-in absolute start-0 top-full z-50 mt-1 w-60 rounded-xl border border-zinc-700 bg-zinc-900 p-1.5 shadow-2xl">
              {m.items.map((it: any, i: number) => it.type === 'sep' ? <Sep key={i} /> : <Item key={i} {...it} />)}
            </div>
          )}
        </div>
      ))}
      <div className="ms-auto flex items-center gap-1 pe-0" style={NODRAG}>
        <button type="button" onClick={onOpenSettings} title="Settings"
          className="grid h-8 w-8 shrink-0 cursor-pointer place-items-center rounded-md text-zinc-500 transition active:scale-95 hover:bg-zinc-800 hover:text-zinc-200">
          <Settings2 size={14} />
        </button>
        <WindowControls />
      </div>
    </div>
  );
}
