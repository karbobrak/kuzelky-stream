"use strict";
// Match setup (tab Utkání) and scoring (tab Zápis). Uses api(), $, showTab from app.js.

let view = null; // last MatchView from the server: {zapas, vysledek, soubor, pravidla, casti, lzeZpet}

const pts = (v) => String(v).replace(".", ",");
const pinsFmt = (v) => String(v).replace(/\B(?=(\d{3})+$)/, " ");
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

window.loadMatch = async function (tab) {
  try { view = await api("GET", "/api/zapas"); } catch (e) { return; }
  if (tab === "utkani") { renderForm(); loadSaved(); loadRosters(); }
  else renderScoring();
};

function applyView(v) {
  view = v;
  renderScoring();
}

// ================================================================== Zápis (scoring)

function renderScoring() {
  const has = !!(view && view.zapas);
  $("zNoMatch").hidden = has;
  $("zMatch").hidden = !has;
  if (!has) return;
  const m = view.zapas, r = view.vysledek;
  $("zHome").textContent = m.domaci.nazev;
  $("zAway").textContent = m.hoste.nazev;
  $("zPts").textContent = `${pts(r.dBody)} : ${pts(r.hBody)}`;
  $("zPins").textContent = `kuželky ${pinsFmt(r.dKuzelky)} : ${pinsFmt(r.hKuzelky)}${r.dohrano ? " · konec" : ""}`;
  $("btnUndo").disabled = !view.lzeZpet;

  // Part selector.
  const parts = $("zParts");
  parts.replaceChildren();
  for (let p = 1; p <= view.casti; p++) {
    const b = document.createElement("button");
    b.textContent = `Část ${p}`;
    b.className = (p === m.aktualniCast ? "on " : "") + (partDone(p) ? "done" : "");
    b.onclick = () => send("/api/zapas/cast", { cast: p });
    parts.append(b);
  }
  // Graphics mode.
  document.querySelectorAll("#zModes button").forEach((b) => b.classList.toggle("on", b.dataset.mode === m.stavGrafiky));
  if (document.activeElement !== $("zMessage")) $("zMessage").value = m.zprava || "";

  renderDuels();
  refreshGfxPreview();
}

function partDuels(p) {
  const n = view.pravidla.soubojuNaCast, out = [];
  for (let i = (p - 1) * n; i < p * n && i < view.zapas.souboje.length; i++) out.push(i);
  return out;
}
function partDone(p) { return partDuels(p).every((i) => view.vysledek.souboje[i].dohrano); }

function renderDuels() {
  const m = view.zapas, r = view.vysledek, lanes = view.pravidla.drah;
  const box = $("zDuels");
  const focused = document.activeElement && box.contains(document.activeElement) ? document.activeElement.dataset.key : null;
  const duels = partDuels(m.aktualniCast);
  box.replaceChildren();
  duels.forEach((di, k) => {
    const d = m.souboje[di], dr = r.souboje[di];
    const card = document.createElement("div");
    card.className = "card duel";
    let head = `<th class="name">Souboj ${di + 1}</th>`;
    for (let j = 0; j < lanes; j++) head += `<th>Dráha ${j + 1}</th>`;
    head += `<th>Průběžně</th><th>Součet</th><th>SB</th><th>Body</th>`;
    const row = (side) => {
      const home = side === "d";
      let h = `<td class="name">${esc(home ? d.domaci : d.hoste) || (home ? "domácí" : "hosté")}</td>`;
      for (let j = 0; j < lanes; j++) {
        const v = d.useky[j][side];
        // Tab/Enter order: lane by lane, all players of the part (as the scores come in).
        const order = j * duels.length * 2 + k * 2 + (home ? 0 : 1);
        h += `<td><input class="pin" inputmode="numeric" data-key="${di}:${j}:${side}" data-order="${order}" value="${v ?? ""}"></td>`;
      }
      const lv = d.prubezne[side];
      h += `<td><input class="pin live" inputmode="numeric" data-key="${di}:-1:${side}" data-order="${1000 + k * 2 + (home ? 0 : 1)}" value="${lv ?? ""}"></td>`;
      const sum = home ? dr.dSoucet : dr.hSoucet, sb = home ? dr.dSB : dr.hSB, p = home ? dr.dBody : dr.hBody;
      const win = dr.dohrano && p > (home ? dr.hBody : dr.dBody);
      h += `<td class="sum">${sum}</td><td class="sb">${pts(sb)}</td><td class="pts${win ? " win" : ""}">${dr.dohrano ? pts(p) : "–"}</td>`;
      return `<tr>${h}</tr>`;
    };
    card.innerHTML = `<table><thead><tr>${head}</tr></thead><tbody>${row("d")}${row("h")}</tbody></table>`;
    box.append(card);
  });
  box.querySelectorAll("input.pin").forEach((inp) => {
    markWarn(inp);
    inp.addEventListener("change", () => saveCell(inp));
    inp.addEventListener("input", () => markWarn(inp));
    inp.addEventListener("keydown", (e) => {
      if (e.key === "Enter") { e.preventDefault(); saveCell(inp); focusNext(inp); }
    });
    inp.addEventListener("focus", () => inp.select());
  });
  if (focused) { const el = box.querySelector(`[data-key="${focused}"]`); if (el) el.focus(); }
}

function markWarn(inp) {
  const v = inp.value.trim();
  const n = Number(v);
  inp.classList.toggle("err", v !== "" && (!/^\d+$/.test(v) || n > view.pravidla.maxKuzelekNaDrahu));
  inp.classList.toggle("warn", /^\d+$/.test(v) && n > 200 && n <= view.pravidla.maxKuzelekNaDrahu);
}

function focusNext(inp) {
  const all = [...$("zDuels").querySelectorAll("input.pin")].sort((a, b) => a.dataset.order - b.dataset.order);
  const i = all.indexOf(inp);
  if (i >= 0 && i + 1 < all.length) all[i + 1].focus();
}

async function saveCell(inp) {
  const [duel, lane, side] = inp.dataset.key.split(":");
  const raw = inp.value.trim();
  if (raw !== "" && !/^\d+$/.test(raw)) { inp.classList.add("err"); return; }
  const value = raw === "" ? null : Number(raw);
  const cur = (() => {
    const d = view.zapas.souboje[duel];
    return Number(lane) === -1 ? d.prubezne[side] : d.useky[lane][side];
  })();
  if ((cur ?? null) === value) return; // unchanged
  await send("/api/zapas/hodnota", { souboj: Number(duel), usek: Number(lane), strana: side, hodnota: value });
}

async function send(url, body) {
  try { applyView(await api("POST", url, body)); showMsg(""); }
  catch (e) { showMsg(e.message); }
}

$("btnUndo").onclick = () => send("/api/zapas/zpet", {});
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "z" && !$("tab-zapis").hidden && $("tab-zapis").classList.contains("active")) {
    e.preventDefault();
    send("/api/zapas/zpet", {});
  }
});
document.querySelectorAll("#zModes button").forEach((b) => {
  b.onclick = () => send("/api/zapas/grafika", { rezim: b.dataset.mode, zprava: $("zMessage").value });
});
$("btnMsg").onclick = () => send("/api/zapas/grafika", { rezim: "zprava", zprava: $("zMessage").value });
$("zMessage").addEventListener("keydown", (e) => { if (e.key === "Enter") $("btnMsg").click(); });

// Graphics preview: the transparent overlay on a checkerboard (works without streaming).
const gfxLoader = new Image();
gfxLoader.onload = () => { $("gfxPreview").src = gfxLoader.src; };
window.refreshGfxPreview = function () {
  if ($("tab-zapis").classList.contains("active")) gfxLoader.src = "/api/grafika.png?t=" + Date.now();
};

// ================================================================== Utkání (setup)

const mform = $("matchForm");
let rosters = [];

function renderForm() {
  const m = view && view.zapas;
  const n = view ? view.pravidla.hracuNaTym : 6;
  const per = view ? view.pravidla.soubojuNaCast : 2;
  $("mfTitle").textContent = m ? `Aktuální utkání: ${m.domaci.nazev} – ${m.hoste.nazev}` : "Nové utkání";
  $("btnMatchSave").textContent = m ? "Uložit změny" : "Založit utkání";
  $("btnMatchNew").hidden = !m;
  $("btnMatchClose").hidden = !m;
  const tb = $("mfPlayers");
  tb.replaceChildren();
  for (let i = 0; i < n; i++) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${i + 1}.</td><td>${Math.floor(i / per) + 1}.</td>
      <td><input name="d${i}"></td><td><input name="h${i}"></td>`;
    tb.append(tr);
  }
  const f = mform.elements;
  const today = new Date().toISOString().slice(0, 10);
  f["datum"].value = m ? m.datum : today;
  f["soutez"].value = m ? m.soutez : "1. KLM";
  f["kolo"].value = m ? m.kolo : "";
  f["misto"].value = m ? m.misto : "";
  f["domaci.nazev"].value = m ? m.domaci.nazev : "";
  f["domaci.zkratka"].value = m ? m.domaci.zkratka : "";
  f["hoste.nazev"].value = m ? m.hoste.nazev : "";
  f["hoste.zkratka"].value = m ? m.hoste.zkratka : "";
  for (let i = 0; i < n; i++) {
    f["d" + i].value = m ? m.souboje[i].domaci : "";
    f["h" + i].value = m ? m.souboje[i].hoste : "";
  }
  $("mfMsg").textContent = "";
}

function formToMatch() {
  const f = mform.elements, n = view ? view.pravidla.hracuNaTym : 6;
  const m = {
    datum: f["datum"].value, soutez: f["soutez"].value.trim(), kolo: f["kolo"].value.trim(), misto: f["misto"].value.trim(),
    domaci: { nazev: f["domaci.nazev"].value.trim(), zkratka: f["domaci.zkratka"].value.trim() },
    hoste: { nazev: f["hoste.nazev"].value.trim(), zkratka: f["hoste.zkratka"].value.trim() },
    souboje: [],
  };
  for (let i = 0; i < n; i++) m.souboje.push({ domaci: f["d" + i].value.trim(), hoste: f["h" + i].value.trim() });
  return m;
}

async function loadRosters() {
  try { rosters = (await api("GET", "/api/soupisky")) || []; } catch (_) { rosters = []; }
  $("teamList").replaceChildren(...rosters.map((r) => new Option(r.nazev)));
}

// Picking a known team fills its short name and players (only into empty fields).
["domaci", "hoste"].forEach((side, s) => {
  mform.elements[side + ".nazev"].addEventListener("change", (e) => {
    const r = rosters.find((x) => x.nazev === e.target.value);
    if (!r) return;
    const f = mform.elements;
    if (!f[side + ".zkratka"].value) f[side + ".zkratka"].value = r.zkratka || "";
    (r.hraci || []).forEach((p, i) => {
      const inp = f[(s === 0 ? "d" : "h") + i];
      if (inp && !inp.value) inp.value = p;
    });
  });
});

mform.addEventListener("submit", async (e) => {
  e.preventDefault();
  const msg = $("mfMsg");
  const isNew = !(view && view.zapas);
  try {
    view = await api(isNew ? "POST" : "PUT", isNew ? "/api/zapas/novy" : "/api/zapas/nastaveni", formToMatch());
    renderForm();
    msg.textContent = isNew ? "Utkání založeno ✓ – pokračujte na záložce Zápis" : "Uloženo ✓";
    msg.className = "small ok-text";
    loadSaved(); loadRosters();
  } catch (err) { msg.textContent = err.message; msg.className = "small bad-text"; }
});

$("btnMatchNew").onclick = async () => {
  if (!confirm("Založit nové utkání s týmy a hráči z formuláře?\n\nAktuální utkání zůstane uložené v seznamu.")) return;
  const msg = $("mfMsg");
  try {
    view = await api("POST", "/api/zapas/novy", formToMatch());
    renderForm(); loadSaved();
    msg.textContent = "Nové utkání založeno ✓"; msg.className = "small ok-text";
  } catch (err) { msg.textContent = err.message; msg.className = "small bad-text"; }
};
$("btnMatchClose").onclick = async () => {
  if (!confirm("Zavřít utkání? Zůstane uložené a půjde znovu otevřít. Grafika přestane ukazovat skóre.")) return;
  view = await api("POST", "/api/zapas/zavrit", {});
  renderForm();
};

async function loadSaved() {
  let list = [];
  try { list = (await api("GET", "/api/zapasy")) || []; } catch (_) { /* ignore */ }
  const tb = $("savedList");
  tb.replaceChildren();
  if (!list.length) { tb.innerHTML = `<tr><td class="muted">Zatím žádná.</td></tr>`; return; }
  for (const s of list) {
    const tr = document.createElement("tr");
    const current = view && s.soubor === view.soubor;
    tr.innerHTML = `<td>${esc(s.datum)}</td><td><b>${esc(s.domaci)}</b> – <b>${esc(s.hoste)}</b></td>
      <td class="muted small">změněno ${esc(s.zmeneno)}</td><td></td>`;
    const b = document.createElement("button");
    b.textContent = current ? "Otevřené" : "Otevřít";
    b.disabled = current;
    b.onclick = async () => {
      try { view = await api("POST", "/api/zapasy/otevrit", { soubor: s.soubor }); renderForm(); loadSaved(); }
      catch (err) { $("mfMsg").textContent = err.message; }
    };
    tr.lastElementChild.append(b);
    tb.append(tr);
  }
}

// Open the tab from the address (#zapis / #utkani) once this script is loaded.
if (location.hash === "#zapis" || location.hash === "#utkani") window.loadMatch(location.hash.slice(1));
