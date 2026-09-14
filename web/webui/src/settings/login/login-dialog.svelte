<script lang="ts">
import Button from "@/components/button.svelte";
import Dialog from "@/components/dialog.svelte";
import Icon from "@/components/icon.svelte";
import { errorText } from "@/connection";
import type { LoginState } from "./login-state";

interface Props {
	state: LoginState;
	providerName: string;
	onReply: (value: string) => void | Promise<void>;
	onCancel: () => void | Promise<void>;
	onClose: () => void | Promise<void>;
}

let { state: loginState, providerName, onReply, onCancel, onClose }: Props = $props();
let prompt = $state<HTMLInputElement>();
let openedUrl = $state<string | null>(null);
let open = $state(true);
let submitting = $state(false);
let submitError = $state<string | null>(null);
let terminal = $derived(loginState.status !== "active");
let title = $derived(
	loginState.status === "success"
		? `${providerName} connected`
		: loginState.status === "error"
			? "Couldn't connect"
			: `Connect ${providerName}`,
);
let description = $derived<string>(
	loginState.instructions && loginState.status === "active" ? loginState.instructions : "",
);

$effect(() => {
	const deviceUri = loginState.deviceCode?.verificationUri;
	if (!deviceUri || openedUrl === deviceUri) return;
	openedUrl = deviceUri;
	window.open(deviceUri, "_blank", "noopener,noreferrer");
});

$effect(() => {
	const input = prompt;
	if (!input || loginState.input?.kind !== "prompt") return;
	queueMicrotask(() => input.focus());
});

function openUrl(url: string): void {
	window.open(url, "_blank", "noopener,noreferrer");
}

async function reply(value: string): Promise<void> {
	if (submitting) return;
	submitting = true;
	submitError = null;
	const input = loginState.input;
	try {
		await onReply(value);
	} catch (cause) {
		if (loginState.status === "active" && loginState.input === input)
			submitError = errorText(cause);
	} finally {
		submitting = false;
	}
}

function submitPrompt(): void {
	const value = prompt?.value ?? "";
	const allowEmpty = loginState.input?.kind === "prompt" && loginState.input.allowEmpty;
	if (value.trim() || allowEmpty) void reply(value);
}

async function dismiss(): Promise<void> {
	if (submitting) return;
	submitting = true;
	try {
		if (terminal) await onClose();
		else await onCancel();
	} catch (cause) {
		submitError = errorText(cause);
	} finally {
		submitting = false;
	}
}
</script>

<Dialog
	bind:open
	{title}
	{description}
	testid="login-dialog"
	class="login-dialog"
	onOpenChange={(next) => {
		if (!next) void dismiss();
		return false;
	}}
>
	<div data-provider={loginState.providerId} data-status={loginState.status}>
	{#if terminal && submitError}<p role="alert" class="u-text-feedback-error tr-text-ui">{submitError}</p>{/if}
		{#if loginState.status === "success"}
			<p
				class="login-status--success u-flex u-items-center u-gap-sm tr-text-ui"
				data-testid="login-success"
			>
				<Icon name="check" size={16} />
				{providerName} is connected.
			</p>
		{:else if loginState.status === "error"}
			<p
				class="u-flex u-items-start u-gap-sm u-text-feedback-error tr-text-ui"
				data-testid="login-error"
			>
				<Icon name="triangle-alert" size={16} class="login-status__icon" />
				<span class="u-min-w-0 login-status__message">{loginState.error ?? "Login failed."}</span>
			</p>
		{:else}
			<div class="u-flex u-flex-col u-gap-md">
				{#if submitError}<p role="alert" class="u-text-feedback-error tr-text-ui">{submitError} You can retry or cancel.</p>{/if}
				{#if loginState.url}
					<div class="u-flex u-flex-col u-gap-xs">
						<Button data-testid="login-open-url" onclick={() => openUrl(loginState.url ?? "")}>
							<Icon name="external-link" size={16} />
							Open sign-in page
						</Button>
						<code
							class="login-url u-rounded u-bg-control-bg u-px-sm u-py-xs tr-code-text u-text-text-muted"
						>
							{loginState.url}
						</code>
					</div>
				{/if}

				{#if loginState.deviceCode}
					<div
						class="card u-flex u-flex-col u-gap-xs u-p-md"
						data-testid="login-device-code"
					>
						<span class="u-text-text-muted tr-text-metadata">
							Enter this code at
							<a
								href={loginState.deviceCode.verificationUri}
								target="_blank"
								rel="noopener noreferrer"
								data-testid="login-device-url"
								class="login-device-link u-inline-flex u-items-center u-gap-0.5 u-outline-none"
							>
								{loginState.deviceCode.verificationUri}
								<Icon name="external-link" size={12} />
							</a>
						</span>
						<code class="login-device-code tr-code-otp u-text-center u-text-text-default">
							{loginState.deviceCode.userCode}
						</code>
					</div>
				{/if}

				{#if loginState.input?.kind === "select"}
					<div class="u-flex u-flex-col u-gap-xs">
						{#if loginState.input.message}
							<p class="u-text-text-muted tr-text-ui">{loginState.input.message}</p>
						{/if}
						{#each loginState.input.options as option (option.id)}
							<button
								type="button"
								data-testid="login-option"
								data-option={option.id}
								disabled={submitting}
								onclick={() => void reply(option.id)}
								class="btn login-option u-text-left"
								data-variant="outline"
							>
								{option.label}
							</button>
						{/each}
					</div>
				{/if}

				{#if loginState.input?.kind === "prompt"}
					<div class="u-flex u-flex-col u-gap-xs">
						{#if loginState.input.message}
							<p class="u-text-text-muted tr-text-ui">{loginState.input.message}</p>
						{/if}
						<div class="u-flex u-gap-sm">
							<input
								bind:this={prompt}
								class="text-field-input u-min-w-0 u-flex-1"
								data-testid="login-input"
								disabled={submitting}
								aria-label={loginState.input.message || "Provider configuration"}
								type={loginState.input.secret ? "password" : "text"}
								placeholder={loginState.input.placeholder ?? ""}
								onkeydown={(event) => {
									if (event.isComposing || event.keyCode === 229 || event.key !== "Enter") return;
									event.preventDefault();
									submitPrompt();
								}}
							/>
							<Button data-testid="login-submit" disabled={submitting} onclick={submitPrompt}>{submitting ? "Submitting…" : "Submit"}</Button>
						</div>
					</div>
				{/if}

				{#if loginState.progress}
					<p
						class="u-flex u-items-center u-gap-sm u-text-text-muted tr-text-ui"
						data-testid="login-progress"
					>
						<Icon name="loader-circle" size={16} class="login-spinner" />
						{loginState.progress}
					</p>
				{:else if !loginState.url && !loginState.deviceCode && !loginState.input}
					<p
						class="u-flex u-items-center u-gap-sm u-text-text-muted tr-text-ui"
						data-testid="login-working"
					>
						<Icon name="loader-circle" size={16} class="login-spinner" />
						Working…
					</p>
				{/if}
			</div>
		{/if}
	</div>

	{#snippet actions()}
		{#if terminal}
			<Button variant="outline" data-testid="login-close" disabled={submitting} onclick={() => void dismiss()}>Done</Button>
		{:else}
			<Button variant="outline" data-testid="login-cancel" disabled={submitting} onclick={() => void dismiss()}>Cancel</Button>
		{/if}
	{/snippet}
</Dialog>

<style>
	:global(.login-dialog) {
		max-block-size: 85vh;
		overflow-y: auto;
	}

	.login-status--success {
		color: var(--feedback-success);
	}

	:global(.login-status__icon) {
		margin-top: var(--space-2xs);
	}

	.login-status__message {
		overflow-wrap: break-word;
	}

	.login-url,
	.login-device-code {
		user-select: all;
	}

	.login-url,
	.login-device-link {
		word-break: break-all;
	}

	.login-device-link {
		color: var(--primary);
		text-decoration-line: underline;
		text-underline-offset: 2px;
	}

	.login-device-link:hover {
		opacity: 0.8;
	}

	.login-device-link:focus-visible {
		outline: var(--focus-ring-width, 2px) solid var(--border-focus);
		outline-offset: 2px;
	}

	.login-option {
		justify-content: flex-start;
	}

	:global(.login-spinner) {
		animation: login-spinner-rotate 1s linear infinite;
	}

	@keyframes login-spinner-rotate {
		to { transform: rotate(1turn); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.login-spinner) { animation: none; }
	}
</style>
