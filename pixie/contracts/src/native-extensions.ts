export type NativeResourceState = "loaded" | "failed" | "not-loaded" | "not-observed" | "missing";
export interface NativeExtensionSource {
	source: string;
	scope: string;
	origin: string;
}
export interface NativeExtensionInventory {
	version: 1;
	configurationRevisions?: { user: string; project: string };
	context: {
		cwd: string;
		sessionId: string | null;
		reader: "session" | "service" | "not-resident" | "configured-only";
	};
	packages: {
		source: string;
		scope: string;
		filtered: boolean;
		installed: boolean;
		name: string | null;
		version: string | null;
		state: NativeResourceState;
	}[];
	paths: { path: string; scope: string }[];
	resources: (NativeExtensionSource & { path: string; enabled: boolean; state: NativeResourceState; resourceKey?: string; configurationSupported?: boolean })[];
	extensions: {
		path: string;
		resolvedPath: string;
		source: NativeExtensionSource;
		name: string | null;
		version: string | null;
		tools: string[];
		commands: string[];
		interfaceSupport: "unknown";
	}[];
	errors: { path: string; code: "load-failed" }[];
	warnings: string[];
	trust?: { projectTrusted: boolean; decision: boolean | null; requiresDecision: boolean };
}
export interface NativeExtensionTarget {
	projectId?: string;
	root?: string;
	sessionId?: string;
}

export interface NativeExtensionChange {
	saved?: boolean;
	loaded: false;
	reload: "deferred";
	reason: "session-busy" | "session-not-resident" | "sdk-loader-install-policy";
	warning?: string | null;
}

export interface NativeExtensionConfiguration extends NativeExtensionTarget {
	scope: "user" | "project";
	resourceKey: string;
	expectedRevision: string;
	enabled: boolean;
	confirmed: true;
}
