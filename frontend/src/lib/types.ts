export type Engine = 'postgres' | 'cockroachdb' | 'redshift' | 'mysql' | 'mariadb' | 'tidb' | 'sqlite' | 'turso' | 'sqlserver' | 'clickhouse' | 'oracle';
export type ConnectionMode = 'direct' | 'ssh' | 'file';
export type SourceStatus = 'draft' | 'verifying' | 'ready' | 'error';

export interface Cluster {
  id: string;
  name: string;
  description: string;
  color: string;
  createdAt: string;
}

export interface Source {
  id: string;
  name: string;
  clusterId: string;
  engine: Engine;
  mode: ConnectionMode;
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  sshHost: string;
  sshPort: number;
  sshUser: string;
  sshAuth: string; // key | password | agent
  sshKeyPath: string;
  sshKeyPassphrase?: string;
  sshPassword?: string;
  status: SourceStatus;
  readOnlyVerified: boolean;
  lastCheckAt?: string;
  lastError?: string;
  createdAt: string;
}

export interface CheckResult {
  key: string;
  label: string;
  ok: boolean;
  detail: string;
  durationMs: number;
}

export interface TableInfo {
  schema: string;
  name: string;
  rowEstimate: number;
  columns?: { name: string; type: string; nullable: boolean; default?: string }[];
}

export interface QueryResult {
  columns: string[];
  rows: any[][];
  rowCount: number;
  totalRows?: number; // exact filtered total; -1/undefined = unknown, use estimate
  durationMs: number;
  truncated: boolean;
}

export interface DoctorFinding {
  severity: 'info' | 'warn' | 'critical';
  title: string;
  detail: string;
  remedy?: string;
  source: string;
}
