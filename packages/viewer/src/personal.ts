import { useCallback, useEffect, useRef, useState } from 'react';
import { PersonalBrowserStorage } from './personal-storage.js';
import { communityFeatures } from './community.js';

export type PersonalUser = { id: string; alias: string; revision: number; moderator: boolean; audio_moderator?: boolean };
export type Favorite = { collection: string; item: string; removed: boolean; revision: number; available: boolean };
export type PendingFavorite = { item: string; remove: boolean; baseRevision: number };
type BrowserSession = { authenticated: boolean; csrf?: string; deployment: string; reauthentication_required?: boolean };
type Profile = { user: PersonalUser; deployment: string; chat_read: boolean; chat_post: boolean };
export class PersonalError extends Error { constructor(message: string, public status: number) { super(message); } }
export async function personalRequest<T>(path: string, csrf = '', method = 'GET', body?: unknown, revision?: number): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (csrf) headers['X-CSRF-Token'] = csrf;
  if (revision !== undefined) headers['If-Match'] = String(revision);
  const response = await fetch(path, { method, headers, credentials: 'same-origin', cache: 'no-store', signal: AbortSignal.timeout(10_000), body: body === undefined ? undefined : JSON.stringify(body) });
  if (!response.headers.get('Content-Type')?.toLowerCase().startsWith('application/json')) throw new PersonalError(response.status === 404 ? 'Personal service is not enabled.' : 'Personal service unavailable.', response.status);
  let value: T & { error?: string };
  try { value = await response.json() as T & { error?: string }; } catch { throw new PersonalError('Personal service unavailable.', response.status); }
  if (!response.ok) throw new PersonalError(value.error || 'Personal service unavailable.', response.status);
  return value;
}
const browserStorage = new PersonalBrowserStorage(() => localStorage);
const safeID = (id: unknown): id is string => typeof id === 'string' && id.length > 0 && id.length <= 512 && !/[/\\\0]/.test(id);
export function readLocalFavorites(storage: Pick<Storage, 'getItem'>, key: string, fallback: string[] = []): string[] {
  try { const value: unknown = JSON.parse(storage.getItem(key) || '[]'); if (!Array.isArray(value)) return fallback; return [...new Set(value.filter(safeID))].slice(0, 10_000); } catch { return fallback; }
}
const readStored = <T,>(key: string, fallback: T): T => browserStorage.read(key, fallback);
const store = (key: string, value: unknown) => browserStorage.write(key, value);
const errorMessage = (reason: unknown) => reason instanceof Error ? reason.message : 'Personal service unavailable.';

export function usePersonal(collection: string) {
  const localKey = `archive.favorites.v1:${collection}`;
  const [local, setLocal] = useState<string[]>([]);
  const [session, setSession] = useState<BrowserSession | null>(null);
  const [profile, setProfile] = useState<Profile | null>(null);
  const [favorites, setFavorites] = useState<Favorite[]>([]);
  const [revision, setRevision] = useState(0);
  const [pending, setPending] = useState<PendingFavorite[]>([]);
  const [error, setError] = useState('');
  const [storageWarning, setStorageWarning] = useState('');
  const [busy, setBusy] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [loadedCollection,setLoadedCollection]=useState(collection);
  const scopeMatches=loadedCollection===collection;
  useEffect(()=>{setLoadedCollection(collection);setProfile(null);setFavorites([]);setPending([]);setLocal([]);setError('');},[collection]);
  const epoch = useRef(0);
  const actionInFlight = useRef(false);
  const pendingKey = profile ? `archive.favorite.pending.v1:${profile.deployment}:${profile.user.id}:${collection}` : '';
  const warnStorage = () => setStorageWarning(browserStorage.temporary(localKey) || (pendingKey && browserStorage.temporary(pendingKey)) ? 'Browser storage is unavailable. Changes are kept only on this page; export them before leaving.' : '');
  const identity = `${collection}:${profile?.deployment || ''}:${profile?.user.id || ''}`;
  const activeIdentity = useRef(identity);
  useEffect(() => { activeIdentity.current = identity; }, [identity]);
  useEffect(() => {
    const read = () => { const values = readStored<unknown>(localKey, []); setLocal(Array.isArray(values) ? [...new Set(values.filter(safeID))].slice(0, 10_000) : []); setStorageWarning(browserStorage.temporary(localKey) ? 'Browser storage is unavailable. Changes are kept only on this page; export them before leaving.' : ''); };
    read(); const changed = (event: StorageEvent) => { if (event.key === localKey) { browserStorage.changed(localKey); read(); } };
    window.addEventListener('storage', changed); return () => window.removeEventListener('storage', changed);
  }, [localKey]);
  const refresh = useCallback(async () => {
    const token = ++epoch.current;
    try {
      if ((await communityFeatures()).personal === false) {
        if (token === epoch.current) { setEnabled(false); setSession(null); setProfile(null); setError(''); }
        return;
      }
      const next = await personalRequest<BrowserSession>('/api/account/v1/session');
      if (token !== epoch.current) return;
      setEnabled(true); setSession(next);
      if (!next.authenticated) { setProfile(null); setFavorites([]); setPending([]); return; }
      const [user, list] = await Promise.all([personalRequest<Profile>('/api/personal/v1/me'), personalRequest<{ favorites: Favorite[]; revision: number }>('/api/personal/v1/favorites')]);
      if (token !== epoch.current) return;
      if (user.deployment !== next.deployment) throw new Error('Account deployment mismatch.');
      setProfile(user); setFavorites(list.favorites); setRevision(list.revision); setError('');
      const key = `archive.favorite.pending.v1:${user.deployment}:${user.user.id}:${collection}`;
      const saved = readStored<unknown>(key, []);
      setStorageWarning(browserStorage.temporary(localKey) || browserStorage.temporary(key) ? 'Browser storage is unavailable. Changes are kept only on this page; export them before leaving.' : '');
      setPending(Array.isArray(saved) ? saved.filter((v): v is PendingFavorite => { if (typeof v !== 'object' || v === null) return false; const value = v as Partial<PendingFavorite>; return safeID(value.item) && typeof value.remove === 'boolean' && Number.isSafeInteger(value.baseRevision) && value.baseRevision! >= 0; }).slice(0, 10_000) : []);
    } catch (reason) { if (token === epoch.current) { if (reason instanceof PersonalError && reason.status === 404) { setEnabled(false); setSession(null); setProfile(null); setError(''); } else setError(errorMessage(reason)); } }
  }, [collection]);
  useEffect(() => { void refresh(); return () => { epoch.current++; }; }, [collection, refresh]);
  const savePending = (values: PendingFavorite[]) => { setPending(values); if (pendingKey) store(pendingKey, values); warnStorage(); };
  const applyOne = async (change: PendingFavorite, expectedIdentity: string) => {
    if (activeIdentity.current !== expectedIdentity || !profile || !session?.csrf) throw new Error('Sign in again before synchronizing.');
    const value = await personalRequest<{ revision: number; removed: boolean }>(`/api/personal/v1/favorites/${encodeURIComponent(collection)}/${encodeURIComponent(change.item)}`, session.csrf, change.remove ? 'DELETE' : 'PUT', undefined, change.baseRevision);
    if (activeIdentity.current !== expectedIdentity) throw new Error('Account changed during synchronization.');
    setRevision(value.revision); setFavorites(previous => [...previous.filter(f => f.collection !== collection || f.item !== change.item), { collection, item: change.item, removed: value.removed, revision: value.revision, available: true }]);
    return value.revision;
  };
  const toggle = async (item: string) => {
    if (!scopeMatches || !safeID(item) || actionInFlight.current) return;
    if (!profile) { const values = local.includes(item) ? local.filter(id => id !== item) : [...local, item]; store(localKey, values); setLocal(values); warnStorage(); return; }
    const existing = pending.find(p => p.item === item);
    const saved = favorites.some(f => f.collection === collection && f.item === item && !f.removed);
    const change = { item, remove: existing ? !existing.remove : saved, baseRevision: revision };
    const queue = [...pending.filter(p => p.item !== item), change]; savePending(queue);
    actionInFlight.current = true; setBusy(true);
    try { await applyOne(change, identity); savePending(queue.filter(p => p.item !== item)); setError(''); }
    catch (reason) { setError(`${errorMessage(reason)} Your pending change is retained.`); }
    finally { actionInFlight.current = false; setBusy(false); }
  };
  const synchronize = async (rebase = false) => {
    if (!scopeMatches || !profile || actionInFlight.current || !pending.length) return;
    actionInFlight.current = true; setBusy(true); let queue = [...pending];
    try {
      let current = revision;
      if (rebase) { const list = await personalRequest<{ favorites: Favorite[]; revision: number }>('/api/personal/v1/favorites'); current = list.revision; setFavorites(list.favorites); setRevision(current); }
      for (const change of pending) { current = await applyOne({ ...change, baseRevision: rebase ? current : change.baseRevision }, identity); queue = queue.filter(p => p.item !== change.item); savePending(queue); }
      setError('');
    } catch (reason) { setError(`${errorMessage(reason)} Review pending changes before retrying.`); }
    finally { actionInFlight.current = false; setBusy(false); }
  };
  const importLocal = async () => { if (!scopeMatches || !profile || busy) return; const queue = [...pending]; for (const item of local) if (!queue.some(p => p.item === item) && !favorites.some(f => f.collection === collection && f.item === item && !f.removed)) queue.push({ item, remove: false, baseRevision: revision }); savePending(queue); setError('Import prepared. Review and apply pending changes. The browser copy is retained.'); };
  const signIn = async () => { try { const result = await personalRequest<{ url: string }>('/api/account/v1/login', '', 'POST'); location.assign(result.url); } catch (reason) { setError(errorMessage(reason)); } };
  const signOut = async (all = false) => { if (!session?.csrf) return; try { await personalRequest('/api/account/v1/'+(all ? 'logout-all' : 'logout'), session.csrf, 'POST'); await refresh(); } catch (reason) { setError(errorMessage(reason)); } };
  const removeAccount = async () => { if (!session?.csrf) return; try { await personalRequest('/api/personal/v1/me', session.csrf, 'DELETE'); if (pendingKey) store(pendingKey, []); await refresh(); } catch (reason) { setError(errorMessage(reason)); } };
  const updateAlias = async (alias: string) => { if (!session?.csrf) return; try { await personalRequest('/api/personal/v1/me', session.csrf, 'PATCH', { alias }); await refresh(); } catch (reason) { setError(errorMessage(reason)); } };
  const exportData = async () => { const account = profile ? await personalRequest<unknown>('/api/personal/v1/export') : null; return JSON.stringify({ version: 1, collection, account, browser_local_favorites: local, pending_changes: pending }, null, 2); };
  const selected = new Set(profile ? favorites.filter(f => f.collection === collection && !f.removed).map(f => f.item) : local);
  for (const change of pending) { if (change.remove) selected.delete(change.item); else selected.add(change.item); }
  return { enabled, user: scopeMatches ? profile?.user || null : null, deployment: profile?.deployment || session?.deployment || '', csrf: session?.csrf || '', reauthenticationRequired: session?.reauthentication_required || false, chatRead: profile?.chat_read || false, chatPost: profile?.chat_post || false, localFavorites: scopeMatches ? local : [], favorites: scopeMatches ? favorites : [], favoriteIDs: scopeMatches ? [...selected] : [], pending: scopeMatches ? pending : [], error: [error, storageWarning].filter(Boolean).join(' '), busy, refresh, toggle, synchronize, importLocal, signIn, signOut, removeAccount, updateAlias, exportData };
}
export type Personal = ReturnType<typeof usePersonal>;
