import { useState } from 'react';
import { ArrowDownToLine, ExternalLink, Loader2, Rocket, X } from 'lucide-react';
import type { UpdateInfo } from '../lib/api';
import { Button } from './ui';
import { cn } from '../lib/cn';

function fmtSize(n: number): string {
  if (!n || n <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}

// Slim banner under the menubar when a newer GitHub release exists.
// Install path: Windows/Linux one-click (download → swap → restart).
// macOS: downloads the zip, user drags ReadGate.app to Applications.
export default function UpdateBanner({ info, busy, progress, onInstall, onNotes, onLater, onSkip }: {
  info: UpdateInfo;
  busy: boolean;
  progress: { written: number; total: number } | null;
  onInstall: () => void;
  onNotes: () => void;
  onLater: () => void;
  onSkip: () => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const pct = progress && progress.total > 0
    ? Math.min(100, Math.round((progress.written / progress.total) * 100))
    : null;
  const notes = (info.notes || '').trim();

  return (
    <div className="shrink-0 border-b border-emerald-500/25 bg-emerald-500/[0.07] px-3 py-1.5">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px]">
        <span className="flex h-6 w-6 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-300">
          <Rocket size={13} />
        </span>
        <span className="font-medium text-emerald-100">
          {info.latestVersion} available
        </span>
        <span className="font-mono text-[11px] text-emerald-200/60">
          you have {info.currentVersion || 'dev'}
          {info.assetName && <> · {info.assetName}{info.size > 0 && <> ({fmtSize(info.size)})</>}</>}
        </span>
        <span className="ms-auto flex flex-wrap items-center gap-1.5">
          {busy && (
            <span className="flex items-center gap-1.5 font-mono text-[11px] text-emerald-200/80">
              <Loader2 size={12} className="animate-spin" />
              {pct !== null ? `downloading… ${pct}%` : 'working…'}
            </span>
          )}
          {pct !== null && (
            <span className="h-1.5 w-24 overflow-hidden rounded-full bg-zinc-800">
              <span className="block h-full rounded-full bg-emerald-400 transition-all" style={{ width: `${pct}%` }} />
            </span>
          )}
          <Button variant="emerald" className="!px-2.5 !py-1 text-[11px]" onClick={onInstall} disabled={busy}>
            {busy ? <Loader2 size={12} className="animate-spin" /> : <ArrowDownToLine size={12} />}
            {info.canInstall ? 'Download & install' : 'Download update'}
          </Button>
          {notes && (
            <button onClick={() => setExpanded((v) => !v)}
              className="cursor-pointer rounded-md px-2 py-1 text-[11px] text-emerald-200/80 hover:bg-emerald-500/10 hover:text-emerald-100">
              {expanded ? 'Hide notes' : "What's new"}
            </button>
          )}
          <button onClick={onNotes} title="Open the GitHub release page"
            className="flex cursor-pointer items-center gap-1 rounded-md px-2 py-1 text-[11px] text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100">
            <ExternalLink size={11} /> Release page
          </button>
          <button onClick={onLater} title="Remind me later"
            className="cursor-pointer rounded-md px-2 py-1 text-[11px] text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200">
            Later
          </button>
          <button onClick={onSkip} title={`Never show ${info.latestVersion} again`}
            className={cn('cursor-pointer rounded-md p-1 text-zinc-600 hover:bg-zinc-800 hover:text-zinc-300')}>
            <X size={13} />
          </button>
        </span>
      </div>
      {expanded && notes && (
        <pre className="mx-auto mt-1.5 max-h-40 max-w-4xl overflow-auto whitespace-pre-wrap rounded-lg border border-emerald-500/20 bg-black/40 p-2.5 font-mono text-[11px] leading-relaxed text-zinc-300">
          {notes.length > 4000 ? notes.slice(0, 4000) + '\n…(full notes on the release page)' : notes}
        </pre>
      )}
    </div>
  );
}
