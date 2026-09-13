import type { Candidate } from "@/lib/api";

const actionableAccessStates = new Set([
  "needs_credentials",
  "invalid_credentials",
  "needs_host_trust",
  "needs_privilege",
]);

export function isActionableAccessCandidate(candidate: Candidate): boolean {
  return actionableAccessStates.has(candidate.state) && (candidate.entryPointCount ?? 0) > 0 && !candidate.excluded;
}

export function uniqueAccessCandidates(candidates: Candidate[]): Candidate[] {
  const seen = new Set<string>();
  return candidates.filter((candidate) => {
    const identity = `${candidate.siteId}:${candidate.address}`;
    if (seen.has(identity)) return false;
    seen.add(identity);
    return true;
  });
}
