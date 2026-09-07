import { expect, test } from "bun:test";
import { fitWithin } from "@/chat/composer/image-attachment";

test("bounds images without distortion or zero dimensions", () => {
	for (const [width, height, expected] of [
		[800, 600, { width: 800, height: 600 }],
		[1568, 1568, { width: 1568, height: 1568 }],
		[3136, 1568, { width: 1568, height: 784 }],
		[4000, 3000, { width: 1568, height: 1176 }],
		[1568, 3136, { width: 784, height: 1568 }],
		[3000, 4000, { width: 1176, height: 1568 }],
		[3023, 1701, { width: 1568, height: 882 }],
		[100_000, 10, { width: 1568, height: 1 }],
	] as const) {
		expect(fitWithin(width, height, 1568)).toEqual(expected);
	}
});
