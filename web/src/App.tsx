import { useEffect, useState } from 'react';
import { checkEngine, parseBrand, type Brand, type EngineCapabilities } from './brand';
import { request } from './model';
import { useViewer } from './useViewer';
import { ChannelPanel, Library, Screen } from './Viewer';
import './styles.css';
import MixedArchive from './MixedArchive';

function Archive({ brand }: { brand: Brand }) {
  const viewer = useViewer(brand.collection);
  useEffect(() => {
    document.title = `${brand.title} — ${brand.headline}`;
    for (const [key, color] of Object.entries(brand.colors)) document.documentElement.style.setProperty(`--${key}`, color);
    document.documentElement.style.setProperty('--label', brand.colors.label ?? brand.colors.accent);
  }, [brand]);
  return <main>
    <a className="skip-link" href="#library">Skip to episode library</a>
    <header><a className="wordmark" href="/">{brand.logo && <img src={brand.logo} alt="" />}{brand.title}<span>{brand.subtitle}</span></a><nav aria-label="Main navigation"><a href="#screen">Watch</a><a href="#library">Library</a></nav></header>
    <section className="screen-layout" aria-label="Viewing screen"><Screen viewer={viewer}><div className="intro"><p className="eyebrow">{brand.kicker}</p><h1>{brand.headline}</h1><p className="lede">{brand.description}</p></div></Screen><ChannelPanel viewer={viewer} /></section>
    <Library viewer={viewer} metadataFilters />
    <footer><span>{brand.title}</span><span>{viewer.collections.find(c => c.id === brand.collection)?.policy === 'local_preserve' ? 'Verified local video · Source metadata retained' : 'Video streamed from Internet Archive · Source metadata retained'}</span></footer>
  </main>;
}

export default function App({ initialBrand }: { initialBrand?: Brand } = {}) {
  const [brand, setBrand] = useState<Brand | null>(initialBrand ?? null);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([request<unknown>('/site.json', controller.signal), request<EngineCapabilities>('/api/version', controller.signal)]).then(([config, engine]) => {
      const parsed = parseBrand(config);
      checkEngine(parsed,engine);
      setBrand(parsed);
    }).catch(reason => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'The archive is temporarily unavailable.'); });
    return () => controller.abort();
  }, []);
  if (error) return <main className="configuration"><h1>Archive unavailable</h1><p role="alert">{error}</p><button onClick={() => location.reload()}>Try again</button></main>;
  if (!brand) return <main><p role="status">Opening the archive…</p></main>;
  return parsedArchive(brand);
}

function parsedArchive(brand: Brand) {
  if (brand.interface === 'catalog') return <MixedArchive brand={brand}/>;
  return <Archive brand={brand}/>;
}
