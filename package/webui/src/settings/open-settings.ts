import { appStoreApi, type SettingsSection } from "@/store";

export interface SettingsFocusTarget {
	isConnected: boolean;
	focus: () => void;
}

let returnFocus: SettingsFocusTarget | null = null;

export function openSettingsFrom(target: SettingsFocusTarget, section?: SettingsSection): void {
	returnFocus = target;
	appStoreApi.getState().openSettings(section);
}

export function restoreSettingsFocus(): boolean {
	const target = returnFocus;
	returnFocus = null;
	if (!target?.isConnected) return false;
	target.focus();
	return true;
}
