import { expect, test } from "bun:test";

const chatRoot = new URL("../../../webui/src/chat/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, chatRoot)).text();
}

function styleBlock(source: string): string {
	const match = source.match(/<style>([\s\S]*)<\/style>/);
	expect(match).not.toBeNull();
	return match?.[1] ?? "";
}

function mediaBlock(styles: string, query: string): string {
	const start = styles.indexOf(query);
	expect(start).toBeGreaterThanOrEqual(0);
	const open = styles.indexOf("{", start);
	expect(open).toBeGreaterThanOrEqual(0);
	let depth = 0;
	for (let index = open; index < styles.length; index += 1) {
		const character = styles[index];
		if (character === "{") depth += 1;
		else if (character === "}") {
			depth -= 1;
			if (depth === 0) return styles.slice(open, index + 1);
		}
	}
	throw new Error(`Unterminated media query: ${query}`);
}

/**
 * Regression: at 320x400 (and 1024x500) the editor region holds the composer
 * plus the provider/model/thinking, goal and stats toolbar. The toolbar wraps
 * at narrow widths and the editor is `flex-shrink: 0`, so without a floor the
 * transcript (`[data-testid=chat-scroll]`) was squeezed below the acceptance's
 * 64px minimum. These checks keep the two CSS guarantees that bound the column:
 * the transcript floor and the short-viewport toolbar cap.
 */
test("the transcript scroll viewport keeps the 64px short-viewport floor", async () => {
	const transcript = await source("view/chat-transcript.svelte");
	const styles = styleBlock(transcript);
	expect(transcript).toContain('data-testid="chat-scroll"');
	// The measured element is `.chat-viewport`. Mewa's 8rem default is reset,
	// but the reset must never be 0: 4rem is the acceptance floor.
	expect(styles).toMatch(/\.chat-viewport\s*{[^}]*min-height:\s*4rem;/s);
	expect(styles).toMatch(/\.chat-viewport\s*{[^}]*flex:\s*1;/s);
	// The section wrapper carries the same floor so the "load earlier" button
	// cannot consume the viewport's height.
	expect(styles).toMatch(/\.chat-scroller\s*{[^}]*min-height:\s*4rem;/s);
});

test("the auxiliary session toolbar cannot push the composer past the fold", async () => {
	const view = await source("chat-view.svelte");
	const styles = styleBlock(view);
	// The column stays a bounded flex column and the editor keeps a zero
	// minimum so it participates in the column's height resolution.
	expect(view).toContain('class="u-flex u-min-h-0 u-flex-1 u-flex-col chat-view-root"');
	expect(view).toContain('class="chat-view-editor-region u-shrink-0"');
	expect(styles).toMatch(/\.chat-view-editor-region\s*{[^}]*min-height:\s*0;/s);
	// The provider/model/thinking, goal and stats rows are wrapped so a short
	// viewport can bound and scroll them instead of growing the editor.
	expect(view).toContain('<div class="chat-view-session-toolbar">');
	const shortHeight = mediaBlock(styles, "@media (max-height: 600px)");
	expect(shortHeight).toMatch(/\.chat-view-session-toolbar\s*{[^}]*max-height:/);
	expect(shortHeight).toMatch(/\.chat-view-session-toolbar\s*{[^}]*overflow-y:\s*auto;/);
});

test("the composer keeps a compact short-viewport input budget", async () => {
	const composer = await source("composer/composer.svelte");
	const styles = styleBlock(composer);
	const shortHeight = mediaBlock(styles, "@media (max-height: 600px)");
	// The editor budget assumes the input does not keep its 108px desktop
	// minimum on a short viewport.
	expect(shortHeight).toMatch(
		/\.composer-input\s*{[^}]*min-block-size:\s*36px;[^}]*max-block-size:\s*72px;/,
	);
});
