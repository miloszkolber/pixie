import { expect, test } from "bun:test";
import {
	type NavigationLocationV2,
	parseFragment,
	serializeLocation,
} from "@/workspace/navigation/location";

test("v2 chat and file selections round-trip without exposing content", () => {
	const location: NavigationLocationV2 = {
		version: 2,
		primaryArea: "chats",
		primarySelection: { kind: "session", sessionId: "session/1", projectId: "project/1" },
		secondaryArea: "files",
		secondarySelection: {
			kind: "file",
			projectId: "project/1",
			resourceId: "file:1",
		},
		projectId: "project/1",
		projectAreaId: "area/1",
	};

	const fragment = serializeLocation(location);
	expect(fragment).toStartWith("#/v2/chats/");
	expect(fragment).not.toContain("secret content");
	expect(parseFragment(fragment)).toEqual(location);
});

test("v2 diff and module context round-trip as bounded query data", () => {
	const locations: NavigationLocationV2[] = [
		{
			version: 2,
			primaryArea: "settings",
			primarySelection: { kind: "settings", sectionId: "providers" },
			secondaryArea: "git",
			secondarySelection: {
				kind: "diff",
				projectId: "project",
				resourceId: "diff-tab",
				reviewId: "review/1",
			},
			projectId: "project",
			projectAreaId: "project",
		},
		{
			version: 2,
			primaryArea: "chats",
			primarySelection: null,
			secondaryArea: "module:browser",
			secondarySelection: {
				kind: "module",
				moduleId: "browser",
				resourceId: "panel-1",
				context: { scope: "session", sessionId: "session-1", projectId: "project" },
			},
			projectId: "project",
			projectAreaId: "project",
		},
	];

	for (const location of locations) {
		expect(parseFragment(serializeLocation(location))).toEqual(location);
	}
});

test("v2 accepts the bounded opaque ID size in encoded secondary data", () => {
	const projectId = "p".repeat(512);
	const location: NavigationLocationV2 = {
		version: 2,
		primaryArea: "chats",
		primarySelection: { kind: "session", sessionId: "session", projectId },
		secondaryArea: "files",
		secondarySelection: { kind: "file", projectId, resourceId: "f".repeat(512) },
		projectId,
		projectAreaId: "area",
	};
	expect(parseFragment(serializeLocation(location))).toEqual(location);
});

test("v2 serialization whitelists secondary identity fields", () => {
	const location = {
		version: 2 as const,
		primaryArea: "chats" as const,
		primarySelection: null,
		secondaryArea: "files" as const,
		secondarySelection: {
			kind: "file" as const,
			projectId: "project",
			resourceId: "file",
			secret: "must-not-be-routed",
		},
		projectId: "project",
		projectAreaId: null,
	};
	const fragment = serializeLocation(location);
	expect(fragment).not.toContain("must-not-be-routed");
	expect(parseFragment(fragment)).toEqual({
		...location,
		secondarySelection: { kind: "file", projectId: "project", resourceId: "file" },
	});
});

test("v2 omits the project area when no project keeps the route parseable", () => {
	const location: NavigationLocationV2 = {
		version: 2,
		primaryArea: "settings",
		primarySelection: { kind: "settings", sectionId: "providers" },
		secondaryArea: "details",
		secondarySelection: null,
		projectId: null,
		projectAreaId: "area-1",
	};
	const fragment = serializeLocation(location);
	expect(fragment).toBe("#/v2/settings/providers");
	expect(parseFragment(fragment)).toEqual({ ...location, projectAreaId: null });
});

test("malformed v2 resource routes safely fall back to the main location", () => {
	expect(parseFragment("#/v2/chats/session?project=project&secondaryKind=file")).toEqual({
		kind: "main",
	});
	expect(
		parseFragment(
			"#/v2/chats/session?project=project&secondaryKind=file&secondaryId=file&secondaryProject=project&secondaryProject=other",
		),
	).toEqual({
		kind: "main",
	});
	expect(
		parseFragment(
			"#/v2/chats/session?project=project&secondaryKind=module&secondaryId=panel&secondaryModule=browser&secondaryContextScope=instance&secondaryContextId=instance&secondaryContextProject=project",
		),
	).toEqual({
		kind: "main",
	});
});
