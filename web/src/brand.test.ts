import { expect, test } from 'vitest';
import { checkEngine, parseBrand } from './brand';
const valid = { title: 'Archive', subtitle: 'Programs', collection: 'democracy_now_vid', kicker: 'Replay', headline: 'Watch programs', description: 'From the archive', colors: { background: '#14191e', surface: '#1e262e', ink: '#f5f2e9', muted: '#b9c1c8', accent: '#ebd59b', border: '#42505c' } };
test('white-label configuration accepts bounded text and local assets', () => { expect(parseBrand({ ...valid, logo: '/assets/logo.svg' }).title).toBe('Archive'); });
test('mixed catalog is opt-in while older video configurations remain valid',()=> {
  expect(parseBrand(valid).interface).toBeUndefined();
  expect(parseBrand({...valid,interface:'catalog'}).interface).toBe('catalog');
  expect(()=>parseBrand({...valid,interface:'unknown'})).toThrow();
});
test('engine compatibility retains video v1 and requires mixed module capabilities',()=> {
  expect(()=>checkEngine(valid,{api_version:'1',collection:valid.collection})).not.toThrow();
  expect(()=>checkEngine(valid,{api_version:'1',collection:'',collections:['other']})).toThrow();
  const catalog = parseBrand({...valid,interface:'catalog'});
  expect(()=>checkEngine(catalog,{api_version:'1',collection:valid.collection})).toThrow();
  expect(()=>checkEngine(catalog,{api_version:'1',collection:'',collections:['mixed'],modules:{catalog:'1',print:'1',audio:'1',video:'1'}})).not.toThrow();
});
test('configuration rejects CSS injection, external logos and invalid identity', () => {
  for (const invalid of [{ ...valid, colors: { ...valid.colors, accent: 'red;display:none' } }, { ...valid, colors: { ...valid.colors, label: 'blue' } }, { ...valid, logo: '//example.com/a.svg' }, { ...valid, logo: '/assets/../private.svg' }, { ...valid, collection: '../collection' }, { ...valid, title: '' }]) expect(() => parseBrand(invalid)).toThrow();
});
