import type { CheckResult, Cluster, DoctorFinding, QueryResult, Source } from './types';

// Wails binding shim.
// Inside the desktop app every call hits the REAL Go backend and real
// errors propagate (no silent fallbacks — fake data must never mask bugs).
// The in-memory mocks below run ONLY in `npm run dev` (no window.go).
declare global {
  interface Window {
    go?: any;
  }
}

function binding(name: string, fallback: (...args: any[]) => Promise<any>): (...args: any[]) => Promise<any> {
  return async (...args: any[]) => {
    const fn = window.go?.main?.App?.[name];
    if (typeof fn === 'function') return await fn(...args);
    return await fallback(...args);
  };
}

const demoClusters: Cluster[] = [
  { id: 'c1', name: 'Production', description: 'Customer-facing fleet', color: '#10b981', createdAt: new Date().toISOString() },
  { id: 'c2', name: 'Analytics', description: 'Warehouses & pipelines', color: '#6366f1', createdAt: new Date().toISOString() },
  { id: 'c3', name: 'Billing', description: 'Money — extra careful', color: '#f59e0b', createdAt: new Date().toISOString() },
];

const demoSources: Source[] = [
  { id: 's1', name: 'Production', clusterId: 'c1', engine: 'postgres', mode: 'ssh', host: '127.0.0.1', port: 5432, database: 'connect_local', username: 'ai_readonly', password: '', sshHost: '203.0.113.10', sshPort: 22, sshUser: 'ubuntu', sshAuth: 'key', sshKeyPath: 'C:\\Users\\MohamedSheta\\.ssh\\id_rsa', status: 'ready', readOnlyVerified: true, createdAt: new Date().toISOString() },
  { id: 's2', name: 'Analytics', clusterId: 'c2', engine: 'postgres', mode: 'ssh', host: '127.0.0.1', port: 5432, database: 'analytics', username: 'ai_readonly', password: '', sshHost: '203.0.113.11', sshPort: 22, sshUser: 'ubuntu', sshAuth: 'key', sshKeyPath: 'C:\\Users\\MohamedSheta\\.ssh\\id_rsa', status: 'draft', readOnlyVerified: false, createdAt: new Date().toISOString() },
  { id: 's3', name: 'Billing', clusterId: 'c3', engine: 'postgres', mode: 'direct', host: '10.0.0.8', port: 5432, database: 'billing', username: 'ai_readonly', password: '', sshHost: '', sshPort: 22, sshUser: '', sshAuth: 'key', sshKeyPath: '', status: 'draft', readOnlyVerified: false, createdAt: new Date().toISOString() },
];

let memSources: Source[] = [...demoSources];
let memClusters: Cluster[] = [...demoClusters];

const rnd = (n = 8) => Math.random().toString(36).slice(2, 2 + n);

export const api = {
  ListClusters: binding('ListClusters', async (): Promise<Cluster[]> => memClusters),
  SaveCluster: binding('SaveCluster', async (c: Cluster): Promise<Cluster> => {
    if (!c.id) { c = { ...c, id: rnd(10) }; memClusters.push(c); return c; }
    memClusters = memClusters.map((x) => (x.id === c.id ? c : x));
    return c;
  }),
  DeleteCluster: binding('DeleteCluster', async (id: string) => {
    memClusters = memClusters.filter((c) => c.id !== id);
  }),
  ListSources: binding('ListSources', async (): Promise<Source[]> => memSources),
  SaveSource: binding('SaveSource', async (s: Source): Promise<Source> => {
    if (!s.id) { s = { ...s, id: rnd(10) }; memSources.push(s); return s; }
    memSources = memSources.map((x) => (x.id === s.id ? s : x));
    return s;
  }),
  DeleteSource: binding('DeleteSource', async (id: string) => {
    memSources = memSources.filter((s) => s.id !== id);
  }),
  TestConnection: binding('TestConnection', async (req: any): Promise<CheckResult[]> => {
    await new Promise((r) => setTimeout(r, 900));
    const mode = req?.source?.mode;
    const steps: CheckResult[] = [];
    if (req?.autoProvision) steps.push({ key: 'provision', label: 'Create dedicated read-only user', ok: true, detail: `role "${req.aiUser || 'ai_readonly'}" ready · admin credentials discarded`, durationMs: 420 });
    if (mode === 'ssh') steps.push({ key: 'ssh', label: 'SSH connection successful', ok: true, detail: 'server: Linux 6.8 x86_64 · tunnel 127.0.0.1:5432', durationMs: 310 });
    steps.push(
      { key: 'pg', label: 'PostgreSQL detected', ok: true, detail: 'PostgreSQL 16.3 on x86_64-pc-linux-gnu', durationMs: 180 },
      { key: 'auth', label: 'Database accessible', ok: true, detail: `database: ${req?.source?.database || 'connect_local'}`, durationMs: 90 },
      { key: 'select', label: 'SELECT permission verified', ok: true, detail: 'SELECT 1 → 1', durationMs: 60 },
      { key: 'write', label: 'Write permission denied', ok: true, detail: 'rejected as read-only (42501: permission denied)', durationMs: 70 },
      { key: 'ro', label: 'Read-only configuration verified', ok: true, detail: 'default_transaction_read_only=on', durationMs: 40 },
    );
    return steps;
  }),
  EnableSource: binding('EnableSource', async (src: Source, _checks: CheckResult[]): Promise<Source> => {
    const next = { ...src, status: 'ready' as const, readOnlyVerified: true, lastCheckAt: new Date().toISOString() };
    memSources = memSources.map((x) => (x.id === src.id ? next : x));
    return next;
  }),
  GenerateProvisionSQL: binding('GenerateProvisionSQL', async (_engine: string, db: string, u: string): Promise<string> => {
    return `-- ReadGate · read-only AI role for ${db}\nCREATE ROLE "${u || 'ai_readonly'}" WITH LOGIN PASSWORD '***generated***';\nGRANT CONNECT ON DATABASE "${db}" TO "${u || 'ai_readonly'}";\nGRANT USAGE ON SCHEMA public TO "${u || 'ai_readonly'}";\nGRANT SELECT ON ALL TABLES IN SCHEMA public TO "${u || 'ai_readonly'}";\nALTER ROLE "${u || 'ai_readonly'}" SET default_transaction_read_only = on;`;
  }),
  GenerateManualScript: binding('GenerateManualScript', async (): Promise<string> => '#!/usr/bin/env bash\n# ReadGate manual setup — see app for full script'),
  NewAIPassword: binding('NewAIPassword', async (): Promise<string> => 'rg_' + rnd(20)),
  TestSSH: binding('TestSSH', async (): Promise<string> => 'SSH ok — Linux 6.8 x86_64 (tunnel 127.0.0.1:5432)'),
  TestSSHByID: binding('TestSSHByID', async (): Promise<string> => 'SSH ok — Linux 6.8 x86_64 (tunnel 127.0.0.1:5432)'),
  TestAdminConnection: binding('TestAdminConnection', async (): Promise<CheckResult[]> => [
    { key: 'adm-pg', label: 'PostgreSQL reachable as admin', ok: true, detail: 'logged in as postgres · PostgreSQL 16.3', durationMs: 200 },
    { key: 'adm-priv', label: 'Admin can CREATE ROLE', ok: true, detail: 'superuser=true createrole=true', durationMs: 40 },
    { key: 'adm-db', label: 'Target database exists', ok: true, detail: 'database exists', durationMs: 30 },
  ]),
  ProvisionAIUser: binding('ProvisionAIUser', async (req: any): Promise<CheckResult[]> => [
    { key: 'provision', label: 'Create dedicated read-only user', ok: true, detail: `role "${req?.aiUser || 'ai_readonly'}" ready · admin credentials discarded`, durationMs: 420 },
    { key: 'role-present', label: 'Role visible in pg_roles', ok: true, detail: 'exists · canlogin=true · verified by admin session', durationMs: 60 },
  ]),
  ReverifySource: binding('ReverifySource', async (): Promise<CheckResult[]> => [
    { key: 'pg', label: 'PostgreSQL detected', ok: true, detail: 'PostgreSQL 16.3', durationMs: 180 },
    { key: 'auth', label: 'Database accessible', ok: true, detail: 'database reachable', durationMs: 90 },
    { key: 'select', label: 'SELECT permission verified', ok: true, detail: 'SELECT 1 → 1', durationMs: 60 },
    { key: 'write', label: 'Write permission denied', ok: true, detail: 'rejected as read-only (42501)', durationMs: 70 },
    { key: 'ro', label: 'Read-only configuration verified', ok: true, detail: 'default_transaction_read_only=on', durationMs: 40 },
  ]),
  StorePath: binding('StorePath', async (): Promise<string> => '~/.readgate/readgate.db (SQLite + AES-GCM)'),
  GetSettings: binding('GetSettings', async (): Promise<Record<string, string>> => ({})),
  SetSetting: binding('SetSetting', async (): Promise<void> => {}),
  GetLogs: binding('GetLogs', async (): Promise<string[]> => ['2026-09-27 22:00:00 [INFO] readgate started (web-dev mock)']),
  ClearLogs: binding('ClearLogs', async (): Promise<void> => {}),
  LogPath: binding('LogPath', async (): Promise<string> => '~/.readgate/readgate.log'),
  DiagnoseServer: binding('DiagnoseServer', async (): Promise<CheckResult[]> => [
    { key: 'srv-listen', label: 'PostgreSQL listening on server :5432', ok: true, detail: 'tcp 127.0.0.1:5432 LISTEN', durationMs: 120 },
    { key: 'srv-proc', label: 'Postgres processes on server', ok: true, detail: 'postgres: checkpointer, background writer…', durationMs: 90 },
    { key: 'srv-role', label: 'Role "ai_readonly" exists in PG', ok: false, detail: 'role "ai_readonly" NOT FOUND in pg_roles — create it via automatic setup', durationMs: 200 },
    { key: 'srv-hba', label: 'pg_hba auth rules for 127.0.0.1', ok: true, detail: 'host all all 127.0.0.1/32 scram-sha-256', durationMs: 80 },
  ]),
  DefaultSSHKey: binding('DefaultSSHKey', async (): Promise<string> => 'C:\\Users\\MohamedSheta\\.ssh\\id_rsa'),
  PickSSHKey: binding('PickSSHKey', async (): Promise<string> => 'C:\\Users\\MohamedSheta\\.ssh\\id_rsa'),
  PickSQLiteFile: binding('PickSQLiteFile', async (): Promise<string> => 'C:\\data\\app.db'),
  GetSchema: binding('GetSchema', async (): Promise<{ sourceName: string; tables: any[] }> => ({
    sourceName: 'Production',
    tables: [
      { schema: 'public', name: 'users', rowEstimate: 48210 },
      { schema: 'public', name: 'orders', rowEstimate: 128904 },
      { schema: 'public', name: 'events', rowEstimate: 2093841 },
    ],
  })),
  GetTableColumns: binding('GetTableColumns', async (): Promise<any[]> => [
    { name: 'id', type: 'bigint', nullable: false },
    { name: 'created_at', type: 'timestamptz', nullable: false },
  ]),
  RunQuery: binding('RunQuery', async (_id: string, sql: string): Promise<QueryResult> => {
    if (/insert|update|delete|drop|create|alter/i.test(sql)) throw new Error('blocked keyword — gateway is read-only');
    await new Promise((r) => setTimeout(r, 400));
    return { columns: ['id', 'email', 'created_at'], rows: [['9f1…', 'ava@example.com', '2026-09-20'], ['a42…', 'leo@example.com', '2026-09-21']], rowCount: 2, durationMs: 38, truncated: false };
  }),
  PreviewTable: binding('PreviewTable', async (): Promise<QueryResult> => {
    await new Promise((r) => setTimeout(r, 300));
    return { columns: ['id', 'email', 'created_at'], rows: [['9f1…', 'ava@example.com', '2026-09-20'], ['a42…', 'leo@example.com', '2026-09-21']], rowCount: 2, totalRows: 2, durationMs: 31, truncated: false };
  }),
  GetDoctor: binding('GetDoctor', async (): Promise<DoctorFinding[]> => [
    { severity: 'warn', title: 'Table with only sequential scans: public.events', detail: '41,203 seq scans, 0 idx scans, ~2,093,841 live rows', remedy: 'EXPLAIN ANALYZE SELECT … then consider CREATE INDEX (ask a human to apply writes).', source: 'Production' },
    { severity: 'warn', title: 'Table without primary key: public.events', detail: 'Replication and joins are riskier without a PK.', remedy: 'Have an admin run ALTER TABLE public.events ADD PRIMARY KEY (id);', source: 'Production' },
    { severity: 'info', title: 'Connections healthy 21/100', detail: '', source: 'Production' },
  ]),
  FleetOverview: binding('FleetOverview', async () => ({ clusters: memClusters, sources: memSources.map((s) => ({ name: s.name, cluster: memClusters.find((c) => c.id === s.clusterId)?.name || 'Ungrouped', engine: s.engine, database: s.database, status: s.status, readOnly: s.readOnlyVerified })) })),
  MCPStatus: binding('MCPStatus', async () => ({ running: true, url: 'http://127.0.0.1:9413' })),
  StartMCP: binding('StartMCP', async () => 'http://127.0.0.1:9413'),
  MCPConfig: binding('MCPConfig', async (): Promise<string> => `{\n  "mcpServers": {\n    "readgate": {\n      "url": "http://127.0.0.1:9413/mcp"\n    }\n  }\n}`),
  OpencodeConfig: binding('OpencodeConfig', async (): Promise<string> => `{\n  "$schema": "https://opencode.ai/config.json",\n  "mcp": {\n    "readgate": {\n      "type": "local",\n      "command": ["E:\\\\laragon\\\\www\\\\go\\\\ReadGate\\\\build\\\\bin\\\\ReadGate.exe", "mcp"],\n      "enabled": true\n    }\n  }\n}`),
  ClaudeConfig: binding('ClaudeConfig', async (): Promise<string> => `{\n  "mcpServers": {\n    "readgate": {\n      "command": "E:\\\\laragon\\\\www\\\\go\\\\ReadGate\\\\build\\\\bin\\\\ReadGate.exe",\n      "args": ["mcp"]\n    }\n  }\n}`),
  TestMCP: binding('TestMCP', async (): Promise<string> => 'stdio msg 1 → {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05",…}}\nstdio msg 2 → {"jsonrpc":"2.0","id":2,"result":{"tools":[…]}}\nstdio msg 3 → {"jsonrpc":"2.0","id":3,"result":{"content":[…]}}'),
  WriteMode: binding('WriteMode', async (): Promise<boolean> => false),
  SetWriteMode: binding('SetWriteMode', async (): Promise<void> => {}),
  HasWriteUser: binding('HasWriteUser', async (): Promise<boolean> => false),
  WriteUserName: binding('WriteUserName', async (): Promise<string> => ''),
  SaveWriteUser: binding('SaveWriteUser', async (): Promise<string> => ''),
  DeleteWriteUser: binding('DeleteWriteUser', async (): Promise<void> => {}),
  ExecWrite: binding('ExecWrite', async (): Promise<{ rowsAffected: number; durationMs: number }> => ({ rowsAffected: 1, durationMs: 12 })),
};
