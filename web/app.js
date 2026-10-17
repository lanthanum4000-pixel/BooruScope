(() => {
  "use strict";

  const TOKEN = document.querySelector('meta[name="bs-token"]').content;
  const I18N = window.BS_I18N;
  const $ = (id) => document.getElementById(id);
  const el = {
    results: $("results"), spacer: $("spacer"), empty: $("empty"),
    missingHelp: $("missingHelp"), missingDir: $("missingDir"),
    rowCap: $("rowCap"), rowCapVal: $("rowCapVal"),
    idleUnload: $("idleUnload"), idleMinutes: $("idleMinutes"), idleCountdown: $("idleCountdown"), idleBox: $("idleBox"),
    offline: $("offline"), offlineBox: $("offlineBox"),
    alwaysDrift: $("alwaysDrift"), alwaysDriftBox: $("alwaysDriftBox"),
    variant: $("variant"), rerank: $("rerank"), rerankText: $("rerankText"), lang: $("lang"),
    encDot: $("encDot"), encText: $("encText"), encRetry: $("encRetry"),
    libDot: $("libDot"), libText: $("libText"), msgline: $("msgline"),
    overlay: $("dropOverlay"), toast: $("toast"),
    drift: $("drift"), driftScene: $("driftScene"),
  };

  const PAD = 8;
  const MIN_CARD = 110;
  const ROW_CAP_MIN = 3;
  const ROW_CAP_MAX = 6;
  const Q_ROWS = 2;
  const Q_COLS = 2;
  const BUFFER_ROWS = 2;

  let settings = { lang: "", rowCap: 4, variant: 0, rerank: true, idleUnload: false, idleMinutes: 10, offline: true, alwaysDrift: false };
  let idleDeadline = 0;
  let fx = null;
  let revealed = new Set();
  let scrollDir = "down";
  let lastScrollTop = 0;
  const RISE = { colStep: 0.05, rowStep: 0.08, staggerRows: 4, nearTop: 100 };
  let freshQuery = false;
  const DRIFT = { keep: 30, perColumn: 9, minSources: 12, durations: [42, 49, 45, 53, 46], storeKey: "booruscope.driftRows", fadeMs: 1300 };
  let driftRows = loadDriftRows();
  let driftBuiltFrom = null;
  let driftOn = false;
  let driftStopTimer = 0;
  const FX = { aber: 2, cols: 6, breakEvery: 250, liftMs: 980, liftShift: 3, liftScale: 1.03, glitchEvery: 3200, glitchFrac: 0.08, glitchShift: 14, grain: 0.02, wobble: 0.5, wobbleHz: 1, minMs: 900, fadeMs: 1300 };
  let idleTimer = 0;
  let liveRowCap = 4;
  let lang = "en";
  let state = null;
  let results = [];
  let query = null;
  let searchSeq = 0;
  let searching = false;
  let lastSearch = null;
  let message = null;
  let cards = new Map();
  let qtile = null;
  let renderQueued = false;
  let pollTimer = 0;
  let toastTimer = 0;

  function t(key, vars) {
    let s = (I18N[lang] && I18N[lang][key]) ?? I18N.en[key] ?? key;
    if (vars) s = s.replace(/\{(\w+)\}/g, (_, k) => (vars[k] ?? ""));
    return s;
  }

  function withToken(path) {
    return path + (path.includes("?") ? "&" : "?") + "t=" + encodeURIComponent(TOKEN);
  }

  async function api(path, opts = {}) {
    const headers = Object.assign({ "X-BooruScope-Token": TOKEN }, opts.headers || {});
    let r;
    try {
      r = await fetch(path, Object.assign({}, opts, { headers }));
    } catch (e) {
      throw { code: "network" };
    }
    const ct = r.headers.get("content-type") || "";
    const body = ct.includes("json") ? await r.json().catch(() => null) : null;
    if (!r.ok) throw { code: (body && body.error) || "generic", detail: body && body.detail };
    return body;
  }

  function saveSettings() {
    api("/api/settings", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(settings) })
      .then((s) => { if (s) settings = s; })
      .catch(() => {});
  }

  function errText(err) {
    const key = "err." + ((err && err.code) || "generic");
    return I18N.en[key] ? t(key) : t("err.generic");
  }

  function showToast(text) {
    el.toast.textContent = text;
    el.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { el.toast.hidden = true; }, 2200);
  }

  function setMessage(m) {
    message = m;
    renderMessage();
  }

  function renderMessage() {
    el.msgline.classList.toggle("error", !!(message && message.error));
    if (!message) { el.msgline.textContent = ""; return; }
    if (message.error) { el.msgline.textContent = errText(message.error); return; }
    if (message.running) { el.msgline.textContent = t("search.running"); return; }
    if (message.done) {
      const d = message.done;
      el.msgline.textContent = t("search.done", { n: d.results.length, enc: d.encodeMs, search: d.searchMs, rr: d.reranked ? t("search.reranked") : "" });
    }
  }

  function applyLang() {
    document.documentElement.lang = { zh: "zh-Hant", ja: "ja" }[lang] || "en";
    document.title = t("app.title");
    document.querySelectorAll("[data-i18n]").forEach((n) => { n.textContent = t(n.dataset.i18n); });
    el.lang.querySelectorAll("button").forEach((b) => {
      b.classList.toggle("active", b.dataset.lang === lang);
      b.setAttribute("aria-pressed", b.dataset.lang === lang ? "true" : "false");
    });
    el.rowCapVal.textContent = t("rowCap.value", { n: liveRowCap });
    renderState();
    renderMessage();
    cards.forEach((c, i) => fillCardText(c, results[i]));
    if (qtile) {
      qtile.querySelector(".qlabel").textContent = t("query.label");
      qtile.querySelector(".qlens").title = t("qlens.title");
    }
  }

  function applyIdle() {
    el.idleUnload.checked = !!settings.idleUnload;
    el.idleMinutes.value = String(settings.idleMinutes || 10);
    el.idleMinutes.disabled = !settings.idleUnload;
    el.offline.checked = !!settings.offline;
    el.alwaysDrift.checked = !!settings.alwaysDrift;
    el.alwaysDriftBox.title = t("drift.title");
    el.idleBox.title = t("idle.title");
    el.offlineBox.title = t("offline.title");
    tickCountdown();
  }

  function clock(ms) {
    const sec = Math.max(0, Math.ceil(ms / 1000));
    const m = Math.floor(sec / 60), s = sec % 60;
    return m + ":" + String(s).padStart(2, "0");
  }

  function tickCountdown() {
    clearTimeout(idleTimer);
    if (!settings.idleUnload || !idleDeadline) { el.idleCountdown.textContent = ""; return; }
    const left = idleDeadline - Date.now();
    el.idleCountdown.textContent = t("idle.remaining", { t: clock(left) });
    if (left <= 0) { setTimeout(poll, 1500); return; }
    idleTimer = setTimeout(tickCountdown, 1000);
  }

  function applyRowCap(n) {
    n = Math.max(ROW_CAP_MIN, Math.min(ROW_CAP_MAX, parseInt(n, 10) || 4));
    liveRowCap = n;
    el.rowCap.value = String(n);
    el.rowCapVal.textContent = t("rowCap.value", { n });
    return n;
  }

  function renderState() {
    if (!state) return;
    const enc = state.encoder;
    let encText;
    if (enc.state === "error") {
      encText = t(I18N.en["enc.err." + enc.code] ? "enc.err." + enc.code : "enc.err.failed");
    } else {
      const encKey = "enc." + enc.state;
      encText = I18N.en[encKey] ? t(encKey, { step: enc.step || "", device: enc.device || "" }) : enc.state;
      const pct = /(\d{1,3})%/.exec(enc.detail || "");
      if (pct && enc.state === "loading") encText += " " + pct[1] + "%";
      if (enc.state === "install" && enc.phase === "download" && enc.total > 0) encText += " " + Math.floor((100 * enc.done) / enc.total) + "%";
    }
    el.encText.textContent = encText;
    el.encText.title = enc.detail || "";
    el.encDot.className = "dot " + (enc.state === "ready" ? "ok" : enc.state === "error" ? "err" : enc.state === "unloaded" ? "" : "busy");
    el.encRetry.hidden = enc.state !== "error";
    idleDeadline = (state.idle && state.idle.unloadAt) || 0;
    tickCountdown();
    updateDrift();

    const lib = state.library;
    el.libText.textContent = t("lib." + lib.state, { m: lib.variant, rows: (lib.rows || 0).toLocaleString() });
    el.libText.title = lib.detail || lib.dir || "";
    el.libDot.className = "dot " + (lib.state === "ready" ? "ok" : lib.state === "loading" ? "busy" : "err");

    el.variant.textContent = "";
    if (!lib.available || !lib.available.length) {
      const o = document.createElement("option");
      o.textContent = "—";
      el.variant.appendChild(o);
      el.variant.disabled = true;
    } else {
      el.variant.disabled = false;
      for (const m of lib.available) {
        const o = document.createElement("option");
        o.value = String(m);
        o.textContent = t("variant.option", { m });
        el.variant.appendChild(o);
      }
      el.variant.value = String(lib.variant || settings.variant || lib.available[lib.available.length - 1]);
    }

    const canRerank = lib.state !== "ready" || lib.hasRerank;
    el.rerank.disabled = !canRerank;
    el.rerank.checked = settings.rerank && canRerank;
    el.rerankText.textContent = canRerank ? t("rerank.label") : t("rerank.unavailable");

    el.missingHelp.hidden = lib.state !== "missing";
    el.missingDir.textContent = lib.dir || "";
  }

  function busy() {
    if (!state) return true;
    const e = state.encoder.state;
    return searching || (e !== "ready" && e !== "error" && e !== "unloaded") || state.library.state === "loading";
  }

  async function poll() {
    clearTimeout(pollTimer);
    try {
      state = await api("/api/state");
      renderState();
    } catch (e) {
      el.encText.textContent = t("err.network");
      el.encDot.className = "dot err";
    }
    pollTimer = setTimeout(poll, busy() ? 1000 : 5000);
  }

  function metrics() {
    const innerW = Math.max(1, el.results.clientWidth);
    const cols = liveRowCap;
    const cardW = Math.max(MIN_CARD, Math.floor(innerW / cols));
    const thumb = cardW - 2 * PAD;
    const infoH = 50;
    return { cols, cardW, thumb, cardH: thumb + infoH + 2 * PAD };
  }

  function topCap(m) {
    return Q_ROWS * (m.cols - Q_COLS);
  }

  function ordinalAt(r, c, m) {
    if (!query) return r * m.cols + c;
    if (r < Q_ROWS && c < Q_COLS) return -1;
    if (r < Q_ROWS) return r * (m.cols - Q_COLS) + (c - Q_COLS);
    return topCap(m) + (r - Q_ROWS) * m.cols + c;
  }

  function totalRows(m) {
    const n = results.length;
    if (!query) return Math.ceil(n / m.cols);
    if (n <= topCap(m)) return Q_ROWS;
    return Q_ROWS + Math.ceil((n - topCap(m)) / m.cols);
  }

  function tier(score) {
    if (score >= 0.93) return { cls: "high", key: "score.high" };
    if (score >= 0.85) return { cls: "mid", key: "score.mid" };
    return { cls: "low", key: "score.low" };
  }

  function fillCardText(card, r) {
    if (!r) return;
    const tr = tier(r.score);
    const s = r.score.toFixed(3);
    card.title = t("card.title", { id: r.id });
    card.querySelector(".estatus").textContent = s + " · " + t(tr.key);
    card.querySelector(".estatus").className = "estatus " + tr.cls;
    card.querySelector(".nothumb").textContent = t("card.noThumb");
  }

  function makeCard(i) {
    const r = results[i];
    const card = document.createElement("div");
    card.className = "card";
    card.dataset.i = String(i);
    card.innerHTML = '<div class="thumb"><img alt="" draggable="false"><span class="nothumb"></span></div><div class="name"></div><div class="estatus"></div>';
    const img = card.querySelector("img");
    img.addEventListener("load", () => { card.classList.add("loaded"); rememberDriftRow(r.row); maybeReveal(card); });
    img.addEventListener("error", () => { card.classList.add("failed"); maybeReveal(card); });
    img.src = withToken("/api/thumb/" + r.row);
    card.querySelector(".name").textContent = "#" + r.id;
    fillCardText(card, r);
    return card;
  }

  function ensureQtile() {
    if (!query) {
      if (qtile) { qtile.remove(); qtile = null; }
      return;
    }
    if (!qtile) {
      qtile = document.createElement("div");
      qtile.className = "qtile";
      qtile.innerHTML = '<div class="qframe"><img alt="" draggable="false"><div class="qgrid" hidden></div>' +
        '<button type="button" class="qact qlens"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
        '<circle cx="10.5" cy="10.5" r="6.5"/><path d="M4 10.5h13"/><path d="M10.5 4c3 3.6 3 9.4 0 13c-3-3.6-3-9.4 0-13"/><path d="m15.5 15.5 5.5 5.5"/></svg></button>' +
        '</div><span class="qlabel"></span>';
      qtile.querySelector(".qlabel").textContent = t("query.label");
      qtile.querySelector(".qframe > img").addEventListener("load", layoutGrid);
      qtile.querySelector(".qlens").addEventListener("click", openLens);
      el.spacer.appendChild(qtile);
    }
    const img = qtile.querySelector(".qframe > img");
    if (query.src && img.getAttribute("src") !== query.src) img.src = query.src;
    if (!query.src) img.removeAttribute("src");
    const lens = qtile.querySelector(".qlens");
    lens.disabled = !query.id;
    lens.title = t("qlens.title");
    if (freshQuery) {
      freshQuery = false;
      qtile.classList.remove("pq-in");
      void qtile.offsetWidth;
      qtile.classList.add("pq-in");
    }
  }

  function imageRect(frame, img) {
    const bw = frame.clientWidth, bh = frame.clientHeight;
    if (!bw || !bh || !img.naturalWidth) return null;
    const sc = Math.min(bw / img.naturalWidth, bh / img.naturalHeight);
    const w = Math.round(img.naturalWidth * sc), h = Math.round(img.naturalHeight * sc);
    return { x: Math.round((bw - w) / 2), y: Math.round((bh - h) / 2), w, h };
  }

  function layoutGrid() {
    if (!qtile) return;
    const frame = qtile.querySelector(".qframe");
    const grid = frame.querySelector(".qgrid");
    const r = imageRect(frame, frame.querySelector(":scope > img"));
    if (!r) { grid.hidden = true; return; }
    grid.style.left = r.x + "px";
    grid.style.top = r.y + "px";
    grid.style.width = r.w + "px";
    grid.style.height = r.h + "px";
    grid.style.setProperty("--cx", r.w / 16 + "px");
    grid.style.setProperty("--cy", r.h / 16 + "px");
    grid.style.setProperty("--cx4", r.w / 4 + "px");
    grid.style.setProperty("--cy4", r.h / 4 + "px");
    grid.hidden = false;
  }

  function openLens() {
    if (!query || !query.id) return;
    api("/api/google", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query: query.id }) })
      .then(() => showToast(t("lens.opened")))
      .catch((err) => showToast(errText(err)));
  }

  let menu = null;

  function closeMenu() {
    if (menu) { menu.remove(); menu = null; }
  }

  function openMenu(x, y, r) {
    closeMenu();
    menu = div("ctxmenu");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "ctxitem";
    b.textContent = t("ctx.search");
    b.addEventListener("click", () => { closeMenu(); searchRow(r.row); });
    menu.appendChild(b);
    document.body.appendChild(menu);
    menu.style.left = Math.max(4, Math.min(x, window.innerWidth - menu.offsetWidth - 4)) + "px";
    menu.style.top = Math.max(4, Math.min(y, window.innerHeight - menu.offsetHeight - 4)) + "px";
    b.focus();
  }

  function wireMenu() {
    document.addEventListener("contextmenu", (e) => {
      if (e.target.closest("input, textarea")) return;
      e.preventDefault();
      const card = e.target.closest(".card");
      const r = card && results[parseInt(card.dataset.i, 10)];
      if (r) openMenu(e.clientX, e.clientY, r);
      else closeMenu();
    });
    document.addEventListener("mousedown", (e) => { if (menu && !menu.contains(e.target)) closeMenu(); });
    document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeMenu(); });
    window.addEventListener("blur", closeMenu);
    window.addEventListener("resize", closeMenu);
    el.results.addEventListener("scroll", closeMenu, { passive: true });
  }

  function loadDriftRows() {
    try {
      const v = JSON.parse(localStorage.getItem(DRIFT.storeKey) || "[]");
      return Array.isArray(v) ? v.filter((n) => Number.isInteger(n) && n >= 0).slice(0, DRIFT.keep) : [];
    } catch (e) {
      return [];
    }
  }

  function rememberDriftRow(row) {
    const i = driftRows.indexOf(row);
    if (i === 0) return;
    if (i > 0) driftRows.splice(i, 1);
    driftRows.unshift(row);
    if (driftRows.length > DRIFT.keep) driftRows.length = DRIFT.keep;
    try { localStorage.setItem(DRIFT.storeKey, JSON.stringify(driftRows)); } catch (e) {}
  }

  function driftSources() {
    const rows = driftRows.slice();
    const total = (state && state.library && state.library.rows) || 0;
    for (let tries = 0; rows.length < DRIFT.minSources && total > 0 && tries < 200; tries++) {
      const r = Math.floor(Math.random() * total);
      if (!rows.includes(r)) rows.push(r);
    }
    return rows;
  }

  function buildDrift() {
    const rows = driftSources();
    if (!rows.length) return false;
    driftBuiltFrom = driftRows.join(",");
    const cols = Math.max(5, Math.min(10, Math.round(el.drift.parentElement.clientWidth / 150)));
    el.driftScene.style.setProperty("--dw-cols", String(cols));
    const frag = document.createDocumentFragment();
    for (let c = 0; c < cols; c++) {
      const col = div("drift-col" + (c % 2 ? " down" : ""));
      const track = div("drift-track");
      track.style.setProperty("--dw-duration", DRIFT.durations[c % DRIFT.durations.length] + "s");
      track.style.setProperty("--dw-delay", -c * 3.7 + "s");
      for (let copy = 0; copy < 2; copy++) {
        const set = div("drift-set");
        for (let r = 0; r < DRIFT.perColumn; r++) {
          const img = new Image();
          img.className = "drift-tile";
          img.alt = "";
          img.decoding = "async";
          img.draggable = false;
          img.src = withToken("/api/thumb/" + rows[(c + r * cols) % rows.length]);
          img.addEventListener("error", () => { img.style.visibility = "hidden"; });
          set.appendChild(img);
        }
        track.appendChild(set);
      }
      col.appendChild(track);
      frag.appendChild(col);
    }
    el.driftScene.replaceChildren(frag);
    return true;
  }

  function refreshDrift() {
    if (!driftOn || driftRows.join(",") === driftBuiltFrom) return;
    el.drift.classList.remove("ready");
    setTimeout(() => {
      if (!driftOn) return;
      buildDrift();
      el.drift.classList.add("ready");
    }, DRIFT.fadeMs);
  }

  function updateDrift() {
    const cold = searching && !!state && state.encoder.state !== "ready";
    if ((settings.alwaysDrift || cold) && !driftOn) driftOn = buildDrift();
    else if (driftOn && !settings.alwaysDrift && !searching) driftOn = false;
    const wasOn = el.drift.classList.contains("ready");
    el.drift.classList.toggle("ready", driftOn);
    if (driftOn) {
      clearTimeout(driftStopTimer);
      driftStopTimer = 0;
      el.drift.classList.toggle("paused", document.hidden);
    } else if (wasOn) {
      driftStopTimer = setTimeout(() => {
        driftStopTimer = 0;
        el.drift.classList.add("paused");
      }, DRIFT.fadeMs);
    } else if (!driftStopTimer) {
      el.drift.classList.add("paused");
    }
  }

  function div(cls) {
    const d = document.createElement("div");
    d.className = cls;
    return d;
  }

  function fxFilters() {
    if (document.getElementById("bsChanR")) return;
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("width", "0");
    svg.setAttribute("height", "0");
    svg.style.position = "absolute";
    for (const [id, values] of [["bsChanR", "1 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 1 0"], ["bsChanGB", "0 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 1 0"]]) {
      const f = document.createElementNS(ns, "filter");
      f.setAttribute("id", id);
      const m = document.createElementNS(ns, "feColorMatrix");
      m.setAttribute("type", "matrix");
      m.setAttribute("values", values);
      f.appendChild(m);
      svg.appendChild(f);
    }
    document.body.appendChild(svg);
  }

  function fxStart() {
    if (!qtile || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const frame = qtile.querySelector(".qframe");
    const img = frame.querySelector(":scope > img");
    if (!img.getAttribute("src")) return;
    if (!img.complete || !img.naturalWidth) {
      img.addEventListener("load", () => { if (searching) fxStart(); }, { once: true });
      return;
    }
    const rect = imageRect(frame, img);
    if (!rect) return;
    const { w, h } = rect;
    const key = img.src + "|" + w + "x" + h;
    if (fx && fx.key === key && !fx.ending) return;
    fxKill();
    fxFilters();
    const box = div("qfx");
    box.style.left = rect.x + "px";
    box.style.top = rect.y + "px";
    box.style.width = w + "px";
    box.style.height = h + "px";
    const wrap = div("qfx-wrap");
    box.appendChild(wrap);
    for (const [cls, dx] of [["qfx-red", -FX.aber], ["qfx-cyan", FX.aber]]) {
      const g = new Image();
      g.className = "qfx-ghost " + cls;
      g.src = img.src;
      g.style.transform = "translateX(" + dx + "px)";
      wrap.appendChild(g);
    }
    const tiles = div("qfx-tiles");
    const cols = FX.cols, rows = Math.max(1, Math.round((cols * h) / w));
    tiles.style.gridTemplateColumns = "repeat(" + cols + ",1fr)";
    tiles.style.gridTemplateRows = "repeat(" + rows + ",1fr)";
    const cw = w / cols, ch = h / rows, cells = [];
    for (let i = 0; i < cols * rows; i++) {
      const c = div("qfx-tile");
      c.style.backgroundImage = 'url("' + img.src + '")';
      c.style.backgroundSize = w + "px " + h + "px";
      c.style.backgroundPosition = -((i % cols) * cw) + "px " + -(Math.floor(i / cols) * ch) + "px";
      tiles.appendChild(c);
      cells.push(c);
    }
    wrap.appendChild(tiles);
    for (const cls of ["qfx-grid", "qfx-scan", "qfx-band"]) wrap.appendChild(div(cls));
    const grain = document.createElement("canvas");
    grain.className = "qfx-grain";
    grain.width = 160;
    grain.height = 120;
    wrap.appendChild(grain);
    const tear = div("qfx-tear");
    const tearImg = new Image();
    tearImg.src = img.src;
    tear.appendChild(tearImg);
    wrap.appendChild(tear);
    box.appendChild(div("qfx-vign"));
    frame.insertBefore(box, frame.querySelector(".qgrid"));
    img.classList.add("fx-hidden");
    const f = (fx = { box, img, key, ending: false, raf: 0, timers: [], started: performance.now() });
    const wobble = (ts) => {
      const tt = (ts - f.started) / 1000, hz = FX.wobbleHz;
      const x = FX.wobble * (0.6 * Math.sin(tt * 2 * Math.PI * hz) + 0.4 * Math.sin(tt * 2 * Math.PI * hz * 2.33 + 1.7));
      const y = FX.wobble * 0.35 * Math.sin(tt * 2 * Math.PI * hz * 1.13 + 0.8);
      wrap.style.transform = "translate(" + x.toFixed(2) + "px," + y.toFixed(2) + "px)";
      f.raf = requestAnimationFrame(wobble);
    };
    f.raf = requestAnimationFrame(wobble);
    f.timers.push(setInterval(() => {
      const c = cells[Math.floor(Math.random() * cells.length)];
      if (c.classList.contains("lift")) return;
      const dx = (Math.random() * 2 - 1) * FX.liftShift, dy = (Math.random() * 2 - 1) * FX.liftShift;
      c.style.transform = "translate(" + dx + "px," + dy + "px) scale(" + FX.liftScale + ")";
      c.classList.add("lift");
      setTimeout(() => { c.classList.remove("lift"); c.style.transform = ""; }, FX.liftMs);
    }, FX.breakEvery));
    f.timers.push(setInterval(() => {
      if (f.ending) return;
      const bandH = h * FX.glitchFrac, y = Math.random() * (h - bandH);
      tear.style.top = y + "px";
      tear.style.height = bandH + "px";
      tear.style.display = "block";
      tearImg.style.top = -y + "px";
      tearImg.style.height = h + "px";
      tearImg.style.transform = "translateX(" + (Math.random() < 0.5 ? -1 : 1) * FX.glitchShift + "px)";
      setTimeout(() => { tear.style.display = "none"; }, 120);
    }, FX.glitchEvery));
    const gctx = grain.getContext("2d");
    const gid = gctx.createImageData(160, 120);
    f.timers.push(setInterval(() => {
      const d = gid.data;
      for (let i = 0; i < d.length; i += 4) {
        d[i] = d[i + 1] = d[i + 2] = (Math.random() * 255) | 0;
        d[i + 3] = Math.random() < 0.5 ? FX.grain * 255 : 0;
      }
      gctx.putImageData(gid, 0, 0);
    }, 80));
  }

  function fxEnd() {
    const f = fx;
    if (!f || f.ending) return;
    const wait = Math.max(0, FX.minMs - (performance.now() - f.started));
    setTimeout(() => {
      if (fx !== f || f.ending) return;
      f.ending = true;
      f.timers.forEach(clearInterval);
      f.timers = [];
      cancelAnimationFrame(f.raf);
      f.box.classList.add("end");
      f.img.classList.remove("fx-hidden");
      setTimeout(() => { if (fx === f) fxKill(); }, FX.fadeMs);
    }, wait);
  }

  function fxKill() {
    const f = fx;
    if (!f) return;
    f.timers.forEach(clearInterval);
    cancelAnimationFrame(f.raf);
    f.box.remove();
    f.img.classList.remove("fx-hidden");
    fx = null;
  }

  function render() {
    renderQueued = false;
    const m = metrics();
    const rows = totalRows(m);
    el.spacer.style.height = rows * m.cardH + "px";
    el.empty.hidden = !!query || results.length > 0;
    ensureQtile();
    if (qtile) {
      const c = Math.min(Q_COLS, m.cols);
      qtile.style.left = "0px";
      qtile.style.top = "0px";
      qtile.style.width = c * m.cardW + "px";
      qtile.style.height = Q_ROWS * m.cardH + "px";
    }
    const top = el.results.scrollTop;
    const h = el.results.clientHeight;
    const r0 = Math.max(0, Math.floor(top / m.cardH) - BUFFER_ROWS);
    const r1 = Math.min(rows - 1, Math.ceil((top + h) / m.cardH) + BUFFER_ROWS);
    const keep = new Set();
    for (let r = r0; r <= r1; r++) {
      for (let c = 0; c < m.cols; c++) {
        const i = ordinalAt(r, c, m);
        if (i < 0 || i >= results.length) continue;
        keep.add(i);
        let card = cards.get(i);
        if (!card) {
          card = makeCard(i);
          if (!revealed.has(i)) card.classList.add("pending");
          cards.set(i, card);
          el.spacer.appendChild(card);
        }
        card._col = c;
        card._row = r;
        card.style.left = c * m.cardW + "px";
        card.style.top = r * m.cardH + "px";
        card.style.width = m.cardW + "px";
        card.style.height = m.cardH + "px";
        const th = card.firstChild;
        th.style.width = m.thumb + "px";
        th.style.height = m.thumb + "px";
      }
    }
    for (const [i, card] of cards) {
      if (!keep.has(i)) { card.remove(); cards.delete(i); }
      else maybeReveal(card);
    }
    layoutGrid();
    if (searching) fxStart();
  }

  function queueRender() {
    if (renderQueued) return;
    renderQueued = true;
    requestAnimationFrame(render);
  }

  function clearResults() {
    for (const card of cards.values()) card.remove();
    cards = new Map();
    results = [];
    revealed = new Set();
    scrollDir = "down";
    lastScrollTop = 0;
  }

  function maybeReveal(card) {
    const i = parseInt(card.dataset.i, 10);
    if (revealed.has(i)) return;
    if (!card.classList.contains("loaded") && !card.classList.contains("failed")) return;
    const top = el.results.scrollTop, bot = top + el.results.clientHeight;
    const y = parseFloat(card.style.top) || 0, h = parseFloat(card.style.height) || 0;
    if (y >= bot || y + h <= top) return;
    revealed.add(i);
    card.classList.remove("pending");
    if (scrollDir === "up") return;
    const row = card._row || 0;
    const delay = (card._col || 0) * RISE.colStep + (row < RISE.staggerRows && top <= RISE.nearTop ? row * RISE.rowStep : 0);
    card.style.animationDelay = delay.toFixed(2) + "s";
    card.classList.add("rise");
    card.addEventListener("animationend", function done(e) {
      if (e.animationName !== "card-rise") return;
      card.classList.remove("rise");
      card.style.animationDelay = "";
      card.removeEventListener("animationend", done);
    });
  }

  function setQuery(src) {
    fxKill();
    if (query && query.blob && !hist.some((h) => h.query && h.query.src === query.src)) URL.revokeObjectURL(query.src);
    query = { src, blob: !!src && src.startsWith("blob:") };
    freshQuery = true;
  }

  const HIST_MAX = 30;
  let hist = [{ id: 0, query: null, results: [], done: null, scroll: 0 }];
  let histPos = 0;
  let histSeq = 0;

  function histSaveScroll() {
    if (hist[histPos]) hist[histPos].scroll = el.results.scrollTop;
  }

  function histPush() {
    const entry = { id: ++histSeq, query: Object.assign({}, query), results, done: lastSearch, scroll: 0 };
    const dropped = hist.splice(histPos + 1);
    hist.push(entry);
    while (hist.length > HIST_MAX + 1) dropped.push(hist.splice(1, 1)[0]);
    for (const h of dropped) {
      if (h.query && h.query.blob && !hist.some((k) => k.query && k.query.src === h.query.src)) URL.revokeObjectURL(h.query.src);
    }
    histPos = hist.length - 1;
    try { history.pushState({ bs: entry.id }, ""); } catch (e) {}
  }

  function histRestore(entry) {
    searchSeq++;
    searching = false;
    fxKill();
    closeMenu();
    for (const card of cards.values()) card.remove();
    cards = new Map();
    query = entry.query ? Object.assign({}, entry.query) : null;
    results = entry.results;
    lastSearch = entry.done;
    revealed = new Set(results.map((_, i) => i));
    freshQuery = !!query;
    setMessage(entry.done ? { done: entry.done } : null);
    updateDrift();
    render();
    el.results.scrollTop = entry.scroll || 0;
    lastScrollTop = el.results.scrollTop;
    render();
  }

  function wireHistory() {
    try { history.replaceState({ bs: 0 }, ""); } catch (e) {}
    window.addEventListener("popstate", (e) => {
      const id = e.state && typeof e.state.bs === "number" ? e.state.bs : -1;
      const i = hist.findIndex((h) => h.id === id);
      if (i < 0) return;
      histSaveScroll();
      histPos = i;
      histRestore(hist[i]);
    });
  }

  async function runSearch(send, src) {
    const seq = ++searchSeq;
    histSaveScroll();
    clearResults();
    setQuery(src);
    searching = true;
    el.results.scrollTop = 0;
    setMessage({ running: true });
    render();
    idleDeadline = 0;
    tickCountdown();
    updateDrift();
    setTimeout(poll, 150);
    try {
      const res = await send(() => seq === searchSeq);
      if (seq !== searchSeq) return;
      if (!query.src) query.src = withToken("/api/query/" + res.query);
      query.id = res.query;
      results = res.results || [];
      lastSearch = res;
      setMessage({ done: res });
      histPush();
    } catch (e) {
      if (seq !== searchSeq) return;
      setMessage({ error: e });
      if (e.code === "encoder_not_ready" || e.code === "no_index") poll();
    } finally {
      if (seq === searchSeq) {
        searching = false;
        updateDrift();
        if (settings.alwaysDrift) setTimeout(refreshDrift, 2500);
        render();
        fxEnd();
        poll();
      }
    }
  }

  function searchBlob(blob) {
    const src = URL.createObjectURL(blob);
    return runSearch(() => api("/api/search/image", { method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: blob }), src);
  }

  function searchURL(url) {
    const json = { method: "POST", headers: { "Content-Type": "application/json" } };
    return runSearch(async (current) => {
      const f = await api("/api/fetch", Object.assign({ body: JSON.stringify({ url }) }, json));
      if (current()) {
        query.src = withToken("/api/query/" + f.query);
        query.id = f.query;
        render();
      }
      return api("/api/search/query", Object.assign({ body: JSON.stringify({ query: f.query }) }, json));
    }, null);
  }

  function searchRow(row) {
    const json = { method: "POST", headers: { "Content-Type": "application/json" } };
    return runSearch(async (current) => {
      const f = await api("/api/fetch/row", Object.assign({ body: JSON.stringify({ row }) }, json));
      if (current()) {
        query.src = withToken("/api/query/" + f.query);
        query.id = f.query;
        render();
      }
      return api("/api/search/query", Object.assign({ body: JSON.stringify({ query: f.query }) }, json));
    }, null);
  }

  function urlFromDrop(dt) {
    const ok = (u) => !!u && /^(https?:|data:image\/)/i.test(u.trim());
    const html = dt.getData("text/html");
    if (html) {
      const doc = new DOMParser().parseFromString(html, "text/html");
      const img = doc.querySelector("img[src]");
      if (img && ok(img.getAttribute("src"))) return img.getAttribute("src").trim();
    }
    const list = (dt.getData("text/uri-list") || "").split(/\r?\n/).map((s) => s.trim()).find((s) => s && !s.startsWith("#"));
    if (ok(list)) return list;
    const plain = (dt.getData("text/plain") || "").trim();
    if (ok(plain)) return plain;
    return null;
  }

  async function handleDrop(dt) {
    const files = Array.from(dt.files || []).filter((f) => !f.type || f.type.startsWith("image/"));
    if (files.length) return searchBlob(files[0]);
    const url = urlFromDrop(dt);
    if (!url) { setMessage({ error: { code: "nothing" } }); return; }
    let parsed = null;
    try { parsed = new URL(url, location.href); } catch (e) {}
    if (url.startsWith("data:") || (parsed && parsed.origin === location.origin)) {
      try {
        const r = await fetch(parsed && parsed.origin === location.origin ? withToken(parsed.pathname) : url);
        if (!r.ok) throw new Error();
        return searchBlob(await r.blob());
      } catch (e) {
        setMessage({ error: { code: "bad_image" } });
        return;
      }
    }
    return searchURL(url);
  }

  function hasPayload(e) {
    const types = Array.from((e.dataTransfer && e.dataTransfer.types) || []);
    return types.some((x) => x === "Files" || x === "text/uri-list" || x === "text/html" || x === "text/plain");
  }

  function wireDrop() {
    let depth = 0;
    window.addEventListener("dragenter", (e) => {
      if (!hasPayload(e)) return;
      e.preventDefault();
      depth++;
      el.overlay.classList.add("show");
    });
    window.addEventListener("dragover", (e) => {
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
    });
    window.addEventListener("dragleave", () => {
      depth = Math.max(0, depth - 1);
      if (!depth) el.overlay.classList.remove("show");
    });
    window.addEventListener("drop", (e) => {
      e.preventDefault();
      depth = 0;
      el.overlay.classList.remove("show");
      if (e.dataTransfer) handleDrop(e.dataTransfer);
    });
  }

  function wireControls() {
    el.rowCap.addEventListener("input", () => { applyRowCap(el.rowCap.value); render(); });
    el.rowCap.addEventListener("change", () => {
      settings.rowCap = applyRowCap(el.rowCap.value);
      saveSettings();
    });
    el.idleUnload.addEventListener("change", () => {
      settings.idleUnload = el.idleUnload.checked;
      applyIdle();
      saveSettings();
      setTimeout(poll, 200);
    });
    el.idleMinutes.addEventListener("change", () => {
      const n = Math.max(1, Math.min(1440, parseInt(el.idleMinutes.value, 10) || 10));
      settings.idleMinutes = n;
      applyIdle();
      saveSettings();
      setTimeout(poll, 200);
    });
    el.offline.addEventListener("change", () => {
      settings.offline = el.offline.checked;
      saveSettings();
    });
    el.alwaysDrift.addEventListener("change", () => {
      settings.alwaysDrift = el.alwaysDrift.checked;
      saveSettings();
      updateDrift();
    });
    el.variant.addEventListener("change", () => {
      settings.variant = parseInt(el.variant.value, 10) || 0;
      saveSettings();
      setTimeout(poll, 150);
    });
    el.rerank.addEventListener("change", () => {
      settings.rerank = el.rerank.checked;
      saveSettings();
    });
    el.lang.addEventListener("click", (e) => {
      const b = e.target.closest("button[data-lang]");
      if (!b) return;
      settings.lang = lang = b.dataset.lang;
      applyLang();
      saveSettings();
    });
    el.encRetry.addEventListener("click", () => {
      api("/api/encoder/restart", { method: "POST" }).catch(() => {}).finally(poll);
    });
    el.spacer.addEventListener("dblclick", (e) => {
      const card = e.target.closest(".card");
      if (!card) return;
      const r = results[parseInt(card.dataset.i, 10)];
      if (!r) return;
      api("/api/open", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ row: r.row }) })
        .then(() => showToast(t("open.done")))
        .catch((err) => showToast(errText(err)));
    });
    el.results.addEventListener("scroll", () => {
      const st = el.results.scrollTop;
      if (st !== lastScrollTop) scrollDir = st < lastScrollTop ? "up" : "down";
      lastScrollTop = st;
      queueRender();
    }, { passive: true });
    new ResizeObserver(queueRender).observe(el.results);
  }


  function heartbeat() {
    const ping = () => fetch("/api/ping", { method: "POST", headers: { "X-BooruScope-Token": TOKEN } }).catch(() => {});
    ping();
    setInterval(ping, 4000);
    window.addEventListener("pagehide", () => navigator.sendBeacon("/api/bye?t=" + encodeURIComponent(TOKEN)));
  }

  async function init() {
    heartbeat();
    wireHistory();
    document.addEventListener("visibilitychange", updateDrift);
    wireMenu();
    wireDrop();
    wireControls();
    try {
      state = await api("/api/state");
      settings = state.settings;
    } catch (e) {}
    const nav = (navigator.language || "").toLowerCase();
    lang = settings.lang || (nav.startsWith("zh") ? "zh" : nav.startsWith("ja") ? "ja" : "en");
    applyRowCap(settings.rowCap);
    updateDrift();
    applyIdle();
    applyLang();
    render();
    poll();
  }

  init();
})();
