import { useEffect, useRef, useState } from 'react';
import { EpisodeArtwork } from './Artwork.js';
import type { ChannelChat } from './chat.js';
import { request, type Episode } from './model.js';
import type { Personal } from './personal.js';
import { EpisodeMetadata, type Viewer } from './Viewer.js';

// Viewer tools shared by the video archive sites: links, theater mode, sharing,
// the account panel, favorites, channel chat and the library layout toggle.
// Class names match the sites' CSS; product names come from props.
export function episodeLink(collection: string, item?: string, channel = false) {
  const link = new URL(location.origin + location.pathname);
  link.searchParams.set('collection', collection);
  if (channel) link.searchParams.set('view', 'channel');
  else if (item) link.searchParams.set('episode', item);
  return link.href;
}
export function useViewerLinks(viewer: Viewer) {
  const latest = useRef(viewer);
  const [error, setError] = useState('');
  useEffect(() => { latest.current = viewer; }, [viewer]);
  useEffect(() => {
    // Each navigation cancels the previous lookup, so a slow earlier request
    // cannot replace a newer Back/Forward selection.
    let controller = new AbortController();
    const open = async () => {
      controller.abort(); controller = new AbortController();
      const current = controller;
      setError('');
      const link = new URL(location.href);
      if (link.searchParams.get('collection') && link.searchParams.get('collection') !== viewer.collection) { setError('This link belongs to a different archive.'); return; }
      if (link.searchParams.get('view') === 'channel') { await latest.current.tune(); return; }
      const item = link.searchParams.get('episode');
      if (!item) return;
      try { const value = await request<{ episode: Episode }>(`/api/episodes/${encodeURIComponent(item)}?collection=${encodeURIComponent(viewer.collection)}`, current.signal); if (current.signal.aborted) return; if (value.episode.collection_id && value.episode.collection_id !== viewer.collection) { setError('The shared episode belongs to a different archive.'); return; } await latest.current.watch(value.episode); }
      catch { if (!current.signal.aborted) setError('The shared episode is unavailable in this archive.'); }
    };
    void open(); const changed = () => { void open(); }; window.addEventListener('popstate', changed);
    return () => { controller.abort(); window.removeEventListener('popstate', changed); };
  }, [viewer.collection]);
  return error;
}
export function useTheater() {
  const [theater, setTheater] = useState(false);
  useEffect(() => { const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setTheater(false); }; window.addEventListener('keydown', escape); return () => window.removeEventListener('keydown', escape); }, []);
  return { theater, setTheater };
}
export function VideoTools({ viewer, theater, onTheater }: { viewer: Viewer; theater: boolean; onTheater: (value: boolean) => void }) {
  const [message, setMessage] = useState('');
  const [copyLink, setCopyLink] = useState('');
  const share = async (native: boolean) => {
    const link = episodeLink(viewer.collection, viewer.session?.episode.id, viewer.mode === 'channel');
    setCopyLink(link);
    try {
      if (native && navigator.share) { await navigator.share({ title: viewer.session?.episode.title || 'Scheduled replay channel', url: link }); setMessage('Share opened.'); }
      else { await navigator.clipboard.writeText(link); setMessage('Link copied.'); }
    } catch (reason) { if (!(reason instanceof DOMException && reason.name === 'AbortError')) setMessage('Select and copy the link below.'); }
  };
  const link = viewer.session ? episodeLink(viewer.collection, viewer.session.episode.id, viewer.mode === 'channel') : '';
  return <div className="viewer-tools" role="group" aria-label="Viewing and sharing controls">
    <button aria-pressed={theater} onClick={() => onTheater(!theater)}>{theater ? 'Exit theater mode' : 'Theater mode'}</button>
    {viewer.session && <><button onClick={() => { void share(true); }}>Share {viewer.mode === 'channel' ? 'channel' : 'episode'}</button><button onClick={() => { void share(false); }}>Copy link</button><a href={`https://bsky.app/intent/compose?text=${encodeURIComponent(viewer.session.episode.title + ' ' + link)}`} target="_blank" rel="noreferrer">Share on Bluesky</a><a href={`mailto:?subject=${encodeURIComponent(viewer.session.episode.title)}&body=${encodeURIComponent(link)}`}>Share by email</a></>}
    {copyLink && <label>Share link<input readOnly value={copyLink} onFocus={event => event.target.select()} /></label>}
    {message && <p role="status">{message}</p>}
  </div>;
}
function download(text: string, name: string) { const url = URL.createObjectURL(new Blob([text], { type: 'application/json' })); const link = document.createElement('a'); link.href = url; link.download = name; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); }
export function AccountPanel({ personal, product }: { personal: Personal; product: string }) {
  const [confirmImport, setConfirmImport] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [alias, setAlias] = useState('');
  const [notice, setNotice] = useState('');
  return <section className="account-panel" aria-label={`${product} personal preferences`}>
    <h2>Your archive</h2><p>{personal.user ? `Signed in to ${product} as ${personal.user.alias}.` : 'Favorites stay in this browser. Anonymous viewing is always available.'}</p>
    {personal.enabled && !personal.user && <button onClick={() => { void personal.signIn(); }}>Optional sign-in to sync favorites</button>}
    <button onClick={() => { void personal.exportData().then(text => { download(text, 'archive-personal-export.json'); setNotice('Export includes pending changes and browser-local favorites.'); }).catch(() => setNotice('Export unavailable. Your saved records are retained.')); }}>Export {personal.user ? 'personal data' : 'browser favorites'}</button>
    {personal.user && <><button onClick={() => { void personal.signOut(); }}>Sign out</button><button onClick={() => { void personal.signOut(true); }}>Sign out all devices</button>
      <details><summary>Public alias and browser import</summary><label>Public chat alias<input value={alias} placeholder={personal.user.alias} maxLength={80} onChange={event => setAlias(event.target.value)} /></label><button disabled={!alias.trim()} onClick={() => { void personal.updateAlias(alias); }}>Save alias</button>
        <p>Import {personal.localFavorites.length} browser favorites into {product}, deployment {personal.deployment}. The browser copy remains until you choose to remove it.</p><label><input type="checkbox" checked={confirmImport} onChange={event => setConfirmImport(event.target.checked)} /> I reviewed the destination and count</label><button disabled={!confirmImport || personal.busy} onClick={() => { void personal.importLocal(); }}>Prepare import</button>
      </details>
      {personal.pending.length > 0 && <div className="pending-preferences"><p>{personal.pending.length} pending {personal.pending.length === 1 ? 'change' : 'changes'} for this account.</p><ul>{personal.pending.map(p => <li key={p.item}>{p.remove ? 'Remove' : 'Add'} {p.item}</li>)}</ul><button disabled={personal.busy} onClick={() => { void personal.synchronize(true); }}>Refresh and apply these changes</button></div>}
      <details><summary>Delete this product account</summary><p>Deletion removes this product's profile, synchronized favorites and chat bodies, and revokes sessions. Browser favorites and the external provider account remain yours.</p>{personal.reauthenticationRequired && <button onClick={() => { void personal.signIn(); }}>Sign in again before deletion</button>}<label><input type="checkbox" checked={confirmDelete} onChange={event => setConfirmDelete(event.target.checked)} /> Delete my account for {product}</label><button disabled={!confirmDelete || personal.busy} onClick={() => { void personal.removeAccount(); }}>Delete product account</button></details>
    </>}
    {personal.enabled && <p className="retention-note">Chat lasts 30 days. Personal backups last 35 days. Opaque deletion records last 90 days. Archive records are retained independently.</p>}
    {personal.error && <p role="status">{personal.error}</p>}{notice && <p role="status">{notice}</p>}
  </section>;
}
export function FavoriteAction({ episode, personal }: { episode: Episode; personal: Personal }) { const saved = personal.favoriteIDs.includes(episode.id); return <button className="favorite-button" aria-pressed={saved} disabled={personal.busy} aria-label={`${saved ? 'Remove' : 'Save'} favorite ${episode.title}`} onClick={() => { void personal.toggle(episode.id); }}>{saved ? 'Saved favorite' : 'Save favorite'}</button>; }
export function FavoriteShelf({ viewer, personal, layout, artworkFallback = 'ARCHIVE' }: { viewer: Viewer; personal: Personal; layout: 'grid' | 'table'; artworkFallback?: string }) {
  const [page, setPage] = useState(1);
  const [result, setResult] = useState<{ key: string; episodes: Record<string, Episode> } | null>(null);
  const currentPage = Math.min(page, Math.max(1, Math.ceil(personal.favoriteIDs.length / 24)));
  const ids = personal.favoriteIDs.slice((currentPage - 1) * 24, currentPage * 24);
  const key = JSON.stringify([viewer.collection, ids]);
  useEffect(() => {
    const controller = new AbortController();
    const values = JSON.parse(key) as [string, string[]];
    void Promise.all(values[1].map(async id => { try { const value = await request<{ episode: Episode }>(`/api/episodes/${encodeURIComponent(id)}?collection=${encodeURIComponent(values[0])}`, controller.signal); return value.episode.collection_id && value.episode.collection_id !== values[0] ? null : value.episode; } catch { return null; } })).then(episodes => { if (!controller.signal.aborted) setResult({ key, episodes: Object.fromEntries(episodes.filter((e): e is Episode => !!e).map(e => [e.id, e])) }); });
    return () => controller.abort();
  }, [key]);
  const episodes = result?.key === key ? result.episodes : null;
  return <section className="favorite-shelf" id="library" aria-label="Saved favorites"><h2>Saved favorites</h2><p>{personal.favoriteIDs.length} saved {personal.favoriteIDs.length === 1 ? 'reference' : 'references'} in {personal.user ? 'this product account' : 'this browser'}.</p>{!episodes && <p role="status">Checking current availability…</p>}
    {layout === 'table' ? <div className="episode-table-wrap"><table className="episode-table"><caption>Saved favorite references</caption><thead><tr><th scope="col">Original date</th><th scope="col">Program</th><th scope="col">Available metadata</th><th scope="col">Actions</th></tr></thead><tbody>{ids.map(id => { const episode = episodes?.[id]; return <tr key={id}><td>{episode?.date || 'Date unknown'}</td><th scope="row">{episode?.title || id}</th><td>{episode ? <EpisodeMetadata episode={episode} /> : episodes ? 'Currently unavailable. Reference retained.' : 'Checking availability.'}</td><td>{episode && <button onClick={() => { void viewer.watch(episode); }}>Watch</button>}<button disabled={personal.busy} onClick={() => { void personal.toggle(id); }}>Remove favorite</button></td></tr>; })}</tbody></table></div> : <div className="episodes">{ids.map(id => { const episode = episodes?.[id]; return <article key={id}>{episode ? <><button className="episode-art" aria-label={`Watch ${episode.title}`} onClick={() => { void viewer.watch(episode); }}><EpisodeArtwork episode={episode} fallback={artworkFallback} /></button><h3><button className="text-button" onClick={() => { void viewer.watch(episode); }}>{episode.title}</button></h3><time dateTime={episode.date}>{episode.date || 'Date unknown'}</time><EpisodeMetadata episode={episode} /><FavoriteAction episode={episode} personal={personal} /></> : <><h3>{id}</h3><p>{episodes ? 'Currently unavailable. The favorite reference is retained.' : 'Checking availability.'}</p><button disabled={personal.busy} onClick={() => { void personal.toggle(id); }}>Remove favorite reference</button></>}</article>; })}</div>}
    {!ids.length && <p>No saved favorites on this page.</p>}<nav aria-label="Favorite pages"><button disabled={currentPage === 1} onClick={() => setPage(currentPage - 1)}>Previous favorites</button><span>Page {currentPage}</span><button disabled={currentPage * 24 >= personal.favoriteIDs.length} onClick={() => setPage(currentPage + 1)}>Next favorites</button></nav>
  </section>;
}
export function ChatPanel({ chat, personal, viewer }: { chat: ChannelChat; personal: Personal; viewer: Viewer }) {
  const [body, setBody] = useState('');
  const [reportID, setReportID] = useState('');
  const [reason, setReason] = useState('');
  const [moderation, setModeration] = useState({ action: 'hide', target: '', reason: '', hours: 24 });
  if (!chat.enabled) return null;
  return <section className="chat-panel" aria-label="Scheduled channel discussion"><header><h2>Channel discussion</h2><p>{chat.history ? 'Earlier channel messages' : 'Current channel discussion'} · Scheduled archival replay</p></header>
    {!chat.room?.read ? <p>Channel discussion is disabled.</p> : <><p>{chat.room.current ? `Current program: ${chat.room.current.episode}. Original date ${chat.room.current.original_date || 'unknown'}.` : 'No current channel program.'}</p>
      <ol className="chat-messages">{chat.messages.map((message, index) => <li key={message.id}>{(index === 0 || chat.messages[index - 1].revision !== message.revision || chat.messages[index - 1].start !== message.start) && <h3 className="chat-program">Replay program {message.episode} · Original date {message.original_date || 'unknown'} · Scheduled {new Date(message.start).toLocaleString()}</h3>}<div><strong>{message.alias}</strong> <time dateTime={message.created_at}>{new Date(message.created_at).toLocaleString()}</time></div><p>{message.body}</p>{personal.user && <div className="message-actions">{message.user_id === personal.user.id ? <button disabled={chat.busy} onClick={() => { void chat.remove(message.id); }}>Delete my message</button> : <><button onClick={() => setReportID(message.id)}>Report</button><button disabled={chat.busy} onClick={() => { void chat.block(message.user_id); }}>Block this user</button></>}{personal.user.moderator && <><button onClick={() => setModeration({ ...moderation, action: 'hide', target: message.id })}>Review withdrawal</button><button onClick={() => setModeration({ ...moderation, action: 'mute', target: message.user_id })}>Review posting restriction</button></>}</div>}</li>)}</ol>
      {!chat.messages.length && <p>No visible messages on this page.</p>}<nav aria-label="Chat history"><button disabled={!chat.hasOlder} onClick={chat.older}>Earlier messages</button><button disabled={!chat.history} onClick={chat.current}>Current discussion</button></nav>
      {reportID && <form onSubmit={event => { event.preventDefault(); void chat.report(reportID, reason).then(sent => { if (sent) { setReportID(''); setReason(''); } }); }}><label>Private report reason<textarea value={reason} maxLength={1000} onChange={event => setReason(event.target.value)} /></label><button disabled={!reason.trim() || chat.busy}>Submit report</button><button type="button" onClick={() => setReportID('')}>Cancel report</button></form>}
      {!personal.user ? <p>{personal.enabled ? <button onClick={() => { void personal.signIn(); }}>Sign in to post</button> : 'Posting requires this product’s optional sign-in service.'}</p> : <>{!chat.canPost && <p>Posting follows the current channel program. {viewer.mode !== 'channel' || viewer.paused ? <button onClick={() => { void viewer.tune(); }}>Rejoin channel to post</button> : 'Wait for the current context to refresh.'}</p>}<form onSubmit={event => { event.preventDefault(); void chat.send(body).then(sent => { if (sent) setBody(''); }); }}><label>Message for the current channel<textarea disabled={!chat.canPost} value={body} maxLength={1000} onChange={event => setBody(event.target.value)} /></label><p>{[...body].length}/1,000 characters · Plain text only</p><button disabled={!chat.canPost || !body.trim() || chat.busy}>Send message</button></form></>}
    </>}
    {chat.blocked.length > 0 && <details><summary>Blocked users</summary>{chat.blocked.map(id => <button key={id} disabled={chat.busy} onClick={() => { void chat.block(id, true); }}>Unblock {id.slice(0, 8)}</button>)}</details>}
    {personal.user?.moderator && <details className="moderation-panel"><summary>Moderator controls</summary><button onClick={() => { void chat.reviewReports(); }}>Load private reports</button>{chat.reports.map(report => <article key={report.id}><p>{report.body}</p><p>Private reason: {report.reason}</p><button onClick={() => setModeration({ ...moderation, action: report.resolved ? 'reopen' : 'resolve', target: report.id })}>{report.resolved ? 'Review reopening' : 'Review resolution'}</button></article>)}<form onSubmit={event => { event.preventDefault(); void chat.moderate(moderation.action, moderation.target, moderation.reason, moderation.hours); }}><label>Action<select value={moderation.action} onChange={event => setModeration({ ...moderation, action: event.target.value })}>{['hide', 'mute', 'ban', 'resolve', 'reopen'].map(action => <option key={action}>{action}</option>)}</select></label><label>Opaque target ID<input value={moderation.target} onChange={event => setModeration({ ...moderation, target: event.target.value })} /></label><label>Private audit reason<textarea value={moderation.reason} maxLength={1000} onChange={event => setModeration({ ...moderation, reason: event.target.value })} /></label>{['mute', 'ban'].includes(moderation.action) && <label>Restriction hours<input type="number" min="1" max="720" value={moderation.hours} onChange={event => setModeration({ ...moderation, hours: Number(event.target.value) })} /></label>}<button disabled={chat.busy || !moderation.reason.trim() || !moderation.target}>Apply reviewed action</button></form></details>}
    <p className="retention-note">Messages and resolved report evidence expire after 30 days. Deleted bodies leave the serving store immediately. {chat.room?.moderation_contact && <>Moderation contact: {chat.room.moderation_contact}</>}</p>{chat.error && <p role="status">{chat.error} Your draft is retained.</p>}
  </section>;
}

function readLibraryLayout(key: string): 'grid' | 'table' {
  try { return localStorage.getItem(key) === 'table' ? 'table' : 'grid'; }
  catch { return 'grid'; }
}
export function useLibraryLayout(collection: string) {
  const key = `archive-library-layout:v1:${collection}`;
  const [layout, update] = useState<'grid' | 'table'>(() => readLibraryLayout(key));
  useEffect(() => { update(readLibraryLayout(key)); }, [key]);
  const setLayout = (next: 'grid' | 'table') => {
    update(next);
    try { localStorage.setItem(key, next); } catch { /* Keep the current view in memory. */ }
  };
  return [layout, setLayout] as const;
}
