export namespace main {
	
	export class AdminTestRequest {
	    source: model.Source;
	    adminUser: string;
	    adminPassword: string;
	
	    static createFrom(source: any = {}) {
	        return new AdminTestRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], model.Source);
	        this.adminUser = source["adminUser"];
	        this.adminPassword = source["adminPassword"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProvisionRequest {
	    source: model.Source;
	    adminUser: string;
	    adminPassword: string;
	    aiUser: string;
	    aiPassword: string;
	
	    static createFrom(source: any = {}) {
	        return new ProvisionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], model.Source);
	        this.adminUser = source["adminUser"];
	        this.adminPassword = source["adminPassword"];
	        this.aiUser = source["aiUser"];
	        this.aiPassword = source["aiPassword"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TestRequest {
	    source: model.Source;
	    adminUser: string;
	    adminPassword: string;
	    aiUser: string;
	    aiPassword: string;
	    autoProvision: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TestRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], model.Source);
	        this.adminUser = source["adminUser"];
	        this.adminPassword = source["adminPassword"];
	        this.aiUser = source["aiUser"];
	        this.aiPassword = source["aiPassword"];
	        this.autoProvision = source["autoProvision"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace model {
	
	export class CheckResult {
	    key: string;
	    label: string;
	    ok: boolean;
	    detail: string;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new CheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.ok = source["ok"];
	        this.detail = source["detail"];
	        this.durationMs = source["durationMs"];
	    }
	}
	export class Cluster {
	    id: string;
	    name: string;
	    description: string;
	    color: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Cluster(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.color = source["color"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class ColumnInfo {
	    name: string;
	    type: string;
	    nullable: boolean;
	    default?: string;
	
	    static createFrom(source: any = {}) {
	        return new ColumnInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.nullable = source["nullable"];
	        this.default = source["default"];
	    }
	}
	export class DoctorFinding {
	    severity: string;
	    title: string;
	    detail: string;
	    remedy?: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new DoctorFinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.severity = source["severity"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.remedy = source["remedy"];
	        this.source = source["source"];
	    }
	}
	export class Filter {
	    column: string;
	    op: string;
	    value: string;
	    logic: string;
	
	    static createFrom(source: any = {}) {
	        return new Filter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.column = source["column"];
	        this.op = source["op"];
	        this.value = source["value"];
	        this.logic = source["logic"];
	    }
	}
	export class SourcePublic {
	    name: string;
	    cluster: string;
	    engine: string;
	    database: string;
	    status: string;
	    readOnly: boolean;
	    tableCount?: number;
	
	    static createFrom(source: any = {}) {
	        return new SourcePublic(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cluster = source["cluster"];
	        this.engine = source["engine"];
	        this.database = source["database"];
	        this.status = source["status"];
	        this.readOnly = source["readOnly"];
	        this.tableCount = source["tableCount"];
	    }
	}
	export class FleetOverview {
	    clusters: Cluster[];
	    sources: SourcePublic[];
	
	    static createFrom(source: any = {}) {
	        return new FleetOverview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.clusters = this.convertValues(source["clusters"], Cluster);
	        this.sources = this.convertValues(source["sources"], SourcePublic);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class QueryResult {
	    columns: string[];
	    rows: any[][];
	    rowCount: number;
	    totalRows: number;
	    durationMs: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new QueryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.columns = source["columns"];
	        this.rows = source["rows"];
	        this.rowCount = source["rowCount"];
	        this.totalRows = source["totalRows"];
	        this.durationMs = source["durationMs"];
	        this.truncated = source["truncated"];
	    }
	}
	export class TableInfo {
	    schema: string;
	    name: string;
	    rowEstimate: number;
	    columns?: ColumnInfo[];
	
	    static createFrom(source: any = {}) {
	        return new TableInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schema = source["schema"];
	        this.name = source["name"];
	        this.rowEstimate = source["rowEstimate"];
	        this.columns = this.convertValues(source["columns"], ColumnInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SchemaInfo {
	    sourceName: string;
	    tables: TableInfo[];
	
	    static createFrom(source: any = {}) {
	        return new SchemaInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceName = source["sourceName"];
	        this.tables = this.convertValues(source["tables"], TableInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Source {
	    id: string;
	    name: string;
	    clusterId: string;
	    engine: string;
	    mode: string;
	    host: string;
	    port: number;
	    database: string;
	    username: string;
	    password: string;
	    sshHost: string;
	    sshPort: number;
	    sshUser: string;
	    sshAuth: string;
	    sshKeyPath: string;
	    sshKeyPassphrase?: string;
	    sshPassword?: string;
	    status: string;
	    readOnlyVerified: boolean;
	    lastCheckAt?: string;
	    lastError?: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Source(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.clusterId = source["clusterId"];
	        this.engine = source["engine"];
	        this.mode = source["mode"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.database = source["database"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.sshHost = source["sshHost"];
	        this.sshPort = source["sshPort"];
	        this.sshUser = source["sshUser"];
	        this.sshAuth = source["sshAuth"];
	        this.sshKeyPath = source["sshKeyPath"];
	        this.sshKeyPassphrase = source["sshKeyPassphrase"];
	        this.sshPassword = source["sshPassword"];
	        this.status = source["status"];
	        this.readOnlyVerified = source["readOnlyVerified"];
	        this.lastCheckAt = source["lastCheckAt"];
	        this.lastError = source["lastError"];
	        this.createdAt = source["createdAt"];
	    }
	}
	
	
	export class WriteResult {
	    rowsAffected: number;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new WriteResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rowsAffected = source["rowsAffected"];
	        this.durationMs = source["durationMs"];
	    }
	}

}

