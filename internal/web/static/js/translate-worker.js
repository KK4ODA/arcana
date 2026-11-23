// Translation worker: runs Mozilla's Bergamot engine (WebAssembly) on this
// device. Models come from Arcana (installed language packs); nothing is
// sent anywhere. Modelled on Firefox's translations-engine.worker.js.
/* global loadBergamot */
importScripts('../vendor/bergamot/bergamot-translator.js');

// Alignment of each file in the engine's memory (as Firefox uses).
const ALIGN = { model: 256, lex: 64, vocab: 64, srcvocab: 64, trgvocab: 64 };

let bergamot = null;
let service = null;
const models = new Map(); // "en-es" → TranslationModel

function config(gemm) {
  const c = {
    'beam-size': '1', normalize: '1.0', 'word-penalty': '0', 'max-length-break': '128', 'mini-batch-words': '1024',
    workspace: '128', 'max-length-factor': '2.0', 'skip-cost': 'true', 'cpu-threads': '0', quiet: 'true',
    'quiet-translation': 'true', 'gemm-precision': gemm || 'int8shiftAlphaAll', alignment: 'soft',
  };
  const indent = '            ';
  let out = '\n';
  for (const [k, v] of Object.entries(c)) out += `${indent}${k}: ${v}\n`;
  return out + indent;
}

async function fetchBuffer(url) {
  const r = await fetch(url, { cache: 'force-cache' });
  if (!r.ok) throw new Error(`${r.status} ${await r.text().catch(() => r.statusText)}`);
  return r.arrayBuffer();
}

async function engine(url) {
  if (service) return;
  const wasmBinary = await fetchBuffer(url);
  bergamot = await new Promise((resolve, reject) => {
    const m = loadBergamot({
      INITIAL_MEMORY: 41_943_040,
      print: () => {},
      printErr: () => {},
      onAbort() { reject(new Error('The translation engine could not start.')); },
      onRuntimeInitialized: async () => { await Promise.resolve(); resolve(m); },
      wasmBinary,
    });
  });
  service = new bergamot.BlockingService({ cacheSize: 0 });
}

async function model(from, to, files) {
  const key = `${from}-${to}`;
  if (models.has(key)) return models.get(key);
  const mine = files.filter((f) => f.from === from && f.to === to);
  if (!mine.length) throw new Error(`No ${from} → ${to} model is installed.`);
  const mem = {};
  let gemm = '';
  for (const f of mine) {
    const buf = await fetchBuffer(f.url);
    const am = new bergamot.AlignedMemory(buf.byteLength, ALIGN[f.fileType] || 64);
    am.getByteArrayView().set(new Uint8Array(buf));
    mem[f.fileType] = am;
    if (f.fileType === 'model') gemm = f.gemm;
  }
  const vocabs = new bergamot.AlignedMemoryList();
  if (mem.vocab) vocabs.push_back(mem.vocab);
  else if (mem.srcvocab && mem.trgvocab) { vocabs.push_back(mem.srcvocab); vocabs.push_back(mem.trgvocab); } else throw new Error('Incomplete language pack.');
  const tm = new bergamot.TranslationModel(from, to, config(gemm), mem.model, mem.lex || null, vocabs, null);
  models.set(key, tm);
  return tm;
}

function translate(chain, texts) {
  const messages = new bergamot.VectorString();
  const options = new bergamot.VectorResponseOptions();
  for (const t of texts) {
    messages.push_back(t);
    options.push_back({ qualityScores: false, alignment: true, html: true });
  }
  let responses;
  try {
    responses = chain.length === 1 ? service.translate(chain[0], messages, options)
      : service.translateViaPivoting(chain[0], chain[1], messages, options);
    const out = [];
    for (let i = 0; i < responses.size(); i++) out.push(responses.get(i).getTranslatedText());
    return out;
  } finally {
    messages.delete();
    options.delete();
    if (responses) responses.delete();
  }
}

self.onmessage = async (e) => {
  const { id, engineUrl, from, to, files, texts } = e.data || {};
  try {
    await engine(engineUrl);
    const chain = [];
    if (from === 'en' || to === 'en') chain.push(await model(from, to, files));
    else { chain.push(await model(from, 'en', files)); chain.push(await model('en', to, files)); }
    // Empty strings confuse the engine; keep their places.
    const idx = [];
    const send = [];
    texts.forEach((t, i) => { if (t && t.trim()) { idx.push(i); send.push(t); } });
    const got = send.length ? translate(chain, send) : [];
    const results = texts.slice();
    idx.forEach((i, k) => { results[i] = got[k]; });
    self.postMessage({ id, results });
  } catch (err) {
    self.postMessage({ id, error: err && err.message ? err.message : String(err) });
  }
};
