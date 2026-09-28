import { useState } from 'react';
import { Copy, Check, Braces, Plug2, ShieldCheck, Boxes, List, Table2, TableProperties, SquareTerminal, Gauge, CopyPlus, Stethoscope, Database, ChevronRight, ChevronDown, FlaskConical, Loader2, Route, Lock, FolderSearch, BarChart3, Network, Turtle, MessageSquare, Code2, Globe } from 'lucide-react';
import { api } from '../lib/api';
import { Button, Card, Badge } from './ui';
import { cn } from '../lib/cn';

function SetupSection({ id, icon: Icon, accent, title, file, badge, openId, onToggle, children }: {
  id: string; icon: any; accent: string; title: string; file: string; badge?: string;
  openId: string | null; onToggle: (id: string) => void; children: React.ReactNode;
}) {
  const open = openId === id;
  return (
    <div className={cn('overflow-hidden rounded-lg border transition-colors', open ? 'border-emerald-500/40 bg-zinc-950' : 'border-zinc-800 bg-zinc-950')}>
      <button type="button" onClick={() => onToggle(id)}
        className="flex w-full cursor-pointer select-none items-center gap-2.5 px-2.5 py-2 text-left [&_svg]:pointer-events-none">
        <Icon size={14} className={cn('shrink-0', accent)} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[12px] font-semibold text-zinc-100">{title}</span>
          <span className="block truncate font-mono text-[10px] text-zinc-500">{file}</span>
        </span>
        {badge && <span className="acc-bg acc-on hidden shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px] font-bold sm:inline">{badge}</span>}
        <ChevronDown size={13} className={cn('shrink-0 text-zinc-600 transition-transform', open && 'rotate-180')} />
      </button>
      {open && <div className="grid gap-2.5 border-t border-zinc-800 px-2.5 py-2.5">{children}</div>}
    </div>
  );
}

function Steps({ items }: { items: string[] }) {
  return (
    <div className="grid gap-1">
      {items.map((s, i) => (
        <div key={i} className="flex items-start gap-2 text-[11px] leading-relaxed text-zinc-400">
          <span className="grid h-[18px] w-[18px] shrink-0 place-items-center rounded bg-zinc-800 font-mono text-[10px] text-zinc-300">{i + 1}</span>
          <span>{s}</span>
        </div>
      ))}
    </div>
  );
}

function ConfigBlock({ id, json, copiedId, onCopy }: { id: string; json: string; copiedId: string | null; onCopy: (id: string, text: string) => void }) {
  return (
    <div>
      <div className="mb-1 flex items-center">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-zinc-600">Paste this</span>
        <button type="button" onClick={() => onCopy(id, json)}
          className="ml-auto flex cursor-pointer items-center gap-1 rounded-md border border-zinc-700 px-1.5 py-1 text-[10px] text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 [&_svg]:pointer-events-none">
          {copiedId === id ? <Check size={11} /> : <Copy size={11} />} {copiedId === id ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre className="max-h-64 overflow-auto rounded-lg codeblock bg-black/60 p-2.5 font-mono text-[11px] leading-relaxed text-emerald-200/90">{json}</pre>
    </div>
  );
}

interface ToolMeta {
  icon: any;
  name: string;
  short: string;
  group: string;
  detail: string;
  params: { name: string; req: boolean; desc: string }[];
  example: string;
  when: string;
}

const TOOLS: ToolMeta[] = [
  // ---- start here ----
  {
    icon: Boxes, name: 'fleet_overview', short: 'Clusters + source names. AI starts here.', group: 'Start here',
    detail: 'Returns the whole fleet map: clusters (id, name, color) plus every source as {name, cluster, engine, database, status, readOnly}. No hosts, ports, users, or passwords — names only. Cheap and always safe: the AI should call this first in every session to learn which source names exist.',
    params: [],
    example: `{"name": "fleet_overview", "arguments": {}}`,
    when: 'First call of any session; whenever the AI needs to know what databases it can touch.',
  },
  {
    icon: List, name: 'list_sources', short: 'Enabled sources grouped by cluster.', group: 'Start here',
    detail: 'Same fleet payload as fleet_overview, focused on sources. Use it to confirm a source is enabled (status ready + read-only proven) before querying it — disabled or unverified sources are rejected by every other tool.',
    params: [],
    example: `{"name": "list_sources", "arguments": {}}`,
    when: 'Checking a source is ready before running queries against it.',
  },
  {
    icon: Boxes, name: 'list_clusters', short: 'Cluster list (fleet groups).', group: 'Start here',
    detail: 'Returns just the clusters (Production vs Analytics vs Billing…). Helps the AI reason about which group a question belongs to before picking a source.',
    params: [],
    example: `{"name": "list_clusters", "arguments": {}}`,
    when: 'Ambiguous requests like “check production” — resolve the group first.',
  },
  // ---- find data ----
  {
    icon: FolderSearch, name: 'search_tables', short: 'Fuzzy-find tables by name fragment.', group: 'Find data',
    detail: 'ILIKE search over table names across all schemas, ordered by row estimate. Much cheaper than pulling a full schema when the AI is hunting one table (“where are orders kept?”).',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'pattern', req: true, desc: 'Name fragment, e.g. “order”' },
      { name: 'limit', req: false, desc: 'Max matches (default 20, cap 50)' },
    ],
    example: `{"name": "search_tables", "arguments": {"source": "Production", "pattern": "order"}}`,
    when: 'AI knows roughly what it wants but not the exact table name.',
  },
  {
    icon: Table2, name: 'schema(source)', short: 'Table names + estimates, one fast query.', group: 'Find data',
    detail: 'Full table inventory for a source (up to 500 tables) with row estimates from pg_class — one fast catalog query, no table scans. The AI follows up with columns() per interesting table.',
    params: [{ name: 'source', req: true, desc: 'Source name from fleet_overview' }],
    example: `{"name": "schema", "arguments": {"source": "Production"}}`,
    when: 'Exploring an unfamiliar database; building the table list for a question.',
  },
  {
    icon: BarChart3, name: 'table_stats', short: 'Size · rows · scans · bloat per table.', group: 'Find data',
    detail: 'Storage + activity profile: pretty + byte size, row estimate, sequential vs index scans, dead tuples (bloat hint), and index count. Omit table to get the biggest tables first — the fastest way to aim slow-query triage.',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'table', req: false, desc: '"table" or "schema.table" — omit for top tables by size' },
      { name: 'limit', req: false, desc: 'Rows when listing top tables (default 20, cap 50)' },
    ],
    example: `{"name": "table_stats", "arguments": {"source": "Production", "table": "public.orders"}}`,
    when: '“Why is this slow?” / “what are the biggest tables?” / pre-EXPLAIN triage.',
  },
  // ---- inspect ----
  {
    icon: TableProperties, name: 'columns(source, table)', short: 'Column inventory for one table.', group: 'Inspect',
    detail: 'Ordered column list with data types, nullability, and defaults from information_schema. Everything the AI needs to write a correct SELECT without guessing column names.',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'table', req: false, desc: '"table" or "schema.table" (default schema public)' },
    ],
    example: `{"name": "columns", "arguments": {"source": "Production", "table": "public.orders"}}`,
    when: 'Before writing any query against a table.',
  },
  {
    icon: List, name: 'indexes(source, table)', short: 'Index inventory for one table.', group: 'Inspect',
    detail: 'Every index on the table with its full definition (BTREE vs GIN, which columns, partial predicates). Pair with explain() to see whether a slow query can use one.',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'table', req: true, desc: '"table" or "schema.table"' },
    ],
    example: `{"name": "indexes", "arguments": {"source": "Production", "table": "public.orders"}}`,
    when: 'Slow query on a table — is there an index it should be using?',
  },
  {
    icon: Network, name: 'relationships', short: 'Foreign-key graph for JOIN reasoning.', group: 'Inspect',
    detail: 'Foreign-key edges as from table.column → referenced table.column. Pass a table to see only its edges, or omit it for the whole graph (capped at 100–200). This is how the AI discovers correct JOIN paths instead of hallucinating them.',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'table', req: false, desc: '"table" or "schema.table" — omit for full map' },
      { name: 'limit', req: false, desc: 'Max edges (default 100, cap 200)' },
    ],
    example: `{"name": "relationships", "arguments": {"source": "Production", "table": "public.orders"}}`,
    when: 'Any multi-table question — “orders with customer names” needs the FK path.',
  },
  // ---- query ----
  {
    icon: SquareTerminal, name: 'query(source, sql)', short: 'Guarded SELECT only · 500-row cap · 15s timeout.', group: 'Query',
    detail: 'Runs one read-only statement (SELECT / WITH / EXPLAIN / SHOW) inside a READ ONLY transaction with a 15s statement timeout. Writes, DDL, stacked statements, and data-modifying CTEs are rejected by the guard. A LIMIT is auto-appended when missing; max 500 rows.',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'sql', req: true, desc: 'Single SELECT/WITH/EXPLAIN statement' },
      { name: 'limit', req: false, desc: 'Row cap (default 200, max 500)' },
    ],
    example: `{"name": "query", "arguments": {"source": "Production", "sql": "SELECT status, count(*) FROM public.orders GROUP BY 1", "limit": 50}}`,
    when: 'Answering any data question — the workhorse tool.',
  },
  {
    icon: CopyPlus, name: 'sample(source, table)', short: 'First N rows, read-only.', group: 'Query',
    detail: 'Quick peek at the first N rows of a table (default 50). Same read-only transaction as query(), but no SQL to write — ideal for “what does this table look like?”',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'table', req: true, desc: '"table" or "schema.table"' },
      { name: 'limit', req: false, desc: 'Rows (default 50)' },
    ],
    example: `{"name": "sample", "arguments": {"source": "Production", "table": "public.orders", "limit": 10}}`,
    when: 'Getting a feel for values/formats before writing a real query.',
  },
  {
    icon: Gauge, name: 'explain(source, sql)', short: 'Plan JSON for slow-query reasoning.', group: 'Query',
    detail: 'EXPLAIN (FORMAT JSON) for any read-only statement, so the AI can spot seq scans, nested loops, and bad row estimates. Falls back to text plan when JSON is unavailable.',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'sql', req: true, desc: 'The slow SELECT to plan' },
    ],
    example: `{"name": "explain", "arguments": {"source": "Production", "sql": "SELECT * FROM public.orders WHERE total > 100"}}`,
    when: 'Any query that is slow or might be — before suggesting an index.',
  },
  {
    icon: Turtle, name: 'slow_queries', short: 'Heaviest statements, ranked.', group: 'Query',
    detail: 'Top statements by mean execution time from pg_stat_statements (needs the extension + pg_monitor grant — otherwise it degrades gracefully to currently-running queries from pg_stat_activity with setup instructions).',
    params: [
      { name: 'source', req: true, desc: 'Source name from fleet_overview' },
      { name: 'limit', req: false, desc: 'Statements (default 10, cap 20)' },
    ],
    example: `{"name": "slow_queries", "arguments": {"source": "Production", "limit": 5}}`,
    when: '“What is slow on this database?” — start of any performance session.',
  },
  // ---- health ----
  {
    icon: Stethoscope, name: 'doctor(source)', short: 'Seq scans · missing PKs · pressure · long queries.', group: 'Health',
    detail: 'Read-only health sweep: reachability, table inventory, biggest tables, seq-scan-only tables, tables without primary keys, connection pressure, and long-running queries. Each finding carries severity + remedy. Same probes as the Doctor tab.',
    params: [{ name: 'source', req: true, desc: 'Source name from fleet_overview' }],
    example: `{"name": "doctor", "arguments": {"source": "Production"}}`,
    when: 'Health checkups, onboarding a new source, “is something wrong?” questions.',
  },
];

const GROUPS = ['Start here', 'Find data', 'Inspect', 'Query', 'Health'];

const WORKFLOW = [
  { t: 'Orient', d: 'fleet_overview → which clusters and source names exist? Is the target ready + read-only proven?' },
  { t: 'Locate', d: 'search_tables or schema → find the table; table_stats → is it big, scanned, bloated?' },
  { t: 'Understand', d: 'columns + indexes + relationships → exact shape, usable indexes, JOIN paths.' },
  { t: 'Answer', d: 'query or sample → read the data. explain first if it could be slow.' },
  { t: 'Advise', d: 'doctor + slow_queries → health context and what to fix (a human applies writes).' },
];

function ToolRow({ tool, open, onToggle }: { tool: ToolMeta; open: boolean; onToggle: () => void }) {
  const Icon = tool.icon;
  return (
    <div className={cn('overflow-hidden rounded-lg border transition-colors', open ? 'border-emerald-500/40 bg-zinc-950' : 'border-zinc-800 bg-zinc-950')}>
      <button type="button" onClick={onToggle}
        className="flex w-full cursor-pointer select-none items-center gap-2.5 px-2.5 py-1.5 text-left [&_svg]:pointer-events-none">
        <Icon size={13} className={cn('shrink-0', open ? 'acc-text' : 'text-zinc-500')} />
        <code className="shrink-0 font-mono text-[11px] text-emerald-300">{tool.name}</code>
        <span className="min-w-0 flex-1 truncate text-[11px] text-zinc-500">{tool.short}</span>
        <ChevronDown size={13} className={cn('shrink-0 text-zinc-600 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="grid gap-2 border-t border-zinc-800 px-2.5 py-2">
          <div className="text-[11px] leading-relaxed text-zinc-400">{tool.detail}</div>
          {tool.params.length > 0 && (
            <div className="grid gap-1">
              {tool.params.map((p) => (
                <div key={p.name} className="flex items-baseline gap-2 font-mono text-[11px]">
                  <span className="shrink-0 text-zinc-200">{p.name}</span>
                  <span className={cn('shrink-0 rounded px-1 text-[10px]', p.req ? 'bg-emerald-500/10 text-emerald-300' : 'bg-zinc-800 text-zinc-500')}>
                    {p.req ? 'required' : 'optional'}
                  </span>
                  <span className="min-w-0 flex-1 font-sans text-zinc-500">{p.desc}</span>
                </div>
              ))}
            </div>
          )}
          <div>
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-zinc-600">Example call</div>
            <div className="flex items-start gap-1.5">
              <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-all rounded-md codeblock bg-black/60 p-2 font-mono text-[10px] leading-relaxed text-emerald-200/90">{tool.example}</pre>
              <button type="button" title="Copy example" onClick={() => navigator.clipboard.writeText(tool.example)}
                className="grid h-7 w-7 shrink-0 cursor-pointer place-items-center rounded-md text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200 [&_svg]:pointer-events-none">
                <Copy size={12} />
              </button>
            </div>
          </div>
          <div className="flex items-start gap-1.5 text-[11px] text-zinc-500">
            <span className="shrink-0 font-medium text-zinc-300">Use when</span>
            <span>{tool.when}</span>
          </div>
        </div>
      )}
    </div>
  );
}

export default function McpPanel({ fleet, mcpUrl }: { fleet: any; mcpUrl: string }) {
  const [cfgs, setCfgs] = useState<Record<string, string>>({});
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [selftest, setSelftest] = useState('');
  const [testing, setTesting] = useState(false);
  const [openTool, setOpenTool] = useState<string | null>('fleet_overview');
  const [openSetup, setOpenSetup] = useState<string | null>('opencode');
  const [query, setQuery] = useState('');

  // opencode first: fetch its config + the shared stdio config eagerly.
  if (!cfgs.opencode) api.OpencodeConfig().then((v) => setCfgs((c) => ({ ...c, opencode: String(v) }))).catch(() => {});
  if (!cfgs.stdio) api.ClaudeConfig().then((v) => setCfgs((c) => ({ ...c, stdio: String(v) }))).catch(() => {});

  function copyBlock(id: string, text: string) {
    navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId((c) => (c === id ? null : c)), 1200);
  }

  const base = (mcpUrl || 'http://127.0.0.1:9413').replace(/\/$/, '');
  const remoteJson = `{\n  "$schema": "https://opencode.ai/config.json",\n  "mcp": {\n    "readgate": {\n      "type": "remote",\n      "url": "${base}/mcp",\n      "enabled": true\n    }\n  }\n}`;

  async function runSelftest() {
    setTesting(true); setSelftest('');
    try {
      setSelftest(String(await api.TestMCP()));
    } catch (e: any) {
      setSelftest('self-test failed: ' + (e?.message || String(e)));
    } finally {
      setTesting(false);
    }
  }

  const q = query.trim().toLowerCase();
  const visible = q ? TOOLS.filter((t) => (t.name + ' ' + t.short + ' ' + t.detail).toLowerCase().includes(q)) : TOOLS;

  const toggleSetup = (id: string) => setOpenSetup((o) => (o === id ? null : id));

  return (
    <div className="mx-auto grid max-w-6xl items-start gap-3">
      {/* status hero — one clear title row, no competing headers */}
      <Card className="flex flex-wrap items-center gap-3 p-3.5">
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-300"><Plug2 size={18} /></div>
        <div className="min-w-0 flex-1">
          <div className="text-[15px] font-semibold text-white">MCP server — the AI talks to your fleet here</div>
          <div className="mt-0.5 text-xs text-zinc-500"><code className="font-mono text-zinc-300">ReadGate.exe mcp</code> over stdio · names only, never hosts, keys, or passwords</div>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge tone="green"><span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> stdio · local</Badge>
          <Badge tone="indigo">{TOOLS.length} tools · read-only</Badge>
          <Button variant="outline" className="!py-1.5 text-[11px]" onClick={runSelftest} disabled={testing}>
            {testing ? <Loader2 size={12} className="animate-spin" /> : <FlaskConical size={12} />} {testing ? 'Testing…' : 'Test MCP'}
          </Button>
        </div>
        {selftest && (
          <pre className="max-h-56 w-full overflow-auto whitespace-pre-wrap rounded-lg border border-zinc-800 bg-black/60 p-2.5 font-mono text-[10px] leading-relaxed text-zinc-300">{selftest}</pre>
        )}
      </Card>

      {/* setup + tools */}
      <div className="grid items-start gap-3 xl:grid-cols-5">
        <Card className="p-4 xl:col-span-2">
          <div className="mb-1 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-100"><Copy size={14} className="text-sky-300" /> Connection setup</div>
          <div className="mb-2.5 text-[11px] leading-relaxed text-zinc-500">Expand your client, paste the config, restart its session. Opencode first — it is the primary target.</div>
          <div className="grid gap-1.5">
            <SetupSection id="opencode" icon={SquareTerminal} accent="acc-text" title="opencode · local stdio" file="opencode.json — project root or ~/.config/opencode/" badge="START HERE" openId={openSetup} onToggle={toggleSetup}>
              <Steps items={[
                'Copy the JSON below and merge it into opencode.json under the top-level "mcp" key.',
                'Restart the opencode session so it launches the server.',
                'Verify: opencode run "using readgate fleet_overview, which sources are ready?"',
              ]} />
              <ConfigBlock id="opencode" json={cfgs.opencode || 'loading…'} copiedId={copiedId} onCopy={copyBlock} />
              <div className="text-[11px] leading-relaxed text-zinc-500">opencode launches <code className="font-mono text-zinc-300">ReadGate.exe mcp</code> itself — no port, no app window needed. If you move the exe, reopen this tab to refresh the path.</div>
            </SetupSection>

            <SetupSection id="claude" icon={MessageSquare} accent="text-orange-300" title="Claude Desktop" file="%APPDATA%\\Claude\\claude_desktop_config.json" openId={openSetup} onToggle={toggleSetup}>
              <Steps items={[
                'Copy the JSON below and merge it under "mcpServers".',
                'Fully quit Claude (tray icon → Quit) and reopen it.',
                'Look for the readgate tools in a new chat — ask it to call fleet_overview.',
              ]} />
              <ConfigBlock id="claude" json={cfgs.stdio || 'loading…'} copiedId={copiedId} onCopy={copyBlock} />
              <div className="text-[11px] leading-relaxed text-zinc-500">Same local-stdio trick as opencode: Claude starts the exe itself, so the app can stay closed.</div>
            </SetupSection>

            <SetupSection id="cursor" icon={Code2} accent="text-sky-300" title="Cursor · Windsurf · Cline" file=".cursor/mcp.json · ~/.cursor/mcp.json · mcp_config.json" openId={openSetup} onToggle={toggleSetup}>
              <Steps items={[
                'Cursor: project .cursor/mcp.json (shared) or ~/.cursor/mcp.json (personal).',
                'Windsurf: ~/.codeium/windsurf/mcp_config.json. Cline: VS Code MCP settings file.',
                'Paste the same stdio block, save, and restart the editor / MCP server.',
              ]} />
              <ConfigBlock id="cursor" json={cfgs.stdio || 'loading…'} copiedId={copiedId} onCopy={copyBlock} />
              <div className="text-[11px] leading-relaxed text-zinc-500">All three speak the {"{mcpServers}"} shape, so one config covers them.</div>
            </SetupSection>

            <SetupSection id="http" icon={Globe} accent="text-zinc-400" title="Any client · HTTP remote" file={`POST ${base}/mcp — app window must stay open`} openId={openSetup} onToggle={toggleSetup}>
              <Steps items={[
                'Point any MCP-capable client at the endpoint above (protocol 2024-11-05).',
                'Handshake: initialize → notifications/initialized → tools/list → tools/call.',
                'Keep this app open — closing it drops the HTTP server (stdio mode has no such need).',
              ]} />
              <ConfigBlock id="http" json={remoteJson} copiedId={copiedId} onCopy={copyBlock} />
              <div className="text-[11px] leading-relaxed text-zinc-500">opencode remote variant shown — useful when the app runs on another host on loopback-only setups.</div>
            </SetupSection>
          </div>
          <div className="mt-3 rounded-lg border border-zinc-800 bg-zinc-950 p-2.5 text-[11px] leading-relaxed text-zinc-400">
            <span className="font-medium text-zinc-200">Try with opencode:</span> <code className="font-mono text-emerald-300">opencode run "using readgate fleet_overview, which sources are ready?"</code>
          </div>
        </Card>

        <Card className="p-4 xl:col-span-3">
          <div className="mb-1 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-100"><Braces size={14} className="text-indigo-300" /> Tools the AI sees</div>
          <div className="mb-2 text-[11px] text-zinc-500">Click any tool for parameters, an example call, and when to use it.</div>
          <div className="relative mb-2">
            <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Filter tools… e.g. index, slow, join"
              className="w-full rounded-lg border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-xs text-zinc-100 outline-none placeholder:text-zinc-600 focus:border-emerald-500/60" />
          </div>
          {GROUPS.map((g) => {
            const inGroup = visible.filter((t) => t.group === g);
            if (inGroup.length === 0) return null;
            return (
              <div key={g} className="mb-2 last:mb-0">
                <div className="mb-1 mt-2 text-[10px] font-semibold uppercase tracking-wider text-zinc-600 first:mt-0">{g} · {inGroup.length}</div>
                <div className="grid gap-1">
                  {inGroup.map((t) => (
                    <ToolRow key={t.name} tool={t} open={openTool === t.name} onToggle={() => setOpenTool((o) => (o === t.name ? null : t.name))} />
                  ))}
                </div>
              </div>
            );
          })}
          {visible.length === 0 && <div className="p-3 text-center text-[11px] text-zinc-600">No tools match “{query}”.</div>}
        </Card>
      </div>

      {/* workflow + guardrails */}
      <div className="grid items-start gap-3 md:grid-cols-2">
        <Card className="p-4">
          <div className="mb-2 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-100"><Route size={14} className="text-amber-300" /> Recommended workflow</div>
          <div className="grid gap-1.5">
            {WORKFLOW.map((s, i) => (
              <div key={s.t} className="flex items-start gap-2.5 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5">
                <span className="acc-bg acc-on grid h-5 w-5 shrink-0 place-items-center rounded-md font-mono text-[10px] font-bold">{i + 1}</span>
                <div className="min-w-0">
                  <div className="text-[12px] font-semibold text-zinc-200">{s.t}</div>
                  <div className="text-[11px] leading-relaxed text-zinc-500">{s.d}</div>
                </div>
              </div>
            ))}
          </div>
        </Card>

        <Card className="p-4">
          <div className="mb-2 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-100"><Lock size={12} className="text-emerald-300" /> Guardrails — what the AI can and cannot do</div>
          <div className="grid gap-1 font-mono text-[11px]">
            {[
              ['sees', 'names only (clusters, sources, tables, columns) — never hosts, ports, users, passwords'],
              ['runs', 'SELECT / WITH / EXPLAIN / SHOW, single statement, auto-LIMIT, 500-row cap, 15s timeout'],
              ['blocked', 'INSERT UPDATE DELETE DDL GRANT stacked statements data-modifying CTEs'],
              ['writes', 'app UI only + confirmed — no MCP tool can write, ever'],
              ['identity', 'dedicated ai_readonly role in a READ ONLY transaction'],
            ].map(([k, v]) => (
              <div key={k} className="flex items-start gap-2 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5">
                <span className={cn('shrink-0 font-semibold', k === 'blocked' ? 'text-red-300' : 'text-emerald-300')}>{k}</span>
                <span className="text-zinc-400">{v}</span>
              </div>
            ))}
          </div>
          <div className="mt-2 flex items-center gap-1.5 text-[11px] text-zinc-500"><ShieldCheck size={12} className="text-emerald-400" /> Writes need a human — the AI diagnoses, you apply.</div>
        </Card>
      </div>

      <Card className="p-4">
        <div className="mb-2 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-100"><Boxes size={14} className="text-amber-300" /> What the AI understands (fleet_overview)</div>
        <pre className="max-h-80 min-h-32 overflow-auto rounded-lg codeblock bg-black/60 p-2.5 font-mono text-[11px] text-zinc-300">{JSON.stringify(fleet, null, 2)}</pre>
        <div className="mt-2 flex items-center gap-1.5 text-[11px] text-zinc-500"><ShieldCheck size={12} className="text-emerald-400" /> No SSH hosts · no users · no passwords · no ports in this payload.</div>
      </Card>
    </div>
  );
}

export function FleetCards({ fleet, onSelect }: { fleet: any; onSelect: (name: string) => void }) {
  const clusters = fleet?.clusters || [];
  const sources = fleet?.sources || [];
  return (
    <div className="grid gap-3 md:grid-cols-3">
      {clusters.map((c: any) => {
        const members = sources.filter((s: any) => s.cluster === c.name);
        const ready = members.filter((m: any) => m.status === 'ready' && m.readOnly).length;
        return (
          <Card key={c.id} className="overflow-hidden">
            <div className="h-1" style={{ background: c.color }} />
            <div className="p-3">
              <div className="flex items-center gap-1.5">
                <Boxes size={13} style={{ color: c.color }} />
                <div className="text-[13px] font-semibold text-zinc-100">{c.name}</div>
                <span className="ml-auto"><Badge tone={ready === members.length && members.length > 0 ? 'green' : 'zinc'}>{ready}/{members.length} ready</Badge></span>
              </div>
              {c.description && <div className="mb-2 mt-0.5 text-[11px] text-zinc-500">{c.description}</div>}
              <div className="grid gap-1">
                {members.length === 0 && <div className="rounded-md border border-dashed border-zinc-800 px-2 py-1.5 text-[11px] text-zinc-600">No sources yet — Add Database → assign here.</div>}
                {members.map((m: any) => (
                  <button key={m.name} type="button" onClick={() => onSelect(m.name)} className="group flex cursor-pointer items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-950 px-2.5 py-1.5 text-left hover:border-zinc-600 [&_svg]:pointer-events-none">
                    <Database size={12} className="shrink-0 text-zinc-500" />
                    <span className="truncate text-xs text-zinc-200">{m.name}</span>
                    <span className="ml-auto flex items-center gap-1.5">
                      <Badge tone={m.status === 'ready' && m.readOnly ? 'green' : 'zinc'}>{m.status === 'ready' && m.readOnly ? 'ready · ro' : m.status}</Badge>
                      <ChevronRight size={12} className="text-zinc-700 group-hover:text-zinc-400" />
                    </span>
                  </button>
                ))}
              </div>
            </div>
          </Card>
        );
      })}
    </div>
  );
}
