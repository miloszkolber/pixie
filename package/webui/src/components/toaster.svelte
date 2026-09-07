<script lang="ts">
import { onDestroy, onMount } from "svelte";
import { appStore, appStoreApi } from "../store";
import Icon from "./icon.svelte";

const TOAST_DURATION_MS = 5000;

interface ToastTimer {
	timeout: ReturnType<typeof setTimeout> | undefined;
	remaining: number;
	startedAt: number;
}

const timers = new Map<string, ToastTimer>();
let toasts = $derived($appStore.toasts);
let pointerPaused = false;
let focusPaused = false;
let documentPaused = false;

function paused(): boolean {
	return pointerPaused || focusPaused || documentPaused;
}

function arm(id: string, timer: ToastTimer): void {
	if (paused() || timer.timeout !== undefined) return;
	timer.startedAt = Date.now();
	timer.timeout = setTimeout(() => {
		timers.delete(id);
		appStoreApi.getState().dismissToast(id);
	}, timer.remaining);
}

function pauseTimers(): void {
	const now = Date.now();
	for (const timer of timers.values()) {
		if (timer.timeout === undefined) continue;
		clearTimeout(timer.timeout);
		timer.timeout = undefined;
		timer.remaining = Math.max(0, timer.remaining - (now - timer.startedAt));
	}
}

function resumeTimers(): void {
	if (paused()) return;
	for (const [id, timer] of timers) arm(id, timer);
}

function setPointerPaused(next: boolean): void {
	pointerPaused = next;
	if (next) pauseTimers();
	else resumeTimers();
}

function setFocusPaused(next: boolean): void {
	focusPaused = next;
	if (next) pauseTimers();
	else resumeTimers();
}

$effect(() => {
	const currentToasts = toasts;
	const current = new Set(currentToasts.map((toast) => toast.id));
	for (const [id, timer] of timers) {
		if (!current.has(id)) {
			if (timer.timeout !== undefined) clearTimeout(timer.timeout);
			timers.delete(id);
		}
	}
	for (const toast of currentToasts) {
		if (toast.variant === "error" || timers.has(toast.id)) continue;
		const timer: ToastTimer = {
			timeout: undefined,
			remaining: TOAST_DURATION_MS,
			startedAt: 0,
		};
		timers.set(toast.id, timer);
		arm(toast.id, timer);
	}
});

onMount(() => {
	function handleVisibilityChange(): void {
		documentPaused = document.visibilityState === "hidden";
		if (documentPaused) pauseTimers();
		else resumeTimers();
	}

	handleVisibilityChange();
	document.addEventListener("visibilitychange", handleVisibilityChange);
	return () => document.removeEventListener("visibilitychange", handleVisibilityChange);
});

onDestroy(() => {
	for (const timer of timers.values()) {
		if (timer.timeout !== undefined) clearTimeout(timer.timeout);
	}
	timers.clear();
});
</script>

<div
	class="toast-container"
	role="region"
	aria-label="Notifications"
	aria-live="polite"
	aria-relevant="additions removals"
	onpointerenter={() => setPointerPaused(true)}
	onpointerleave={() => setPointerPaused(false)}
	onfocusin={() => setFocusPaused(true)}
	onfocusout={(event) => {
		if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocusPaused(false);
	}}
>
	{#each toasts as toast (toast.id)}
		<div class="toast" data-testid="toast" data-variant={toast.variant} role="status">
			<div class="toast-content">
				<div class="toast-text">
					{#if toast.title}<div class="toast-title">{toast.title}</div>{/if}
					<div class="toast-description">{toast.message}</div>
				</div>
				<button
					type="button"
					class="toast-close"
					aria-label="Dismiss notification"
					onclick={() => appStoreApi.getState().dismissToast(toast.id)}
				>
					<Icon name="x" size={14} />
				</button>
			</div>
		</div>
	{/each}
</div>

<style>
 .toast-container { top: var(--space-400); bottom: auto; max-height: 40dvh; overflow-y: auto; }
</style>
