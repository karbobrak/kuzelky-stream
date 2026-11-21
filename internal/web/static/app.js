"use strict";

const $ = (id) => document.getElementById(id);

async function api(method, url, body) {
  const opt = { method, headers: { "Content-Type": "application/json" } };
  if (body !== undefined) opt.body = JSON.stringify(body);
  const res = await fetch(url, opt);
  let data = {};
  try { data = await res.json(); } catch (_) { /* empty body */ }
  if (!res.ok) throw new Error(data.chyba || `${res.status} ${res.statusText}`);
  return data;
}

function showMsg(text, kind = "bad") {
  const m = $("msg");
  m.textContent = text;
  m.className = "msg " + kind;
  m.hidden = !text;
}

// ------------------------------------------------------------------ tabs
function showTab(name) {
  const b = document.querySelector(`.tabs button[data-tab="${name}"]`);
  if (!b) return;
  document.querySelectorAll(".tabs button").forEach((x) => x.classList.toggle("active", x === b));
  document.querySelectorAll(".tab").forEach((t) => t.classList.toggle("active", t.id === "tab-" + name));
  history.replaceState(null, "", "#" + name);
  if (name === "nastaveni") loadSettings();
  if ((name === "zapis" || name === "utkani") && window.loadMatch) window.loadMatch(name);
  if (name === "obraz" && window.loadEditor) window.loadEditor();
}
document.addEventListener("click", (e) => {
  const g = e.target.closest("[data-goto]");
  if (g) { e.preventDefault(); showTab(g.dataset.goto); }
});
document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => showTab(b.dataset.tab)));

// ------------------------------------------------------------------ status
const STATE_TEXT = { zastaveno: "Zastaveno", spoustim: "Připojuji…", bezi: "Běží", obnovuji: "Obnovuji…" };
let running = false;

function setState(el, state) {
  el.textContent = STATE_TEXT[state] || state;
  el.className = "state-" + state;
}

function fmtSince(iso) {
  if (!iso) return "–";
  const s = Math.floor((Date.now() - new Date(iso)) / 1000);
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  return `${h}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
}

async function refreshStatus() {
  let s;
  try { s = await api("GET", "/api/stav"); } catch (_) { return; }
  running = s.bezi;
  $("ver").textContent = s.verze ? "v" + s.verze.replace(/^v/, "") : "";
  $("btnStart").hidden = running;
  $("btnStop").hidden = !running;
  $("btnRestartCam").disabled = !running;
  $("btnRestartOut").disabled = !running;
  $("chkOverlay").checked = s.grafika;
  $("chkOverlay2").checked = s.grafika;

  const pill = $("pill");
  if (!running) { pill.textContent = "Zastaveno"; pill.className = "pill off"; }
  else if (s.vysilani.stav === "bezi" && !s.nahradniObraz) { pill.textContent = "● Vysílá se"; pill.className = "pill live"; }
  else if (s.vysilani.stav === "bezi") { pill.textContent = "● Vysílá se – náhradní obraz"; pill.className = "pill warn"; }
  else { pill.textContent = "Obnovuji spojení…"; pill.className = "pill warn"; }

  $("outTitle").textContent = { youtube: "YouTube", soubor: "Výstup do souboru", nic: "Výstup (test výkonu)" }[s.cil] || "Výstup";
  setState($("camState"), s.kamera.stav);
  setState($("outState"), s.vysilani.stav);
  $("camFps").textContent = running ? s.kameraFps.toFixed(0) : "–";
  $("camRestarts").textContent = running ? s.kamera.restartu : "–";
  $("outFps").textContent = running ? s.vysilaniFps.toFixed(1) : "–";
  $("outBitrate").textContent = running ? `${Math.round(s.datovyTokKbps)} kb/s` : "–";
  $("outSpeed").textContent = running ? `${s.rychlost.toFixed(2)}×` : "–";
  $("outRestarts").textContent = running ? s.vysilani.restartu : "–";
  $("outDropped").textContent = running ? s.zahozeno : "–";
  $("outEncoder").textContent = s.koder || "–";
  $("outSince").textContent = running ? fmtSince(s.od) : "–";
  showErr($("camErr"), running && s.kamera.stav !== "bezi" ? s.kamera.posledniChyba : "");
  showErr($("outErr"), running && s.vysilani.stav !== "bezi" ? s.vysilani.posledniChyba : "");
  $("slateBadge").hidden = !(running && s.nahradniObraz);
  if (!running) { $("preview").hidden = true; $("previewEmpty").hidden = false; }
  renderYouTube(s);
}

const LIFE_TEXT = {
  created: "založen", ready: "připraven – čeká na video", testing: "test", live: "● ŽIVĚ",
  complete: "ukončen", revoked: "zrušen", liveStarting: "spouští se…", testStarting: "spouští se…",
};
const HEALTH_TEXT = { good: "výborný", ok: "dobrý", bad: "špatný", noData: "žádná data" };
const STREAM_TEXT = { active: "přijímá video", inactive: "nepřijímá video", ready: "připraven", created: "založen", error: "chyba" };

function renderYouTube(s) {
  const y = s.youtube;
  const api = y.rezim === "api" && s.cil === "youtube";
  $("ytCard").hidden = !api;
  $("keyHint").hidden = api;
  if (!api) return;
  $("ytTitle").textContent = y.nazev || (y.prihlaseno ? "Přenos se založí po spuštění vysílání" : "Nepřihlášeno – Nastavení → YouTube");
  $("ytLink").textContent = y.odkaz || "";
  $("ytLink").href = y.odkaz || "#";
  $("btnCopyLink").hidden = !y.odkaz;
  $("ytLife").textContent = LIFE_TEXT[y.stavPrenosu] || y.stavPrenosu || (y.odkaz ? "pokračuje po spuštění" : "–");
  $("ytLife").className = y.stavPrenosu === "live" ? "state-bezi" : "";
  $("ytHealth").textContent = y.stavStreamu
    ? `${STREAM_TEXT[y.stavStreamu] || y.stavStreamu}${y.zdravi ? ", kvalita " + (HEALTH_TEXT[y.zdravi] || y.zdravi) : ""}` : "–";
  $("ytPrivacy").textContent = { public: "veřejné", unlisted: "neveřejné", private: "soukromé" }[y.soukromi] || "–";
  $("ytChannel").textContent = y.kanal || "–";
  const issues = $("ytIssues");
  issues.replaceChildren(...(y.problemy || []).map((t) => Object.assign(document.createElement("li"), { textContent: t })));
  issues.hidden = !(y.problemy && y.problemy.length);
  showErr($("ytErr"), y.chyba || "");
  $("btnEndBroadcast").hidden = !y.odkaz;
}

$("btnCopyLink").onclick = async () => {
  try { await navigator.clipboard.writeText($("ytLink").href); $("btnCopyLink").textContent = "Zkopírováno ✓"; }
  catch (_) { $("btnCopyLink").textContent = "Nelze kopírovat"; }
  setTimeout(() => ($("btnCopyLink").textContent = "Kopírovat odkaz"), 2000);
};
$("btnEndBroadcast").onclick = (e) => {
  if (confirm("Ukončit přenos utkání na YouTube?\n\nVysílání se zastaví a video se uzavře jako záznam. " +
    "Další spuštění založí nový přenos s novou adresou.")) act(e.currentTarget, "/api/youtube/ukoncit-prenos");
};

function showErr(el, text) {
  el.textContent = text ? "Poslední chyba: " + text : "";
  el.hidden = !text;
}

// ------------------------------------------------------------------ preview (double-buffered, no flicker)
const previewLoader = new Image();
previewLoader.onload = () => {
  $("preview").src = previewLoader.src;
  $("preview").hidden = false;
  $("previewEmpty").hidden = true;
};
function refreshPreview() {
  if (running && document.visibilityState === "visible") previewLoader.src = "/api/nahled.jpg?t=" + Date.now();
}

// ------------------------------------------------------------------ log
let logSeq = 0;
async function refreshLog() {
  let lines;
  try { lines = await api("GET", "/api/log?od=" + logSeq); } catch (_) { return; }
  if (!lines || !lines.length) return;
  const el = $("log");
  const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 20;
  el.textContent += lines.map((l) => l.text).join("\n") + "\n";
  logSeq = lines[lines.length - 1].seq;
  const all = el.textContent.split("\n");
  if (all.length > 600) el.textContent = all.slice(-500).join("\n");
  if (atBottom) el.scrollTop = el.scrollHeight;
}

// ------------------------------------------------------------------ buttons
async function act(button, url, body) {
  button.disabled = true;
  showMsg("");
  try { await api("POST", url, body ?? {}); }
  catch (e) { showMsg(e.message); }
  finally { button.disabled = false; refreshStatus(); }
}
$("btnStart").onclick = (e) => act(e.currentTarget, "/api/start");
$("btnStop").onclick = (e) => {
  if (confirm("Opravdu zastavit vysílání? Na YouTube vysílání skončí.")) act(e.currentTarget, "/api/stop");
};
$("btnRestartCam").onclick = (e) => act(e.currentTarget, "/api/restart-kamery");
$("btnRestartOut").onclick = (e) => {
  if (confirm("Restartovat spojení s YouTube? Divák uvidí krátký výpadek.")) act(e.currentTarget, "/api/restart-vysilani");
};
async function setOverlay(on) {
  try { await api("POST", "/api/grafika", { zapnuto: on }); } catch (e) { showMsg(e.message); }
  refreshStatus();
  if (window.refreshGfxPreview) window.refreshGfxPreview();
}
$("chkOverlay").onchange = (e) => setOverlay(e.target.checked);
$("chkOverlay2").onchange = (e) => setOverlay(e.target.checked);
document.addEventListener("keydown", (e) => {
  const t = e.target.tagName;
  if (t === "INPUT" || t === "SELECT" || t === "TEXTAREA" || e.ctrlKey || e.altKey || e.metaKey) return;
  if (e.key === "g" || e.key === "G") setOverlay(!$("chkOverlay").checked);
});

// ------------------------------------------------------------------ settings
const form = $("settings");
let loaded = null; // last settings from the server

function fieldValue(el) {
  if (el.type === "checkbox") return el.checked;
  if (el.type === "number") return el.value === "" ? 0 : Number(el.value);
  return el.value;
}

function formToConfig() {
  const cfg = structuredClone(loaded.nastaveni);
  for (const el of form.elements) {
    if (!el.name) continue;
    const [sec, key] = el.name.split(".");
    if (el.name === "vystup.rozliseni") {
      const [w, h] = el.value.split("x").map(Number);
      cfg.vystup.sirka = w; cfg.vystup.vyska = h;
    } else if (el.name === "kamera.fpsWebkamery") {
      cfg.kamera.fpsWebkamery = Number(el.value);
    } else if (el.name === "vystup.fps") {
      cfg.vystup.fps = Number(el.value);
    } else {
      cfg[sec][key] = fieldValue(el);
    }
  }
  return cfg;
}

function configToForm(view) {
  const cfg = view.nastaveni;
  for (const el of form.elements) {
    if (!el.name) continue;
    const [sec, key] = el.name.split(".");
    if (el.name === "vystup.rozliseni") {
      const v = `${cfg.vystup.sirka}x${cfg.vystup.vyska}`;
      if (![...el.options].some((o) => o.value === v)) el.add(new Option(v.replace("x", " × "), v));
      el.value = v;
      continue;
    }
    let v = cfg[sec][key];
    if (el.name === "kamera.zdroj" && !v) v = "rtsp";
    if (el.tagName === "SELECT" && v != null && v !== "" && ![...el.options].some((o) => o.value === String(v))) {
      el.add(new Option(String(v), String(v))); // e.g. a webcam that is not plugged in right now
    }
    if (el.type === "checkbox") el.checked = !!v;
    else el.value = v ?? "";
    if (el.dataset.secret) {
      el.value = "";
      el.placeholder = view[el.dataset.secret] ? "uloženo – pro změnu zadejte nové" : "není nastaveno";
    }
  }
}

async function loadSettings() {
  try {
    loaded = await api("GET", "/api/nastaveni");
    configToForm(loaded);
    updateCamSource();
    updateYtSettings();
    refreshCloud();
    $("saveMsg").textContent = "";
  } catch (e) { $("saveMsg").textContent = e.message; }
}

// ---- webcam
let wcLoaded = false;
function updateCamSource() {
  const wc = $("camSource").value === "webkamera";
  $("camWebcam").hidden = !wc;
  $("camRtsp").hidden = wc;
  if (wc && !wcLoaded) loadWebcams();
}
$("camSource").onchange = updateCamSource;
$("btnWcRefresh").onclick = () => loadWebcams();
$("wcDev").onchange = () => loadWebcamModes();

function fillSelect(sel, items, keepFirst) {
  const cur = sel.value;
  const first = keepFirst ? [sel.options[0]] : [];
  sel.replaceChildren(...first, ...items.map(([v, t]) => new Option(t, v)));
  if (cur && ![...sel.options].some((o) => o.value === cur)) sel.add(new Option(cur + " (nepřipojeno)", cur));
  sel.value = cur || (sel.options[0] ? sel.options[0].value : "");
}

async function loadWebcams() {
  const msg = $("wcMsg");
  msg.textContent = "Hledám zařízení…"; msg.className = "small muted";
  try {
    const r = await api("GET", "/api/webkamery");
    wcLoaded = true;
    fillSelect($("wcDev"), r.kamery.map((n) => [n, n]), false);
    fillSelect($("wcMic"), r.mikrofony.map((n) => [n, n]), true);
    msg.textContent = r.kamery.length ? `Nalezeno – kamery: ${r.kamery.length}, mikrofony: ${r.mikrofony.length}`
      : "Žádná webkamera nenalezena – je připojená?";
    msg.className = r.kamery.length ? "small ok-text" : "small bad-text";
    await loadWebcamModes();
  } catch (e) { msg.textContent = e.message; msg.className = "small bad-text"; }
}

async function loadWebcamModes() {
  const dev = $("wcDev").value;
  if (!dev) return;
  try {
    const r = await api("GET", "/api/webkamery/rezimy?nazev=" + encodeURIComponent(dev));
    fillSelect($("wcRes"), (r.rezimy || []).map((m) => [m.rozliseni, `${m.rozliseni.replace("x", " × ")} (až ${m.fps} fps)`]), true);
  } catch (e) { $("wcMsg").textContent = e.message; $("wcMsg").className = "small bad-text"; }
}

function updateYtSettings() {
  const api = $("ytMode").value === "api";
  $("ytApi").hidden = !api;
  $("ytKey").hidden = api;
  const y = loaded.nastaveni.youtube;
  const signed = !!y.kanal;
  $("ytLogin").innerHTML = "";
  $("ytLogin").append(signed
    ? Object.assign(document.createElement("span"), { className: "ok-text", textContent: "✓ Přihlášen kanál: " + y.kanal })
    : Object.assign(document.createElement("span"), { className: "muted", textContent: "Nepřihlášeno" }));
  $("btnYtLogin").textContent = signed ? "Přihlásit jiný kanál" : "Přihlásit k YouTube";
  $("btnYtLogout").hidden = !signed;
  // Open the Google Cloud section when it is not filled in yet.
  const det = document.querySelector("#ytApi details");
  det.querySelector("summary").textContent = loaded.vestavenyKlient
    ? "Přístup k API (Google Cloud) – vestavěný v aplikaci, není třeba nic vyplňovat"
    : "Přístup k API (Google Cloud) – vyplňuje se jednou";
  if (!loaded.vestavenyKlient && (!y.clientId || !loaded.secretNastaven)) det.open = true;
}
$("ytMode").onchange = updateYtSettings;

$("btnYtLogin").onclick = () => googleLogin("/api/youtube/prihlaseni");
$("btnCloudLogin").onclick = () => googleLogin("/api/cloud/prihlaseni");
$("btnCloudLogout").onclick = async () => {
  if (!confirm("Odhlásit Google Disk? Záloha a tabulka výsledků se zastaví.")) return;
  try { await api("POST", "/api/cloud/odhlasit", {}); await loadSettings(); } catch (err) { $("saveMsg").textContent = err.message; }
};

async function refreshCloud() {
  let s;
  try { s = await api("GET", "/api/cloud"); } catch (_) { return; }
  const el = $("cloudState");
  let text, cls = "small";
  if (!s.zaloha && !s.tabulka) { text = "Vypnuto."; cls += " muted"; }
  else if (s.chybiPrihlaseni) { text = "Google Disk není přihlášený – klikněte na Přihlásit Google Disk."; cls += " bad-text"; }
  else if (s.chyba) { text = "Chyba: " + s.chyba; cls += " bad-text"; }
  else if (s.cekajici) { text = `Čeká na nahrání: ${s.cekajici}` + (s.posledni ? ` (naposledy ${s.posledni})` : ""); }
  else { text = s.posledni ? `✓ Synchronizováno v ${s.posledni}` : "Připraveno, zatím nic nenahráno."; cls += " ok-text"; }
  if (s.ucet) text = `Účet ${s.ucet} · ` + text;
  el.textContent = text; el.className = cls;
  $("btnCloudLogin").textContent = s.ucet ? "Přihlásit jiný účet" : "Přihlásit Google Disk";
  $("btnCloudLogout").hidden = !s.ucet;
  $("cloudFolder").hidden = !s.slozka; $("cloudFolder").href = s.slozka || "#";
  $("cloudSheet").hidden = !s.tabulkaOdkaz; $("cloudSheet").href = s.tabulkaOdkaz || "#";
  const z = document.getElementById("zCloud");
  if (z) {
    z.textContent = !(s.zaloha || s.tabulka) ? "" : s.chyba || s.chybiPrihlaseni ? "☁ záloha nefunguje"
      : s.cekajici ? "☁ ukládám…" : s.posledni ? "☁ zálohováno " + s.posledni : "";
    z.className = "small " + (s.chyba || s.chybiPrihlaseni ? "bad-text" : "muted");
  }
}
setInterval(() => { if ($("tab-nastaveni").classList.contains("active") || $("tab-zapis").classList.contains("active")) refreshCloud(); }, 3000);

$("btnCloudSync").onclick = async (e) => {
  e.currentTarget.disabled = true;
  $("cloudState").textContent = "Synchronizuji…";
  try { await api("POST", "/api/cloud/synchronizovat", {}); } catch (err) { $("saveMsg").textContent = err.message; }
  e.currentTarget.disabled = false;
  refreshCloud();
};
$("btnCloudGfx").onclick = async () => {
  if (!confirm("Přepsat rozmístění grafiky, předvolby, výřez a pravidla verzí z Google Disku?")) return;
  try { await api("POST", "/api/cloud/obnovit-grafiku", {}); await loadSettings(); $("saveMsg").textContent = "Obnoveno z Disku ✓"; }
  catch (err) { $("saveMsg").textContent = err.message; }
};

async function googleLogin(url) {
  const msg = $("saveMsg");
  try {
    const r = await api("POST", url, {});
    window.open(r.url, "_blank", "noopener");
    msg.textContent = "Dokončete přihlášení v nové záložce, pak sem přepněte zpět."; msg.className = "small";
    const until = Date.now() + 5 * 60 * 1000;
    const poll = setInterval(async () => {
      const v = await api("GET", "/api/nastaveni");
      const changed = v.nastaveni.youtube.kanal !== loaded.nastaveni.youtube.kanal ||
        v.nastaveni.cloud.ucet !== loaded.nastaveni.cloud.ucet;
      if (changed || Date.now() > until) {
        clearInterval(poll);
        await loadSettings();
      }
    }, 2000);
  } catch (err) { msg.textContent = err.message; msg.className = "small bad-text"; }
};
$("btnYtLogout").onclick = async () => {
  if (!confirm("Odhlásit YouTube kanál? Pro další vysílání bude potřeba se znovu přihlásit.")) return;
  try { await api("POST", "/api/youtube/odhlasit", {}); await loadSettings(); }
  catch (err) { $("saveMsg").textContent = err.message; }
};

const IMPACT_TEXT = {
  kamera: "Kamera se znovu připojí. Divák uvidí na pár sekund náhradní obraz, YouTube se nepřeruší.",
  vysilani: "Spojení s YouTube se restartuje. Divák uvidí krátký výpadek.",
  vse: "Vysílání se úplně restartuje. Divák uvidí výpadek několika sekund.",
};

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const cfg = formToConfig();
  const msg = $("saveMsg");
  msg.className = "small";
  try {
    const check = await api("POST", "/api/nastaveni/kontrola", cfg);
    if (check.dopad !== "zadny" && !confirm("Vysílání běží. " + IMPACT_TEXT[check.dopad] + "\n\nUložit a použít teď?")) return;
    await api("PUT", "/api/nastaveni", cfg);
    msg.textContent = "Uloženo ✓";
    msg.className = "small ok-text";
    await loadSettings();
    msg.textContent = "Uloženo ✓";
  } catch (err) {
    msg.textContent = err.message;
    msg.className = "small bad-text";
  }
});
$("btnRevert").onclick = loadSettings;
$("btnReload").onclick = async () => {
  const msg = $("saveMsg");
  try {
    await api("POST", "/api/nastaveni/znovu-nacist", {});
    await loadSettings();
    msg.textContent = "Načteno ze souboru ✓"; msg.className = "small ok-text";
  } catch (err) { msg.textContent = err.message; msg.className = "small bad-text"; }
};

$("btnTestCam").onclick = async (e) => {
  const out = $("camTest");
  e.currentTarget.disabled = true;
  out.textContent = "Zkouším se připojit…"; out.className = "small";
  try {
    const r = await api("POST", "/api/test/kamera", formToConfig().kamera);
    out.textContent = `✓ ${r.kodek} ${r.sirka}×${r.vyska}, ${fpsText(r.fps)} snímků/s, zvuk: ${r.zvuk ? r.zvukKodek : "ne"}`;
    out.className = "small ok-text";
  } catch (err) { out.textContent = "✗ " + err.message; out.className = "small bad-text"; }
  finally { e.currentTarget.disabled = false; }
};

function fpsText(f) {
  const [a, b] = String(f).split("/").map(Number);
  return b ? (a / b).toFixed(1).replace(/\.0$/, "") : f;
}

const ENC_NAMES = { h264_nvenc: "NVIDIA", h264_qsv: "Intel", h264_amf: "AMD", libx264: "procesor" };
$("btnTestEnc").onclick = async (e) => {
  const out = $("encTest");
  e.currentTarget.disabled = true;
  out.textContent = "Zkouším…"; out.className = "small";
  try {
    const r = await api("POST", "/api/test/kodery", {});
    out.innerHTML = "";
    for (const [k, ok] of Object.entries(r)) {
      const s = document.createElement("span");
      s.textContent = `${ok ? "✓" : "✗"} ${ENC_NAMES[k]}  `;
      s.className = ok ? "ok-text" : "muted";
      out.append(s);
    }
  } catch (err) { out.textContent = err.message; out.className = "small bad-text"; }
  finally { e.currentTarget.disabled = false; }
};

// ------------------------------------------------------------------ loops
if (location.hash.length > 1) showTab(location.hash.slice(1));
refreshStatus(); refreshLog();
setInterval(refreshStatus, 1000);
setInterval(refreshLog, 1500);
setInterval(refreshPreview, 1000);
