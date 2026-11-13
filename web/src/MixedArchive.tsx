import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useCatalog, catalogRequest, representationPath, representationURL, sourceProvider, catalogFavoriteID, catalogSelectionURL, type CatalogCollection, type CatalogItem, type CatalogMedia, type CatalogRepresentation } from '@subcult/archive-viewer/catalog';
import { AudioPlayer, type AudioAsset } from '@subcult/archive-viewer/audio';
import { Player, playbackURL, useMediaPreferences, usePersonal, type Playback } from '@subcult/archive-viewer';
import type { Brand } from './brand';
import { CatalogPersonal, CatalogShare } from './CatalogTools';
const DocumentView = lazy(()=>import('./DocumentView'));
const CollectionChannels = lazy(()=>import('./CollectionChannels'));

type Selection = {item:CatalogItem;representation:CatalogRepresentation;collection:string};
type Transport = {media:CatalogMedia;policy:string;url:string;source_url:string;source_provider?:string;file:string};
function SelectedItem({selection,onClose,focus,onFocus}: {selection:Selection;onClose:()=>void;focus:boolean;onFocus:()=>void}) {
  const {item,representation,collection} = selection;
  const path = representationPath(item,representation);
  const [asset,setAsset] = useState<Transport>();
  const [download,setDownload] = useState<string>();
  const [audio,setAudio] = useState<AudioAsset>();
  const [legacy,setLegacy] = useState<Playback>();
  const [paused,setPaused] = useState(true);
  const [setIndex,setSetIndex] = useState(-1);
  const [error,setError] = useState('');
  const [attempt,setAttempt] = useState(0);
  const section = useRef<HTMLElement>(null);
  useEffect(()=> {
    if (representation.media !== 'print') return;
    const controller = new AbortController();
    void catalogRequest<Transport>(`${path}/resolve?${new URLSearchParams({collection,action:'download'})}`,controller.signal)
      .then(value=>{ if(!controller.signal.aborted)setDownload(representationURL(value,'download')); })
      .catch(()=>undefined);
    return ()=>controller.abort();
  },[path,collection,representation.media]);
  useEffect(()=>{section.current?.scrollIntoView({block:'start'});section.current?.focus();},[]);
  useEffect(()=> {
    const controller = new AbortController();
    const params = new URLSearchParams({collection});
    void (async()=> {
      if (representation.media === 'print' && !/\.pdf$/i.test(representation.file)) throw new Error('This reader supports PDF files. Open the source page for this format.');
      // Continuous recordings have a separate derivative receipt and resolver.
      if (representation.media === 'audio' && representation.extension.audio?.recording) {
        const value = await catalogRequest<AudioAsset>(`${path}/audio/resolve?${params}`,controller.signal,60000);
        if(!controller.signal.aborted)setAudio(value);
        return;
      }
      if (representation.legacy_episode_id) {
        const response = await catalogRequest<Playback>(`/api/playback/${encodeURIComponent(representation.legacy_episode_id)}?${params}`,controller.signal);
        playbackURL(response);
        if(!controller.signal.aborted){setLegacy(response);setAsset({...response,media:'video',file:representation.file});} return;
      }
      const resolved = await catalogRequest<Transport>(`${path}/resolve?${params}`,controller.signal);
      if (resolved.media !== representation.media) throw new Error('The media format has changed.');
      representationURL(resolved);
      if(!controller.signal.aborted)setAsset(resolved);
    })().catch(reason=> {if(!controller.signal.aborted)setError(reason instanceof Error ? reason.message : 'This item is unavailable.');});
    return ()=>controller.abort();
  },[path,collection,representation,attempt]);
  // The resolver names the reviewed source page; other providers are not Internet Archive items.
  const source = asset && !legacy ? {url: asset.source_url, provider: sourceProvider(asset)} : {url: `https://archive.org/details/${encodeURIComponent(representation.source_item_id)}`, provider: 'archive.org'};
  return <section ref={section} tabIndex={-1} className="catalog-selection" aria-label="Selected archive item">
    <button onClick={onClose}>Close item</button><button aria-pressed={focus} onClick={onFocus}>{focus ? 'Exit focus mode' : 'Focus mode'}</button><p className="eyebrow">{representation.media} · {collection}</p><h2>{item.title}</h2>
    <CatalogShare url={catalogSelectionURL(location.href,collection,item.id,representation.id)} title={item.title}/>
    <p>{[item.creator,item.date].filter(Boolean).join(' · ')}</p><p>{item.description}</p>
    <dl>{representation.extension.print && <><dt>Pages</dt><dd>{representation.extension.print.pages || 'Unknown'}</dd><dt>Print variant</dt><dd>{representation.extension.print.variant || 'Unknown'}</dd></>}<dt>Format</dt><dd>{representation.media} · {representation.file}</dd></dl>
    {!error && !asset && !audio && <p role="status">Checking access…</p>}{error && <p role="alert">{error} <button onClick={()=>{setError('');setAsset(undefined);setAudio(undefined);setLegacy(undefined);setAttempt(value=>value+1);}}>Try again</button></p>}
    {audio && <><label>Recording section<select value={setIndex} onChange={event=>setSetIndex(Number(event.target.value))}><option value={-1}>Full recording</option>{audio.recording.sets.map((set,index)=>set.reviewed && <option key={index} value={index}>{set.title}</option>)}</select></label><AudioPlayer asset={audio} set={setIndex>=0 ? audio.recording.sets[setIndex] : undefined}/></>}
    {asset?.media === 'print' && <Suspense fallback={<p role="status">Opening PDF reader…</p>}><DocumentView url={asset.url} title={item.title} path={path} collection={collection}/></Suspense>}
    {legacy && <Player key={legacy.url} session={{mode:'ondemand',collection,episode:{id:representation.legacy_episode_id!,title:item.title,date:item.date,description:item.description,synthetic:false},playback:legacy,offset:0,receivedAt:performance.now(),latency:0,generation:1}} paused={paused} onPaused={setPaused}/>}
    {asset?.media === 'video' && !legacy && <NativeMedia key={asset.url} asset={asset} title={item.title} onError={()=>setError('The video source is unavailable. Try another format or open the source page.')}/>}
    {asset?.media === 'audio' && <NativeMedia key={asset.url} asset={asset} title={item.title} onError={()=>setError('The audio source is unavailable. Try another format or open the source page.')}/>}
    {!audio && representation.extension.audio && <dl>{Object.entries(representation.extension.audio.credits || {}).map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>}
    {download && <p><a href={download} download={representation.file}>Download original PDF</a></p>}
    <p><a href={source.url} target="_blank" rel="noreferrer">{source.provider === 'archive.org' ? 'Source item at Internet Archive' : `Source page at ${source.provider}`}</a> · {representation.file}</p>
  </section>;
}
function NativeMedia({asset,title,onError}: {asset:Transport;title:string;onError:()=>void}) {
 const media=useRef<HTMLMediaElement>(null);useMediaPreferences(media);
 return asset.media==='audio' ? <audio ref={media as React.RefObject<HTMLAudioElement>} crossOrigin="anonymous" controls preload="metadata" src={asset.url} aria-label={title} onError={onError}/> : <video ref={media as React.RefObject<HTMLVideoElement>} crossOrigin="anonymous" controls playsInline preload="metadata" src={asset.url} aria-label={title} onError={onError}/>;
}
export default function MixedArchive({brand}: {brand:Brand}) {
  const [collections,setCollections] = useState<CatalogCollection[]>([]);
  const [collectionError,setCollectionError] = useState('');
  const [collection,setCollection] = useState(()=>new URLSearchParams(typeof window === 'undefined' ? '' : window.location.search).get('collection') || '');
  const [media,setMedia] = useState<CatalogMedia | ''>('');
  const [query,setQuery] = useState('');
  const [page,setPage] = useState(1);
  const [selection,setSelection] = useState<Selection>();
  const [creator,setCreator]=useState(''),[dateFrom,setDateFrom]=useState(''),[dateTo,setDateTo]=useState('');
  const [layout,setLayout]=useState<'grid'|'table'>('grid'),[focus,setFocus]=useState(false),[channelsOpen,setChannelsOpen]=useState(false);
  const personal=usePersonal(collection);
  const catalog = useCatalog({collection,media,query,page,creator,dateFrom,dateTo});
  useEffect(()=>{
   const params=new URLSearchParams(location.search),id=params.get('item'),representation=params.get('representation');
   if(!id || !representation || !collection)return;
   const controller=new AbortController();
   void catalogRequest<CatalogItem>(`/api/catalog/v1/items/${encodeURIComponent(id)}?${new URLSearchParams({collection})}`,controller.signal).then(item=>{
    const rep=item.representations.find(value=>value.id===representation && value.collections.includes(collection));
    if(!controller.signal.aborted && rep)setSelection({item,representation:rep,collection});
   }).catch(()=>undefined);
   return()=>controller.abort();
  },[collection]);
  useEffect(()=>{
   const esc=(event:KeyboardEvent)=>{if(event.key==='Escape')setFocus(false);};window.addEventListener('keydown',esc);return()=>window.removeEventListener('keydown',esc);
  },[]);
  function select(value:Selection){setSelection(value);history.replaceState(null,'',catalogSelectionURL(location.href,value.collection,value.item.id,value.representation.id));}
  function close(){setSelection(undefined);setFocus(false);const url=new URL(location.href);url.searchParams.delete('item');url.searchParams.delete('representation');if(collection)url.searchParams.set('collection',collection);else url.searchParams.delete('collection');history.replaceState(null,'',url);}
  function openFavorite(item:CatalogItem){const rep=item.representations.find(value=>value.collections.includes(collection));if(rep)select({item,representation:rep,collection});}
  function actions(item:CatalogItem){return <>{item.representations.flatMap(rep=>rep.collections.map(member=><button key={`${rep.id}:${member}`} onClick={()=>select({item,representation:rep,collection:member})}>{rep.media === 'print' ? 'Read' : rep.media === 'audio' ? 'Listen' : 'Watch'}{item.representations.filter(other=>other.media===rep.media).length>1 ? ` · ${rep.file}` : ''}{rep.collections.length>1 ? ` · ${member}` : ''}</button>))}{collection && <button aria-pressed={personal.favoriteIDs.includes(catalogFavoriteID(item.id))} disabled={personal.busy} onClick={()=>{void personal.toggle(catalogFavoriteID(item.id));}}>{personal.favoriteIDs.includes(catalogFavoriteID(item.id)) ? 'Remove favorite' : 'Favorite'}</button>}</>;}

  useEffect(()=> {
    document.title = `${brand.title} — ${brand.headline}`;
    for (const [key,color] of Object.entries(brand.colors)) document.documentElement.style.setProperty(`--${key}`,color);
    document.documentElement.style.setProperty('--label', brand.colors.label ?? brand.colors.accent);
  },[brand]);
  useEffect(()=> {
    const controller = new AbortController();
    void catalogRequest<CatalogCollection[]>('/api/catalog/v1/collections',controller.signal).then(value=> {if(!controller.signal.aborted)setCollections(value);}).catch(reason=>{if(!controller.signal.aborted)setCollectionError(reason.message);});
    return ()=>controller.abort();
  },[]);
  function changeFilter(update:()=>void) {update();setPage(1);close();}
  return <main className={`mixed-archive${focus && selection ? ' reading-focus' : ''}`}><a className="skip-link" href="#catalog">Skip to archive catalog</a>
    <header><a className="wordmark" href="/">{brand.logo && <img src={brand.logo} alt=""/>}{brand.title}<span>{brand.subtitle}</span></a><a href="#catalog">Browse collections</a></header>
    <section className="catalog-intro"><p className="eyebrow">{brand.kicker}</p><h1>{brand.headline}</h1><p>{brand.description}</p></section>
    <section id="catalog" aria-label="Archive catalog">
      <div className="catalog-filters"><label>Collection<select value={collection} onChange={event=>changeFilter(()=>{setCollection(event.target.value);const url=new URL(location.href);url.searchParams.set('collection',event.target.value);history.replaceState(null,'',url);})}><option value="">All collections</option>{collections.map(c=><option key={c.id} value={c.id}>{c.title} ({c.items})</option>)}</select></label>
        <label>Media<select value={media} onChange={event=>changeFilter(()=>setMedia(event.target.value as CatalogMedia | ''))}><option value="">All media</option><option value="print">Print</option><option value="audio">Audio</option><option value="video">Video</option></select></label>
        <label>Search titles and descriptions<input type="search" value={query} maxLength={200} onChange={event=>changeFilter(()=>setQuery(event.target.value))}/></label>
        <label>Creator<input value={creator} maxLength={200} onChange={event=>changeFilter(()=>setCreator(event.target.value))}/></label>
        <label>Original date from<input type="date" value={dateFrom} onChange={event=>changeFilter(()=>setDateFrom(event.target.value))}/></label><label>Original date to<input type="date" value={dateTo} onChange={event=>changeFilter(()=>setDateTo(event.target.value))}/></label></div>
      <p>Date-range filters match complete source date strings. Partial and unknown dates remain visible when date filters are empty.</p>
      <div role="group" aria-label="Catalog layout"><button aria-pressed={layout==='grid'} onClick={()=>setLayout('grid')}>Grid</button><button aria-pressed={layout==='table'} onClick={()=>setLayout('table')}>Table</button></div>
      {collectionError && <p role="alert">Collection names are unavailable: {collectionError}</p>}
      {catalog.pending && <p role="status">Loading catalog…</p>}{catalog.error && <p role="alert">{catalog.error} <button onClick={catalog.retry}>Try again</button></p>}
      {catalog.page && <><p role="status">{catalog.page.total} {catalog.page.total === 1 ? 'item' : 'items'} · Metadata search</p>{catalog.page.items.length === 0 && <p>No items match these filters.</p>}
        {layout==='table' ? <div className="catalog-table-wrap"><table><caption>Archive catalog</caption><thead><tr><th scope="col">Item</th><th scope="col">Original date</th><th scope="col">Creator</th><th scope="col">Available formats</th><th scope="col">Actions</th></tr></thead><tbody>{catalog.page.items.map(item=><tr key={item.id}><th scope="row">{item.title}</th><td>{item.date || 'Unknown'}</td><td>{item.creator || 'Unknown'}</td><td>{item.representations.map(rep=>`${rep.media}: ${rep.file}`).join(' · ')}</td><td>{actions(item)}</td></tr>)}</tbody></table></div> : <div className="catalog-items">{catalog.page.items.map(item=><article key={item.id}><p className="eyebrow">{[...new Set(item.representations.map(rep=>rep.media))].join(' / ')}</p><h2>{item.title}</h2><p>{[item.creator,item.date].filter(Boolean).join(' · ')}</p><p className="catalog-description">{item.description}</p>{actions(item)}</article>)}</div>}
        <nav className="catalog-pages" aria-label="Catalog pages"><button disabled={page<=1} onClick={()=>{setPage(value=>value-1);setSelection(undefined);}}>Previous</button><span>Page {page}</span><button disabled={page*24>=catalog.page.total} onClick={()=>{setPage(value=>value+1);setSelection(undefined);}}>Next</button></nav></>}
    </section>
    {collection && <CatalogPersonal key={`${collection}:${personal.user?.id || 'browser'}`} personal={personal} collection={collection} onOpen={openFavorite}/>}
    {collection && <details onToggle={event=>setChannelsOpen(event.currentTarget.open)}><summary>Scheduled programs for this collection</summary>{channelsOpen && <Suspense fallback={<p role="status">Opening channel controls…</p>}><CollectionChannels collection={collection} media={collections.find(c=>c.id===collection)?.media || []}/></Suspense>}</details>}
    {selection && <SelectedItem key={`${selection.item.id}:${selection.representation.id}:${selection.collection}`} selection={selection} onClose={close} focus={focus} onFocus={()=>setFocus(!focus)}/>}
    <footer><span>{brand.title}</span><span>Source metadata retained · Access reviewed per collection</span><a href="https://subcult.tv/support">Support SUBCULT’s archive software</a></footer>
  </main>;
}
