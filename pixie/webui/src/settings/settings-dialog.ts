import type { AgentProfile } from "@pixie/contracts";
import { SettingsSection } from "./state";

export interface SettingsTabDescriptor {
	section: SettingsSection;
	label: string;
}

const ADMIN_TABS: readonly SettingsTabDescriptor[] = [
	{ section: SettingsSection.Pi, label: "Pi" },
	{ section: SettingsSection.Providers, label: "Providers" },
	{ section: SettingsSection.Models, label: "Models" },
	{ section: SettingsSection.Tools, label: "Tools" },
	{ section: SettingsSection.Signet, label: "Signet" },
	{ section: SettingsSection.System, label: "System" },
];

const GENERIC_TABS: readonly SettingsTabDescriptor[] = [
	{ section: SettingsSection.Agent, label: "Agent" },
	{ section: SettingsSection.Signet, label: "Signet" },
	{ section: SettingsSection.System, label: "System" },
];

const PENDING_TABS: readonly SettingsTabDescriptor[] = [
	{ section: SettingsSection.System, label: "System" },
];

export function resolveSettingsSection(
	section: SettingsSection,
	profile: AgentProfile | null,
): SettingsSection {
	if (profile === null) return SettingsSection.System;
	if (!profile.pi || !profile.operations.administration) {
		return section === SettingsSection.Signet || section === SettingsSection.System
			? section
			: SettingsSection.Agent;
	}
	const available = settingsTabs(false, false, profile);
	return available.some((tab) => tab.section === section) ? section : SettingsSection.Providers;
}

export function settingsTabs(
	genericAgent = false,
	profilePending = false,
	profile?: AgentProfile | null,
): readonly SettingsTabDescriptor[] {
	if (profilePending) return PENDING_TABS;
	if (genericAgent) return GENERIC_TABS;
	return ADMIN_TABS.filter((tab) => {
		if (tab.section === SettingsSection.Signet) return profile?.capabilities?.mcp === 1;
		return true;
	});
}
