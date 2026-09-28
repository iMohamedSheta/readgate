import { useEffect, useState } from 'react';
import { Check, Copy, Database, Dices, FolderOpen, KeyRound, Loader2, Plug2, Save, Server, ShieldCheck, TerminalSquare, UserPlus, X } from 'lucide-react';
import { api } from '../lib/api';
import type { CheckResult, Source } from '../lib/types';
import { Badge, Button, Card, Field, Input, Label, PasswordInput, Sheet } from './ui';
import { cn } from '../lib/cn';

interface Props {
  open: boolean;
  clusters: { id: string; name: string }[];
  initial?: Partial<Source>;
  editing?: Source | null;
  onClose: () => void;
  onEnabled: (s: Source) => void;
  onSavedDraft: (s: Source) => void;
}

// Engine catalog mirrors the backend registry (internal/engines):
// ready engines onboard today, the rest are honestly labeled.
const engines: { id: string; label: string; group: string; port: number; admin?: string; ai?: string; file?: boolean; token?: boolean; ready: boolean; note?: string }[] = [
  { id: 'postgres', label: 'PostgreSQL', group: 'Ready now', port: 5432, admin: 'postgres', ai: 'ai_readonly', ready: true },
  { id: 'mysql', label: 'MySQL', group: 'Ready now', port: 3306, admin: 'root', ai: 'ai_readonly', ready: true },
  { id: 'mariadb', label: 'MariaDB', group: 'Ready now', port: 3306, admin: 'root', ai: 'ai_readonly', ready: true },
  { id: 'tidb', label: 'TiDB', group: 'Ready now', port: 4000, admin: 'root', ai: 'ai_readonly', ready: true },
  { id: 'sqlite', label: 'SQLite', group: 'Ready now', port: 0, file: true, ready: true, note: 'Local file · no server' },
  { id: 'turso', label: 'Turso', group: 'Ready now', port: 443, token: true, ai: 'token', ready: true, note: 'libsql URL + token' },
  { id: 'sqlserver', label: 'SQL Server', group: 'Ready now', port: 1433, admin: 'sa', ai: 'ai_readonly', ready: true },
  { id: 'cockroachdb', label: 'CockroachDB', group: 'Beta · PG wire', port: 26257, admin: 'root', ai: 'ai_readonly', ready: true, note: 'Best effort' },
  { id: 'redshift', label: 'Redshift', group: 'Beta · PG wire', port: 5439, admin: 'admin', ai: 'ai_readonly', ready: true, note: 'Best effort' },
  { id: 'clickhouse', label: 'ClickHouse', group: 'Coming soon', port: 8123, ready: false, note: 'Needs driver' },
  { id: 'oracle', label: 'Oracle', group: 'Planned', port: 1521, ready: false, note: 'Needs client libs' },
];
const engineGroups = ['Ready now', 'Beta · PG wire', 'Coming soon', 'Planned'];

export default function AddDatabaseWizard({ open, clusters, initial, editing, onClose, onEnabled, onSavedDraft }: Props) {
  const [form, setForm] = useState<any>({
    name: 'Production', engine: 'postgres', mode: 'ssh',
    host: '127.0.0.1', port: 5432, database: 'connect_local',
    sshHost: '', sshUser: 'ubuntu', sshPort: 22, sshAuth: 'key', sshKeyPath: '', sshKeyPassphrase: '', sshPassword: '',
    clusterId: clusters[0]?.id || '',
    setup: 'auto', adminUser: 'postgres', adminPassword: '', aiUser: 'ai_readonly', aiPassword: '',
  });
  const [checks, setChecks] = useState<CheckResult[] | null>(null);
  const [testing, setTesting] = useState(false);
  const [savingDraft, setSavingDraft] = useState(false);
  const [sshMsg, setSshMsg] = useState('');
  const [sshBusy, setSshBusy] = useState(false);
  const [diagBusy, setDiagBusy] = useState(false);
  const [stepBusy, setStepBusy] = useState('');
  const [err, setErr] = useState('');
  const [sqlPreview, setSqlPreview] = useState('');
  const [step, setStep] = useState<'form' | 'verify' | 'done'>('form');

  useEffect(() => {
    if (open) {
      setChecks(null); setStep('form'); setTesting(false);
      setErr(''); setSshMsg('');
      if (editing) {
        setForm({
          name: editing.name, engine: editing.engine, mode: editing.mode,
          host: editing.host, port: editing.port, database: editing.database,
          sshHost: editing.sshHost, sshUser: editing.sshUser, sshPort: editing.sshPort || 22,
          sshAuth: editing.sshAuth || 'key',
          sshKeyPath: editing.sshKeyPath, sshKeyPassphrase: editing.sshKeyPassphrase || '',
          sshPassword: editing.sshPassword || '',
          clusterId: editing.clusterId,
          setup: 'auto', adminUser: 'postgres', adminPassword: '',
          aiUser: editing.username, aiPassword: editing.password,
        });
      } else {
        if (initial) setForm((f: any) => ({ ...f, ...initial }));
        api.NewAIPassword().then((p: any) => setForm((f: any) => ({ ...f, aiPassword: f.aiPassword || String(p) }))).catch(() => {});
        api.DefaultSSHKey().then((p: any) => setForm((f: any) => ({ ...f, sshKeyPath: f.sshKeyPath || String(p) }))).catch(() => {});
      }
    }
  }, [open]);

  useEffect(() => {
    api.GenerateProvisionSQL(form.engine, form.database || 'connect_local', form.aiUser || 'ai_readonly', form.aiPassword || '').then((s: any) => setSqlPreview(String(s))).catch(() => {});
  }, [form.engine, form.database, form.aiUser, form.aiPassword]);

  if (!open) return null;
  const set = (k: string, v: any) => setForm((f: any) => ({ ...f, [k]: v }));
  const isSqlite = form.engine === 'sqlite';
  const isTurso = form.engine === 'turso';
  const isFileish = isSqlite || isTurso;

  function pickEngine(id: string) {
    const meta: any = engines.find((e) => e.id === id);
    if (!meta || !meta.ready) return;
    setForm((f: any) => ({
      ...f,
      engine: id,
      mode: meta.file ? 'file' : (meta.token ? 'direct' : (f.mode === 'file' ? 'ssh' : f.mode)),
      port: meta.port || f.port,
      adminUser: meta.admin || f.adminUser,
      aiUser: meta.ai || f.aiUser,
    }));
  }

  function buildSrc(): Source {
    return {
      id: editing?.id || '', name: form.name || 'Untitled', clusterId: form.clusterId, engine: form.engine, mode: form.mode,
      host: form.mode === 'ssh' ? '127.0.0.1' : (isFileish ? '' : form.host), port: isFileish ? 0 : (Number(form.port) || 5432),
      database: form.database, username: isSqlite ? 'ro' : (isTurso ? 'token' : form.aiUser), password: isSqlite ? '' : form.aiPassword,
      sshHost: form.sshHost, sshPort: Number(form.sshPort) || 22, sshUser: form.sshUser, sshAuth: form.sshAuth || 'key',
      sshKeyPath: form.sshKeyPath, sshKeyPassphrase: form.sshKeyPassphrase, sshPassword: form.sshPassword,
      status: editing?.status || 'draft', readOnlyVerified: editing?.readOnlyVerified || false,
      createdAt: editing?.createdAt || '',
    };
  }

  async function regenAIPassword() {
    try {
      set('aiPassword', String(await api.NewAIPassword()));
    } catch (e: any) {
      setErr(e?.message || String(e));
    }
  }

  async function saveDraft() {
    setSavingDraft(true); setErr('');
    try {
      const saved = (await api.SaveSource({ ...buildSrc(), status: 'draft' })) as unknown as Source;
      onSavedDraft(saved);
      onClose();
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setSavingDraft(false);
    }
  }

  async function browseKey() {
    try {
      const p = String(await api.PickSSHKey());
      if (p) set('sshKeyPath', p);
    } catch (e: any) {
      setSshMsg('picker failed: ' + (e?.message || String(e)));
    }
  }
  async function browseFile() {
    try {
      const p = String(await api.PickSQLiteFile());
      if (p) set('database', p);
    } catch (e: any) {
      setErr('picker failed: ' + (e?.message || String(e)));
    }
  }
  async function testSSH() {
    setSshBusy(true); setSshMsg('testing ssh…'); setErr('');
    try {
      setSshMsg(String(await api.TestSSH(buildSrc())));
    } catch (e: any) {
      setSshMsg('failed: ' + (e?.message || String(e)));
    } finally {
      setSshBusy(false);
    }
  }

  async function diagnose() {
    setDiagBusy(true); setErr('');
    try {
      const rows = (await api.DiagnoseServer(buildSrc())) as CheckResult[];
      setChecks((prev) => [...(prev || []), ...rows]);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setDiagBusy(false);
    }
  }

  // Standalone steps: test the admin leg, or create/reset the AI role —
  // rows append to the same proof card. Full proof still via Test Connection.
  async function runStep(kind: 'admin' | 'provision') {
    setStepBusy(kind); setErr('');
    try {
      const src = buildSrc();
      const rows = (kind === 'admin'
        ? await api.TestAdminConnection({ source: src, adminUser: form.adminUser, adminPassword: form.adminPassword })
        : await api.ProvisionAIUser({ source: src, adminUser: form.adminUser, adminPassword: form.adminPassword, aiUser: form.aiUser, aiPassword: form.aiPassword })) as CheckResult[];
      setStep('verify');
      setChecks((prev) => [...(prev || []), ...rows]);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setStepBusy('');
    }
  }

  async function runTest() {
    setTesting(true); setStep('verify'); setChecks(null); setErr('');
    try {
      const src = buildSrc();
      const res = (await api.TestConnection({
        source: src, adminUser: form.adminUser, adminPassword: form.adminPassword,
        aiUser: form.aiUser, aiPassword: form.aiPassword, autoProvision: form.setup === 'auto',
      })) as CheckResult[];
      setChecks(res);
      const failed = res.some((c: CheckResult) => (c.key === 'write' || c.key === 'select' || c.key === 'ro') && !c.ok);
      if (!failed) {
        const saved = (await api.SaveSource({ ...src, status: 'draft' })) as unknown as Source;
        const enabled = (await api.EnableSource(saved, res)) as unknown as Source;
        setStep('done');
        onEnabled(enabled);
      }
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setTesting(false);
    }
  }

  return (
    <Sheet open={open} onClose={onClose}
      title={editing ? `Edit ${editing.name}` : 'Add Database'}
      subtitle="SSH tunnel · dedicated read-only user · proof before enable">
      <div className="grid gap-4 px-4 py-4">

        {step !== 'done' ? (
          <div className="grid gap-4">
            <div className="grid grid-cols-2 gap-4">
              <Field label="Database name"><Input value={form.name} onChange={(e) => set('name', e.target.value)} placeholder="Production" /></Field>
              <div>
                <Label>Cluster (fleet group)</Label>
                <select value={form.clusterId} onChange={(e) => set('clusterId', e.target.value)} className="w-full rounded-lg border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm outline-none">
                  {clusters.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                </select>
              </div>
            </div>

            <div>
              <Label icon={<Database size={11} />}>Database</Label>
              <div className="grid gap-2">
                {engineGroups.map((g) => {
                  const list = engines.filter((e) => e.group === g);
                  if (list.length === 0) return null;
                  return (
                    <div key={g}>
                      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-zinc-600">{g}</div>
                      <div className="flex flex-wrap gap-2">
                        {list.map((e) => (
                          <button key={e.id} type="button" disabled={!e.ready} onClick={() => pickEngine(e.id)} title={e.note || e.label}
                            className={cn('rounded-lg border px-3 py-2 text-left text-sm transition', form.engine === e.id ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-200' : 'border-zinc-800 bg-zinc-900 text-zinc-400', !e.ready ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:border-zinc-600')}>
                            <span className="font-medium">{e.label}</span>
                            {e.note && <span className="block text-[10px] text-zinc-600">{e.note}</span>}
                          </button>
                        ))}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>

            {!isFileish && (
            <div>
              <Label icon={<Plug2 size={11} />}>Connection</Label>
              <div className="grid grid-cols-2 gap-2">
                {(['direct', 'ssh'] as const).map((m) => (
                  <button key={m} type="button" onClick={() => set('mode', m)} className={cn('cursor-pointer rounded-lg border px-3 py-2.5 text-left text-sm transition [&_svg]:pointer-events-none', form.mode === m ? 'border-emerald-500/50 bg-emerald-500/10' : 'border-zinc-800 bg-zinc-900')}>
                    <div className="font-medium text-zinc-100">{m === 'direct' ? '● Direct' : '● SSH Tunnel'}</div>
                    <div className="text-xs text-zinc-500">{m === 'direct' ? 'host:port reachable from here' : 'PG stays on 127.0.0.1 · encrypted SSH'}</div>
                  </button>
                ))}
              </div>
            </div>
            )}

            {form.mode === 'ssh' ? (
              <div className="grid grid-cols-2 gap-4">
                <Field label="SSH Host"><Input value={form.sshHost} onChange={(e) => set('sshHost', e.target.value)} placeholder="203.0.113.10" /></Field>
                <Field label="SSH User"><Input value={form.sshUser} onChange={(e) => set('sshUser', e.target.value)} placeholder="ubuntu / root" /></Field>
                <Field label="SSH Port" hint="Rarely anything but 22."><Input value={form.sshPort} onChange={(e) => set('sshPort', e.target.value)} placeholder="22" className="font-mono" /></Field>
                <Field label="Remote DB Port" hint="Postgres port ON the server."><Input value={form.port} onChange={(e) => set('port', e.target.value)} placeholder="5432" className="font-mono" /></Field>
                <div className="col-span-2">
                  <Label icon={<KeyRound size={11} />}>SSH auth</Label>
                  <div className="grid grid-cols-3 gap-2">
                    {([['key', 'Key file', 'Browse & select .pem/ppk'], ['password', 'Password', 'SSH login password'], ['agent', 'Agent', 'ssh-agent / Pageant']] as const).map(([v, t, d]) => (
                      <button key={v} onClick={() => set('sshAuth', v)} className={cn('rounded-lg border px-3 py-2 text-left text-sm', form.sshAuth === v ? 'border-emerald-500/50 bg-emerald-500/10' : 'border-zinc-800 bg-zinc-900')}>
                        <div className="font-medium text-zinc-100">{form.sshAuth === v ? '●' : '○'} {t}</div>
                        <div className="truncate text-[11px] text-zinc-500">{d}</div>
                      </button>
                    ))}
                  </div>
                </div>
                {form.sshAuth === 'key' && (
                  <>
                    <div className="col-span-2">
                      <Field label="SSH Key" hint="Click Browse or paste a path. Stored encrypted, never sent to AI.">
                        <div className="flex gap-2">
                          <Input value={form.sshKeyPath} onChange={(e) => set('sshKeyPath', e.target.value)} placeholder="C:\Users\you\.ssh\id_rsa" className="font-mono" />
                          <Button variant="outline" className="shrink-0" onClick={browseKey}><FolderOpen size={14} /> Browse</Button>
                        </div>
                      </Field>
                    </div>
                    <div className="col-span-2"><Field label="Key passphrase (if any)"><PasswordInput value={form.sshKeyPassphrase} onChange={(v) => set('sshKeyPassphrase', v)} /></Field></div>
                  </>
                )}
                {form.sshAuth === 'password' && (
                  <div className="col-span-2"><Field label="SSH password" hint="Used only to open the tunnel. AES-GCM encrypted in SQLite, masked here."><PasswordInput value={form.sshPassword} onChange={(v) => set('sshPassword', v)} mono /></Field></div>
                )}
                {form.sshAuth === 'agent' && (
                  <div className="col-span-2 rounded-lg border border-zinc-800 bg-zinc-900/60 px-3 py-2 text-xs text-zinc-500">Uses a running ssh-agent (or Pageant via SSH_AUTH_SOCK). No secret is stored at all.</div>
                )}
                <div className="col-span-2 flex items-center gap-2">
                  <Button variant="outline" className="!py-1.5 text-xs" onClick={testSSH} disabled={sshBusy}>
                    {sshBusy ? <Loader2 size={13} className="animate-spin" /> : <Plug2 size={13} />} Test SSH only
                  </Button>
                  {sshMsg && <span className="truncate font-mono text-[11px] text-zinc-400">{sshMsg}</span>}
                </div>
              </div>
            ) : isFileish ? null : (
              <div className="grid grid-cols-3 gap-4">
                <div className="col-span-2"><Field label="Host"><Input value={form.host} onChange={(e) => set('host', e.target.value)} /></Field></div>
                <Field label="Port"><Input value={form.port} onChange={(e) => set('port', e.target.value)} /></Field>
              </div>
            )}

            {isTurso ? (
              <div className="grid gap-4">
                <Field label="Turso URL" hint="libsql://your-db.turso.io, or http://127.0.0.1:8080 for local sqld.">
                  <Input value={form.database} onChange={(e) => set('database', e.target.value)} placeholder="libsql://…" className="font-mono" />
                </Field>
                <Field label="Auth token" hint="Database token. AES-GCM encrypted here — the AI never sees it, only table names.">
                  <PasswordInput value={form.aiPassword} onChange={(v) => set('aiPassword', v)} mono />
                </Field>
              </div>
            ) : isSqlite ? (
              <Field label="Database file" hint="The .db file. It is always opened read-only — no server, no login, no users.">
                <div className="flex gap-2">
                  <Input value={form.database} onChange={(e) => set('database', e.target.value)} placeholder="C:\data\app.db" className="font-mono" />
                  <Button variant="outline" className="shrink-0" onClick={browseFile}><FolderOpen size={14} /> Browse</Button>
                </div>
              </Field>
            ) : (
            <div className="grid grid-cols-2 gap-4">
              <Field label="Database"><Input value={form.database} onChange={(e) => set('database', e.target.value)} placeholder="connect_local" /></Field>
              <Field label="AI user"><Input value={form.aiUser} onChange={(e) => set('aiUser', e.target.value)} /></Field>
            </div>
            )}

            {!isFileish && (<>
            <div>
              <Label icon={<ShieldCheck size={11} />}>Database setup</Label>
              <div className="grid grid-cols-2 gap-2">
                <button onClick={() => set('setup', 'auto')} className={cn('rounded-lg border p-3 text-left text-sm', form.setup === 'auto' ? 'border-emerald-500/50 bg-emerald-500/10' : 'border-zinc-800 bg-zinc-900')}>
                  <div className="font-medium text-zinc-100">● Create dedicated read-only user</div>
                  <div className="text-xs text-zinc-500">Temp admin creds · forgotten after</div>
                </button>
                <button onClick={() => set('setup', 'manual')} className={cn('rounded-lg border p-3 text-left text-sm', form.setup === 'manual' ? 'border-emerald-500/50 bg-emerald-500/10' : 'border-zinc-800 bg-zinc-900')}>
                  <div className="font-medium text-zinc-100">○ I'll create it manually</div>
                  <div className="text-xs text-zinc-500">We generate the SQL script</div>
                </button>
              </div>
            </div>

            {form.setup === 'auto' ? (
              <div className="grid grid-cols-2 gap-3 rounded-xl border border-zinc-800 bg-zinc-900/60 p-3.5">
                <Field label="Admin user (temporary)"><Input value={form.adminUser} onChange={(e) => set('adminUser', e.target.value)} placeholder="postgres" /></Field>
                <Field label="Admin password (never stored)" hint="Used once to CREATE ROLE, then discarded."><PasswordInput value={form.adminPassword} onChange={(v) => set('adminPassword', v)} /></Field>
                <div className="col-span-2 flex flex-wrap items-center gap-1.5 border-t border-zinc-800 pt-2.5">
                  <Button variant="outline" className="!py-1 text-[11px]" onClick={() => runStep('admin')} disabled={stepBusy !== '' || testing}>
                    {stepBusy === 'admin' ? <Loader2 size={12} className="animate-spin" /> : <KeyRound size={12} />} Test admin login
                  </Button>
                  <Button variant="outline" className="!py-1 text-[11px]" onClick={() => runStep('provision')} disabled={stepBusy !== '' || testing}>
                    {stepBusy === 'provision' ? <Loader2 size={12} className="animate-spin" /> : <UserPlus size={12} />} Create AI user
                  </Button>
                  <span className="text-[11px] text-zinc-600">standalone steps — full proof still via Test Connection below</span>
                </div>
              </div>
            ) : (
              <Card className="p-4">
                <div className="mb-2 flex items-center gap-2 text-xs font-medium text-zinc-400"><TerminalSquare size={14} /> Run this as admin, then paste the AI password below</div>
                <pre className="max-h-72 min-h-28 overflow-auto rounded-lg codeblock bg-black/60 p-3 font-mono text-[11px] leading-relaxed text-zinc-300">{sqlPreview}</pre>
              </Card>
            )}

            <Field label="AI password" hint="Masked · AES-GCM encrypted at rest — the AI never sees it, only the name.">
              <div className="flex gap-2">
                <div className="flex-1"><PasswordInput value={form.aiPassword} onChange={(v) => set('aiPassword', v)} mono /></div>
                <Button variant="outline" title="Generate a new random password" onClick={regenAIPassword} className="shrink-0"><Dices size={14} /></Button>
              </div>
            </Field>
            </>)}

            {err && <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 font-mono text-xs text-red-300">{err}</div>}

            {step === 'verify' && (
              <Card className="p-4">
                <div className="mb-3 text-sm font-medium text-zinc-200">Gateway proof — source enables only if all green</div>
                <div className="grid gap-2">
                  {(checks || Array.from({ length: 7 }).map((_, i) => ({ key: 'k' + i, label: ['SSH connection successful', 'PostgreSQL detected', 'Database accessible', 'AI user created', 'SELECT permission verified', 'Write permission denied', 'Read-only configuration verified'][i] || '…', ok: false, detail: 'running…', durationMs: 0 } as CheckResult))).map((c) => (
                    <div key={c.key + c.label} className="flex items-center gap-3 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2">
                      {checks ? (c.ok ? <span className="flex h-5 w-5 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-300"><Check size={13} /></span> : <span className="flex h-5 w-5 items-center justify-center rounded-full bg-red-500/15 text-red-300"><X size={13} /></span>) : <Loader2 size={15} className="animate-spin text-zinc-500" />}
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[13px] text-zinc-200">{c.label}</div>
                        <div className="truncate font-mono text-[11px] text-zinc-500">{c.detail}</div>
                      </div>
                    </div>
                  ))}
                </div>
                {checks && checks.some((c) => !c.ok) && (
                  <div className="mt-3 grid gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
                    <div className="flex items-center justify-between gap-2">
                      <span>Verification failed — fix the red rows, or save as draft and finish later.</span>
                      <Button variant="outline" className="!py-1 text-xs" onClick={saveDraft} disabled={savingDraft}>{savingDraft ? 'Saving…' : 'Save draft anyway'}</Button>
                    </div>
                    {form.mode === 'ssh' && (
                      <div className="flex items-center justify-between gap-2 border-t border-amber-500/20 pt-2">
                        <span className="font-mono text-[11px]">PG rejected the login — inspect the server itself (listener? role? pg_hba?).</span>
                        <Button variant="outline" className="!py-1 text-xs" onClick={diagnose} disabled={diagBusy}>
                          {diagBusy ? <Loader2 size={13} className="animate-spin" /> : <Server size={13} />} Diagnose server via SSH
                        </Button>
                      </div>
                    )}
                  </div>
                )}
              </Card>
            )}

            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 text-xs text-zinc-500"><ShieldCheck size={14} className="text-emerald-400" /> {isSqlite ? 'Opened read-only · AI sees names only' : isTurso ? 'Token stays here · AI sees names only' : 'Admin password is never stored · AI sees names only'}</div>
              <div className="flex gap-2">
                <Button variant="outline" onClick={saveDraft} disabled={savingDraft || testing}>
                  {savingDraft ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />} Save draft
                </Button>
                <Button variant="emerald" onClick={runTest} disabled={testing}>
                  {testing ? <Loader2 size={15} className="animate-spin" /> : <Plug2 size={15} />} Test Connection
                </Button>
              </div>
            </div>
          </div>
        ) : (
          <div className="mx-auto max-w-lg px-4 py-10 text-center">
            <div className="relative mx-auto mb-4 h-16 w-16">
              <span className="absolute inset-0 animate-ping rounded-full bg-emerald-500/20" />
              <span className="relative flex h-16 w-16 items-center justify-center rounded-full border border-emerald-500/40 bg-emerald-500/15 text-emerald-300"><Check size={28} /></span>
            </div>
            <div className="text-xl font-semibold text-zinc-100">Database ready</div>
            <div className="mx-auto mt-1 max-w-sm text-[13px] text-zinc-500">
              The gateway proved this identity read-only. The AI sees the name — never the credentials.
            </div>
            <Card className="mt-5 p-3.5 text-left">
              <div className="flex items-center gap-2.5">
                <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-300"><Database size={17} /></span>
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold text-zinc-100">{form.name || 'Untitled'}</div>
                  <div className="truncate font-mono text-[11px] text-zinc-500">
                    {isSqlite
                      ? `sqlite file · always read-only`
                      : isTurso
                        ? `turso · token auth · guard-enforced read-only`
                        : `${form.engine} · ${form.database} · ${form.aiUser}${form.mode === 'ssh' ? ` · ssh → ${form.sshHost || 'server'}` : ` · ${form.host}:${form.port}`}`}
                  </div>
                </div>
                <span className="ml-auto shrink-0"><Badge tone="green">✓ read-only proven</Badge></span>
              </div>
            </Card>
            {(checks || []).length > 0 && (
              <div className="mt-3 grid gap-1.5 text-left">
                {(checks || []).filter((c) => c.ok).map((c) => (
                  <div key={c.key + c.label} className="flex items-center gap-2.5 rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-3 py-1.5">
                    <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-300"><Check size={11} /></span>
                    <span className="truncate text-xs text-zinc-300">{c.label}</span>
                    <span className="ml-auto shrink-0 font-mono text-[10px] text-zinc-600">{c.durationMs}ms</span>
                  </div>
                ))}
              </div>
            )}
            <div className="mt-4 flex items-center justify-center gap-1.5 font-mono text-[11px] text-zinc-500">
              <Plug2 size={12} className="text-emerald-400" /> live on MCP as "{form.name || 'Untitled'}" · try: doctor("{form.name || 'source'}")
            </div>
            <div className="mt-5 flex justify-center gap-2">
              <Button variant="emerald" onClick={onClose}>Done</Button>
              <Button variant="outline" onClick={() => { setStep('form'); setChecks(null); }}><Copy size={14} /> Add another</Button>
            </div>
          </div>
        )}
      </div>
    </Sheet>
  );
}

export function StatusDot({ status, verified }: { status: string; verified?: boolean }) {
  const color = status === 'ready' && verified ? 'bg-emerald-400' : status === 'error' ? 'bg-red-400' : 'bg-zinc-600';
  return <span className={cn('inline-block h-2 w-2 rounded-full', color)} />;
}

export function CheckPill({ c }: { c: CheckResult }) {
  return <Badge tone={c.ok ? 'green' : 'red'}>{c.ok ? '✓' : '✕'} {c.label}</Badge>;
}
