import { resolve } from "node:path";
import { loadMewaLock, verifyVendoredMewa } from "./mewa-integrity";

const vendorRoot = resolve(import.meta.dir, "..", "vendor");
const lock = await loadMewaLock(resolve(vendorRoot, "mewa.lock.json"));
await verifyVendoredMewa(vendorRoot, lock);

console.log(
	`mewa:check: OK (${lock.version}, ${lock.assets.length} packages, ${lock.icons.length} icons)`,
);
