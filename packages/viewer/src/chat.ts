import { useCallback, useEffect, useRef, useState } from 'react';
import { PersonalError, personalRequest, type Personal } from './personal.js';
import { communityFeatures } from './community.js';
import type { Session } from './model.js';
import type { StationSession } from './useAudioStation.js';

export type ChatMedia = 'video' | 'audio';
export type ChatCursor = number | string;
export type ChatProgram = { media?: ChatMedia; item?: string; representation?: string; revision: string; start: string; episode: string; original_date: string };
export type ChatMessage = ChatProgram & { seq: number; id: string; user_id: string; alias: string; body: string; collection: string; created_at: string };
export type ChatRoom = { media?: ChatMedia; collection: string; current: ChatProgram | null; read: boolean; post: boolean; retention_days: number; poll_seconds: number; moderation_contact: string };
export type ChatReport = { id: string; message_id: string; reason: string; body: string; collection: string; resolved: boolean; created_at: string };
const reasonMessage = (reason: unknown) => reason instanceof Error ? reason.message : 'Chat unavailable.';

export function useChannelChat(collection: string, session: Session | null, paused: boolean, personal: Personal) {
 return useReplayChat(collection, session ? {mode:session.mode, programKey:session.programKey, episode:session.episode.id} : null, paused, personal, 'video');
}
export function useAudioChannelChat(collection: string, session: StationSession | null, paused: boolean, personal: Personal) {
 const program = session?.program;
 return useReplayChat(collection, program ? {mode:'channel',programKey:`${program.revision}:${program.start}`,item:program.item,representation:program.representation} : null, paused, personal, 'audio');
}
export type ChatContext = {mode:string;programKey?:string;episode?:string;item?:string;representation?:string};
export function chatContextMatches(room: ChatRoom | null, session: ChatContext | null, paused: boolean, media: ChatMedia): boolean {
 return !!room?.post && (room.media || 'video') === media && !!room.current && session?.mode === 'channel' && !paused && (media === 'video' ? session.episode === room.current.episode : session.item === room.current.item && session.representation === room.current.representation && room.current.media === 'audio') && session.programKey === `${room.current.revision}:${room.current.start}`;
}
function useReplayChat(collection: string, session: ChatContext | null, paused: boolean, personal: Personal, media: ChatMedia) {
 const mediaQuery = media === 'audio' ? '?media=audio' : '';
 const roomURL = `/api/chat/v1/rooms/${encodeURIComponent(collection)}`;
 const messagesURL = `${roomURL}/messages${mediaQuery}`;
 const moderationURL = `/api/chat/v1/moderation${mediaQuery}`;
  const [room, setRoom] = useState<ChatRoom | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [reports, setReports] = useState<ChatReport[]>([]);
  const [blocked, setBlocked] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [before, setBefore] = useState<ChatCursor>(0);
  const [oldest, setOldest] = useState<ChatCursor>(0);
  const [enabled, setEnabled] = useState(false);
  const generation = useRef(0);
  const actionInFlight = useRef(false);
  const retry = useRef<{ body: string; key: string; context: ChatProgram } | null>(null);
  const refresh = useCallback(async () => {
    const token = generation.current;
    const context = await personalRequest<ChatRoom>(`${roomURL}${mediaQuery}`);
    if (generation.current !== token) return;
    if(context.collection !== collection || (context.media || 'video') !== media) throw new PersonalError('Discussion room mismatch.',409);
    setEnabled(true); setRoom(context);
    if (!context.read) { setMessages([]); return; }
    const page = await personalRequest<{ messages: ChatMessage[]; before: ChatCursor }>(`${messagesURL}${before ? `${mediaQuery ? '&' : '?'}before=${encodeURIComponent(before)}` : ''}`);
    if (generation.current !== token) return;
    if(page.messages.some(message=>message.collection !== collection || (message.media || 'video') !== media)) throw new PersonalError('Discussion message scope mismatch.',409);
    setMessages(page.messages); setOldest(page.before); setError('');
  }, [roomURL, messagesURL, mediaQuery, before, collection, media]);
  useEffect(()=>{setBefore(0);setOldest(0);},[collection,media]);
  useEffect(() => {
    generation.current++; retry.current = null;
    setReports([]); setBlocked([]); setRoom(null); setMessages([]);
    const token = generation.current;
    if (personal.user) void personalRequest<{ blocked: string[] }>('/api/chat/v1/blocks').then(result => { if (generation.current === token) setBlocked(result.blocked); }).catch(reason => { if (generation.current === token) setError(reasonMessage(reason)); });
    let stopped = false, inFlight = false, failures = 0, disabled = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      if (stopped || inFlight) return; inFlight = true;
      try {
        // An engine that says chat is off is not polled at all.
        if ((await communityFeatures()).chat === false) { disabled = true; if (!stopped) { setEnabled(false); setRoom(null); setMessages([]); } return; }
        await refresh(); failures = 0;
      }
      catch (reason) { if (!stopped) { if (reason instanceof PersonalError && reason.status === 404) { setEnabled(false); setRoom(null); setMessages([]); } else setError(reasonMessage(reason)); failures++; } }
      finally { inFlight = false; if (!stopped && !disabled) timer = setTimeout(() => { void poll(); }, Math.min(60_000, 10_000 * 2 ** Math.min(failures, 3))); }
    };
    void poll(); return () => { stopped = true; generation.current++; clearTimeout(timer); };
  }, [refresh, personal.user?.id]);
  const canPost = !!personal.user && room?.collection === collection && chatContextMatches(room,session,paused,media);
  const action = async (callback: () => Promise<void>) => { if (actionInFlight.current) return false; actionInFlight.current = true; setBusy(true); try { await callback(); setError(''); return true; } catch (reason) { setError(reasonMessage(reason)); return false; } finally { actionInFlight.current = false; setBusy(false); } };
  const send = async (body: string) => {
    if (!canPost || !room?.current) { setError('Join the current channel and sign in before posting.'); return false; }
    let sent = false;
    await action(async () => {
      const context = room.current!;
      const previous = retry.current;
      const change = previous && previous.body === body && previous.context.revision === context.revision && previous.context.start === context.start ? previous : { body, key: crypto.randomUUID(), context };
      retry.current = change;
      await personalRequest(messagesURL, personal.csrf, 'POST', { body, idempotency_key: change.key, ...change.context, mode: session!.mode, paused });
      retry.current = null; sent = true; setBefore(0); await refresh();
    });
    return sent;
  };
  const report = (id: string, reason: string) => action(async () => { await personalRequest(`/api/chat/v1/messages/${encodeURIComponent(id)}/reports`, personal.csrf, 'POST', { reason }); });
  const remove = (id: string) => action(async () => { await personalRequest(`/api/chat/v1/messages/${encodeURIComponent(id)}`, personal.csrf, 'DELETE'); await refresh(); });
  const block = (id: string, remove = false) => action(async () => { await personalRequest(`/api/chat/v1/blocks/${encodeURIComponent(id)}`, personal.csrf, remove ? 'DELETE' : 'PUT'); const result = await personalRequest<{ blocked: string[] }>('/api/chat/v1/blocks'); setBlocked(result.blocked); await refresh(); });
  const loadReports = async () => { const token = generation.current; const result = await personalRequest<{ reports: ChatReport[] }>(moderationURL); if (generation.current === token) setReports(result.reports); };
  const reviewReports = () => action(loadReports);
  const moderate = (actionName: string, target: string, reason: string, hours = 0) => action(async () => { await personalRequest(moderationURL, personal.csrf, 'POST', { action: actionName, target, reason, hours }); await refresh(); await loadReports(); });
  return { room, messages, reports, blocked, enabled, error, busy, canPost, history: before !== 0, hasOlder: messages.length === 50, older: () => { if (oldest) setBefore(oldest); }, current: () => setBefore(0), refresh, send, report, remove, block, reviewReports, moderate };
}
export type ChannelChat = ReturnType<typeof useChannelChat>;
