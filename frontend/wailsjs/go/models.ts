export namespace app {
	
	export class CheckView {
	    name: string;
	    status: string;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
	export class InstallView {
	    version: string;
	    source: string;
	    size: string;
	    editable: boolean;
	    active: boolean;
	
	    static createFrom(source: any = {}) {
	        return new InstallView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.source = source["source"];
	        this.size = source["size"];
	        this.editable = source["editable"];
	        this.active = source["active"];
	    }
	}
	export class RemoteRow {
	    version: string;
	    label: string;
	    date: string;
	    size: string;
	    installed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.label = source["label"];
	        this.date = source["date"];
	        this.size = source["size"];
	        this.installed = source["installed"];
	    }
	}
	export class RowView {
	    version: string;
	    source: string;
	    size: string;
	    binDir: string;
	    active: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RowView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.source = source["source"];
	        this.size = source["size"];
	        this.binDir = source["binDir"];
	        this.active = source["active"];
	    }
	}
	export class SettingsView {
	    autostart: boolean;
	    closeToTray: boolean;
	    checkUpdateOnStart: boolean;
	    storePath: string;
	    mirrors: Record<string, string>;
	    proxy: string;
	    indexCacheMinutes: number;
	    version: string;
	    remoteState: Record<string, string>;
	    totalSize: string;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.autostart = source["autostart"];
	        this.closeToTray = source["closeToTray"];
	        this.checkUpdateOnStart = source["checkUpdateOnStart"];
	        this.storePath = source["storePath"];
	        this.mirrors = source["mirrors"];
	        this.proxy = source["proxy"];
	        this.indexCacheMinutes = source["indexCacheMinutes"];
	        this.version = source["version"];
	        this.remoteState = source["remoteState"];
	        this.totalSize = source["totalSize"];
	    }
	}
	export class ToolView {
	    id: string;
	    title: string;
	    kind: string;
	    current?: InstallView;
	    lamp: string;
	    remoteState: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.current = this.convertValues(source["current"], InstallView);
	        this.lamp = source["lamp"];
	        this.remoteState = source["remoteState"];
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

