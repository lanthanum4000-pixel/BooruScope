(() => {
  "use strict";
  const TOKEN = document.querySelector('meta[name="bs-token"]').content;
  const I18N = window.BS_I18N;
  const nav = (navigator.language || "").toLowerCase();
  const lang = nav.startsWith("zh") ? "zh" : nav.startsWith("ja") ? "ja" : "en";
  const t = (k) => (I18N[lang] && I18N[lang][k]) || I18N.en[k] || k;
  const msg = document.getElementById("msg");
  const send = document.getElementById("send");
  document.documentElement.lang = { zh: "zh-Hant", ja: "ja" }[lang] || "en";
  msg.textContent = t("lens.sending");
  send.textContent = t("lens.send");
  const q = new URLSearchParams(location.search).get("q") || "";
  const ext = { "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif", "image/avif": ".avif" };
  fetch("/api/query/" + encodeURIComponent(q) + "?t=" + encodeURIComponent(TOKEN))
    .then((r) => { if (!r.ok) throw new Error(String(r.status)); return r.blob(); })
    .then((blob) => {
      const dt = new DataTransfer();
      dt.items.add(new File([blob], "image" + (ext[blob.type] || ".jpg"), { type: blob.type || "image/jpeg" }));
      document.getElementById("file").files = dt.files;
      send.hidden = false;
      document.getElementById("form").submit();
    })
    .catch(() => { msg.textContent = t("lens.failed"); });
})();
