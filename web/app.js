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
    variant: $("variant"), rerank: $("rerank"), rerankText: $("rerankText"), lang: $("lang"),
    encDot: $("encDot"), encText: $("encText"), encRetry: $("encRetry"),
    libDot: $("libDot"), libText: $("libText"), msgline: $("msgline"),
    overlay: $("dropOverlay"), toast: $("toast"),
  };

  const PAD = 8;
  const MIN_CARD = 110;
  const ROW_CAP_MIN = 3;
  const ROW_CAP_MAX = 6;
  const Q_ROWS = 2;
  const Q_COLS = 2;
  const BUFFER_ROWS = 2;

  let settings = { lang: "", rowCap: 4, variant: 0, rerank: true, idleUnload: false, idleMinutes: 10, offline: true };
  let idleDeadline = 0;
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
    if (qtile) qtile.querySelector(".qlabel").textContent = t("query.label");
  }

  function applyIdle() {
    el.idleUnload.checked = !!settings.idleUnload;
    el.idleMinutes.value = String(settings.idleMinutes || 10);
    el.idleMinutes.disabled = !settings.idleUnload;
    el.offline.checked = !!settings.offline;
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
    return (e !== "ready" && e !== "error" && e !== "unloaded") || state.library.state === "loading";
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
    img.addEventListener("load", () => card.classList.add("loaded"));
    img.addEventListener("error", () => card.classList.add("failed"));
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
      qtile.innerHTML = '<div class="qframe"><img alt=""></div><span class="qlabel"></span><span class="spin"></span>';
      qtile.querySelector(".qlabel").textContent = t("query.label");
      el.spacer.appendChild(qtile);
    }
    const img = qtile.querySelector("img");
    if (query.src && img.getAttribute("src") !== query.src) img.src = query.src;
    if (!query.src) img.removeAttribute("src");
    qtile.classList.toggle("searching", searching);
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
          cards.set(i, card);
          el.spacer.appendChild(card);
        }
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
    }
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
  }

  function setQuery(src) {
    if (query && query.blob) URL.revokeObjectURL(query.src);
    query = { src, blob: !!src && src.startsWith("blob:") };
  }

  async function runSearch(send, src) {
    const seq = ++searchSeq;
    clearResults();
    setQuery(src);
    searching = true;
    el.results.scrollTop = 0;
    setMessage({ running: true });
    render();
    idleDeadline = 0;
    tickCountdown();
    if (state && state.encoder.state === "unloaded") setTimeout(poll, 300);
    try {
      const res = await send();
      if (seq !== searchSeq) return;
      if (!query.src) query.src = withToken("/api/query/" + res.query);
      results = res.results || [];
      lastSearch = res;
      setMessage({ done: res });
    } catch (e) {
      if (seq !== searchSeq) return;
      setMessage({ error: e });
      if (e.code === "encoder_not_ready" || e.code === "no_index") poll();
    } finally {
      if (seq === searchSeq) {
        searching = false;
        render();
        poll();
      }
    }
  }

  function searchBlob(blob) {
    const src = URL.createObjectURL(blob);
    return runSearch(() => api("/api/search/image", { method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: blob }), src);
  }

  function searchURL(url) {
    return runSearch(() => api("/api/search/url", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ url }) }), null);
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
    el.results.addEventListener("scroll", queueRender, { passive: true });
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
    wireDrop();
    wireControls();
    try {
      state = await api("/api/state");
      settings = state.settings;
    } catch (e) {}
    const nav = (navigator.language || "").toLowerCase();
    lang = settings.lang || (nav.startsWith("zh") ? "zh" : nav.startsWith("ja") ? "ja" : "en");
    applyRowCap(settings.rowCap);
    applyIdle();
    applyLang();
    render();
    poll();
  }

  init();
})();
