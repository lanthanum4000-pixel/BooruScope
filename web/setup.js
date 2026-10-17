(() => {
  "use strict";

  const TOKEN = document.querySelector('meta[name="bs-token"]').content;
  const I18N = window.BS_I18N;
  const $ = (id) => document.getElementById(id);

  const RECOMMENDED = 128;

  let lang = (navigator.language || "").toLowerCase().startsWith("zh") ? "zh" : "en";
  let state = null;
  let choice = { pack: "full", variant: RECOMMENDED, rerank: true };
  let dirCheck = null;
  let checkSeq = 0;
  let checkTimer = 0;
  let pollTimer = 0;
  let pickerMissing = false;
  let picking = false;
  let installError = null;

  function t(key, vars) {
    let s = (I18N[lang] && I18N[lang][key]) ?? I18N.en[key] ?? key;
    if (vars) s = s.replace(/\{(\w+)\}/g, (_, k) => (vars[k] ?? ""));
    return s;
  }

  async function api(path, body) {
    const opts = { headers: { "X-BooruScope-Token": TOKEN } };
    if (body !== undefined) {
      opts.method = "POST";
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    let r;
    try {
      r = await fetch(path, opts);
    } catch (e) {
      throw { code: "network" };
    }
    const data = await r.json().catch(() => null);
    if (!r.ok || (data && data.error)) throw { code: (data && data.error) || "generic", detail: data && data.detail };
    return data;
  }

  function bytes(n) {
    if (!n) return "0 B";
    const u = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i >= 3 ? n.toFixed(2) : i >= 1 ? n.toFixed(1) : String(n)) + " " + u[i];
  }

  function duration(sec) {
    if (!isFinite(sec) || sec < 0) return "—";
    if (sec < 60) return Math.ceil(sec) + " s";
    if (sec < 3600) return Math.ceil(sec / 60) + " min";
    return (sec / 3600).toFixed(1) + " h";
  }

  function pack(name) { return state && state.manifest && state.manifest[name]; }

  function selectedFiles() {
    const p = pack(choice.pack);
    if (!p) return [];
    if (choice.pack === "trial") return Object.keys(p.files);
    const out = ["posts.bin", "index_m" + choice.variant + ".pq"];
    if (choice.rerank) out.push("rerank_int8.bin");
    return out.filter((n) => n in p.files);
  }

  function totalBytes() {
    const p = pack(choice.pack);
    if (!p) return 0;
    return selectedFiles().reduce((a, n) => a + (p.files[n] || 0), 0);
  }

  function applyLang() {
    document.documentElement.lang = lang === "zh" ? "zh-Hant" : "en";
    document.title = t("setup.title");
    document.querySelectorAll("[data-i18n]").forEach((n) => { n.textContent = t(n.dataset.i18n); });
    $("lang").querySelectorAll("button").forEach((b) => b.classList.toggle("active", b.dataset.lang === lang));
    renderForm();
    renderProgress();
  }

  function renderVariants() {
    const box = $("variants");
    box.textContent = "";
    const p = pack("full");
    for (const m of [64, 96, 128]) {
      const size = p && p.files["index_m" + m + ".pq"];
      const lab = document.createElement("label");
      lab.className = "opt";
      lab.innerHTML = '<input type="radio" name="variant"><span><b></b><span class="tag" hidden></span><br><small></small></span>';
      const input = lab.querySelector("input");
      input.value = String(m);
      input.checked = choice.variant === m;
      input.disabled = !size;
      input.addEventListener("change", () => { choice.variant = m; renderForm(); });
      lab.querySelector("b").textContent = "M" + m;
      const tag = lab.querySelector(".tag");
      tag.hidden = m !== RECOMMENDED;
      tag.textContent = t("setup.recommended");
      lab.querySelector("small").textContent = size
        ? t("setup.variantLine", { size: bytes(size), mem: bytes(size + (p.files["posts.bin"] || 0)) })
        : t("setup.unavailable");
      box.appendChild(lab);
    }
  }

  function renderForm() {
    if (!state) return;
    const full = pack("full"), trial = pack("trial");
    const mbox = $("manifestBox");
    mbox.textContent = "";
    if (state.manifestLoading && !state.manifest) {
      mbox.textContent = t("setup.manifestLoading");
    } else if (!state.manifest) {
      mbox.className = "warn";
      mbox.append(t("setup.manifestError", { source: state.source }) + " ");
      if (state.manifestAuth && !state.hasToken) mbox.append(t("setup.manifestAuth") + " ");
      const b = document.createElement("button");
      b.type = "button";
      b.className = "link";
      b.textContent = t("setup.retry");
      b.addEventListener("click", () => api("/api/setup/reload", {}).finally(() => setTimeout(loadState, 400)));
      mbox.appendChild(b);
    } else {
      mbox.className = "hint";
    }

    if (!full && trial && choice.pack === "full") choice.pack = "trial";
    if (full && !full.files["index_m" + choice.variant + ".pq"]) {
      const v = [128, 96, 64].find((m) => full.files["index_m" + m + ".pq"]);
      if (v) choice.variant = v;
    }
    if (full && !full.files["rerank_int8.bin"]) choice.rerank = false;

    for (const [id, name, p] of [["packFull", "full", full], ["packTrial", "trial", trial]]) {
      const el = $(id);
      const input = el.querySelector("input");
      input.disabled = !p;
      input.checked = choice.pack === name;
      el.classList.toggle("selected", choice.pack === name && !!p);
      el.classList.toggle("disabled", !p);
    }
    $("fullDesc").textContent = full ? t("setup.fullDesc", { rows: full.rows.toLocaleString() }) : t("setup.unavailable");
    const trialSize = trial ? Object.values(trial.files).reduce((a, b) => a + b, 0) : 0;
    $("trialDesc").textContent = trial ? t("setup.trialDesc", { size: bytes(trialSize) }) : t("setup.unavailable");

    $("fullOpts").hidden = choice.pack !== "full" || !full;
    renderVariants();
    const rr = full && full.files["rerank_int8.bin"];
    $("rerank").disabled = !rr;
    $("rerank").checked = choice.rerank && !!rr;
    $("rerankDesc").textContent = rr ? t("setup.rerankDesc", { size: bytes(rr) }) : t("setup.unavailable");

    $("optionsBox").hidden = !state.shortcuts;
    $("browse").hidden = pickerMissing;

    const need = totalBytes();
    $("total").textContent = state.manifest ? t("setup.total", { size: bytes(need) }) : "";
    const info = [];
    if (dirCheck && dirCheck.freeKnown) info.push(t("setup.free", { free: bytes(dirCheck.free) }));
    if (state.manifest) info.push(t("setup.need", { need: bytes(need) }));
    $("dirInfo").textContent = info.join(" · ");

    let warn = "", infoOnly = false;
    if (pickerMissing) { warn = t("setup.noPicker"); infoOnly = true; }
    if (dirCheck) {
      if (!dirCheck.writable) warn = t("setup.notWritable");
      else if (dirCheck.freeKnown && dirCheck.free < need) warn = t("setup.noSpace");
      else if (dirCheck.hasApp) { warn = t("setup.update"); infoOnly = true; }
    }
    $("dirWarn").hidden = !warn;
    $("dirWarn").textContent = warn;
    $("dirWarn").classList.toggle("info", infoOnly);

    let why = "";
    if (!state.manifest) why = state.manifestLoading ? t("setup.why.manifest") : t("setup.manifestError", { source: state.source });
    else if (!pack(choice.pack)) why = t("setup.why.pack");
    else if (!$("dir").value.trim()) why = t("setup.why.dir");
    else if (!dirCheck) why = t("setup.why.checking");
    else if (!dirCheck.writable) why = t("setup.notWritable");
    else if (dirCheck.freeKnown && dirCheck.free < need) why = t("setup.noSpace");
    $("install").disabled = !!why;
    const note = $("installNote");
    note.textContent = installError ? errText(installError) : why;
    note.classList.toggle("error", !!installError);
    $("browse").disabled = picking;
    $("browse").textContent = picking ? t("setup.picking") : t("setup.browse");
  }

  function errText(e) {
    const key = "setup.err." + (e && e.code);
    return (I18N.en[key] ? t(key) : t("setup.err.generic")) + (e && e.detail ? " (" + e.detail + ")" : "");
  }

  function scheduleCheck() {
    clearTimeout(checkTimer);
    checkTimer = setTimeout(checkDir, 250);
  }

  async function checkDir() {
    const seq = ++checkSeq;
    const dir = $("dir").value.trim();
    if (!dir) { dirCheck = null; renderForm(); return; }
    try {
      const c = await api("/api/setup/check", { dir });
      if (seq === checkSeq) dirCheck = c;
    } catch (e) {
      if (seq === checkSeq) dirCheck = null;
    }
    if (seq === checkSeq) renderForm();
  }

  function showView(id) {
    for (const v of ["formView", "progressView", "doneView"]) $(v).hidden = v !== id;
  }

  function renderProgress() {
    const p = state && state.progress;
    if (!p || p.state === "idle") return;
    if (p.state === "running") {
      showView("progressView");
      $("progStep").textContent = p.step ? t("setup.step." + p.step) : "";
      const list = $("fileList");
      list.textContent = "";
      let remaining = 0;
      for (const f of p.files) {
        const row = document.createElement("div");
        row.className = "file " + f.state;
        row.innerHTML = '<div class="file-head"><span class="file-name"></span><span class="file-state"></span></div><div class="bar-track"><div class="bar-fill"></div></div>';
        row.querySelector(".file-name").textContent = f.name + " · " + bytes(f.size);
        row.querySelector(".file-state").textContent = t("setup.file." + f.state, { done: bytes(f.done), size: bytes(f.size) });
        row.querySelector(".bar-fill").style.width = (f.size ? Math.min(100, (100 * f.done) / f.size) : 0) + "%";
        list.appendChild(row);
        if (f.state !== "done" && f.state !== "skipped") remaining += f.size - f.done;
      }
      $("progSpeed").textContent = p.speed > 0 ? t("setup.speed", { speed: bytes(p.speed), eta: duration(remaining / p.speed) }) : "";
      return;
    }
    showView("doneView");
    const ok = p.state === "done";
    $("doneTitle").textContent = ok ? t("setup.done") : p.state === "cancelled" ? t("setup.cancel") : t("setup.failed");
    $("doneDesc").textContent = ok
      ? t("setup.doneDesc", { dir: p.dir })
      : p.state === "cancelled" ? t("setup.cancelled") : errText({ code: p.code, detail: p.error });
    $("launch").hidden = !ok;
    $("back").hidden = ok;
  }

  async function loadState() {
    try {
      state = await api("/api/setup/state");
    } catch (e) {
      return;
    }
    if (!$("dir").value) {
      $("dir").value = state.defaultDir;
      scheduleCheck();
    }
    renderForm();
    renderProgress();
    if (state.manifestLoading || state.progress.state === "running") {
      clearTimeout(pollTimer);
      pollTimer = setTimeout(loadState, state.progress.state === "running" ? 500 : 800);
    }
  }

  function wire() {
    $("lang").addEventListener("click", (e) => {
      const b = e.target.closest("button[data-lang]");
      if (b) { lang = b.dataset.lang; applyLang(); }
    });
    $("dir").addEventListener("input", () => { installError = null; scheduleCheck(); });
    $("browse").addEventListener("click", async () => {
      picking = true;
      installError = null;
      renderForm();
      try {
        const r = await api("/api/setup/browse", { dir: $("dir").value.trim(), prompt: t("setup.pickPrompt") });
        if (r && r.dir) { $("dir").value = r.dir; checkDir(); }
      } catch (e) {
        if (e.code === "no_picker") pickerMissing = true;
        else if (e.code !== "cancelled") installError = e;
      } finally {
        picking = false;
        renderForm();
      }
    });
    document.querySelectorAll('input[name="pack"]').forEach((r) => r.addEventListener("change", () => { choice.pack = r.value; renderForm(); }));
    $("rerank").addEventListener("change", () => { choice.rerank = $("rerank").checked; renderForm(); });
    $("install").addEventListener("click", async () => {
      $("install").disabled = true;
      installError = null;
      try {
        await api("/api/setup/install", { dir: $("dir").value.trim(), choice, shortcut: state.shortcuts && $("shortcut").checked });
      } catch (e) {
        installError = e;
      }
      await loadState();
      renderForm();
    });
    $("cancel").addEventListener("click", () => { $("cancel").disabled = true; api("/api/setup/cancel", {}).finally(() => { $("cancel").disabled = false; loadState(); }); });
    $("back").addEventListener("click", () => { state.progress.state = "idle"; installError = null; showView("formView"); checkDir(); });
    $("launch").addEventListener("click", async () => {
      try {
        await api("/api/setup/launch", {});
        window.close();
      } catch (e) {
        $("doneDesc").textContent = t("setup.err.launch_failed");
      }
    });
    $("close").addEventListener("click", () => { api("/api/setup/quit", {}).finally(() => window.close()); });
  }

  wire();
  applyLang();
  loadState();
})();
