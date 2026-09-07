import { behavior as behavior0 } from "../controllers/file-upload.js";
import { acquireBehavior } from "../runtime/core.js";

export const behaviors = Object.freeze([behavior0]);
const leasesByRoot = new WeakMap();

export function enhance(root, options) {
  const scope = root || (typeof document === "undefined" ? null : document);
  if (!scope) return [];
  let leases = leasesByRoot.get(scope);
  if (leases) {
    for (const lease of leases) lease.update(options);
  } else {
    leases = [];
    try {
      for (const entry of behaviors) leases.push(acquireBehavior(entry, scope, options));
    } catch (error) {
      const errors = [error];
      for (const lease of leases.reverse()) {
        try { lease.destroy(); } catch (cleanupError) { errors.push(cleanupError); }
      }
      if (errors.length > 1) throw new AggregateError(errors, 'Component setup failed');
      throw error;
    }
    leasesByRoot.set(scope, leases);
  }
  return leases.map((lease) => lease.state);
}

export function destroy(root) {
  const scope = root || (typeof document === "undefined" ? null : document);
  if (!scope) return;
  const leases = leasesByRoot.get(scope) || [];
  leasesByRoot.delete(scope);
  const errors = [];
  for (const lease of leases.reverse()) {
    try { lease.destroy(); } catch (error) { errors.push(error); }
  }
  if (errors.length) throw new AggregateError(errors, 'Component cleanup failed');
}

export const behavior = { name: "file-upload", enhance, destroy };
