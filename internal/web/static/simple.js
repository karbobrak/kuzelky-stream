"use strict";
// The simple main screen for the club: link to the match on vysledky.kuzelky.cz, one button to
// start / end the broadcast of the match, the YouTube link and the picture. Uses api(), $ and
// refreshStatus() from app.js. Everything technical is under "Podrobnosti pro správce" and in the
// tabs shown with "Pokročilé".

const CKA = "https://vysledky.kuzelky.cz";
let simpleMatch = null;   // last /api/zapas
let simpleBusy = false;
let urlEdited = false;    // the user typed into the field: do not overwrite it

// normalizeUrl mirrors the server (cka.CheckURL): https, no trailing slash, no query.
function normalizeUrl(u) {
  try {
    const x = new URL(u.trim());
    if (x.host !== "vysledky.kuzelky.cz" || !x.pathname.startsWith("/detail-zapasu/")) return "";
    return "https://" + x.host + x.pathname.replace(/\/+$/, "");
  } catch (_) { return ""; }
}

function simpleMsg(text, bad) {
  const el = $("sMsg");
  el.textContent = text || "";
  el.hidden = !text;
  el.className = "simple-msg" + (bad ? " bad" : "");
}

function updateOpenLink() {
  const u = normalizeUrl($("sUrl").value);
  $("sOpen").href = u || CKA;
  $("sOpen").textContent = u ? "Otevřít stránku zápasu ↗" : "Otevřít vysledky.kuzelky.cz ↗";
}
$("sUrl").addEventListener("input", () => { urlEdited = true; updateOpenLink(); });

async function refreshSimpleMatch() {
  try { simpleMatch = await api("GET", "/api/zapas"); } catch (_) { return; }
  const m = simpleMatch.zapas, r = simpleMatch.vysledek, n = simpleMatch.nacitani || {};
  if (!urlEdited && document.activeElement !== $("sUrl")) {
    $("sUrl").value = (m && m.odkazVysledky) || "";
    updateOpenLink();
  }
  const box = $("sMatch");
  if (!m) { box.hidden = true; return; }
  const pts = (v) => String(v).replace(".", ",");
  let info = `<b>${esc(m.domaci.nazev)}</b> – <b>${esc(m.hoste.nazev)}</b> <span class="score">${pts(r.dBody)} : ${pts(r.hBody)}</span>`;
  if (m.odkazVysledky) {
    info += n.chyba ? `<span class="bad-text small">výsledky se nenačítají: ${esc(n.chyba)}</span>`
      : `<span class="muted small">${m.nacitatVysledky ? "výsledky se načítají" : "načítání vypnuto"}${n.stav ? " · zápas " + esc(n.stav) : ""}${n.posledni ? " · " + esc(n.posledni) : ""}</span>`;
  }
  box.innerHTML = info;
  box.hidden = false;
}

let lastStatus = null;

// Called by app.js with every status (1× per second).
window.renderSimple = function (s) {
  lastStatus = s;
  const running = s.bezi;
  const y = s.youtube || {};
  const nonstop = y.rezim === "api" && y.nonstop && s.cil === "youtube";
  const matchOn = running && (!nonstop || y.druh === "zapas");
  $("btnGo").hidden = matchOn;
  $("btnEnd").hidden = !matchOn;
  $("btnAlley").hidden = running || !nonstop;
  $("sUrl").disabled = matchOn;
  if (!running && !s.preruseno) $("btnInterrupt").hidden = true; // only useful while broadcasting

  const onAir = $("sOnAir");
  onAir.hidden = !running;
  onAir.textContent = !running ? "" : matchOn ? "Na YouTube běží přenos zápasu." :
    "Na YouTube běží nonstop vysílání kuželny (bez skóre). Před zápasem vložte odkaz a klikněte Začít přenos zápasu.";

  const link = y.odkaz || y.kanalLive || "";
  $("sYt").hidden = !link;
  $("sYtLink").href = link;
  $("sYtLink").textContent = link;
  const live = y.stavPrenosu === "live";
  $("sYtState").textContent = running ? (live ? "● ŽIVĚ" : "spouští se…") : "";
  $("sYtState").className = "small " + (live ? "live-text" : "muted");
};

$("btnGo").onclick = async () => {
  if (simpleBusy) return;
  simpleBusy = true;
  $("btnGo").disabled = true;
  try {
    const raw = $("sUrl").value.trim();
    const m = simpleMatch && simpleMatch.zapas;
    if (raw) {
      const u = normalizeUrl(raw);
      if (!u) throw new Error("Odkaz musí být stránka zápasu na vysledky.kuzelky.cz (…/detail-zapasu/…).");
      if (!m || m.odkazVysledky !== u) {
        simpleMsg("Načítám zápas z výsledků ČKA…");
        await api("POST", "/api/zapas/z-odkazu", { odkaz: u });
      } else if (!m.nacitatVysledky) {
        await api("POST", "/api/zapas/zdroj", { odkaz: u, automaticky: true });
      }
    } else if (!m) {
      throw new Error("Vložte odkaz na zápas z vysledky.kuzelky.cz.");
    }
    urlEdited = false;
    simpleMsg("Zakládám přenos zápasu na YouTube…");
    await api("POST", "/api/prenos/zapas", {});
    simpleMsg("Přenos zápasu běží. Odkaz pro diváky je níže, na YouTube naskočí do minuty.");
  } catch (e) {
    simpleMsg(e.message, true);
  } finally {
    simpleBusy = false;
    $("btnGo").disabled = false;
    refreshStatus();
    refreshSimpleMatch();
  }
};

$("btnEnd").onclick = async () => {
  const y = (lastStatus && lastStatus.youtube) || {};
  const nonstop = y.rezim === "api" && y.nonstop;
  if (!confirm(nonstop
    ? "Ukončit zápas?\n\nPřenos zápasu se na YouTube uloží jako video, skóre zmizí a dál se bude vysílat kuželna."
    : "Ukončit přenos zápasu?\n\nVysílání skončí a video zůstane na YouTube jako záznam.")) return;
  $("btnEnd").disabled = true;
  try {
    simpleMsg("Ukončuji přenos zápasu…");
    await api("POST", "/api/prenos/konec-zapasu", {});
    simpleMsg(nonstop ? "Zápas je ukončený, vysílá se kuželna." : "Přenos je ukončený.");
    $("sUrl").value = ""; urlEdited = false; updateOpenLink();
  } catch (e) { simpleMsg(e.message, true); }
  finally { $("btnEnd").disabled = false; refreshStatus(); refreshSimpleMatch(); }
};

$("btnAlley").onclick = async () => {
  $("btnAlley").disabled = true;
  try { await api("POST", "/api/start", {}); simpleMsg("Vysílá se kuželna."); }
  catch (e) { simpleMsg(e.message, true); }
  finally { $("btnAlley").disabled = false; refreshStatus(); }
};

$("btnSCopy").onclick = async () => {
  try { await navigator.clipboard.writeText($("sYtLink").href); $("btnSCopy").textContent = "Zkopírováno ✓"; }
  catch (_) { $("btnSCopy").textContent = "Nelze kopírovat"; }
  setTimeout(() => ($("btnSCopy").textContent = "Kopírovat odkaz"), 2000);
};

// "Pokročilé" shows the other tabs (remembered in this browser).
function setAdvanced(on) {
  document.body.classList.toggle("adv-on", on);
  $("btnAdv").textContent = on ? "Skrýt pokročilé" : "Pokročilé ▸";
  try { localStorage.setItem("ks-pokrocile", on ? "1" : ""); } catch (_) { /* private mode */ }
}
$("btnAdv").onclick = () => {
  const on = !document.body.classList.contains("adv-on");
  setAdvanced(on);
  if (!on) showTab("vysilani");
};
let advStored = false;
try { advStored = localStorage.getItem("ks-pokrocile") === "1"; } catch (_) { /* ignore */ }
const advTabs = ["zapis", "utkani", "obraz", "nastaveni"];
setAdvanced(advStored || advTabs.includes(location.hash.slice(1)));

updateOpenLink();
refreshSimpleMatch();
setInterval(() => { if ($("tab-vysilani").classList.contains("active")) refreshSimpleMatch(); }, 5000);
