import { expect, test } from "bun:test";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

test("owned foundation files introduce no second generated visual system", async () => {
	const pin = await source("foundation/tokens.css");
	expect(pin).not.toContain("tailwind");
	// The header inventories vendor role names for review; only emitted
	// declarations can become a second visual system.
	const declarations = pin.slice(pin.indexOf(":root"));
	expect(declarations).not.toContain("--tr-");
	expect(declarations).not.toContain("--space-");
	expect(declarations).not.toContain("--size-");
	expect(declarations).not.toContain("--radius-");
	expect(declarations).not.toContain("generated/");

	const mewa = await source("mewa.css");
	expect(mewa).not.toContain("tailwind");
	expect(mewa).not.toContain("styles/generated");
	expect(mewa).not.toContain("styles/palette");
	expect(mewa).not.toContain("--tr-");
	expect(mewa).not.toContain("--space-base");
	expect(mewa).not.toContain("--radius-");
});

test("pixie product tokens stay out of the owned mewa cascade", async () => {
	const indexCss = await source("index.css");
	expect(indexCss).toContain("./styles/generated/typography.css");
	expect(indexCss).toContain("./styles/generated/colors.css");
	expect(indexCss).not.toContain("mewa.css");
	expect(indexCss).not.toContain("foundation/tokens.css");

	const mewa = await source("mewa.css");
	expect(mewa).not.toContain("./styles/");
	expect(mewa).not.toContain("./index.css");
});
