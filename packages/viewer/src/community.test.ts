import { afterEach, expect, test, vi } from 'vitest';
import { communityFeatures, resetCommunityFeatures } from './community.js';

afterEach(() => { resetCommunityFeatures(); vi.unstubAllGlobals(); });
const answer = (body: unknown, ok = true) => vi.fn().mockResolvedValue({ ok, json: async () => body });

test('reads whether accounts and chat run from the version response', async () => {
  const fetch = answer({ api_version: '1', community: { personal: true, chat: false } });
  vi.stubGlobal('fetch', fetch);
  expect(await communityFeatures()).toEqual({ personal: true, chat: false });
  await communityFeatures();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0][0]).toBe('/api/version');
});

test('older engines, errors and malformed answers leave both unknown so callers keep probing', async () => {
  for (const fetch of [answer({ api_version: '1' }), answer({}, false), vi.fn().mockRejectedValue(new Error('offline')), answer({ community: { personal: 'yes', chat: null } })]) {
    resetCommunityFeatures();
    vi.stubGlobal('fetch', fetch);
    const value = await communityFeatures();
    expect(value.personal).toBeUndefined();
    expect(value.chat).toBeUndefined();
  }
});
