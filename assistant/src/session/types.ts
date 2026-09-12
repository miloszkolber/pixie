/**
 * Pure contracts for the vanilla native-session flow.
 *
 * These types deliberately contain no SDK objects.  The native adapter can
 * translate them to AgentSession calls without making the flow coordinator a
 * second session implementation.
 */

export const SESSION_FLOW_VERSION = 1 as const;

export type NativeTrustSetting = "ask" | "always" | "never";
export type ResourceScope = "user" | "project";
export type ResourceKind = "extension" | "skill" | "prompt" | "theme" | "unknown";

export interface NativeResource {
	readonly path: string;
	readonly resolvedPath?: string;
	readonly scope: ResourceScope;
	readonly kind?: ResourceKind;
	/** Project resources default to requiring native trust. */
	readonly requiresTrust?: boolean;
}

export interface SessionResourceInput {
	readonly resources?: readonly NativeResource[];
	/** Stored project decision. Null means that the project has not been decided. */
	readonly projectTrust?: boolean | null;
	/** Native settings default used only when there is no stored decision. */
	readonly defaultProjectTrust?: NativeTrustSetting;
}

export type TrustState = "not-required" | "trusted" | "untrusted";

export interface TrustResolution {
	readonly state: TrustState;
	readonly allowed: boolean;
	readonly decision: boolean | NativeTrustSetting | null;
	readonly reason: "no-project-resources" | "stored-decision" | "default-always" | "trust-required";
}

export interface NativeSessionIdentity {
	/** Opaque Pixie association; never derived from a secret or endpoint. */
	readonly sessionKey: string;
	/** Stable native session identity. */
	readonly sessionId: string;
	/** Kept separate to prevent browser/host/native ID coercion. */
	readonly nativeSessionId: string;
	readonly bootId: string;
	readonly childGeneration: number;
	readonly cwd: string;
	readonly agentDir: string;
}

export interface NativeModel {
	readonly provider: string;
	readonly id: string;
	readonly name?: string;
}

export type NativeThinkingLevel = string;

export interface SessionCreateRequest extends SessionResourceInput {
	readonly identity: NativeSessionIdentity;
	readonly availableModels?: readonly NativeModel[];
	readonly availableThinkingLevels?: readonly NativeThinkingLevel[];
	readonly model?: NativeModel;
	readonly thinkingLevel?: NativeThinkingLevel;
}

export interface PromptTextBlock {
	readonly type: "text";
	readonly text: string;
}

export interface PromptImageBlock {
	readonly type: "image";
	readonly mimeType: string;
	readonly data: string;
}

export interface PromptResourceBlock {
	readonly type: "resource";
	readonly resource: {
		readonly uri?: string;
		readonly mimeType?: string;
		readonly text?: string;
		readonly _meta?: { readonly name?: string };
	};
}

export type PromptBlock = PromptTextBlock | PromptImageBlock | PromptResourceBlock;

export interface PromptRequest {
	readonly sessionKey: string;
	readonly generation: number;
	readonly deliveryId: string;
	readonly runId: string;
	readonly content: readonly PromptBlock[];
}

export interface NativeImage {
	readonly type: "image";
	readonly mimeType: string;
	readonly data: string;
}

/** Presentation metadata is safe to replay; image bytes and resource text are not replayed here. */
export type PromptReplayBlock =
	| { readonly type: "text"; readonly text: string }
	| { readonly type: "image"; readonly mimeType: string; readonly bytes: number }
	| {
			readonly type: "resource";
			readonly uri: string;
			readonly mimeType?: string;
			readonly name: string;
	  };

export interface ValidatedPrompt {
	readonly text: string;
	readonly images: readonly NativeImage[];
	readonly replay: readonly PromptReplayBlock[];
}

export type DeliveryStatus =
	| "prepared"
	| "dispatching"
	| "accepted"
	| "settled"
	| "rejected"
	| "uncertain"
	| "interrupted";

export interface DeliveryState {
	readonly deliveryId: string;
	readonly runId: string;
	readonly generation: number;
	readonly status: DeliveryStatus;
	readonly prompt: ValidatedPrompt;
	readonly reason?: string;
	readonly stopReason?: string;
}

export type AbortOutcomeKind =
	| "aborted"
	| "already-idle"
	| "timed-out"
	| "rejected"
	| "interrupted"
	| "stale-generation";

export interface AbortOutcome {
	readonly kind: AbortOutcomeKind;
	readonly generation: number;
	readonly runId?: string;
	readonly reason?: string;
}

export interface AbortState {
	readonly requestId: string;
	readonly generation: number;
	readonly runId: string;
}

export type FlowPhase = "ready" | "prompting" | "aborting" | "reopening" | "closed";

export interface ReopenState {
	readonly requestId: string;
	readonly fromGeneration: number;
	readonly generation: number;
}

export interface SessionFlowState {
	readonly version: typeof SESSION_FLOW_VERSION;
	readonly identity: NativeSessionIdentity;
	readonly phase: FlowPhase;
	readonly resources: readonly NativeResource[];
	readonly loadedResources: readonly NativeResource[];
	readonly trust: TrustResolution;
	readonly availableModels: readonly NativeModel[];
	readonly availableThinkingLevels: readonly NativeThinkingLevel[];
	readonly model?: NativeModel;
	readonly thinkingLevel?: NativeThinkingLevel;
	readonly delivery?: DeliveryState;
	readonly abort?: AbortState;
	readonly reopen?: ReopenState;
	readonly lastAbort?: AbortOutcome;
}

export interface SessionReplay {
	readonly version: typeof SESSION_FLOW_VERSION;
	readonly sessionKey: string;
	readonly sessionId: string;
	readonly nativeSessionId: string;
	readonly bootId: string;
	readonly childGeneration: number;
	readonly phase: FlowPhase;
	readonly trust: Pick<TrustResolution, "state" | "allowed" | "reason">;
	readonly resources: readonly NativeResource[];
	readonly loadedResources: readonly NativeResource[];
	readonly model?: NativeModel;
	readonly thinkingLevel?: NativeThinkingLevel;
	readonly delivery?: {
		readonly deliveryId: string;
		readonly runId: string;
		readonly generation: number;
		readonly status: DeliveryStatus;
	};
	readonly lastAbort?: Pick<AbortOutcome, "kind" | "generation" | "runId">;
}

export type FlowErrorCode =
	| "invalid-request"
	| "invalid-resource"
	| "trust-required"
	| "session-closed"
	| "wrong-session"
	| "stale-generation"
	| "invalid-phase"
	| "duplicate-delivery"
	| "unknown-delivery"
	| "invalid-delivery-state"
	| "unknown-model"
	| "unsupported-thinking"
	| "invalid-reopen";

export interface FlowError {
	readonly code: FlowErrorCode;
	readonly message: string;
}

export type FlowResult<T> =
	| { readonly ok: true; readonly value: T }
	| { readonly ok: false; readonly error: FlowError };
