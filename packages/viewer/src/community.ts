export type CommunityFeatures = { personal?: boolean; chat?: boolean };

let features: Promise<CommunityFeatures> | null = null;

/**
 * Whether this engine runs accounts and chat, from `/api/version`. Engines
 * before core 0.4.9 do not say, so the result is empty and callers keep probing
 * the account and chat routes, treating a 404 as "not enabled".
 */
export function communityFeatures(): Promise<CommunityFeatures> {
  features ??= fetch('/api/version', { cache: 'no-store', credentials: 'same-origin', signal: AbortSignal.timeout(10_000) })
    .then(response => response.ok ? response.json() as Promise<{ community?: CommunityFeatures }> : {})
    .then(value => {
      const community = (value as { community?: unknown }).community;
      if (typeof community !== 'object' || community === null) return {};
      const { personal, chat } = community as Record<string, unknown>;
      return { personal: typeof personal === 'boolean' ? personal : undefined, chat: typeof chat === 'boolean' ? chat : undefined };
    })
    .catch(() => ({}));
  return features;
}

/** Forget the cached answer; for tests. */
export function resetCommunityFeatures() { features = null; }
