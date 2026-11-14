import { afterEach, expect, test, vi } from 'vitest';
import { catalogCreatorsURL, catalogFavoriteID, catalogLabelsURL, catalogSeriesIssue, catalogSelectionURL, catalogRequest, catalogURL, representationURL, sourceProvider, type RepresentationTransport } from './catalog.js';

test('metadata queries preserve collection scope and escape user text',()=> {
  const url = new URL(catalogURL({collection:'mixed',media:'print',query:'&collection=other',page:2}),'https://test.invalid');
  expect(url.searchParams.get('collection')).toBe('mixed');
  expect(url.searchParams.get('q')).toBe('&collection=other');
  expect(url.searchParams.get('page')).toBe('2');
});
test('label and text filters are sent only when set',()=> {
  const url = new URL(catalogURL({collection:'regional',media:'print',query:'',page:1,label:'strikes',text:'eight hour'}),'https://test.invalid');
  expect(url.searchParams.get('label')).toBe('strikes');
  expect(url.searchParams.get('text')).toBe('eight hour');
  const plain = new URL(catalogURL({collection:'regional',media:'print',query:'',page:1,text:'  '}),'https://test.invalid');
  expect(plain.searchParams.has('text')).toBe(false);
  expect(plain.searchParams.has('label')).toBe(false);
  expect(catalogLabelsURL('regional','topic')).toBe('/api/catalog/v1/labels?collection=regional&kind=topic');
  expect(catalogLabelsURL('')).toBe('/api/catalog/v1/labels');
});
test('a series filter asks for the series label, which the engine returns in issue order',()=> {
  const url = new URL(catalogURL({collection:'zines',media:'',query:'',page:1,series:'riot-grrrl'}),'https://test.invalid');
  expect(url.searchParams.getAll('label')).toEqual(['riot-grrrl']);
  expect(new URL(catalogURL({collection:'zines',media:'',query:'',page:1,series:'riot-grrrl',label:'riot-grrrl'}),'https://test.invalid').searchParams.getAll('label')).toEqual(['riot-grrrl']);
  expect(()=>catalogURL({collection:'zines',media:'',query:'',page:1,series:'riot-grrrl',label:'strikes'})).toThrow('not both');
  expect(catalogLabelsURL('zines','series')).toBe('/api/catalog/v1/labels?collection=zines&kind=series');
});
test('an item names its issue of a series',()=> {
  const item = {labels:[
    {id:'feminism',kind:'topic' as const,label:'Feminism'},
    {id:'riot-grrrl',kind:'series' as const,label:'Riot Grrrl',issue_position:3,issue_label:'Volume 3'},
    {id:'bikini-kill',kind:'series' as const,label:'Bikini Kill',issue_position:0,issue_label:'1'},
  ]};
  expect(catalogSeriesIssue(item,'riot-grrrl')).toEqual({id:'riot-grrrl',kind:'series',label:'Riot Grrrl',issue_position:3,issue_label:'Volume 3'});
  expect(catalogSeriesIssue(item,'bikini-kill')?.issue_position).toBe(0);
  expect(catalogSeriesIssue(item,'feminism')).toBeUndefined();
  expect(catalogSeriesIssue(item,'missing')).toBeUndefined();
  expect(catalogSeriesIssue({},'riot-grrrl')).toBeUndefined();
});
test('creator suggestions keep the collection scope and escape it',()=> {
  expect(catalogCreatorsURL('riot-grrrl')).toBe('/api/catalog/v1/creators?collection=riot-grrrl');
  expect(catalogCreatorsURL('a&kind=x')).toBe('/api/catalog/v1/creators?collection=a%26kind%3Dx');
  expect(catalogCreatorsURL('')).toBe('/api/catalog/v1/creators');
});
const asset: RepresentationTransport = {media:'print',policy:'local_preserve',file:'paper.pdf',source_url:'https://archive.org/details/paper',url:'/api/catalog/v1/items/paper/representations/pdf/media?collection=mixed&action=read&file=paper.pdf'};
test('representation URLs bind source identity, exact file and viewer action',()=> {
  expect(representationURL(asset)).toBe(asset.url);
  expect(representationURL({...asset,policy:'source_stream',url:'https://archive.org/download/paper/paper.pdf'})).toBe('https://archive.org/download/paper/paper.pdf');
  for (const value of [
    {...asset,url:'//evil.invalid/api/catalog/v1/items/paper/representations/pdf/media'},
    {...asset,url:asset.url+'&collection=other'},
    {...asset,url:asset.url.replace('action=read','action=download')},
    {...asset,url:asset.url.replace('file=paper.pdf','file=other.pdf')},
    {...asset,policy:'source_stream',url:'https://archive.org/download/other/paper.pdf'},
    {...asset,source_url:'https://evil.invalid/details/paper'}
  ]) expect(()=>representationURL(value)).toThrow();
});

test('other source providers are accepted only for reviewed local originals within their host',()=> {
  const generic = {...asset,source_provider:'marxists.org',source_url:'https://www.marxists.org/history/usa/pubs/proletarec/index.htm'};
  expect(representationURL(generic)).toBe(asset.url);
  expect(sourceProvider(generic)).toBe('marxists.org');
  expect(sourceProvider(asset)).toBe('archive.org');
  for (const value of [
    {...generic,policy:'source_stream',url:'https://www.marxists.org/history/usa/pubs/proletarec/1910.pdf'},
    {...generic,source_url:'https://evil.invalid/history/usa/pubs/proletarec/index.htm'},
    {...generic,source_url:'https://marxists.org.evil.invalid/index.htm'},
    {...generic,source_url:'http://www.marxists.org/index.htm'},
    {...generic,source_url:'https://www.marxists.org:8443/index.htm'},
    {...generic,source_url:'https://user@www.marxists.org/index.htm'},
    {...generic,source_provider:'ia.archive.org',source_url:'https://ia.archive.org/details/paper'},
    {...generic,source_provider:'Marxists.org'}
  ]) expect(()=>representationURL(value)).toThrow();
});


test('download URLs require the explicit action returned by the policy resolver',()=> {
  const download = {...asset,url:asset.url.replace('action=read','action=download')};
  expect(representationURL(download,'download')).toBe(download.url);
  expect(()=>representationURL(download)).toThrow();
  expect(()=>representationURL(asset,'download')).toThrow();
  expect(()=>representationURL({...download,url:download.url+'&action=read'},'download')).toThrow();
});


afterEach(()=>vi.restoreAllMocks());
test('a deadline reports a retryable error but caller cancellation stays cancellation',async()=> {
  const deadline = new AbortController();
  vi.spyOn(AbortSignal,'timeout').mockReturnValue(deadline.signal);
  vi.spyOn(globalThis,'fetch').mockImplementation(async()=>{deadline.abort();throw new DOMException('signal timed out','TimeoutError');});
  await expect(catalogRequest('/resolve',new AbortController().signal,60000)).rejects.toThrow('Please try again');
  expect(AbortSignal.timeout).toHaveBeenCalledWith(60000);
  const caller = new AbortController();caller.abort();
  await expect(catalogRequest('/resolve',caller.signal)).rejects.toThrow('signal timed out');
  await expect(catalogRequest('/resolve',caller.signal,60001)).rejects.toThrow('Invalid request timeout');
});

test('mixed-media filters and share links preserve collection and exact logical identity',()=>{
 const url=new URL(catalogURL({collection:'print',media:'print',query:'',page:2,creator:'A & B',dateFrom:'2026-01-01',dateTo:'2026-10-03'}),'https://test.invalid');
 expect(url.searchParams.get('creator')).toBe('A & B');expect(url.searchParams.get('date_to')).toBe('2026-10-03');
 const shared=new URL(catalogSelectionURL('https://test.invalid/?item=old#catalog','print','print:item','scan:pdf'));
 expect(shared.searchParams.get('item')).toBe('print:item');expect(shared.searchParams.get('representation')).toBe('scan:pdf');expect(shared.hash).toBe('');
 expect(catalogFavoriteID('print:item')).toBe('catalog:print:item');
});

test('accepts only exact safe nested source audio paths',()=>{
 const audio={...asset,media:'audio' as const,policy:'source_stream',file:'disc 1/01  song.mp3',url:'https://archive.org/download/paper/disc%201/01%20%20song.mp3'};
 expect(representationURL(audio)).toBe(audio.url);
 expect(representationURL({...audio,file:'disc/song..mp3',url:'https://archive.org/download/paper/disc/song..mp3'})).toBe('https://archive.org/download/paper/disc/song..mp3');
 for(const file of ['/song.mp3','disc/../song.mp3','disc//song.mp3','disc/./song.mp3','disc/\nsong.mp3','disc\\song.mp3']) {
  expect(()=>representationURL({...audio,file,url:'https://archive.org/download/paper/'+file.split('/').map(encodeURIComponent).join('/')})).toThrow();
 }
 expect(()=>representationURL({...audio,file:'disc 1/another.mp3'})).toThrow();
 expect(()=>representationURL({...audio,media:'print'})).toThrow();
});
