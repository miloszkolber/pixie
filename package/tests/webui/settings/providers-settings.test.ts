import { expect, test } from "bun:test";
import type { ProviderStatus } from "@pixie/contracts";
import {
	invalidateProviderReadiness,
	providerAvailability,
	readinessStatusText,
	settleProviderReadiness,
} from "@/settings/sections/providers-settings";

const provider: ProviderStatus = {
	id: "openai",
	name: "OpenAI",
	configured: true,
	modelCount: 1,
	availableModelCount: 1,
	readinessCheck: true,
};

test("provider cards gate readiness controls and expose safe status states", async () => {
	expect(providerAvailability(provider, null)).toEqual({
		usable: false,
		qualifier: "readiness not checked",
	});
	expect(providerAvailability(provider, "ready")).toEqual({
		usable: true,
		qualifier: "readiness confirmed",
	});
	expect(providerAvailability({ ...provider, available: false }, "ready").usable).toBeFalse();
	expect(readinessStatusText("checking")).toBe("Checking configuration…");
	expect(readinessStatusText("ready")).toBe("Configured");
	expect(readinessStatusText("issue")).toBe("Configuration has an issue");
	expect(readinessStatusText("not-ready")).toBe("Not configured");
	expect(readinessStatusText("failed")).toBe("Couldn't check configuration.");

	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/provider-card.svelte", import.meta.url),
	).text();
	expect(source).toMatch(/Check configuration for \$\{provider\.name\}/);
	expect(source).toContain("runtime unavailable");
	expect(source).toContain("provider.detail");
	for (const testId of [
		"provider-row",
		"provider-readiness",
		"provider-signout",
		"provider-apikey",
		"provider-signin",
	]) {
		expect(source).toContain(`data-testid="${testId}"`);
	}
});

test("a deferred readiness result cannot survive provider replacement or logout invalidation", () => {
	const checking = { revision: 4, status: "checking" as const };
	const afterStatusReplacement = invalidateProviderReadiness(checking);
	expect(settleProviderReadiness(afterStatusReplacement, 4, "ready")).toEqual({
		revision: 5,
		status: null,
	});
	const afterLogout = invalidateProviderReadiness(afterStatusReplacement);
	expect(settleProviderReadiness(afterLogout, 5, "failed")).toEqual({
		revision: 6,
		status: null,
	});
});

test("late login starts are cancelled instead of installing a hidden login", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/providers-settings.svelte", import.meta.url),
	).text();
	expect(source).toContain("const sequence = ++loginStartSequence");
	expect(source).toContain("mounted && sequence === loginStartSequence");
	expect(source).toMatch(/if \(!isCurrent\(\)\)[\s\S]*provider\.loginCancel/);
});
