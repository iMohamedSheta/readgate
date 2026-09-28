import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Database, Plus, Search, ShieldCheck, Boxes, Table2,
  SquareTerminal, Stethoscope, Plug2, FolderPlus, Trash2, RefreshCw, KeyRound, Lock,
  Loader2, X, ChevronRight, ChevronDown, ChevronLeft, Pencil, LayoutGrid, Folder, Server,
  Filter, Code2, RotateCcw,
} from 'lucide-react';
import { api } from './lib/api';
import type { CheckResult, Cluster, Source, TableInfo } from './lib/types';
import { applyAppearance, loadLocalAppearance, mergeSettingsMap, saveLocalAppearance, type Appearance } from './lib/appearance';
import Menubar from './components/Menu';
import SettingsSheet from './components/SettingsSheet';
import type { Tab } from './components/menuTypes';
import { Badge, Button, Card, Empty, IconBtn, Input, ConfirmModal } from './components/ui';
import AddDatabaseWizard, { StatusDot } from './components/Wizard';
import McpPanel, { FleetCards } from './components/McpPanel';
import { QueryPane, DoctorPane, ResultsGrid } from './components/Data';
import { cn } from './lib/cn';

function ResizeHandle({ onDown, title }: { onDown: (e: React.MouseEvent) => void; title: string }) {
  return (
    <div onMouseDown={onDown} title={title}
      className="w-1.5 shrink-0 cursor-col-resize self-stretch rounded-full bg-transparent transition-colors hover:bg-zinc-700 active:acc-bg" />
  );
}

interface BuilderFilter {
  column: string;
  op: string;
  value: string;
  logic: 'AND' | 'OR';
}

const FILTER_OPS = [
  { v: 'contains', label: 'contains' },
  { v: 'eq', label: '=' },
  { v: 'ne', label: '≠' },
  { v: 'gt', label: '>' },
  { v: 'gte', label: '≥' },
  { v: 'lt', label: '<' },
  { v: 'lte', label: '≤' },
  { v: 'starts', label: 'starts with' },
  { v: 'ends', label: 'ends with' },
  { v: 'like', label: 'LIKE %' },
  { v: 'in', label: 'IN (a,b)' },
  { v: 'null', label: 'is null' },
  { v: 'notnull', label: 'not null' },
];

export default function App() {
  const [clusters, setClusters] = useState<Cluster[]>([]);
  const [sources, setSources] = useState<Source[]>([]);
  const [activeId, setActiveId] = useState<string>('');
  const [tab, setTab] = useState<Tab>('fleet');
  const [wizard, setWizard] = useState(false);
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [tableFilter, setTableFilter] = useState('');
  const [selTable, setSelTable] = useState<TableInfo | null>(null);
  const [sample, setSample] = useState<any>(null);
  const [sampling, setSampling] = useState(false);
  const [sampleErr, setSampleErr] = useState('');
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(50);
  const [refreshing, setRefreshing] = useState(false);
  // Beekeeper-style builder filters: {column, op, value, logic(AND|OR with previous)}
  const [filters, setFilters] = useState<BuilderFilter[]>([]);
  const [rawMode, setRawMode] = useState(false);
  const [rawWhere, setRawWhere] = useState('');
  // page cache: nothing refetches until Refresh (keyed by everything)
  const pageCache = useRef(new Map<string, any>());
  const cacheOrder = useRef<string[]>([]);
  const lastReq = useRef('');
  // column cache per table (schema list itself is names-only and fast)
  const colCache = useRef(new Map<string, any[]>());
  const [fleet, setFleet] = useState<any>(null);
  const [mcpUrl, setMcpUrl] = useState('http://127.0.0.1:9413');
  const [storePath, setStorePath] = useState('');
  const [showNewCluster, setShowNewCluster] = useState(false);
  const [newClusterName, setNewClusterName] = useState('');
  const [actionBusy, setActionBusy] = useState('');
  const [actionChecks, setActionChecks] = useState<CheckResult[] | null>(null);
  const [actionMsg, setActionMsg] = useState('');
  // Beekeeper-style connection tree + cross-pane navigation
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [schemaCache, setSchemaCache] = useState<Record<string, TableInfo[]>>({});
  const [schemaLoading, setSchemaLoading] = useState('');
  const [schemaErr, setSchemaErr] = useState<Record<string, string>>({});
  const [pendingTable, setPendingTable] = useState<{ schema: string; name: string } | null>(null);
  const [querySeed, setQuerySeed] = useState<{ n: number; sql: string } | null>(null);
  const [editingSource, setEditingSource] = useState<Source | null>(null);
  const [dragSrc, setDragSrc] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<{ title: string; body: string; confirmLabel: string; action: () => Promise<void> } | null>(null);
  const [confirmBusy, setConfirmBusy] = useState(false);
  const [appearance, setAppearance] = useState<Appearance>(() => loadLocalAppearance());
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [sideOpen, setSideOpen] = useState(true);
  const [mcpConfig, setMcpConfig] = useState('');
  const [logs, setLogs] = useState<string[]>([]);
  const [logPath, setLogPath] = useState('');
  const [allowWrites, setAllowWrites] = useState(false);
  const [version, setVersion] = useState('dev');

  // resizable panels (drag the divider; persisted locally + in SQLite ui.*)
  function usePanelWidth(key: string, def: number, min: number, max: number) {
    const [w, setW] = useState(() => {
      const v = Number(localStorage.getItem('readgate-' + key));
      return Number.isFinite(v) && v >= min && v <= max ? v : def;
    });
    const start = (e: React.MouseEvent) => {
      e.preventDefault();
      const x0 = e.clientX, w0 = w;
      const mv = (ev: MouseEvent) => setW(Math.min(max, Math.max(min, w0 + ev.clientX - x0)));
      const up = () => {
        window.removeEventListener('mousemove', mv);
        window.removeEventListener('mouseup', up);
        setW((cur) => {
          try {
            localStorage.setItem('readgate-' + key, String(cur));
            api.SetSetting('ui.' + key, String(cur)).catch(() => {});
          } catch {}
          return cur;
        });
      };
      window.addEventListener('mousemove', mv);
      window.addEventListener('mouseup', up);
    };
    const apply = (v: number) => {
      if (Number.isFinite(v) && v >= min && v <= max) setW(v);
    };
    return [w, start, apply] as const;
  }
  const [sideW, startSide, applySideW] = usePanelWidth('sidebarWidth', 240, 200, 440);
  const [browseW, startBrowse, applyBrowseW] = usePanelWidth('browseWidth', 250, 180, 520);

  const active = useMemo(() => sources.find((s) => s.id === activeId) || sources[0], [sources, activeId]);
  const orphans = useMemo(() => sources.filter((s) => !clusters.some((c) => c.id === s.clusterId)), [sources, clusters]);

  async function refresh() {
    const [c, s, f, m] = await Promise.all([api.ListClusters(), api.ListSources(), api.FleetOverview(), api.MCPStatus()]);
    setClusters(c as Cluster[]); setSources(s as Source[]); setFleet(f);
    setMcpUrl((m as any)?.url || 'http://127.0.0.1:9413');
    try { setStorePath(String(await api.StorePath())); } catch {}
    try { setMcpConfig(String(await api.MCPConfig())); } catch {}
    if (!activeId && (s as Source[])[0]) setActiveId((s as Source[])[0].id);
  }

  useEffect(() => { refresh(); }, []);

  useEffect(() => {
    api.Version().then((v: any) => { if (v) setVersion(String(v)); }).catch(() => {});
  }, []);

  // appearance: instant local first, then backend merge wins per key
  useEffect(() => { applyAppearance(appearance); }, [appearance]);
  useEffect(() => { if (settingsOpen) reloadLogs(); }, [settingsOpen ]);
  useEffect(() => {
    api.GetSettings().then((m: any) => {
      if (!m) return;
      setAppearance((prev) => {
        const merged = mergeSettingsMap(prev, m as Record<string, string>);
        saveLocalAppearance(merged);
        return merged;
      });
      const sw = Number(m['ui.sidebarWidth']);
      if (Number.isFinite(sw)) applySideW(sw);
      const bw = Number(m['ui.browseWidth']);
      if (Number.isFinite(bw)) applyBrowseW(bw);
      if (m['app.allowWrites'] === 'on') setAllowWrites(true);
    }).catch(() => {});
  }, []);

  function patchAppearance(p: Partial<Appearance>) {
    setAppearance((prev) => {
      const next = { ...prev, ...p };
      saveLocalAppearance(next);
      (Object.keys(p) as (keyof Appearance)[]).forEach((k) => {
        api.SetSetting('appearance.' + k, next[k]).catch(() => {});
      });
      return next;
    });
  }

  async function reloadLogs() {
    try {
      setLogs(((await api.GetLogs(200)) as unknown as string[]) || []);
      setLogPath(String(await api.LogPath()));
    } catch {}
  }

  async function clearLogs() {
    try {
      await api.ClearLogs();
      setLogs([]);
    } catch {}
  }

  function toggleWriteMode() {
    setAllowWrites((v) => {
      const next = !v;
      api.SetWriteMode(next).catch(() => {});
      return next;
    });
  }
  async function copyMCP() {
    try {
      const cfg = mcpConfig || String(await api.MCPConfig());
      navigator.clipboard.writeText(cfg);
      setActionMsg('MCP config copied — paste into opencode.json');
    } catch (e: any) {
      setActionMsg('copy failed: ' + (e?.message || String(e)));
    }
  }

  useEffect(() => {
    if (!active || tab !== 'browse') return;
    const useList = (list: TableInfo[]) => {
      setTables(list);
      const want = pendingTable;
      setPendingTable(null);
      const match = (want && list.find((t) => t.schema === want.schema && t.name === want.name))
        || (selTable && list.find((t) => t.schema === selTable.schema && t.name === selTable.name))
        || list[0];
      if (!match) return;
      // cached view (data + filters) survives tab switches untouched
      if (selTable && match.schema === selTable.schema && match.name === selTable.name && sample) return;
      selectTable(match);
    };
    const cached = schemaCache[active.id];
    if (cached && cached.length) {
      useList(cached);
      return;
    }
    setTables([]); setSelTable(null); setSample(null);
    api.GetSchema(active.id).then((sch: any) => {
      const list = sch.tables || [];
      setSchemaCache((c) => ({ ...c, [active.id]: list }));
      setSchemaErr((e) => { const n = { ...e }; delete n[active.id]; return n; });
      useList(list);
    }).catch((e: any) => {
      setSchemaErr((er) => ({ ...er, [active.id]: e?.message || String(e) }));
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active?.id, tab]);

  function cachePut(k: string, v: any) {
    if (!pageCache.current.has(k)) {
      cacheOrder.current.push(k);
      if (cacheOrder.current.length > 40) {
        const old = cacheOrder.current.shift();
        if (old) pageCache.current.delete(old);
      }
    }
    pageCache.current.set(k, v);
  }

  function clearSourceCache() {
    const pfx = (active?.id || '') + '|';
    for (const k of [...pageCache.current.keys()]) {
      if (k.startsWith(pfx)) pageCache.current.delete(k);
    }
  }

  async function loadPage(t: TableInfo, p: number, size: number, flt: BuilderFilter[], raw: string, force = false) {
    if (!active) return;
    const key = `${active.id}|${t.schema}.${t.name}|${p}|${size}|${JSON.stringify(flt)}|${raw}`;
    lastReq.current = key;
    setSelTable(t); setPage(p); setSampleErr('');
    const hit = force ? undefined : pageCache.current.get(key);
    if (hit) {
      setSample(hit);
      return;
    }
    setSampling(true);
    try {
      const res = await api.PreviewTable(active.id, t.schema, t.name, size, p * size, flt, raw);
      cachePut(key, res);
      if (lastReq.current === key) setSample(res);
    } catch (e: any) {
      if (lastReq.current === key) {
        setSampleErr(e?.message || String(e));
        setSample(null);
      }
    } finally {
      if (lastReq.current === key) setSampling(false);
    }
  }

  // Selecting a table ALWAYS refreshes its data + lazy-loads its columns.
  // Paging, filtering and tab switches reuse cache; only Refresh clears it.
  function selectTable(t: TableInfo) {
    if (!active) return;
    setFilters([]); setRawWhere(''); setRawMode(false); setSample(null); setSampleErr('');
    const ck = `${active.id}|${t.schema}.${t.name}`;
    const cols = colCache.current.get(ck);
    setSelTable(cols ? { ...t, columns: cols } : t);
    if (!cols) {
      api.GetTableColumns(active.id, t.schema, t.name).then((c: any) => {
        const arr = (c || []) as any[];
        colCache.current.set(ck, arr);
        setTables((ts) => ts.map((x) => (x.schema === t.schema && x.name === t.name ? { ...x, columns: arr } : x)));
        setSelTable((cur) => (cur && cur.schema === t.schema && cur.name === t.name ? { ...cur, columns: arr } : cur));
      }).catch(() => {});
    }
    loadPage(t, 0, pageSize, [], '', true);
  }

  // debounced filter apply (Beekeeper-style: edit → auto refetch page 1)
  useEffect(() => {
    if (!selTable || tab !== 'browse') return;
    const raw = rawMode ? rawWhere : '';
    const key = `${active?.id}|${selTable.schema}.${selTable.name}|0|${pageSize}|${JSON.stringify(filters)}|${raw}`;
    if (key === lastReq.current) return;
    const h = setTimeout(() => loadPage(selTable, 0, pageSize, filters, raw), 500);
    return () => clearTimeout(h);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters, rawWhere, rawMode]);

  async function refreshTables() {
    if (!active || refreshing) return;
    setRefreshing(true); setSampleErr('');
    // Refresh = clear EVERYTHING for this source, then reload schema + data
    clearSourceCache();
    for (const k of [...colCache.current.keys()]) {
      if (k.startsWith(active.id + '|')) colCache.current.delete(k);
    }
    setSchemaCache((c) => { const n = { ...c }; delete n[active.id]; return n; });
    try {
      const sch = (await api.GetSchema(active.id)) as unknown as { tables: TableInfo[] };
      const list = sch.tables || [];
      setTables(list);
      setSchemaCache((c) => ({ ...c, [active.id]: list }));
      setSchemaErr((e) => { const n = { ...e }; delete n[active.id]; return n; });
      if (selTable) {
        const still = list.find((t) => t.schema === selTable.schema && t.name === selTable.name);
        if (still) {
          setSelTable({ ...still, columns: undefined });
          loadPage(still, page, pageSize, filters, rawMode ? rawWhere : '', true);
          api.GetTableColumns(active.id, still.schema, still.name).then((c: any) => {
            const arr = (c || []) as any[];
            colCache.current.set(`${active.id}|${still.schema}.${still.name}`, arr);
            setSelTable((cur) => (cur ? { ...cur, columns: arr } : cur));
          }).catch(() => {});
        }
      }
    } catch (e: any) {
      setSchemaErr((er) => ({ ...er, [active.id]: e?.message || String(e) }));
    } finally {
      setRefreshing(false);
    }
  }

  async function createCluster() {
    if (!newClusterName.trim()) return;
    const palette = ['#10b981', '#6366f1', '#f59e0b', '#ec4899', '#06b6d4'];
    await api.SaveCluster({ id: '', name: newClusterName.trim(), description: '', color: palette[clusters.length % palette.length], createdAt: '' });
    setNewClusterName(''); setShowNewCluster(false); refresh();
  }

  async function reverify() {
    if (!active) return;
    setActionBusy('verify'); setActionChecks(null); setActionMsg('');
    try {
      setActionChecks((await api.ReverifySource(active.id)) as CheckResult[]);
      refresh();
    } catch (e: any) {
      setActionMsg(e?.message || String(e));
    } finally {
      setActionBusy('');
    }
  }

  async function serverCheck() {
    if (!active) return;
    setActionBusy('server'); setActionChecks(null); setActionMsg('');
    try {
      setActionChecks((await api.DiagnoseServer(active)) as CheckResult[]);
    } catch (e: any) {
      setActionMsg(e?.message || String(e));
    } finally {
      setActionBusy('');
    }
  }
  async function sshTest() {
    if (!active) return;
    setActionBusy('ssh'); setActionChecks(null); setActionMsg('');
    try {
      setActionMsg(String(await api.TestSSHByID(active.id)));
    } catch (e: any) {
      setActionMsg(e?.message || String(e));
    } finally {
      setActionBusy('');
    }
  }

  function renderSource(s: Source) {
    const isOpen = !!expanded[s.id];
    const cached = schemaCache[s.id];
    const err = schemaErr[s.id];
    return (
        <div key={s.id} draggable
        onDragStart={(e) => { e.dataTransfer.setData('text/readgate-source', s.id); e.dataTransfer.effectAllowed = 'move'; setDragSrc(s.id); }}
        onDragEnd={() => { setDragSrc(null); setDropTarget(null); }}
        className={cn('rounded-lg', dragSrc === s.id && 'opacity-40')}>
        <div onClick={() => setActiveId(s.id)}
          className={cn('group flex w-full cursor-pointer select-none items-center gap-0.5 rounded-lg pr-1 text-left text-[13px] transition-colors hover:bg-zinc-800', active?.id === s.id && 'acc-bar bg-zinc-800')}>
          <span onClick={(e) => e.stopPropagation()}>
            <IconBtn title={isOpen ? 'Collapse tables' : 'Expand tables'} size="sm" onClick={() => toggleExpand(s)}>
              {schemaLoading === s.id ? <Loader2 size={13} className="animate-spin" /> : isOpen ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
            </IconBtn>
          </span>
          <button type="button" onClick={() => { setActiveId(s.id); }} className="dyp flex min-w-0 flex-1 cursor-pointer items-center gap-2 py-1.5 text-left [&_svg]:pointer-events-none">
            <StatusDot status={s.status} verified={s.readOnlyVerified} />
            <Database size={13} className="shrink-0 text-zinc-500" />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium text-zinc-200">{s.name}</span>
              <span className="block truncate font-mono text-[10px] text-zinc-500">{s.engine} · {s.database}{s.mode === 'ssh' ? ' · ssh' : ''}</span>
            </span>
            {s.readOnlyVerified && <Lock size={11} className="text-emerald-500/70" />}
          </button>
          <span onClick={(e) => e.stopPropagation()}>
            <IconBtn title={`Edit ${s.name}`} size="sm" className="opacity-0 group-hover:opacity-100" onClick={() => { setEditingSource(s); setWizard(true); }}><Pencil size={13} /></IconBtn>
          </span>
        </div>
        {isOpen && (
          <div className="mb-1 ml-5 border-l border-zinc-800 pl-1">
            {err && (
              <div className="rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-red-300">
                schema failed: {err}
                <button className="ml-1 underline hover:text-red-100" onClick={() => { setActiveId(s.id); reverify(); }}>re-verify</button>
              </div>
            )}
            {!err && !cached && schemaLoading !== s.id && <div className="px-2 py-1 text-[11px] text-zinc-600">no schema — check connection</div>}
            {(cached || []).map((t) => (
              <button key={t.schema + '.' + t.name} onClick={() => openTable(s, t.schema, t.name)}
                className="flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left font-mono text-[12px] text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100">
                <Table2 size={12} className="shrink-0 text-zinc-600" />
                <span className="truncate">{t.schema}.{t.name}</span>
                <span className="ml-auto font-mono text-[10px] text-zinc-600">{t.rowEstimate > 1000 ? (t.rowEstimate / 1000).toFixed(1) + 'k' : t.rowEstimate}</span>
              </button>
            ))}
          </div>
        )}
      </div>
    );
  }

  async function toggleExpand(s: Source) {
    const willOpen = !expanded[s.id];
    setExpanded((e) => ({ ...e, [s.id]: willOpen }));
    if (willOpen && !schemaCache[s.id]) {
      setSchemaLoading(s.id);
      try {
        const sch = (await api.GetSchema(s.id)) as unknown as { tables: TableInfo[] };
        setSchemaCache((c) => ({ ...c, [s.id]: sch.tables || [] }));
        setSchemaErr((e) => { const n = { ...e }; delete n[s.id]; return n; });
      } catch (e: any) {
        setSchemaCache((c) => ({ ...c, [s.id]: [] }));
        setSchemaErr((e2) => ({ ...e2, [s.id]: e?.message || String(e) }));
      } finally {
        setSchemaLoading('');
      }
    }
  }

  async function moveSource(sourceId: string, clusterId: string) {
    const s = sources.find((x) => x.id === sourceId);
    if (!s || s.clusterId === clusterId) return;
    try {
      await api.SaveSource({ ...s, clusterId });
      setActionMsg(`moved ${s.name} → ${clusters.find((c) => c.id === clusterId)?.name || 'Ungrouped'}`);
      refresh();
    } catch (e: any) {
      setActionMsg('move failed: ' + (e?.message || String(e)));
    }
  }

  function clusterDropProps(clusterId: string) {
    return {
      onDragOver: (e: React.DragEvent) => { if (dragSrc) { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; setDropTarget(clusterId); } },
      onDragLeave: () => setDropTarget((t) => (t === clusterId ? null : t)),
      onDrop: (e: React.DragEvent) => {
        e.preventDefault();
        const sid = e.dataTransfer.getData('text/readgate-source') || dragSrc;
        setDropTarget(null); setDragSrc(null);
        if (sid) moveSource(sid, clusterId);
      },
    };
  }

  function openTable(s: Source, schema: string, name: string) {
    setActiveId(s.id);
    setPendingTable({ schema, name });
    setTab('browse');
  }

  function selectTablePage(t: TableInfo, p: number) {
    loadPage(t, p, pageSize, filters, rawMode ? rawWhere : '');
  }

  function totalLabel(): string {
    if (sample && typeof sample.totalRows === 'number' && sample.totalRows >= 0) {
      return sample.totalRows.toLocaleString();
    }
    return '~' + (selTable?.rowEstimate || 0).toLocaleString();
  }

  function hasNextPage(): boolean {
    if (!sample) return false;
    if (typeof sample.totalRows === 'number' && sample.totalRows >= 0) {
      return (page + 1) * pageSize < sample.totalRows;
    }
    return !!sample.truncated;
  }

  function FilterBuilder({ columns, filters, setFilters, rawMode, setRawMode, rawWhere, setRawWhere }: {
    columns: string[]; filters: BuilderFilter[]; setFilters: (f: BuilderFilter[]) => void;
    rawMode: boolean; setRawMode: (v: boolean) => void; rawWhere: string; setRawWhere: (v: string) => void;
  }) {
    const patch = (i: number, p: Partial<BuilderFilter>) =>
      setFilters(filters.map((f, j) => (j === i ? { ...f, ...p } : f)));
    const noValue = (op: string) => op === 'null' || op === 'notnull';
    return (
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/80 px-2.5 py-2">
        <div className="flex items-center gap-1.5">
          <Filter size={12} className="text-zinc-500" />
          <span className="text-[11px] font-medium text-zinc-400">
            Filters {filters.length > 0 && <span className="acc-text font-mono">· {filters.length} active</span>}
            {rawMode && <span className="acc-text font-mono"> · raw</span>}
          </span>
          <span className="ml-auto flex gap-1">
            <button onClick={() => setRawMode(!rawMode)} title={rawMode ? 'Builder mode' : 'Raw WHERE mode (<>)'}
              className={cn('rounded-md border px-1.5 py-1 font-mono text-[11px]',
                rawMode ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-200' : 'border-zinc-700 text-zinc-400 hover:bg-zinc-800')}>
              {'<>'}
            </button>
            {(filters.length > 0 || rawWhere) && (
              <button onClick={() => { setFilters([]); setRawWhere(''); }} title="Clear all filters"
                className="flex items-center gap-1 rounded-md border border-zinc-700 px-1.5 py-1 text-[11px] text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200">
                <RotateCcw size={11} /> Clear
              </button>
            )}
          </span>
        </div>
        {rawMode ? (
          <input value={rawWhere} onChange={(e) => setRawWhere(e.target.value)}
            placeholder="status = 'paid' AND total > 100  — no semicolons"
            spellCheck={false}
            className="mt-1.5 w-full rounded-md border border-zinc-700 bg-zinc-950 px-2 py-1.5 font-mono text-[11px] text-zinc-100 outline-none placeholder:text-zinc-600 focus:border-emerald-500/60" />
        ) : (
          <div className="mt-1.5 grid gap-1">
            {filters.map((f, i) => (
              <div key={i} className="flex items-center gap-1">
                {i === 0 ? (
                  <span className="w-12 shrink-0 font-mono text-[10px] text-zinc-600">WHERE</span>
                ) : (
                  <button onClick={() => patch(i, { logic: f.logic === 'AND' ? 'OR' : 'AND' })} title="Toggle AND/OR"
                    className="w-12 shrink-0 rounded border border-zinc-700 bg-zinc-950 py-1 font-mono text-[10px] text-emerald-300/90 hover:bg-zinc-800">
                    {f.logic}
                  </button>
                )}
                <select value={f.column} onChange={(e) => patch(i, { column: e.target.value })}
                  className="min-w-0 flex-1 rounded border border-zinc-700 bg-zinc-950 px-1.5 py-1 font-mono text-[11px] text-zinc-200 outline-none">
                  {columns.map((c) => <option key={c} value={c}>{c}</option>)}
                </select>
                <select value={f.op} onChange={(e) => patch(i, { op: e.target.value })}
                  className="shrink-0 rounded border border-zinc-700 bg-zinc-950 px-1.5 py-1 font-mono text-[11px] text-zinc-200 outline-none">
                  {FILTER_OPS.map((o) => <option key={o.v} value={o.v}>{o.label}</option>)}
                </select>
                {!noValue(f.op) && (
                  <input value={f.value} onChange={(e) => patch(i, { value: e.target.value })}
                    placeholder={f.op === 'in' ? 'a, b, c' : f.op === 'like' ? '%foo%' : 'value'}
                    className="min-w-0 flex-1 rounded border border-zinc-700 bg-zinc-950 px-1.5 py-1 font-mono text-[11px] text-zinc-100 outline-none placeholder:text-zinc-600 focus:border-emerald-500/60" />
                )}
                <button onClick={() => setFilters(filters.filter((_, j) => j !== i))} title="Remove filter"
                  className="shrink-0 rounded p-1 text-zinc-600 hover:bg-zinc-800 hover:text-red-300"><X size={12} /></button>
              </div>
            ))}
            <button onClick={() => setFilters([...filters, { column: columns[0] || '', op: 'contains', value: '', logic: 'AND' }])}
              className="flex items-center justify-center gap-1 rounded-md border border-dashed border-zinc-700 px-2 py-1 text-[11px] text-zinc-500 hover:border-zinc-500 hover:text-zinc-200">
              <Plus size={11} /> Add filter
            </button>
          </div>
        )}
      </div>
    );
  }
  function openInQuery(schema: string, name: string) {
    const eng = (active as any)?.engine || 'postgres';
    const mysqlish = eng === 'mysql' || eng === 'mariadb' || eng === 'tidb';
    const q = mysqlish ? `\`${schema}\`.\`${name}\`` : `"${schema}"."${name}"`;
    setQuerySeed({ n: Date.now(), sql: `SELECT * FROM ${q} LIMIT 200` });
    setTab('query');
  }

  const filtered = tables.filter((t) => (t.schema + '.' + t.name).toLowerCase().includes(tableFilter.toLowerCase()));

  const tabs: { id: Tab; label: string; icon: any }[] = [
    { id: 'fleet', label: 'Fleet', icon: Boxes },
    { id: 'browse', label: 'Browse', icon: Table2 },
    { id: 'query', label: 'Query', icon: SquareTerminal },
    { id: 'doctor', label: 'Doctor', icon: Stethoscope },
    { id: 'mcp', label: 'MCP', icon: Plug2 },
  ];

  return (
    <div className="flex h-full flex-col bg-zinc-950 text-zinc-200">
      <Menubar tab={tab} onTab={setTab}
        onAdd={() => { setEditingSource(null); setWizard(true); }}
        onNewCluster={() => { setTab('fleet'); setShowNewCluster(true); }}
        onCopyMCP={copyMCP}
        onOpenSettings={() => setSettingsOpen(true)}
        onToggleSidebar={() => setSideOpen((v) => !v)} />
      <div className="flex min-h-0 flex-1">
      {/* sidebar — fleet */}
      {sideOpen && (
      <>
      <aside className="glassbar flex min-h-0 shrink-0 flex-col border-r border-zinc-800 bg-zinc-900/60" style={{ width: sideW }}>
        <div className="flex items-center gap-2 px-3 pb-2.5 pt-3">
          <div className="acc-bg acc-on flex h-7 w-7 items-center justify-center rounded-lg"><ShieldCheck size={16} strokeWidth={2.5} /></div>
          <div>
            <div className="text-sm font-bold tracking-tight text-white">ReadGate</div>
          </div>
          <Button variant="emerald" className="ml-auto !px-2 !py-1 text-[11px]" onClick={() => setWizard(true)}><Plus size={13} /> Add</Button>
        </div>

        <div className="flex-1 overflow-y-auto px-2 pb-2">
          {clusters.map((c) => {
            const members = sources.filter((s) => s.clusterId === c.id);
            const ungrouped = c.id === '' ? sources.filter((s) => !s.clusterId) : [];
            const list = c.id === '' ? ungrouped : members;
            if (c.id !== '' || list.length > 0) return (
              <div key={c.id || 'ungrouped'} className="mb-1">
                <div {...clusterDropProps(c.id)}
                  className={cn('flex items-center gap-1.5 rounded-md px-2 pb-0.5 pt-2',
                    dropTarget === c.id && 'bg-emerald-500/10 outline outline-1 outline-emerald-500/40')}>
                  {c.id === '' ? <Folder size={11} className="text-zinc-600" /> : <LayoutGrid size={11} style={{ color: c.color || '#52525b' }} />}
                  <span className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">{c.name || 'Ungrouped'}</span>
                  <span className="ml-auto font-mono text-[10px] text-zinc-600">
                    {list.filter((x) => x.readOnlyVerified).length}/{list.length}{dropTarget === c.id ? ' · drop here' : ''}
                  </span>
                  {c.id !== '' && (
                    <IconBtn title="Delete cluster" size="sm" className="hover:!text-red-300" onClick={() => setConfirm({
                      title: `Delete cluster "${c.name}"?`,
                      body: 'Sources inside become Ungrouped. Connections themselves are kept.',
                      confirmLabel: 'Delete cluster',
                      action: async () => { await api.DeleteCluster(c.id); refresh(); },
                    })}><Trash2 size={13} /></IconBtn>
                  )}
                </div>
                {list.map((s) => renderSource(s))}
              </div>
            );
            return null;
          })}
          {orphans.length > 0 && (
            <div className="mb-1">
              <div {...clusterDropProps('')}
                className={cn('flex items-center gap-1.5 rounded-md px-2 pb-0.5 pt-2',
                  dropTarget === '' && 'bg-emerald-500/10 outline outline-1 outline-emerald-500/40')}>
                <Folder size={11} className="text-zinc-600" />
                <span className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Ungrouped — drag into a cluster</span>
                <span className="ml-auto font-mono text-[10px] text-zinc-600">{orphans.length}{dropTarget === '' ? ' · drop here' : ''}</span>
              </div>
              {orphans.map((s) => renderSource(s))}
            </div>
          )}

          <button onClick={() => setShowNewCluster(true)} className="mt-2 flex w-full items-center gap-2 rounded-lg border border-dashed border-zinc-800 px-3 py-2 text-xs text-zinc-500 hover:border-zinc-700 hover:text-zinc-300">
            <FolderPlus size={13} /> New cluster — group the fleet
          </button>
          {showNewCluster && (
            <div className="mt-2 rounded-lg border border-zinc-800 bg-zinc-950 p-2.5">
              <Input value={newClusterName} onChange={(e) => setNewClusterName(e.target.value)} placeholder="e.g. EU-prod, Staging…" onKeyDown={(e) => e.key === 'Enter' && createCluster()} />
              <div className="mt-2 flex gap-1.5">
                <Button variant="emerald" className="!py-1.5 text-xs" onClick={createCluster}>Create</Button>
                <Button variant="ghost" className="!py-1.5 text-xs" onClick={() => setShowNewCluster(false)}>Cancel</Button>
              </div>
            </div>
          )}
        </div>

        <div className="border-t border-zinc-800 p-3">
          <div className="flex items-center gap-2 rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-3 py-2">
            <span className="relative flex h-2 w-2"><span className="absolute h-full w-full animate-ping rounded-full bg-emerald-400 opacity-60" /><span className="h-2 w-2 rounded-full bg-emerald-400" /></span>
            <span className="font-mono text-[11px] text-emerald-200/90">mcp · {mcpUrl.replace('http://', '')}</span>
            <button onClick={refresh} className="ml-auto text-zinc-500 hover:text-zinc-200"><RefreshCw size={13} /></button>
          </div>
          <div className="mt-2 flex items-center gap-1.5 text-[11px] text-zinc-600"><KeyRound size={12} /> AI sees names only · keys stay here</div>
        </div>
      </aside>
      <div className="flex items-stretch py-2">
        <ResizeHandle onDown={startSide} title="Drag to resize sidebar" />
      </div>
      </>
      )}

      {/* main */}
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-zinc-800 bg-zinc-950/80 px-3 py-2 backdrop-blur">
          <div className="flex max-w-full items-center gap-0.5 overflow-x-auto rounded-lg border border-zinc-800 bg-zinc-900 p-0.5">
            {tabs.map((t) => (
              <button key={t.id} onClick={() => setTab(t.id)} title={t.label}
                className={cn('flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1 text-xs text-zinc-400 hover:text-zinc-100', tab === t.id && 'bg-zinc-700/60 text-white shadow')}>
                <t.icon size={13} /> <span className="hidden min-[1100px]:inline">{t.label}</span>
              </button>
            ))}
          </div>
          {active && tab !== 'fleet' && tab !== 'mcp' && (
            <div className="flex min-w-0 items-center gap-1.5 text-[13px]">
              <span className="hidden text-zinc-600 min-[900px]:inline">→</span>
              <span className="max-w-40 truncate font-medium text-zinc-100">{active.name}</span>
              <Badge tone={active.readOnlyVerified ? 'green' : 'zinc'}>{active.readOnlyVerified ? 'ready · read-only proven' : active.status}</Badge>
              {active.mode === 'ssh' && <span className="hidden min-[1300px]:inline"><Badge tone="indigo"><Server size={10} /> {active.sshHost || 'server'} · :5432</Badge></span>}
            </div>
          )}
          <div className="ms-auto flex flex-wrap items-center justify-end gap-1.5">
            {active && tab !== 'fleet' && tab !== 'mcp' && (
              <>
                <IconBtn title={`Edit ${active.name}`} onClick={() => { setEditingSource(active); setWizard(true); }}><Pencil size={14} /></IconBtn>
                <Button variant="outline" className="!py-1.5 text-[11px]" onClick={reverify} disabled={actionBusy !== ''} title="Re-run read-only proof">
                  {actionBusy === 'verify' ? <Loader2 size={12} className="animate-spin" /> : <ShieldCheck size={12} />} <span className="hidden min-[1250px]:inline">Verify</span>
                </Button>
                {active.mode === 'ssh' && (
                  <Button variant="outline" className="!py-1.5 text-[11px]" onClick={sshTest} disabled={actionBusy !== ''} title="SSH handshake only">
                    {actionBusy === 'ssh' ? <Loader2 size={12} className="animate-spin" /> : <Plug2 size={12} />} <span className="hidden min-[1250px]:inline">SSH test</span>
                  </Button>
                )}
                {active.mode === 'ssh' && (
                  <Button variant="outline" className="!py-1.5 text-[11px]" onClick={serverCheck} disabled={actionBusy !== ''} title="Inspect server: listener, role, pg_hba">
                    {actionBusy === 'server' ? <Loader2 size={12} className="animate-spin" /> : <Server size={12} />} <span className="hidden min-[1250px]:inline">Server check</span>
                  </Button>
                )}
              </>
            )}
            {active && <Button variant="outline" className="!py-1.5 text-[11px]" onClick={() => active && setTab('doctor')} title="Ask AI to diagnose"><Stethoscope size={12} /> <span className="hidden min-[1400px]:inline">Ask AI to diagnose</span></Button>}
            <Button variant="emerald" className="!py-1.5 text-[11px]" onClick={() => setWizard(true)}><Plus size={12} /> <span className="hidden min-[1100px]:inline">Add Database</span></Button>
          </div>
        </header>

        <div className="grid-paper flex-1 overflow-y-auto p-3">
          {tab === 'fleet' && (
            <div className="mx-auto grid max-w-5xl gap-3">
              <Card className="flex flex-wrap items-center gap-3 p-4">
                <div className="flex items-center gap-3">
                  <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-300"><Boxes size={18} /></div>
                  <div>
                    <div className="text-[15px] font-semibold text-white">Your database fleet, AI-readable — credentials never leave this box.</div>
                    <div className="mt-0.5 text-xs text-zinc-500">Group sources into <span className="text-zinc-300">clusters</span> so the agent reasons about Production vs Analytics vs Billing. Every source enables only after the gateway <span className="text-emerald-300">proves read-only</span>.</div>
                  </div>
                </div>
                <div className="ml-auto flex gap-1.5">
                  <Button variant="outline" className="!py-1.5 text-[11px]" onClick={() => setTab('mcp')}><Plug2 size={13} /> MCP setup</Button>
                  <Button variant="emerald" className="!py-1.5 text-[11px]" onClick={() => setWizard(true)}><Plus size={13} /> Add Database</Button>
                </div>
              </Card>
              {clusters.length === 0 && sources.length === 0 ? (
                <Empty icon={<Database size={22} />} title="Empty fleet — add your first database"
                  hint="Create a source with an SSH tunnel + dedicated read-only user. Nothing is demo data: everything here comes from SQLite." />
              ) : (
                <FleetCards fleet={fleet} onSelect={(name) => { const s = sources.find((x) => x.name === name); if (s) { setActiveId(s.id); setTab('browse'); } }} />
              )}
              <Card className="p-3">
                <div className="mb-1.5 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-zinc-500"><ShieldCheck size={12} /> Onboarding invariant — enforced, not trusted</div>
                <div className="flex flex-wrap gap-1.5 font-mono text-[11px]">
                  {['Connectivity ✓', 'Authentication ✓', 'Read access ✓', 'Write denied ✓', '→ Source enabled'].map((s) => (
                    <span key={s} className="rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1 text-zinc-400">{s}</span>
                  ))}
                </div>
                <div className="mt-2 flex items-center gap-1.5 font-mono text-[11px] text-zinc-600"><Server size={11} /> store: {storePath || 'loading…'} · SQLite (WAL) + AES-GCM passwords</div>
              </Card>
            </div>
          )}

          {tab === 'browse' && (
            !active ? <Empty icon={<Database size={20} />} title="No source selected" hint="Add a database to browse it Beekeeper-style." /> : (
              <div className="flex flex-col gap-3 lg:flex-row">
                <div className="min-h-0 shrink-0" style={{ width: browseW }}>
                <Card className="flex max-h-[75vh] flex-col overflow-hidden">
                  <div className="flex items-center gap-1.5 border-b border-zinc-800 p-2">
                    <div className="relative flex-1">
                      <Search size={13} className="absolute left-2.5 top-2 text-zinc-600" />
                      <Input value={tableFilter} onChange={(e) => setTableFilter(e.target.value)} placeholder="Filter tables…" className="!pl-8" />
                    </div>
                    <IconBtn title="Refresh schema + data" variant="outline" onClick={refreshTables} disabled={refreshing}>
                      <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
                    </IconBtn>
                  </div>
                  <div className="border-b border-zinc-800 px-2.5 py-1 font-mono text-[10px] text-zinc-600">
                    {refreshing ? 'Refreshing schema + data…' : `${filtered.length} tables · ${pageSize}/page`}
                  </div>
                  <div className="flex-1 overflow-y-auto p-1">
                    {active && schemaErr[active.id] && (
                      <div className="m-1 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-red-300">
                        schema failed: {schemaErr[active.id]}
                      </div>
                    )}
                    {filtered.map((t) => (
                      <button key={t.schema + '.' + t.name} onClick={() => selectTable(t)}
                        className={cn('flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs hover:bg-zinc-800', selTable?.name === t.name && selTable?.schema === t.schema && 'bg-zinc-800')}>
                        <Table2 size={12} className="text-zinc-500" />
                        <span className="truncate font-mono text-zinc-200">{t.schema}.{t.name}</span>
                        <span className="ml-auto font-mono text-[10px] text-zinc-600">{t.rowEstimate > 1000 ? (t.rowEstimate / 1000).toFixed(1) + 'k' : t.rowEstimate}</span>
                      </button>
                    ))}
                    {filtered.length === 0 && (
                      <div className="p-3 text-[11px] text-zinc-600">
                        {active && schemaErr[active.id]
                          ? 'Connection or permission problem — hit Verify in the header to see which check fails.'
                          : 'No tables. Check connection or schema grants.'}
                      </div>
                    )}
                  </div>
                </Card>
                </div>
                <div className="hidden items-stretch py-1 lg:flex">
                  <ResizeHandle onDown={startBrowse} title="Drag to resize tables panel" />
                </div>
                <div className="grid min-w-0 flex-1 content-start gap-3">
                  {selTable ? (
                    <>
                      <Card className="p-3">
                        <div className="flex items-center gap-1.5">
                          <Table2 size={13} className="text-zinc-500" />
                          <span className="font-mono text-[13px] text-zinc-100">{selTable.schema}.{selTable.name}</span>
                          <Badge tone="zinc">~{selTable.rowEstimate.toLocaleString()} rows</Badge>
                          <div className="ml-auto flex gap-1">
                            <Button variant="outline" className="!py-1 text-[11px]" onClick={() => selTable && openInQuery(selTable.schema, selTable.name)}><SquareTerminal size={11} /> Open in Query</Button>
                            <Button variant="ghost" className="!py-1 text-[11px]" onClick={() => setTab('doctor')}><Stethoscope size={11} /> Diagnose</Button>
                          </div>
                        </div>
                        <div className="mt-2 flex flex-wrap gap-1">
                          {(selTable.columns || []).map((c) => (
                            <span key={c.name} className="rounded-md border border-zinc-800 bg-zinc-950 px-1.5 py-0.5 font-mono text-[10px] text-zinc-400">{c.name} <span className="text-zinc-600">{c.type}</span></span>
                          ))}
                        </div>
                      </Card>
                      {selTable && (
                        <div className="flex items-center gap-1.5 rounded-xl border border-zinc-800 bg-zinc-900/80 px-2.5 py-1.5 text-[11px] text-zinc-500">
                          <button disabled={page === 0 || sampling} onClick={() => selectTablePage(selTable, page - 1)}
                            className="flex items-center gap-0.5 rounded-md px-1.5 py-1 hover:bg-zinc-800 hover:text-zinc-200 disabled:opacity-40">
                            <ChevronLeft size={13} /> Prev
                          </button>
                          <span className="font-mono">
                            Page {page + 1} · {sample ? `${page * pageSize + 1}–${page * pageSize + sample.rowCount}` : '…'} of {totalLabel()}
                          </span>
                          <button disabled={sampling || !hasNextPage()} onClick={() => selectTablePage(selTable, page + 1)}
                            className="flex items-center gap-0.5 rounded-md px-1.5 py-1 hover:bg-zinc-800 hover:text-zinc-200 disabled:opacity-40">
                            Next <ChevronRight size={13} />
                          </button>
                          <select value={pageSize} disabled={sampling}
                            onChange={(e) => { const n = Number(e.target.value); setPageSize(n); loadPage(selTable, 0, n, filters, rawMode ? rawWhere : ''); }}
                            className="ml-auto rounded-md border border-zinc-700 bg-zinc-950 px-1.5 py-1 font-mono text-[11px] text-zinc-300 outline-none">
                            {[25, 50, 100].map((n) => <option key={n} value={n}>{n}/page</option>)}
                          </select>
                        </div>
                      )}
                      {selTable && <FilterBuilder
                        columns={(selTable.columns || []).map((c) => c.name)}
                        filters={filters} setFilters={setFilters}
                        rawMode={rawMode} setRawMode={setRawMode}
                        rawWhere={rawWhere} setRawWhere={setRawWhere} />}
                      <ResultsGrid res={sample} edit={selTable && active ? {
                        sourceId: active.id,
                        schema: selTable.schema, table: selTable.name,
                        engine: (active as any).engine || 'postgres',
                        colTypes: Object.fromEntries((selTable.columns || []).map((c) => [c.name, c.type])),
                        allowWrites,
                        onApplied: () => loadPage(selTable, page, pageSize, filters, rawMode ? rawWhere : '', true),
                      } : undefined} />
                      {sampling && (
                        <div className="flex items-center gap-2 rounded-xl border border-zinc-800 bg-zinc-950 px-4 py-3 text-xs text-zinc-500">
                          <Loader2 size={14} className="animate-spin text-emerald-400" /> Loading preview…
                        </div>
                      )}
                      {sampleErr && (
                        <div className="rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-3 font-mono text-[11px] leading-relaxed text-red-300">
                          preview failed: {sampleErr}
                        </div>
                      )}
                    </>
                  ) : <Empty icon={<Table2 size={20} />} title="Pick a table" hint="Schema loads via the ai_readonly role — exactly what the AI sees." />}
                </div>
              </div>
            )
          )}

          {tab === 'query' && (
            <div className="mx-auto max-w-5xl">
              <QueryPane source={active} seed={querySeed} />
              <div className="mt-2 font-mono text-[11px] text-zinc-600">guard: SELECT/WITH/EXPLAIN/SHOW only · no stacked statements · writes rejected · runs as {active?.username || 'ai_readonly'} in READ ONLY txn</div>
            </div>
          )}

          {tab === 'doctor' && (
            <div className="mx-auto max-w-3xl"><DoctorPane source={active} /></div>
          )}

          {tab === 'mcp' && (
            <div className="mx-auto max-w-5xl"><McpPanel fleet={fleet} mcpUrl={mcpUrl} /></div>
          )}
        </div>

        {/* action results strip */}
        {(actionChecks || actionMsg) && (
          <div className="border-t border-zinc-800 bg-zinc-950 px-3 py-2">
            <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-1.5">
              {(actionChecks || []).map((c) => (
                <Badge key={c.key} tone={c.ok ? 'green' : 'red'}>{c.ok ? '✓' : '✕'} {c.label} · <span className="font-mono opacity-70">{c.detail}</span></Badge>
              ))}
              {actionMsg && <span className="font-mono text-[11px] text-zinc-400">{actionMsg}</span>}
              <button className="ml-auto rounded p-1 text-zinc-500 hover:text-zinc-200" onClick={() => { setActionChecks(null); setActionMsg(''); }}><X size={13} /></button>
            </div>
          </div>
        )}

        {/* sources strip */}
        <footer className="flex items-center gap-1.5 border-t border-zinc-800 bg-zinc-950 px-3 py-1.5 text-[11px] text-zinc-500">
          <span className="flex items-center gap-1 font-medium"><Database size={11} /> {sources.length} sources</span>
          <span>·</span>
          <span className="flex items-center gap-1 text-emerald-300/90"><ShieldCheck size={11} /> {sources.filter((s) => s.readOnlyVerified).length} proven read-only</span>
          <span>·</span>
          <span className="flex items-center gap-1"><LayoutGrid size={11} /> {clusters.length} clusters</span>
          <span className="ml-auto flex items-center gap-2">
            {active && (
              <button onClick={() => setConfirm({
                title: `Delete "${active.name}"?`,
                body: 'The connection, its stored credentials and check history are removed from SQLite. This cannot be undone.',
                confirmLabel: `Delete ${active.name}`,
                action: async () => { await api.DeleteSource(active.id); refresh(); },
              })} className="flex items-center gap-1 hover:text-red-300"><Trash2 size={11} /> delete {active.name}</button>
            )}
          </span>
        </footer>
      </main>

      <ConfirmModal open={!!confirm} title={confirm?.title || ''} body={confirm?.body || ''}
        confirmLabel={confirm?.confirmLabel} busy={confirmBusy}
        onCancel={() => { if (!confirmBusy) setConfirm(null); }}
        onConfirm={async () => {
          if (!confirm) return;
          setConfirmBusy(true);
          try { await confirm.action(); } finally { setConfirmBusy(false); setConfirm(null); }
        }} />

      <AddDatabaseWizard open={wizard} clusters={clusters} editing={editingSource}
        onClose={() => { setWizard(false); setEditingSource(null); }}
        onEnabled={(s) => { refresh(); setActiveId(s.id); setEditingSource(null); }}
        onSavedDraft={(s) => { refresh(); setActiveId(s.id); setEditingSource(null); }} />
      </div>

      <SettingsSheet open={settingsOpen} onClose={() => setSettingsOpen(false)}
        appearance={appearance} onPatch={patchAppearance}
        storePath={storePath} mcpUrl={mcpUrl} mcpConfig={mcpConfig}
        logs={logs} logPath={logPath} onReloadLogs={reloadLogs} onClearLogs={clearLogs}
        writeMode={allowWrites} onToggleWriteMode={toggleWriteMode} version={version}
        counts={{ sources: sources.length, clusters: clusters.length, verified: sources.filter((s) => s.readOnlyVerified).length }} />
    </div>
  );
}
