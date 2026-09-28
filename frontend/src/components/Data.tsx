import { useEffect, useMemo, useState } from 'react';
import { Play, Table2, Loader2, TriangleAlert, Info, OctagonAlert, Stethoscope, Plus, X, Download, History, ArrowUpDown, Search, Copy, Check, ListFilter, Pencil, Trash2, RotateCcw, KeyRound } from 'lucide-react';
import { api } from '../lib/api';
import type { QueryResult, DoctorFinding, Source } from '../lib/types';
import { Badge, Button, Card, Empty, Sheet, ConfirmModal, PasswordInput, Field } from './ui';
import { cn } from '../lib/cn';

function toCSV(res: QueryResult): string {
  const rows = res.rows || [];
  const esc = (v: any) => {
    const s = String(v ?? '');
    return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  return [(res.columns || []).map(esc).join(','), ...rows.map((r) => r.map(esc).join(','))].join('\n');
}

export interface EditCtx {
  sourceId: string;
  schema: string;
  table: string;
  engine: string;
  colTypes: Record<string, string>;
  allowWrites: boolean;
  onApplied: () => void;
}

export function ResultsGrid({ res, edit }: { res: QueryResult | null; edit?: EditCtx }) {
  const [sort, setSort] = useState<{ col: number; dir: 1 | -1 } | null>(null);
  const [filter, setFilter] = useState('');
  const [colFilters, setColFilters] = useState<Record<number, string>>({});
  const [showCols, setShowCols] = useState(false);
  const [detail, setDetail] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);
  // staged Beekeeper-style edits (cleared on new data)
  const [editMode, setEditMode] = useState(false);
  const [updates, setUpdates] = useState<Record<string, Record<number, string | null>>>({});
  const [deletes, setDeletes] = useState<number[]>([]);
  const [inserts, setInserts] = useState<(string | null | undefined)[][]>([]);
  const [cell, setCell] = useState<{ ri: number; ci: number } | null>(null);
  const [draft, setDraft] = useState('');
  const [draftNull, setDraftNull] = useState(false);
  const [confirm, setConfirm] = useState<string[] | null>(null);
  const [confirmBusy, setConfirmBusy] = useState(false);
  const [applyMsg, setApplyMsg] = useState('');
  const [wuName, setWuName] = useState<string | null>(null);
  const [wuOpen, setWuOpen] = useState(false);
  const [wuUser, setWuUser] = useState('');
  const [wuPass, setWuPass] = useState('');
  const [wuErr, setWuErr] = useState('');
  const [wuBusy, setWuBusy] = useState(false);

  const serverEngine = !!edit && edit.engine !== 'sqlite' && edit.engine !== 'turso';

  useEffect(() => { setSort(null); setFilter(''); setColFilters({}); setShowCols(false); setDetail(null); }, [res]);
  useEffect(() => {
    setEditMode(false); setUpdates({}); setDeletes([]); setInserts([]);
    setCell(null); setConfirm(null); setApplyMsg('');
  }, [res]);
  useEffect(() => {
    if (!edit) return;
    if (!edit.allowWrites) { setWuName(null); return; }
    if (!serverEngine) { setWuName(edit.engine === 'turso' ? '(token)' : '(file)'); return; }
    setWuName(null);
    api.WriteUserName(edit.sourceId).then((n: any) => setWuName(String(n || ''))).catch(() => setWuName(''));
  }, [edit?.sourceId, edit?.allowWrites]);

  const view = useMemo(() => {
    if (!res) return [];
    let rows = res.rows || [];
    const q = filter.trim().toLowerCase();
    if (q) rows = rows.filter((r) => r.some((v) => String(v ?? '').toLowerCase().includes(q)));
    const active = Object.entries(colFilters).filter(([, v]) => v.trim() !== '');
    if (active.length > 0) {
      rows = rows.filter((r) => active.every(([j, v]) =>
        String(r[Number(j)] ?? '').toLowerCase().includes(v.trim().toLowerCase())));
    }
    if (sort) {
      const { col, dir } = sort;
      rows = [...rows].sort((a, b) => {
        const x = a[col], y = b[col];
        if (x == null && y == null) return 0;
        if (x == null) return 1;
        if (y == null) return -1;
        if (typeof x === 'number' && typeof y === 'number') return (x - y) * dir;
        return String(x).localeCompare(String(y)) * dir;
      });
    }
    return rows;
  }, [res, sort, filter]);

  if (!res) return <Empty icon={<Table2 size={22} />} title="Run a SELECT to see rows" hint="Guarded: SELECT / WITH / EXPLAIN only · LIMIT auto-applied · 15s timeout." />;

  // ---------- staged-edit helpers (Beekeeper-style, app UI only) ----------
  const stagedCount = Object.keys(updates).length + deletes.length + inserts.length;
  const riOf = (row: any[]) => (res?.rows || []).indexOf(row);

  function qid(name: string) {
    const e = edit?.engine || '';
    if (e === 'mysql' || e === 'mariadb' || e === 'tidb') return '`' + name.replace(/`/g, '``') + '`';
    if (e === 'sqlserver') return '[' + name.replace(/\]/g, ']]') + ']';
    return '"' + name.replace(/"/g, '""') + '"';
  }

  function lit(v: string | null | undefined, type: string): string {
    if (v === null || v === undefined) return 'NULL';
    const t = (type || '').toLowerCase();
    if (/int|serial|bigint|smallint|tinyint|decimal|numeric|number|float|double|real|money|bit|bool/.test(t)) {
      const s = v.trim();
      if (/^(true|false)$/i.test(s)) return /true/i.test(s) ? '1' : '0';
      if (s === '' || isNaN(Number(s))) throw new Error(`"${v}" is not a number`);
      return s;
    }
    return "'" + v.replace(/'/g, "''") + "'";
  }

  function whereFor(row: any[]): string {
    return (res?.columns || []).map((c, j) => {
      const v = row[j];
      return v === null || v === undefined ? `${qid(c)} IS NULL` : `${qid(c)} = ${lit(String(v), edit?.colTypes[c] || '')}`;
    }).join(' AND ');
  }

  function buildStatements(): string[] {
    if (!res || !edit) return [];
    const out: string[] = [];
    const tn = `${qid(edit.schema)}.${qid(edit.table)}`;
    view.forEach((row) => {
      const ri = riOf(row);
      if (deletes.includes(ri)) {
        out.push(`DELETE FROM ${tn} WHERE ${whereFor(row)}`);
        return;
      }
      const ch = updates[ri];
      if (ch) {
        const set = Object.keys(ch).map(Number)
          .map((ci) => `${qid(res.columns[ci])} = ${lit(ch[ci], edit.colTypes[res.columns[ci]] || '')}`)
          .join(', ');
        if (set) out.push(`UPDATE ${tn} SET ${set} WHERE ${whereFor(row)}`);
      }
    });
    inserts.forEach((vals) => {
      const cols = (res.columns || []).map(qid).join(', ');
      const vs = (res.columns || []).map((c, j) => (vals[j] === undefined ? 'DEFAULT' : lit(vals[j], edit.colTypes[c] || ''))).join(', ');
      out.push(`INSERT INTO ${tn} (${cols}) VALUES (${vs})`);
    });
    return out;
  }

  function commitCell(ri: number, ci: number, val: string | null) {
    if (!res) return;
    const orig = res.rows[ri]?.[ci];
    const same = val === null ? (orig === null || orig === undefined) : String(orig ?? '') === val;
    setUpdates((u) => {
      const next = { ...u };
      if (same) {
        if (next[ri]) {
          const row = { ...next[ri] };
          delete row[ci];
          if (Object.keys(row).length === 0) delete next[ri];
          else next[ri] = row;
        }
      } else {
        next[ri] = { ...(next[ri] || {}), [ci]: val };
      }
      return next;
    });
    setCell(null);
  }

  function startEdit(ri: number, ci: number) {
    if (!res || deletes.includes(ri)) return;
    const staged = updates[ri]?.[ci];
    const cur = staged !== undefined ? staged : res.rows[ri]?.[ci];
    setDraft(cur === null || cur === undefined ? '' : String(cur));
    setDraftNull(cur === null || cur === undefined);
    setCell({ ri, ci });
  }

  async function applyAll(stmts: string[]) {
    if (!edit) return;
    setConfirmBusy(true); setApplyMsg('');
    try {
      let affected = 0;
      for (const s of stmts) {
        const r = await api.ExecWrite(edit.sourceId, s, true) as any;
        affected += Number(r?.rowsAffected || 0);
      }
      setConfirm(null); setUpdates({}); setDeletes([]); setInserts([]); setCell(null);
      setApplyMsg(`applied ${stmts.length} statement(s) · ${affected} row(s) affected`);
      edit.onApplied();
    } catch (e: any) {
      setApplyMsg('apply failed: ' + (e?.message || String(e)));
    } finally {
      setConfirmBusy(false);
    }
  }

  async function saveWu() {
    if (!edit) return;
    if (!wuUser.trim() || !wuPass) { setWuErr('username + password are required'); return; }
    setWuBusy(true); setWuErr('');
    try {
      const errText = String(await api.SaveWriteUser(edit.sourceId, wuUser.trim(), wuPass));
      if (errText) { setWuErr(errText); return; }
      setWuName(wuUser.trim()); setWuUser(''); setWuPass(''); setWuOpen(false);
    } catch (e: any) {
      setWuErr(e?.message || String(e));
    } finally {
      setWuBusy(false);
    }
  }

  async function removeWu() {
    if (!edit) return;
    try { await api.DeleteWriteUser(edit.sourceId); } catch {}
    setWuName(''); setWuOpen(false);
  }

  function download() {
    const blob = new Blob([toCSV({ ...res!, rows: view })], { type: 'text/csv' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'readgate-export.csv';
    a.click();
    URL.revokeObjectURL(a.href);
  }

  function copyRow(i: number) {
    if (!res) return;
    const obj: Record<string, any> = {};
    (res.columns || []).forEach((c, j) => { obj[c] = view[i]?.[j] ?? null; });
    navigator.clipboard.writeText(JSON.stringify(obj, null, 2));
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  }

  function cellText(v: any): string {
    const s = String(v ?? '');
    return s.length > 300 ? s.slice(0, 300) + '…' : s;
  }

  return (
    <div className="overflow-hidden rounded-xl border border-zinc-800">
      <div className="flex items-center gap-2 border-b border-zinc-800 bg-zinc-900 px-3 py-2 text-xs text-zinc-500">
        <span className="shrink-0">{res.rowCount} rows{view.length !== res.rows.length ? ` · ${view.length} shown` : ''} · {res.durationMs}ms {res.truncated ? '· truncated' : ''}</span>
        <span className="hidden text-[11px] text-zinc-600 xl:inline">click a row for full record</span>
        <button onClick={() => setShowCols((v) => !v)} title={stagedCount > 0 ? 'Revert or apply changes first' : 'Per-column filters'} disabled={stagedCount > 0}
          className={cn('flex items-center gap-1 rounded-md border px-2 py-1 text-xs disabled:opacity-40',
            showCols ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-200' : 'border-zinc-700 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200')}>
          <ListFilter size={12} />
        </button>
        <div className="relative ml-auto w-44">
          <Search size={12} className="absolute left-2 top-2 text-zinc-600" />
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter rows…" disabled={stagedCount > 0}
            className="w-full rounded-md border border-zinc-700 bg-zinc-950 py-1 pl-7 pr-2 text-xs outline-none placeholder:text-zinc-600 focus:border-emerald-500/60 disabled:opacity-40" />
        </div>
        <button onClick={download} title="Export CSV" className="flex items-center gap-1 rounded-md border border-zinc-700 px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200">
          <Download size={12} /> CSV
        </button>
        <Badge tone="green">read-only txn</Badge>
      </div>
      {edit && (
        <div className="flex flex-wrap items-center gap-1.5 border-b border-zinc-800 bg-zinc-900/40 px-3 py-1.5 text-[11px]">
          {!edit.allowWrites ? (
            <span className="text-zinc-500">Edits are disabled — turn on <span className="font-medium text-zinc-300">Settings → General → Allow modifications</span>.</span>
          ) : !editMode ? (
            <>
              <Button variant="outline" className="!py-1 text-[11px]" onClick={() => { setEditMode(true); setApplyMsg(''); }}>
                <Pencil size={12} /> Edit rows
              </Button>
              <span className="text-zinc-600">
                write login: {wuName === null ? '…' : wuName ? <span className="font-mono text-zinc-400">{wuName}</span> : <span className="text-amber-300/90">not set</span>}
              </span>
              {(wuName !== null || !serverEngine) && (
                <button type="button" onClick={() => { setWuUser(''); setWuPass(''); setWuErr(''); setWuOpen(true); }}
                  className="cursor-pointer text-zinc-400 underline decoration-dotted underline-offset-2 hover:text-zinc-200">
                  {!serverEngine ? 'details' : wuName ? 'change' : 'set it'}
                </button>
              )}
            </>
          ) : (
            <>
              <span className="font-medium text-amber-200/90">editing</span>
              <span className="font-mono text-zinc-500">{stagedCount} change{stagedCount === 1 ? '' : 's'} staged</span>
              <Button variant="emerald" className="!py-1 text-[11px]" disabled={stagedCount === 0}
                onClick={() => { try { setConfirm(buildStatements()); } catch (e: any) { setApplyMsg(e?.message || String(e)); } }}>
                <Check size={12} /> Apply{stagedCount > 0 ? ` (${stagedCount})` : ''}
              </Button>
              <Button variant="ghost" className="!py-1 text-[11px]" disabled={stagedCount === 0}
                onClick={() => { setUpdates({}); setDeletes([]); setInserts([]); setCell(null); }}>
                <RotateCcw size={12} /> Revert
              </Button>
              <Button variant="ghost" className="!py-1 text-[11px]"
                onClick={() => { setEditMode(false); setUpdates({}); setDeletes([]); setInserts([]); setCell(null); }}>
                <X size={12} /> Done
              </Button>
              <button type="button" onClick={() => setInserts((a) => [...a, Array((res?.columns || []).length).fill(undefined)])}
                className="flex cursor-pointer items-center gap-1 rounded-md border border-dashed border-zinc-700 px-2 py-1 text-[11px] text-zinc-400 hover:border-zinc-500 hover:text-zinc-200">
                <Plus size={11} /> Add row
              </button>
            </>
          )}
          {applyMsg && <span className="w-full font-mono text-[10px] text-zinc-400">{applyMsg}</span>}
        </div>
      )}
      <div className="max-h-[62vh] min-h-72 overflow-auto bg-zinc-950">
        <table className="w-full border-collapse text-xs">
          <thead className="sticky top-0 bg-zinc-900">
            <tr>
              <th className="w-10 border-b border-zinc-800 px-2 py-2 text-right font-medium text-zinc-600">#</th>
              {editMode && <th className="w-8 border-b border-zinc-800" />}
              {(res.columns || []).map((c, j) => (
                <th key={c + j} onClick={() => { if (stagedCount === 0) setSort((s) => (s?.col === j ? { col: j, dir: s.dir === 1 ? -1 : 1 } : { col: j, dir: 1 })); }}
                  title={stagedCount > 0 ? 'Revert or apply changes to re-sort' : 'Sort'}
                  className={cn('whitespace-nowrap border-b border-zinc-800 px-3 py-2 text-left font-medium text-zinc-400 hover:text-zinc-100', stagedCount > 0 ? 'cursor-not-allowed' : 'cursor-pointer select-none')}>
                  <span className="inline-flex items-center gap-1">{c}
                    <ArrowUpDown size={10} className={cn(sort?.col === j ? 'text-emerald-300' : 'text-zinc-700')} />
                    {sort?.col === j && <span className="text-emerald-300">{sort.dir === 1 ? '▲' : '▼'}</span>}
                  </span>
                </th>
              ))}
            </tr>
            {showCols && (
              <tr>
                <th className="border-b border-zinc-800 bg-zinc-950 px-2 py-1" />
                {(res.columns || []).map((c, j) => (
                  <th key={'f' + j} className="border-b border-zinc-800 bg-zinc-950 px-1.5 py-1">
                    <input value={colFilters[j] || ''} onChange={(e) => setColFilters((f) => ({ ...f, [j]: e.target.value }))}
                      onClick={(e) => e.stopPropagation()} placeholder={`⊂ ${c}`} disabled={stagedCount > 0}
                      className="w-full min-w-20 rounded border border-zinc-800 bg-zinc-900 px-1.5 py-0.5 font-mono text-[11px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-500/60 disabled:opacity-40" />
                  </th>
                ))}
              </tr>
            )}
          </thead>
          <tbody>
            {view.map((r, i) => {
              const ri = riOf(r);
              const gone = deletes.includes(ri);
              return (
              <tr key={i} onClick={() => { if (!editMode) setDetail(i); }}
                className={cn('hover:bg-zinc-900/60', !editMode && 'cursor-pointer', gone && 'bg-red-500/5')}>
                <td className="border-b border-zinc-900 px-2 py-1.5 text-right font-mono text-[11px] text-zinc-600">{i + 1}</td>
                {editMode && (
                  <td className="border-b border-zinc-900 px-1 py-1.5 text-center">
                    <button type="button" title={gone ? 'Undo delete' : 'Delete row'}
                      onClick={(e) => { e.stopPropagation(); setDeletes((d) => gone ? d.filter((x) => x !== ri) : [...d, ri]); }}
                      className={cn('grid h-6 w-6 cursor-pointer place-items-center rounded-md [&_svg]:pointer-events-none',
                        gone ? 'text-amber-300 hover:bg-zinc-800' : 'text-zinc-600 hover:bg-zinc-800 hover:text-red-300')}>
                      {gone ? <RotateCcw size={12} /> : <Trash2 size={12} />}
                    </button>
                  </td>
                )}
                {r.map((v, j) => {
                  if (!editMode) {
                    return (
                      <td key={j} title={v == null ? 'NULL' : String(v)}
                        className={cn('max-w-64 truncate border-b border-zinc-900 px-3 py-1.5 font-mono text-zinc-300',
                          v == null && 'italic text-zinc-600')}>
                        {v == null ? 'NULL' : cellText(v)}
                      </td>
                    );
                  }
                  const staged = updates[ri]?.[j];
                  const shown = staged !== undefined ? staged : v;
                  const editing = cell?.ri === ri && cell?.ci === j;
                  return (
                    <td key={j} title={shown == null ? 'NULL (click to edit)' : String(shown) + ' (click to edit)'}
                      onClick={(e) => { e.stopPropagation(); if (!gone) startEdit(ri, j); }}
                      className={cn('max-w-64 truncate border-b border-zinc-900 px-3 py-1.5 font-mono',
                        gone ? 'text-zinc-700 line-through' : staged !== undefined ? 'bg-amber-500/5 text-amber-200' : 'text-zinc-300',
                        !gone && 'cursor-text')}>
                      {editing ? (
                        <span className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                          {draftNull ? (
                            <span className="flex-1 italic text-zinc-600">NULL</span>
                          ) : (
                            <input autoFocus value={draft} onChange={(e) => setDraft(e.target.value)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') commitCell(ri, j, draftNull ? null : draft);
                                if (e.key === 'Escape') setCell(null);
                              }}
                              onBlur={() => commitCell(ri, j, draftNull ? null : draft)}
                              className="w-full min-w-16 flex-1 rounded border border-emerald-500/60 bg-zinc-950 px-1 py-0.5 text-zinc-100 outline-none" />
                          )}
                          <button type="button" title="Toggle NULL"
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={() => setDraftNull((b) => !b)}
                            className={cn('grid h-6 w-6 shrink-0 cursor-pointer place-items-center rounded font-mono text-[11px]',
                              draftNull ? 'bg-emerald-500/15 text-emerald-300' : 'text-zinc-600 hover:bg-zinc-800 hover:text-zinc-300')}>
                            ∅
                          </button>
                        </span>
                      ) : shown == null ? (
                        <span className="italic text-zinc-600">NULL</span>
                      ) : staged !== undefined ? (
                        <span title={`was: ${v == null ? 'NULL' : String(v)}`}>{String(shown)}</span>
                      ) : cellText(v)}
                    </td>
                  );
                })}
              </tr>
              );
            })}
            {editMode && inserts.map((vals, ii) => (
              <tr key={'n' + ii} className="bg-emerald-500/5">
                <td className="border-b border-zinc-900 px-2 py-1.5 text-right font-mono text-[11px] text-emerald-400/70">+</td>
                <td className="border-b border-zinc-900 px-1 py-1.5 text-center">
                  <button type="button" title="Discard new row"
                    onClick={() => setInserts((a) => a.filter((_, k) => k !== ii))}
                    className="grid h-6 w-6 cursor-pointer place-items-center rounded-md text-zinc-600 hover:bg-zinc-800 hover:text-red-300 [&_svg]:pointer-events-none">
                    <X size={12} />
                  </button>
                </td>
                {(res.columns || []).map((c, j) => (
                  <td key={j} className="border-b border-zinc-900 px-1.5 py-1">
                    <div className="flex items-center gap-1">
                      <input value={vals[j] ?? ''} placeholder={vals[j] === null ? 'NULL' : vals[j] === undefined ? 'DEFAULT' : ''}
                        disabled={vals[j] === null}
                        onChange={(e) => setInserts((a) => a.map((r, k) => (k === ii ? r.map((v, jj) => (jj === j ? e.target.value : v)) : r)))}
                        className="w-full min-w-16 rounded border border-zinc-800 bg-zinc-950 px-1 py-0.5 font-mono text-[11px] text-zinc-100 outline-none placeholder:text-zinc-700 focus:border-emerald-500/60" />
                      <button type="button" title="Toggle NULL"
                        onClick={() => setInserts((a) => a.map((r, k) => (k === ii ? r.map((v, jj) => (jj === j ? (v === null ? '' : null) : v)) : r)))}
                        className={cn('grid h-6 w-6 shrink-0 cursor-pointer place-items-center rounded font-mono text-[11px]',
                          vals[j] === null ? 'bg-emerald-500/15 text-emerald-300' : 'text-zinc-600 hover:bg-zinc-800 hover:text-zinc-300')}>
                        ∅
                      </button>
                    </div>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {view.length === 0 && <div className="p-6 text-center text-xs text-zinc-600">No rows match.</div>}
      </div>

      <Sheet open={detail !== null} onClose={() => setDetail(null)}
        title={detail !== null ? `Row ${detail + 1}` : 'Row'}
        subtitle={res ? `${(res.columns || []).length} columns · full values` : undefined}>
        {detail !== null && res && (
          <div className="grid gap-2 px-4 py-4">
            <Button variant="outline" className="!py-1 text-[11px]" onClick={() => copyRow(detail)}>
              {copied ? <Check size={12} /> : <Copy size={12} />} {copied ? 'Copied row JSON' : 'Copy row as JSON'}
            </Button>
            {(res.columns || []).map((c, j) => {
              const v = view[detail]?.[j];
              return (
                <div key={c + j} className="overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950">
                  <div className="border-b border-zinc-800 bg-zinc-900 px-2.5 py-1 font-mono text-[11px] text-emerald-300/90">{c}</div>
                  <pre className="max-h-96 min-h-16 overflow-auto whitespace-pre-wrap break-all p-2.5 font-mono text-[11px] leading-relaxed text-zinc-200">{v == null ? 'NULL' : String(v)}</pre>
                </div>
              );
            })}
          </div>
        )}
      </Sheet>

      <ConfirmModal open={confirm !== null} title={`Apply ${confirm?.length || 0} statement(s)?`}
        body={(confirm || []).join('\n') + ((confirm || []).some((s) => /^\s*(UPDATE|DELETE)\b/i.test(s) && !/\bWHERE\b/i.test(s.replace(/'[^']*'/g, "''"))) ? '\n\n⚠ one or more statements have no WHERE — they touch every row.' : '')}
        confirmLabel={`Apply to ${edit?.table || 'table'}`} busy={confirmBusy}
        onCancel={() => { if (!confirmBusy) setConfirm(null); }}
        onConfirm={() => confirm && applyAll(confirm)} />

      {wuOpen && edit && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center p-4">
          <div className="sheet-dim absolute inset-0 bg-black/70 backdrop-blur-sm" onClick={() => { if (!wuBusy) setWuOpen(false); }} />
          <div className="pop-in sheet-bg relative w-full max-w-sm rounded-xl border border-zinc-700 p-4 shadow-2xl">
            <div className="flex items-center gap-2 text-sm font-semibold text-zinc-100">
              <span className="flex h-7 w-7 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-300"><KeyRound size={14} /></span>
              Write login — {edit.table}
            </div>
            <div className="mt-2 text-xs leading-relaxed text-zinc-400">
              A privileged database login (e.g. postgres, root, sa) used <span className="text-zinc-200">only</span> for confirmed edits in this app. AES-GCM encrypted, never logged, never sent to the AI.
            </div>
            {wuName ? (
              <div className="mt-3 flex items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2">
                <span className="font-mono text-xs text-zinc-200">{wuName}</span>
                <button type="button" onClick={removeWu}
                  className="ml-auto flex cursor-pointer items-center gap-1 rounded-md px-1.5 py-1 text-[11px] text-zinc-500 hover:bg-zinc-800 hover:text-red-300">
                  <Trash2 size={12} /> Remove
                </button>
              </div>
            ) : (
              <div className="mt-3 grid gap-2">
                <Field label="Username">
                  <input value={wuUser} onChange={(e) => setWuUser(e.target.value)} placeholder="postgres" autoComplete="off"
                    className="w-full rounded-lg border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm text-zinc-100 outline-none placeholder:text-zinc-600 focus:border-emerald-500/60" />
                </Field>
                <Field label="Password">
                  <PasswordInput value={wuPass} onChange={setWuPass} mono />
                </Field>
                {wuErr && <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 font-mono text-xs text-red-300">{wuErr}</div>}
                <div className="flex justify-end gap-2">
                  <Button variant="ghost" onClick={() => setWuOpen(false)} disabled={wuBusy}>Cancel</Button>
                  <Button variant="emerald" onClick={saveWu} disabled={wuBusy}>{wuBusy ? 'Saving…' : 'Save login'}</Button>
                </div>
              </div>
            )}
            {wuName ? (
              <div className="mt-3 flex justify-end">
                <Button variant="ghost" onClick={() => setWuOpen(false)}>Close</Button>
              </div>
            ) : null}
          </div>
        </div>
      )}
    </div>
  );
}

interface QTab {
  id: string;
  name: string;
  sql: string;
  res: QueryResult | null;
  err: string;
  running: boolean;
}

interface HistEntry {
  at: string;
  sql: string;
  rows: number;
  ms: number;
  ok: boolean;
}

let tabSeq = 1;

export function QueryPane({ source, seed }: { source?: Source; seed?: { n: number; sql: string } | null }) {
  const [tabs, setTabs] = useState<QTab[]>([{ id: 't1', name: 'Query 1', sql: '-- pick a table in Browse → Open in Query, or start here\nSELECT version();', res: null, err: '', running: false }]);
  const [activeId, setActiveId] = useState('t1');
  const [history, setHistory] = useState<HistEntry[]>([]);
  const [showHist, setShowHist] = useState(false);
  const active = tabs.find((t) => t.id === activeId) || tabs[0];

  // tables asking to "open in query" arrive here as a seed
  useEffect(() => {
    if (!seed) return;
    const id = 't' + Date.now();
    setTabs((ts) => [...ts, { id, name: 'Query ' + ++tabSeq, sql: seed.sql, res: null, err: '', running: false }]);
    setActiveId(id);
  }, [seed]);

  function patch(id: string, p: Partial<QTab>) {
    setTabs((ts) => ts.map((t) => (t.id === id ? { ...t, ...p } : t)));
  }

  async function run(id: string) {
    const tab = tabs.find((t) => t.id === id);
    if (!tab || !source) return;
    patch(id, { running: true, err: '' });
    const start = Date.now();
    try {
      const res = (await api.RunQuery(source.id, tab.sql)) as unknown as QueryResult;
      patch(id, { res, running: false });
      setHistory((h) => [{ at: new Date().toLocaleTimeString(), sql: tab.sql, rows: res.rowCount, ms: Date.now() - start, ok: true }, ...h].slice(0, 30));
    } catch (e: any) {
      const msg = e?.message || String(e);
      patch(id, { err: msg, res: null, running: false });
      setHistory((h) => [{ at: new Date().toLocaleTimeString(), sql: tab.sql, rows: 0, ms: Date.now() - start, ok: false }, ...h].slice(0, 30));
    }
  }

  function addTab() {
    const id = 't' + Date.now();
    setTabs((ts) => [...ts, { id, name: 'Query ' + ++tabSeq, sql: '-- new query (Ctrl+Enter to run)\nSELECT 1', res: null, err: '', running: false }]);
    setActiveId(id);
  }

  function closeTab(id: string) {
    setTabs((ts) => {
      if (ts.length === 1) return ts;
      const next = ts.filter((t) => t.id !== id);
      if (activeId === id) setActiveId(next[next.length - 1].id);
      return next;
    });
  }

  return (
    <div className="grid gap-3">
      {/* Beekeeper-style tab bar */}
      <div className="flex items-center gap-1 overflow-x-auto">
        {tabs.map((t) => (
          <div key={t.id} onClick={() => setActiveId(t.id)}
            className={cn('group flex shrink-0 cursor-pointer items-center gap-1.5 rounded-t-lg border border-b-0 px-3 py-1.5 text-[13px]',
              t.id === active.id ? 'border-zinc-700 bg-zinc-900 text-zinc-100' : 'border-transparent text-zinc-500 hover:bg-zinc-900/60 hover:text-zinc-300')}>
            <span className={cn('h-1.5 w-1.5 rounded-full', t.running ? 'animate-pulse bg-amber-400' : t.err ? 'bg-red-400' : t.res ? 'bg-emerald-400' : 'bg-zinc-700')} />
            {t.name}
            {tabs.length > 1 && (
              <button onClick={(e) => { e.stopPropagation(); closeTab(t.id); }} title="Close tab"
                className="grid h-6 w-6 place-items-center rounded-md text-zinc-500 opacity-0 transition active:scale-95 hover:bg-zinc-700 hover:text-zinc-200 group-hover:opacity-100"><X size={12} /></button>
            )}
          </div>
        ))}
        <button onClick={addTab} title="New query tab" className="rounded-md p-1.5 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200"><Plus size={15} /></button>
      </div>

      <Card className="overflow-hidden !rounded-tl-none">
        <div className="flex items-center justify-between border-b border-zinc-800 px-3 py-2">
          <span className="font-mono text-xs text-zinc-500">-- {source?.name || 'no source'} · read-only · auto LIMIT 200 · Ctrl+Enter</span>
          <div className="flex gap-1.5">
            <Button variant="ghost" className="!py-1.5 text-xs" onClick={() => setShowHist((v) => !v)}><History size={14} /> History ({history.length})</Button>
            <Button variant="emerald" onClick={() => active && run(active.id)} disabled={active?.running || !source} className="!py-1.5">
              {active?.running ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />} Run
            </Button>
          </div>
        </div>
        <textarea
          value={active?.sql || ''} onChange={(e) => active && patch(active.id, { sql: e.target.value })} spellCheck={false}
          onKeyDown={(e) => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && active) { e.preventDefault(); run(active.id); } }}
          className="h-36 w-full resize-y bg-zinc-950 p-3 font-mono text-[13px] leading-relaxed text-zinc-100 outline-none" />
      </Card>

      {showHist && (
        <Card className="max-h-44 overflow-y-auto p-1.5">
          {history.length === 0 && <div className="p-3 text-xs text-zinc-600">No queries yet this session.</div>}
          {history.map((h, i) => (
            <button key={i} onClick={() => active && patch(active.id, { sql: h.sql })}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left font-mono text-[11px] hover:bg-zinc-800">
              <span className={h.ok ? 'text-emerald-400' : 'text-red-400'}>{h.ok ? '✓' : '✕'}</span>
              <span className="text-zinc-500">{h.at}</span>
              <span className="flex-1 truncate text-zinc-300">{h.sql.replace(/\s+/g, ' ')}</span>
              <span className="text-zinc-600">{h.ok ? `${h.rows} rows · ${h.ms}ms` : 'error'}</span>
            </button>
          ))}
        </Card>
      )}

      {active?.err && <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 font-mono text-xs text-red-300">{active.err}</div>}
      <ResultsGrid res={active?.res || null} />
    </div>
  );
}

export function DoctorPane({ source }: { source?: Source }) {
  const [findings, setFindings] = useState<DoctorFinding[] | null>(null);
  const [loading, setLoading] = useState(false);

  async function load() {
    if (!source) return;
    setLoading(true);
    try {
      setFindings((await api.GetDoctor(source.id)) as unknown as DoctorFinding[]);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => { setFindings(null); }, [source?.id]);

  const icon = (s: string) =>
    s === 'critical' ? <OctagonAlert size={15} className="text-red-300" /> :
    s === 'warn' ? <TriangleAlert size={15} className="text-amber-300" /> :
    <Info size={15} className="text-sky-300" />;

  return (
    <div className="grid gap-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-sm text-zinc-300"><Stethoscope size={15} className="text-emerald-300" /> AI health check — same <code className="font-mono text-xs text-emerald-300">doctor(source)</code> tool the agent uses</div>
        <Button variant="outline" onClick={load} disabled={loading || !source}>{loading ? <Loader2 size={14} className="animate-spin" /> : null} Diagnose</Button>
      </div>
      {!findings && <Empty icon={<Stethoscope size={22} />} title="Run Diagnose to spot problems" hint="Seq-scan tables · missing PKs · connection pressure · long queries. All probes are SELECT-only." />}
      <div className="grid gap-2">
        {(findings || []).map((f, i) => (
          <Card key={i} className="p-4">
            <div className="flex items-center gap-2">{icon(f.severity)}<span className="text-sm font-medium text-zinc-100">{f.title}</span>
              <span className="ml-auto"><Badge tone={f.severity === 'info' ? 'indigo' : f.severity === 'warn' ? 'amber' : 'red'}>{f.severity}</Badge></span>
            </div>
            {f.detail && <div className="mt-1 text-xs text-zinc-400">{f.detail}</div>}
            {f.remedy && <div className="mt-2 rounded-lg codeblock bg-black/50 p-2 font-mono text-[11px] text-zinc-300">{f.remedy}</div>}
          </Card>
        ))}
      </div>
    </div>
  );
}
