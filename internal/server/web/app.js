"use strict";
// barsdig web UI: search, audition and mark cues, then export them.
// Plain browser JavaScript; talks only to this server's /api and /audio routes.

const $ = (id) => document.getElementById(id);
const PAGE = 200;

const els = {
  q: $("q"), count: $("count"), rows: $("rows"), results: $("results"), empty: $("empty"), sentinel: $("sentinel"),
  bank: $("f-bank"), banks: $("banks"), codec: $("f-codec"), ch: $("f-ch"), min: $("f-min"), max: $("f-max"),
  loop: $("f-loop"), sort: $("f-sort"), desc: $("f-desc"),
  basketList: $("basket-list"), basketCount: $("basket-count"), basketEmpty: $("basket-empty"),
  basketClear: $("basket-clear"), exportBtn: $("export"), exportOut: $("export-out"),
  toggle: $("toggle"), toggleIcon: $("toggle-icon"), nowName: $("now-name"), nowMeta: $("now-meta"),
  wave: $("wave"), canvas: $("wave-canvas"), canvasPlayed: $("wave-canvas-played"), waveLoop: $("wave-loop"), wavePlayed: $("wave-played"), waveHead: $("wave-head"),
  time: $("time"), vol: $("vol"), status: $("status"), audio: $("audio"),
};

// ---- small helpers ---------------------------------------------------------

function store(key, value) {
  try {
    if (value === undefined) return JSON.parse(localStorage.getItem(key));
    localStorage.setItem(key, JSON.stringify(value));
  } catch (_) { /* storage can be unavailable; the page works without it */ }
  return null;
}

function fmtDur(s) {
  if (!isFinite(s) || s < 0) return "0:00";
  if (s < 10) return s.toFixed(2) + "s";
  if (s < 60) return s.toFixed(1) + "s";
  const m = Math.floor(s / 60), r = Math.floor(s % 60);
  return m + ":" + String(r).padStart(2, "0");
}
function fmtClock(s) {
  if (!isFinite(s) || s < 0) s = 0;
  return Math.floor(s / 60) + ":" + String(Math.floor(s % 60)).padStart(2, "0");
}
function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === "class") n.className = v; else if (k === "text") n.textContent = v; else n.setAttribute(k, v);
  }
  for (const kid of kids) if (kid != null) n.append(kid);
  return n;
}
function setStatus(msg, isErr) {
  els.status.textContent = msg || "";
  els.status.classList.toggle("err", !!isErr);
}

// ---- cue list --------------------------------------------------------------

const state = { offset: 0, total: 0, loading: false, done: false, gen: 0 };
const shown = new Map(); // id -> cue (for rows currently in the DOM)

function queryString(offset) {
  const p = new URLSearchParams();
  const add = (k, v) => { if (v !== "" && v != null) p.set(k, v); };
  add("q", els.q.value.trim());
  add("bank", els.bank.value.trim());
  add("codec", els.codec.value);
  add("ch", els.ch.value);
  add("min", els.min.value.trim());
  add("max", els.max.value.trim());
  if (els.loop.checked) p.set("loop", "1");
  add("sort", els.sort.value);
  if (els.desc.checked) p.set("desc", "1");
  p.set("limit", PAGE);
  p.set("offset", offset);
  return p.toString();
}

async function loadPage(reset) {
  if (state.loading && !reset) return;
  if (reset) { state.gen++; state.offset = 0; state.done = false; els.rows.replaceChildren(); shown.clear(); }
  if (state.done) return;
  const gen = state.gen;
  state.loading = true;
  try {
    const res = await fetch("/api/cues?" + queryString(state.offset));
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText);
    const data = await res.json();
    if (gen !== state.gen) return; // a newer search replaced this one
    state.total = data.total;
    for (const c of data.cues) { shown.set(c.id, c); els.rows.append(renderRow(c)); }
    state.offset += data.cues.length;
    state.done = state.offset >= data.total || data.cues.length === 0;
    els.count.textContent = data.total.toLocaleString() + (data.total === 1 ? " cue" : " cues");
    els.empty.hidden = data.total !== 0;
  } catch (err) {
    setStatus("Could not load cues: " + err.message, true);
  } finally {
    if (gen === state.gen) state.loading = false;
  }
}

function renderRow(c) {
  const tr = el("tr", { "data-id": c.id });
  if (c.no_audio) tr.classList.add("noaudio");
  if (player.cue && player.cue.id === c.id) tr.classList.add("playing");

  const play = el("button", { class: "icon-btn", type: "button", "aria-label": "Play " + c.name, text: "▶" });
  play.disabled = c.no_audio;
  play.addEventListener("click", () => playCue(c));

  const mark = el("input", { type: "checkbox", "aria-label": "Mark " + c.name + " for export" });
  mark.checked = basket.has(c.id);
  mark.disabled = c.no_audio;
  mark.addEventListener("change", () => (mark.checked ? basket.add(c) : basket.remove(c.id)));

  const name = el("button", { class: "name", type: "button", text: c.name });
  name.disabled = c.no_audio;
  name.addEventListener("click", () => playCue(c));

  const notes = el("td");
  const badge = (t, cls, title) => notes.append(el("span", { class: "badge " + (cls || ""), text: t, title: title || "" }));
  if (c.no_audio) badge("no audio");
  if (c.loop) badge("loop", "loop", "Has a loop region");
  if (c.prefetch_stub && !c.uses_full_stream) badge("short clip only", "warn", "Only the short prefetch clip stored in the bank exists for this cue.");
  if (c.prefetch_stub && c.uses_full_stream) badge("full stream", "", "The bank only holds a short clip, so the full file from Stream/ is used.");
  if (c.alias_of) badge("alias", "", "Shares its audio with " + c.alias_of);
  if (!c.name_verified && c.bank !== "Stream" && !c.bank.endsWith("/Stream")) badge("name unverified", "warn", "The name does not match the CRC-32 stored in the bank.");

  tr.append(
    el("td", { class: "c-play" }, play), el("td", { class: "c-mark" }, mark), el("td", {}, name),
    el("td", { class: "mono", text: c.bank }), el("td", { class: "mono", text: c.codec || "" }),
    el("td", { class: "r", text: c.no_audio ? "" : fmtDur(c.duration) }), el("td", { class: "r", text: c.channels || "" }), notes);
  return tr;
}

function markRows(id, playing) {
  const tr = els.rows.querySelector('tr[data-id="' + CSS.escape(id) + '"]');
  if (tr) tr.classList.toggle("playing", playing);
}

let debounce;
function refresh() { clearTimeout(debounce); debounce = setTimeout(() => loadPage(true), 200); }
for (const e of [els.q, els.bank, els.min, els.max]) e.addEventListener("input", refresh);
for (const e of [els.codec, els.ch, els.loop, els.sort, els.desc]) e.addEventListener("change", refresh);

new IntersectionObserver((entries) => { if (entries[0].isIntersecting) loadPage(false); }, { root: els.results, rootMargin: "400px" })
  .observe(els.sentinel);

async function loadFacets() {
  try {
    const f = await (await fetch("/api/facets")).json();
    for (const b of f.banks) els.banks.append(el("option", { value: b }));
    for (const c of f.codecs) els.codec.append(el("option", { value: c, text: c }));
    for (const n of f.channels) els.ch.append(el("option", { value: n, text: String(n) }));
  } catch (_) { /* filters stay at "any" */ }
}

// ---- basket and export -----------------------------------------------------

const basket = {
  items: new Map(),
  init() {
    for (const c of store("barsdig.basket") || []) this.items.set(c.id, c);
    this.render();
    this.refreshOld();
  },
  // Items saved by an older version hold only id, name and bank: fetch the rest so they can be played.
  async refreshOld() {
    let changed = false;
    for (const c of [...this.items.values()]) {
      if (c.codec !== undefined) continue;
      try {
        const res = await fetch("/api/cue?id=" + encodeURIComponent(c.id));
        if (res.status === 404) this.items.delete(c.id);
        else if (res.ok) this.items.set(c.id, await res.json());
        else continue;
        changed = true;
      } catch (_) { /* leave it; it is refreshed next time */ }
    }
    if (changed) this.save();
  },
  has(id) { return this.items.has(id); },
  add(c) { this.items.set(c.id, c); this.save(); },
  markPlaying() {
    const cur = player.cue && player.cue.id;
    for (const li of els.basketList.children) li.classList.toggle("playing", li.dataset.id === cur);
  },
  remove(id) {
    this.items.delete(id); this.save();
    const box = els.rows.querySelector('tr[data-id="' + CSS.escape(id) + '"] input[type="checkbox"]');
    if (box) box.checked = false;
  },
  clear() {
    this.items.clear(); this.save();
    for (const b of els.rows.querySelectorAll('input[type="checkbox"]')) b.checked = false;
  },
  save() { store("barsdig.basket", [...this.items.values()]); this.render(); },
  render() {
    const n = this.items.size;
    els.basketCount.textContent = n;
    els.basketEmpty.hidden = n > 0;
    els.basketClear.disabled = n === 0;
    els.exportBtn.disabled = n === 0;
    els.basketList.replaceChildren(...[...this.items.values()].map((c) => {
      const play = el("button", { class: "icon-btn", type: "button", "aria-label": "Play " + c.name, text: "\u25B6" });
      play.disabled = !!c.no_audio;
      play.addEventListener("click", () => playCue(c));
      const rm = el("button", { class: "icon-btn", type: "button", "aria-label": "Remove " + c.name, text: "\u00D7" });
      rm.addEventListener("click", () => this.remove(c.id));
      const meta = c.codec ? c.codec + " \u00B7 " + fmtDur(c.duration) + (c.prefetch_stub && !c.uses_full_stream ? " \u00B7 short clip only" : "") : "";
      const li = el("li", { "data-id": c.id }, play,
        el("div", { class: "t" }, el("div", { class: "n", text: c.name }), el("div", { class: "b", text: c.bank }), el("div", { class: "b", text: meta })), rm);
      return li;
    }));
    this.markPlaying();
  },
};
els.basketClear.addEventListener("click", () => basket.clear());

els.exportBtn.addEventListener("click", async () => {
  const ids = [...basket.items.keys()];
  els.exportBtn.disabled = true;
  els.exportOut.replaceChildren(el("p", { class: "ok", text: "Exporting " + ids.length + (ids.length === 1 ? " cue" : " cues") + "…" }));
  try {
    const res = await fetch("/api/export", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ids }) });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || res.statusText);
    const ok = data.results.filter((r) => !r.error);
    const bad = data.results.filter((r) => r.error);
    const out = [el("p", { class: "ok" }, "Exported " + ok.length + " of " + data.results.length + " to ", el("code", { text: data.out_dir }))];
    if (bad.length) {
      const list = el("ul");
      for (const r of bad) list.append(el("li", { class: "bad", text: r.id + ": " + r.error }));
      out.push(list);
    }
    els.exportOut.replaceChildren(...out);
  } catch (err) {
    els.exportOut.replaceChildren(el("p", { class: "bad", text: "Export failed: " + err.message }));
  } finally {
    els.exportBtn.disabled = basket.items.size === 0;
  }
});

// ---- player ----------------------------------------------------------------

const WAVE_MAX_SECONDS = 180; // longer recordings skip the waveform (decoding them to draw it would take hundreds of MB)
const player = { cue: null, ready: false, ctrl: null, peaks: null, duration: 0, token: 0, ctx: null, raf: 0 };

function paintWave(canvas, color, w, h, dpr) {
  canvas.width = Math.max(1, Math.floor(w * dpr)); canvas.height = Math.max(1, Math.floor(h * dpr));
  canvas.style.width = w + "px"; // the played copy is clipped by its wrapper, so it must keep the full width
  const g = canvas.getContext("2d");
  g.clearRect(0, 0, canvas.width, canvas.height);
  g.fillStyle = color;
  const mid = canvas.height / 2;
  if (!player.peaks) { g.fillRect(0, mid - dpr / 2, canvas.width, dpr); return; }
  const cols = canvas.width, n = player.peaks.length / 2;
  for (let x = 0; x < cols; x++) {
    const i = Math.min(n - 1, Math.floor((x / cols) * n));
    const y1 = mid - player.peaks[2 * i + 1] * mid, y2 = mid - player.peaks[2 * i] * mid;
    g.fillRect(x, y1, 1, Math.max(1, y2 - y1));
  }
}

// The waveform is drawn twice: grey for what is still to come, accent for what has played.
function drawWave() {
  const w = els.wave.clientWidth, h = els.wave.clientHeight, dpr = window.devicePixelRatio || 1;
  const css = getComputedStyle(document.documentElement);
  paintWave(els.canvas, css.getPropertyValue("--wave").trim() || "#888", w, h, dpr);
  paintWave(els.canvasPlayed, css.getPropertyValue("--wave-played").trim() || "#36f", w, h, dpr);
  els.canvas.style.width = "100%";
}
window.addEventListener("resize", () => { drawWave(); drawLoop(); });
if (window.matchMedia) window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", drawWave);

function computePeaks(buf, cols) {
  const n = buf.length, chans = [];
  for (let c = 0; c < buf.numberOfChannels; c++) chans.push(buf.getChannelData(c));
  const out = new Float32Array(cols * 2);
  const per = Math.max(1, Math.floor(n / cols));
  for (let x = 0; x < cols; x++) {
    let lo = 0, hi = 0;
    const start = x * per, end = Math.min(n, start + per);
    for (const d of chans) for (let i = start; i < end; i++) { const v = d[i]; if (v < lo) lo = v; else if (v > hi) hi = v; }
    out[2 * x] = Math.max(-1, lo); out[2 * x + 1] = Math.min(1, hi);
  }
  return out;
}

// The loop region is a thin strip along the bottom edge, so it cannot be mistaken for playback progress.
function drawLoop() {
  const c = player.cue;
  if (!c || !c.loop || !player.duration || !c.sample_rate) { els.waveLoop.hidden = true; return; }
  const clamp = (v) => Math.max(0, Math.min(1, v));
  const a = clamp(c.loop.start / c.sample_rate / player.duration), b = clamp(c.loop.end / c.sample_rate / player.duration);
  els.waveLoop.hidden = false;
  els.waveLoop.style.left = a * 100 + "%";
  els.waveLoop.style.width = (b - a) * 100 + "%";
  els.waveLoop.title = "Loops from " + fmtClock(c.loop.start / c.sample_rate) + " to " + fmtClock(c.loop.end / c.sample_rate);
}

function tick() {
  cancelAnimationFrame(player.raf);
  const a = els.audio, d = player.duration || a.duration || 0;
  const frac = d ? Math.min(1, a.currentTime / d) : 0;
  els.wavePlayed.style.width = frac * 100 + "%";
  els.waveHead.style.left = "calc(" + frac * 100 + "% - 1px)";
  els.time.textContent = fmtClock(a.currentTime) + " / " + fmtClock(d);
  els.wave.setAttribute("aria-valuemax", String(Math.round(d)));
  els.wave.setAttribute("aria-valuenow", String(Math.round(a.currentTime)));
  if (!a.paused && !a.ended) player.raf = requestAnimationFrame(tick);
}

function setPlayingUI(playing) {
  els.toggleIcon.textContent = playing ? "❚❚" : "▶";
  els.toggle.setAttribute("aria-label", playing ? "Pause" : "Play");
}

async function playCue(c) {
  if (player.cue && player.cue.id === c.id && player.ready && !els.audio.ended) { togglePlay(); return; }
  const token = ++player.token;
  if (player.ctrl) player.ctrl.abort();
  const ctrl = (player.ctrl = new AbortController());
  // Drop the previous cue's audio first so it can never play under this cue's name.
  els.audio.pause();
  els.audio.removeAttribute("src");
  els.audio.load();
  player.ready = false;
  if (player.cue) markRows(player.cue.id, false);
  player.cue = c; player.peaks = null; player.duration = c.duration || 0;
  markRows(c.id, true);
  basket.markPlaying();
  els.nowName.textContent = c.name;
  els.nowMeta.textContent = c.bank + " \u00B7 " + c.codec + " \u00B7 " + fmtDur(c.duration) + (c.loop ? " \u00B7 loops" : "") + (c.prefetch_stub && !c.uses_full_stream ? " \u00B7 short clip only" : "");
  els.toggle.disabled = true; setPlayingUI(false);
  els.wavePlayed.style.width = "0"; els.waveHead.style.left = "0";
  drawWave(); drawLoop();
  setStatus("Loading\u2026");
  const url = "/audio/" + encodeURIComponent(c.id);
  try {
    // Ask for one byte first: the server renders and caches the cue and reports problems (no audio, unsupported codec).
    const probe = await fetch(url, { headers: { Range: "bytes=0-0" }, signal: ctrl.signal });
    if (!probe.ok) throw new Error((await probe.json().catch(() => ({}))).error || probe.statusText);
    if (token !== player.token) return;
    els.audio.src = url; // streamed with Range requests, so seeking does not download the whole file again
    els.audio.volume = Number(els.vol.value);
    player.ready = true;
    els.toggle.disabled = false;
    try { await els.audio.play(); if (token === player.token) setStatus(""); }
    catch (_) { if (token === player.token) setStatus("Press Play to start."); }
    if (token !== player.token) return;
    if (c.duration && c.duration <= WAVE_MAX_SECONDS) decodePeaks(url, token, ctrl.signal);
    else setStatus("Waveform skipped for long recordings. Playback and seeking work as usual.");
  } catch (err) {
    if (err.name === "AbortError" || token !== player.token) return;
    setStatus("Cannot play " + c.name + ": " + err.message, true);
    els.toggle.disabled = true;
  }
}

async function decodePeaks(url, token, signal) {
  try {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    player.ctx = player.ctx || new AC();
    const res = await fetch(url, { signal }); // served from the server's cache
    const buf = await player.ctx.decodeAudioData(await res.arrayBuffer());
    if (token !== player.token) return;
    player.peaks = computePeaks(buf, 1200);
    player.duration = buf.duration;
    drawWave(); drawLoop(); tick();
  } catch (err) {
    if (err.name !== "AbortError" && token === player.token) setStatus("Waveform unavailable: this browser cannot decode the audio for drawing. Playback still works.");
  }
}

function togglePlay() {
  const a = els.audio;
  if (!player.cue || !player.ready) return;
  if (a.paused || a.ended) { if (a.ended) a.currentTime = 0; a.play().catch((e) => setStatus(e.message, true)); } else a.pause();
}
els.toggle.addEventListener("click", togglePlay);
els.audio.addEventListener("play", () => { setPlayingUI(true); cancelAnimationFrame(player.raf); tick(); });
els.audio.addEventListener("pause", () => { setPlayingUI(false); tick(); });
els.audio.addEventListener("ended", () => { setPlayingUI(false); tick(); });
els.audio.addEventListener("loadedmetadata", () => { if (!player.peaks && isFinite(els.audio.duration)) { player.duration = els.audio.duration; drawLoop(); tick(); } });
els.audio.addEventListener("error", () => { if (player.cue && els.audio.getAttribute("src")) setStatus("The browser could not play " + player.cue.name + " (" + player.cue.codec + ").", true); });

function seekFromEvent(e) {
  const r = els.wave.getBoundingClientRect(), d = player.duration || els.audio.duration || 0;
  if (!d) return;
  els.audio.currentTime = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)) * d;
  tick();
}
let dragging = false;
els.wave.addEventListener("pointerdown", (e) => { dragging = true; els.wave.setPointerCapture(e.pointerId); seekFromEvent(e); });
els.wave.addEventListener("pointermove", (e) => { if (dragging) seekFromEvent(e); });
els.wave.addEventListener("pointerup", () => { dragging = false; });
els.wave.addEventListener("keydown", (e) => {
  const step = e.shiftKey ? 15 : 5, a = els.audio;
  if (e.key === "ArrowRight") { a.currentTime = Math.min(a.duration || 0, a.currentTime + step); }
  else if (e.key === "ArrowLeft") { a.currentTime = Math.max(0, a.currentTime - step); }
  else if (e.key === "Home") { a.currentTime = 0; }
  else return;
  e.preventDefault(); tick();
});

els.vol.addEventListener("input", () => { els.audio.volume = Number(els.vol.value); store("barsdig.volume", els.vol.value); });
document.addEventListener("keydown", (e) => {
  if (e.code === "Space" && (e.target === document.body || e.target === els.results)) { e.preventDefault(); togglePlay(); }
});

// ---- start -----------------------------------------------------------------

const savedVol = store("barsdig.volume");
if (savedVol != null) els.vol.value = savedVol;
els.audio.volume = Number(els.vol.value);
basket.init();
loadFacets();
loadPage(true);
