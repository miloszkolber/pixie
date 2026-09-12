import type { Schedule, ScheduleRun, WsParams, WsResult } from "@pixie/contracts";
import { errorText } from "../connection/error-text";
import type { WsTransport } from "../connection/transport";
import { randomId } from "../lib";
import { createExternalStore, toReadableStore } from "../store/external-store";

type MutationMethod =
	| "schedule.create"
	| "schedule.update"
	| "schedule.delete"
	| "schedule.runNow"
	| "schedule.stop";
type Mutation = { [M in MutationMethod]: { method: M; params: WsParams<M> } }[MutationMethod];
type Pending = { mutation: Mutation; label: string };
// Retain uncertain writes when settings close or the project changes. Never turn a retry into a new run.
const pendingByProject = new Map<string, Pending>();

interface SchedulesState {
	jobs: Schedule[];
	selectedId: string | null;
	loaded: boolean;
	loading: boolean;
	busy: boolean;
	error: string | null;
	healthError: string;
	notice: string;
	pending: Pending | null;
}

export function activeExecution(job: Schedule) {
	return job.runs.find((run) => run.status === "running");
}

export function scheduleSessionHref(projectId: string, run: ScheduleRun): string | null {
	return run.sessionId
		? `#/v1/projects/${encodeURIComponent(projectId)}/projectAreas/${encodeURIComponent(projectId)}/chats/${encodeURIComponent(run.sessionId)}`
		: null;
}

export function scheduleTime(value: string, timezone: string): string {
	try {
		return new Intl.DateTimeFormat(undefined, {
			dateStyle: "medium",
			timeStyle: "long",
			timeZone: timezone,
		}).format(new Date(value));
	} catch {
		return value;
	}
}

export class SchedulesModel {
	readonly state = createExternalStore<SchedulesState>(() => ({
		jobs: [],
		selectedId: null,
		loaded: false,
		loading: false,
		busy: false,
		error: null,
		healthError: "",
		notice: "",
		pending: null,
	}));
	readonly readable = toReadableStore(this.state);
	private loadSequence = 0;
	private loadPromise: Promise<void> | null = null;

	constructor(
		readonly projectId: string,
		private readonly transport: Pick<WsTransport, "request">,
	) {
		this.state.setState({ pending: pendingByProject.get(projectId) ?? null });
	}

	select(id: string | null): void {
		this.state.setState({
			selectedId: id && this.state.getState().jobs.some((job) => job.id === id) ? id : null,
		});
	}

	load(): Promise<void> {
		if (this.loadPromise) return this.loadPromise;
		this.loadPromise = this.refresh().finally(() => {
			this.loadPromise = null;
		});
		return this.loadPromise;
	}

	private async refresh(): Promise<void> {
		const sequence = ++this.loadSequence;
		this.state.setState({ loading: true });
		try {
			const [jobs, health] = await Promise.all([
				this.transport.request("schedule.list", { projectId: this.projectId }),
				this.transport.request("schedule.health", { projectId: this.projectId }),
			]);
			if (sequence !== this.loadSequence) return;
			const scoped = jobs.filter((job) => job.projectId === this.projectId);
			const selected = this.state.getState().selectedId;
			this.state.setState({
				jobs: scoped,
				loaded: true,
				healthError: health.error,
				selectedId: scoped.some((job) => job.id === selected) ? selected : (scoped[0]?.id ?? null),
				error: null,
			});
		} catch (error) {
			if (sequence === this.loadSequence)
				this.state.setState({
					error: `Couldn't refresh schedules. Last known results are retained. ${errorText(error)}`,
				});
		} finally {
			if (sequence === this.loadSequence) this.state.setState({ loading: false });
		}
	}

	async mutate<M extends MutationMethod>(
		method: M,
		params: Omit<WsParams<M>, "projectId" | "mutationId">,
		label: string,
	): Promise<boolean> {
		if (this.state.getState().busy || this.state.getState().pending) return false;
		const mutation = {
			method,
			params: { ...params, projectId: this.projectId, mutationId: randomId("schedule") },
		} as Mutation;
		const pending = { mutation, label };
		pendingByProject.set(this.projectId, pending);
		this.state.setState({ pending });
		return this.retry();
	}

	async retry(): Promise<boolean> {
		const { pending, busy } = this.state.getState();
		if (!pending || busy) return false;
		this.state.setState({ busy: true, notice: "" });
		try {
			const { method, params } = pending.mutation;
			const result = await this.transport.request(method, params);
			pendingByProject.delete(this.projectId);
			this.state.setState({ pending: null, notice: pending.label, error: null });
			// Invalidate any read started before the mutation committed.
			this.loadSequence++;
			await this.loadPromise;
			await this.load();
			if (method === "schedule.create" || method === "schedule.update")
				this.select((result as WsResult<"schedule.create">).id);
			return true;
		} catch (error) {
			this.state.setState({
				notice: `The action was not confirmed. ${errorText(error)} Retry sends the same mutation identity. Inspect the ledger before abandoning or reloading.`,
			});
			return false;
		} finally {
			this.state.setState({ busy: false });
		}
	}

	abandon(): void {
		if (this.state.getState().busy) return;
		pendingByProject.delete(this.projectId);
		this.state.setState({
			pending: null,
			notice:
				"Retry discarded. This does not undo a saved action or stop a run. Refresh and inspect the ledger before issuing another action.",
		});
	}
}
