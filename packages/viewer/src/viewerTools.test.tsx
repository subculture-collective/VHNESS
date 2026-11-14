import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { AccountPanel, ChatPanel, FavoriteAction, FavoriteShelf, VideoTools, episodeLink, useLibraryLayout, type ChannelChat, type Episode, type Personal, type Session, type Viewer } from './index.js';

const episode: Episode = { id: 'ep 1', title: 'Rock & roll', description: '', date: '1994-01-02', synthetic: true };
const session: Session = { mode: 'ondemand', episode, playback: { id: 'ep 1', policy: 'local_preserve', url: '/api/media/ep', source_url: 'https://archive.org/details/ep', snapshot_sha256: 'a'.repeat(64), file: { name: 'ep.mp4', duration: 60, width: 640, height: 480, bytes: 100 } }, offset: 0, receivedAt: 0, latency: 0, generation: 1 };

function viewer(overrides: Partial<Viewer> = {}): Viewer {
  return {
    catalog: { episodes: [], total: 0, page: 1 }, query: '', setQuery: () => {},
    collection: 'fixture', setCollection: () => {}, collections: [], year: '',
    setYear: () => {}, page: 1, setPage: () => {}, error: '', playError: '',
    resolving: false, channel: { state: 'unavailable', reason: 'Schedule exhausted',
      revision: '', offset: 0, server_time: '' }, session: null, paused: false,
    mode: 'ondemand', watch: async () => {}, tune: async () => {}, key: '', onPaused: () => {},
    filters: {}, setFilters: () => {}, playbackState: 'unavailable', onPlaybackState: () => {},
    ...overrides,
  };
}
function personal(overrides: Partial<Personal> = {}): Personal {
  return {
    enabled: false, user: null, deployment: 'deployment-1', csrf: '', reauthenticationRequired: false,
    chatRead: false, chatPost: false, localFavorites: [], favorites: [], favoriteIDs: [], pending: [],
    error: '', busy: false, refresh: async () => {}, toggle: async () => {}, synchronize: async () => {},
    importLocal: async () => {}, signIn: async () => {}, signOut: async () => {}, removeAccount: async () => {},
    updateAlias: async () => {}, exportData: async () => '{}',
    ...overrides,
  };
}
function chat(overrides: Partial<ChannelChat> = {}): ChannelChat {
  return {
    room: null, messages: [], reports: [], blocked: [], enabled: true, error: '', busy: false, canPost: false,
    history: false, hasOlder: false, older: () => {}, current: () => {}, refresh: async () => {},
    send: async () => true, report: async () => true, remove: async () => true, block: async () => true,
    reviewReports: async () => true, moderate: async () => true,
    ...overrides,
  };
}
const user = { id: 'user-1', alias: 'Viewer', revision: 1, moderator: false };

beforeEach(() => { vi.stubGlobal('location', { origin: 'https://tv.test', pathname: '/watch/', href: 'https://tv.test/watch/' }); });
afterEach(() => vi.unstubAllGlobals());

describe('episodeLink', () => {
  it('keeps the page path and selects an episode or the channel', () => {
    expect(episodeLink('fixture', 'ep 1')).toBe('https://tv.test/watch/?collection=fixture&episode=ep+1');
    expect(episodeLink('fixture', 'ep 1', true)).toBe('https://tv.test/watch/?collection=fixture&view=channel');
    expect(episodeLink('fixture')).toBe('https://tv.test/watch/?collection=fixture');
  });
});

describe('VideoTools', () => {
  it('offers theater mode and only shows sharing once something is playing', () => {
    const idle = renderToStaticMarkup(<VideoTools viewer={viewer()} theater={false} onTheater={() => {}} />);
    expect(idle).toContain('class="viewer-tools"');
    expect(idle).toContain('aria-pressed="false"');
    expect(idle).toContain('Theater mode');
    expect(idle).not.toContain('Copy link');
    const playing = renderToStaticMarkup(<VideoTools viewer={viewer({ session })} theater onTheater={() => {}} />);
    expect(playing).toContain('Exit theater mode');
    expect(playing).toContain('Share episode');
    const link = 'https://tv.test/watch/?collection=fixture&episode=ep+1';
    expect(playing).toContain(`https://bsky.app/intent/compose?text=${encodeURIComponent(`Rock & roll ${link}`).replace(/&/g, '&amp;')}`);
    expect(playing).toContain(`mailto:?subject=${encodeURIComponent('Rock & roll').replace(/&/g, '&amp;')}&amp;body=${encodeURIComponent(link)}`);
    expect(renderToStaticMarkup(<VideoTools viewer={viewer({ session: { ...session, mode: 'channel' }, mode: 'channel' })} theater={false} onTheater={() => {}} />)).toContain('Share channel');
  });
});

describe('AccountPanel', () => {
  it('names the product and keeps anonymous viewing explicit', () => {
    const anonymous = renderToStaticMarkup(<AccountPanel personal={personal()} product="Fixture TV" />);
    expect(anonymous).toContain('class="account-panel"');
    expect(anonymous).toContain('aria-label="Fixture TV personal preferences"');
    expect(anonymous).toContain('Favorites stay in this browser.');
    expect(anonymous).toContain('Export browser favorites');
    expect(anonymous).not.toContain('Optional sign-in');
    expect(anonymous).not.toContain('retention-note');
    expect(renderToStaticMarkup(<AccountPanel personal={personal({ enabled: true })} product="Fixture TV" />)).toContain('Optional sign-in to sync favorites');
  });
  it('shows account controls and pending changes when signed in', () => {
    const html = renderToStaticMarkup(<AccountPanel personal={personal({ enabled: true, user, localFavorites: ['a', 'b'], pending: [{ item: 'a', remove: false, baseRevision: 1 }] })} product="Fixture TV" />);
    expect(html).toContain('Signed in to Fixture TV as Viewer.');
    expect(html).toContain('Import 2 browser favorites into Fixture TV, deployment deployment-1.');
    expect(html).toContain('class="pending-preferences"');
    expect(html).toContain('1 pending change for this account.');
    expect(html).toContain('Delete my account for Fixture TV');
    expect(html).toContain('class="retention-note"');
  });
});

describe('favorites', () => {
  it('labels the favorite toggle by its state', () => {
    expect(renderToStaticMarkup(<FavoriteAction episode={episode} personal={personal()} />)).toContain('aria-label="Save favorite Rock &amp; roll"');
    const saved = renderToStaticMarkup(<FavoriteAction episode={episode} personal={personal({ favoriteIDs: ['ep 1'] })} />);
    expect(saved).toContain('class="favorite-button"');
    expect(saved).toContain('aria-pressed="true"');
    expect(saved).toContain('Saved favorite');
  });
  it('renders the shelf in both layouts while it checks availability', () => {
    const p = personal({ favoriteIDs: ['ep 1', 'ep 2'] });
    const grid = renderToStaticMarkup(<FavoriteShelf viewer={viewer()} personal={p} layout="grid" />);
    expect(grid).toContain('class="favorite-shelf" id="library"');
    expect(grid).toContain('2 saved references in this browser.');
    expect(grid).toContain('Checking current availability…');
    expect(grid).toContain('class="episodes"');
    expect(grid).toContain('<h3>ep 2</h3>');
    const table = renderToStaticMarkup(<FavoriteShelf viewer={viewer()} personal={p} layout="table" />);
    expect(table).toContain('class="episode-table"');
    expect(table).toContain('<th scope="row">ep 1</th>');
  });
  it('pages favorites 24 at a time', () => {
    const ids = Array.from({ length: 25 }, (_, index) => `item-${index}`);
    const html = renderToStaticMarkup(<FavoriteShelf viewer={viewer()} personal={personal({ favoriteIDs: ids })} layout="table" />);
    expect(html).toContain('item-23');
    expect(html).not.toContain('item-24');
    expect(html).toContain('Page 1');
  });
});

describe('ChatPanel', () => {
  it('renders nothing when chat is off and says when the room is closed', () => {
    expect(renderToStaticMarkup(<ChatPanel chat={chat({ enabled: false })} personal={personal()} viewer={viewer()} />)).toBe('');
    expect(renderToStaticMarkup(<ChatPanel chat={chat()} personal={personal()} viewer={viewer()} />)).toContain('Channel discussion is disabled.');
  });
  it('groups messages by program and shows moderator controls to moderators', () => {
    const program = { revision: 'r1', start: '2026-01-01T00:00:00Z', episode: 'ep 1', original_date: '1994-01-02' };
    const room = { collection: 'fixture', current: program, read: true, post: true, retention_days: 30, poll_seconds: 10, moderation_contact: 'mod@example.test' };
    const message = { ...program, seq: 1, id: 'm1', user_id: 'other', alias: 'Someone', body: 'Hello', collection: 'fixture', created_at: '2026-01-01T00:01:00Z' };
    const html = renderToStaticMarkup(<ChatPanel chat={chat({ room, messages: [message, { ...message, seq: 2, id: 'm2' }] })} personal={personal({ enabled: true, user: { ...user, moderator: true } })} viewer={viewer()} />);
    expect(html).toContain('class="chat-panel"');
    expect(html.match(/class="chat-program"/g)).toHaveLength(1);
    expect(html).toContain('Block this user');
    expect(html).toContain('Rejoin channel to post');
    expect(html).toContain('class="moderation-panel"');
    expect(html).toContain('Moderation contact: mod@example.test');
    const anonymous = renderToStaticMarkup(<ChatPanel chat={chat({ room, messages: [message] })} personal={personal({ enabled: true })} viewer={viewer()} />);
    expect(anonymous).toContain('Sign in to post');
    expect(anonymous).not.toContain('moderation-panel');
  });
});

describe('useLibraryLayout', () => {
  function Layout({ collection }: { collection: string }) { const [layout] = useLibraryLayout(collection); return <span>{layout}</span>; }
  it('reads the saved layout per collection and falls back to the grid', () => {
    const getItem = vi.fn((key: string) => key === 'archive-library-layout:v1:fixture' ? 'table' : null);
    vi.stubGlobal('localStorage', { getItem });
    expect(renderToStaticMarkup(<Layout collection="fixture" />)).toBe('<span>table</span>');
    expect(renderToStaticMarkup(<Layout collection="other" />)).toBe('<span>grid</span>');
    vi.stubGlobal('localStorage', { getItem: () => { throw new Error('blocked'); } });
    expect(renderToStaticMarkup(<Layout collection="fixture" />)).toBe('<span>grid</span>');
  });
});
