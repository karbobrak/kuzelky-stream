# Kuželky Stream – specifikace

Stav: návrh k odsouhlasení (2026-11-21). Zatím žádný kód.

## 1. Cíl

Přenosná aplikace pro Windows. Vezme obraz a zvuk z IP kamery v kuželně (RTSP), vloží do obrazu grafiku se
skóre utkání 1. KLM a vysílá vše na YouTube. Skóre se zadává ručně v aplikaci.

## 2. Rozhodnutá omezení

| Oblast | Rozhodnutí |
|---|---|
| Platforma | Pouze Windows 10/11, 64 bit, na notebooku v kuželně (pokaždé může být jiný) |
| Instalace | Žádná. Složka (zip / flash disk), spustí se jeden `.exe`, bez práv administrátora |
| Server / cloud | Žádný. Vše běží na notebooku, ven jde jen stream na YouTube |
| Mobily | Neřešíme |
| Ovládání | Webové rozhraní v prohlížeči notebooku na `http://127.0.0.1:<port>` |
| Kamera | Reolink P320 (H.264, 2880×1616 @ 30 fps, mikrofon), adresa konfigurovatelná |
| Zvuk | Vysílá se zvuk z kamery (hala, žádná hudba) |
| Zápis | Jen jména, součty po drahách a body. Jednotlivé hody ne |
| Pravidla bodování | Parametrizovaná, aby se dala snadno změnit |
| Technologie | Go (jeden exe s vestavěným webovým rozhraním) + FFmpeg (`ffmpeg.exe` ve složce aplikace) |

## 3. Struktura složky

```
KuzelkyStream\
  KuzelkyStream.exe          hlavní aplikace (Go, webové UI uvnitř)
  ffmpeg\ffmpeg.exe          FFmpeg build pro Windows (GPL) + LICENSE
  ffmpeg\ffprobe.exe
  config.json                nastavení (vzniká při prvním spuštění)
  data\tymy\*.json           uložené soupisky týmů
  data\zapasy\*.json         uložená utkání (jedno utkání = jeden soubor)
  data\zaznamy\*.mp4         volitelný lokální záznam
  logs\*.log                 logy aplikace a FFmpegu (rotace, max. ~20 souborů)
```

Všechny cesty jsou relativní ke složce s exe, takže aplikace jde přenášet mezi notebooky.

## 4. Architektura

> **Změna po testu výpadku (2026-11-21):** v prototypu byl jeden ffmpeg od kamery až po YouTube. Restart
> kamery (~70 s výpadek) tím přerušil spojení s YouTube, YouTube vysílání ukončil a nové se samo nerozjelo
> („Preparing stream“). Proto se kamera a vysílání **oddělují do dvou procesů**. Spojení s YouTube vydrží
> i výpadek kamery a divák mezitím vidí náhradní obraz.

```
                    ffmpeg #1 „příjem“                     KuzelkyStream.exe                  ffmpeg #2 „vysílání“
 Reolink ──RTSP──► dekód → výřez → 16:9 →    ─video──►   hodiny 25 fps:                  ─video─► kodér ─RTMP─► YouTube
                   škálování na výstup        (raw)       poslední snímek kamery,          (raw)
                   zvuk → PCM 48 kHz          ─zvuk──►    nebo náhradní obraz,             ─zvuk─► AAC
                   (smí padat a restartovat)   (PCM)      + grafika (alfa blending v Go)   (PCM)   (běží nepřetržitě)
                                                          zvuk kamery, nebo ticho
```

- **ffmpeg #1 (příjem)** čte kameru a posílá aplikaci hotové snímky ve výstupním rozlišení (raw YUV 4:2:0)
  a zvuk (PCM). Když kamera vypadne, proces skončí a aplikace ho spouští znovu každé 3 s, dokud kamera
  nenaběhne. Změna výřezu, IP kamery apod. restartuje **jen tento proces**, YouTube se nepřeruší.
- **Aplikace** má vlastní hodiny a ffmpegu #2 posílá přesně 25 snímků/s a souvislý zvuk:
  - je-li snímek z kamery čerstvý (< 1 s), pošle ho, jinak náhradní obraz („Přenos z kamery bude za chvíli
    obnoven“ + poslední stav skóre),
  - grafiku vkládá sama (alfa blending jen v oblasti bloků grafiky, ne přes celý snímek),
  - zvuk kamery jde přes malý vyrovnávací buffer (~200 ms). Při výpadku jde ticho.
- **ffmpeg #2 (vysílání)** jen kóduje a posílá na YouTube. Časové značky vytváří z konstantní snímkové
  frekvence, takže zvuk a obraz se nerozjedou (odpadá wall-clock z prototypu).
- Propojení: video pro ffmpeg #2 přes stdin, zvuk přes lokální TCP (`127.0.0.1`, firewall se neptá).
  Z ffmpegu #1 jde video přes stdout a zvuk přes druhé lokální TCP spojení.
- Datový tok mezi procesy: 720p ≈ 35 MB/s, 1080p ≈ 78 MB/s. Pro roury ve Windows to není problém.

```
   prohlížeč na notebooku (Vysílání / Zápis / Utkání / Nastavení)  ◄──►  web UI + API v KuzelkyStream.exe
```

Moduly v Go:

- `config` – načtení a uložení `config.json`, výchozí hodnoty, validace.
- `match` – datový model utkání, výpočet bodů (`scoring`), ukládání, historie změn (zpět).
- `overlay` – vykreslení grafiky do PNG (vestavěný font s českou diakritikou, licence OFL).
- `stream` – sestavení příkazu FFmpeg, spuštění, hlídání, znovupřipojení, statistiky.
- `probe` – test kamery přes ffprobe (rozlišení, kodek, zvuk), detekce hardwarových kodérů.
- `web` – HTTP server, REST API, SSE pro živé aktualizace, statické soubory UI.
- `win` – zákaz uspání (`SetThreadExecutionState`), kontrola jediné spuštěné instance, otevření prohlížeče.

## 5. Nastavení (`config.json`)

**Zásada:** veškeré nastavení je v `config.json` vedle exe a všechno jde měnit za běhu na záložce Nastavení
v prohlížeči. Nic není napevno v kódu a nic se nemusí upravovat ručně v souboru.

- Uložením v UI se `config.json` hned přepíše (atomicky).
- Ruční úprava souboru se načte při startu nebo tlačítkem **Načíst znovu ze souboru**.
- Každá položka je označená, jak se změna projeví:
  - **hned** (za běhu, bez přerušení): poloha, velikost a vzhled grafiky, pravidla, texty,
  - **restart vysílání** (ffmpeg se spustí znovu, výpadek ~3–5 s): kamera, YouTube, výstup, výřez, zvuk.
    UI se nejdřív zeptá: „Použít teď (krátce přeruší vysílání)?“
- Tlačítka **Export nastavení** (bez tajných údajů nebo s nimi) a **Import**, pro přenos na jiný notebook.

| Skupina | Položky | Výchozí |
|---|---|---|
| Kamera | IP/host, port RTSP, uživatel, heslo, cesta streamu, transport | `192.168.100.163`, `554`, –, –, `h264Preview_01_main`, `tcp` |
| Kamera | Vlastní úplná URL (přebije předchozí) | prázdné |
| Kamera | Zdroj obrazu: IP kamera (RTSP) / webkamera (DirectShow: zařízení, mikrofon, rozlišení, fps) | RTSP |
| YouTube | URL serveru, klíč streamu | `rtmp://a.rtmp.youtube.com/live2`, – |
| Výstup | Rozlišení, fps, datový tok videa, kodér, preset x264 | 1280×720, 25, 4000 kb/s, `auto`, `veryfast` |
| Zvuk | Zdroj (kamera / ticho), datový tok, zesílení | kamera, 128 kb/s, 0 dB |
| Obraz | Výřez z kamery (x, y, šířka, výška v % snímku), poměr výřezu (zamknout na 16:9 / volný) | celý snímek, 16:9 |
| Obraz | Převod na 16:9: oříznout / doplnit pruhy / roztáhnout | oříznout |
| Obraz | Otočení 0° / 180°, zrcadlení | 0°, ne |
| Grafika | Pro každý blok grafiky (kap. 9): zobrazit, poloha x/y (v % obrazu), měřítko | viz kap. 9 |
| Grafika | Průhlednost pozadí, barvy týmů, font | 80 %, –, vestavěný |
| Záznam | Lokální MP4 ano/ne | ne |
| Aplikace | Port webového rozhraní, otevřít prohlížeč při startu | `8080` (při obsazení další volný), ano |
| Pravidla | viz kap. 7 | 1. KLM |

Poznámky:

- Uživatel a heslo kamery se do URL vkládají URL-enkódované. Speciální znaky v hesle by jinak URL rozbily.
- Heslo kamery a klíč streamu jsou v `config.json` jako čistý text, aby se dalo nastavení přenášet mezi
  noteboky. Šifrování přes Windows DPAPI by fungovalo jen na jednom počítači. V UI bude upozornění, že složka
  obsahuje tajné údaje. V logu se klíč i heslo vždy maskují.
- Tlačítko **Otestovat kameru** spustí ffprobe a ukáže rozlišení, fps, kodek a zvukovou stopu.
- Tlačítko **Zjistit kodéry** zkusí `h264_nvenc`, `h264_qsv` a `h264_amf` a vybere první funkční. Jinak použije `libx264`.

## 6. Vysílací řetězec (FFmpeg)

Kostra příkazu (sestavuje ho aplikace z nastavení):

```
ffmpeg -hide_banner -loglevel warning -progress pipe:2
  -rtsp_transport tcp -timeout 5000000 -use_wallclock_as_timestamps 1 -i "rtsp://…"     # 0: kamera
  -f image2pipe -framerate 2 -use_wallclock_as_timestamps 1 -i -                         # 1: grafika (PNG ze stdin)
  [-f lavfi -i anullsrc=r=48000:cl=stereo]                                                # 2: ticho, pokud zvuk z kamery není
  -filter_complex "[0:v]crop=W:H:X:Y,<16:9>,scale=1280:720,fps=25[v];[v][1:v]overlay=0:0:eof_action=repeat[out]"
      # <16:9> = podle režimu: další crop na 16:9 (oříznout) / pad (pruhy) / nic (roztáhnout); případně hflip/vflip/transpose
  -map "[out]" -map 0:a
  -c:v <kodér> -b:v 4000k -maxrate 4000k -bufsize 8000k -g 50 -keyint_min 50 -sc_threshold 0 -pix_fmt yuv420p
  -c:a aac -b:a 128k -ar 48000 -ac 2
  -f flv "rtmp://a.rtmp.youtube.com/live2/<klíč>"
```

- Klíčový snímek každé 2 s (`-g = 2 × fps`), jak doporučuje YouTube.
- Výřez a převod na 16:9 se provedou jako první, ještě před zmenšením. Výřez se zadává v % snímku, takže
  platí i po změně rozlišení v kameře. Aplikace hodnoty zarovná na sudé pixely (H.264 to vyžaduje).
- Grafika je celý výstupní snímek s průhledností. Posun bloků se tedy mění jen ve vykresleném PNG a ffmpeg
  se nerestartuje.
- Grafika je PNG s průhledností ve výstupním rozlišení. Aplikace ji posílá 2× za sekundu, i když se nic
  nezměnilo, a ffmpeg tak nikdy nečeká na prázdnou rouru.
- Dekódování 2880×1616 je náročné. Volitelně `-hwaccel d3d11va`. Lepší je v kameře nastavit hlavní stream
  na nižší rozlišení (např. 2560×1440), pokud ho P320 nabízí.
- Náhled: druhý výstup `fps=1, scale=640` jako MJPEG na stdout. Aplikace ho zobrazí v UI.
- Lokální záznam: výstup přes `tee` do `data\zaznamy\`. Pokud zápis na disk selže, vysílání nesmí spadnout
  (`onfail=ignore`).

**Technické riziko č. 1:** synchronizace grafiky dodávané rourou s živým obrazem (časové značky, zablokování
roury). Ověří se jako první, prototypem v kap. 11, krok 1. Záložní řešení: text přes filtr `drawtext` řízený
příkazy `sendcmd`/`zmq`, nebo PNG soubor znovu načítaný demuxerem `image2 -loop 1`.

> Kostra výše je z prototypu (jeden ffmpeg). Ve finální verzi se rozdělí na příjem a vysílání (kap. 4):
> výřez, 16:9 a škálování dělá ffmpeg #1, grafiku vkládá aplikace a kodér a výstup řeší ffmpeg #2 se vstupem
> `-f rawvideo -pix_fmt yuv420p -s WxH -framerate 25 -i pipe:0` a zvukem `-f s16le -ar 48000 -ac 2 -i tcp://…`.

### Ruční restarty (požadavek z testu)

Na záložce Vysílání jsou tlačítka pro případ, že se něco zasekne:

| Tlačítko | Co udělá | Dopad na YouTube |
|---|---|---|
| **Znovu připojit kameru** | restartuje ffmpeg #1 | žádný, divák chvíli vidí náhradní obraz |
| **Restartovat vysílání** | ukončí a znovu naváže spojení s YouTube (ffmpeg #2) | krátký výpadek. Pomáhá, když YouTube visí na „Preparing stream“ |
| **Stop / Start** | úplné zastavení / spuštění | vysílání skončí / začne |

U tlačítka Restartovat vysílání je nápověda: pokud YouTube ani potom nezačne, zkontrolovat ve Studiu
Obsah → Živé přenosy, jestli tam nevisí staré vysílání.

### Hlídání procesu

Stavy vysílání: `Zastaveno → Spouštím → Vysílám → Obnovuji → …`

- Ffmpeg skončí, nebo 10 s nehlásí postup: aplikace ho ukončí a znovu spustí.
- Prodleva před restartem: 2, 5, 10, 30 s (dál po 30 s). Okamžitý restart Reolink odmítá chybou „bad cseq“.
- Do UI jde fps, datový tok, rychlost (`speed`, má být ≈ 1.0×), doba vysílání, počet restartů a posledních
  20 řádků logu.
- Při výpadku delším než pár minut YouTube vysílání ukončí. S trvalým klíčem a automatickým startem
  (nastavuje se v YouTube Studiu) se vysílání po obnovení samo rozjede znovu, jen jako nové video.

## 6a. YouTube přes API (rozhodnuto 2026-11-21)

Uživatel na YouTube nic neklikat nechce. Aplikace proto spravuje přenos sama přes YouTube Data API v3.
Ruční klíč streamu zůstává jako záložní režim (`youtube.rezim = "klic"`).

**Jednorázová příprava (provede správce, ne obsluha v kuželně):**
1. Kanál s povoleným živým vysíláním (ověření telefonem, aktivace až 24 h).
2. Projekt v Google Cloud se zapnutým YouTube Data API v3, OAuth klient typu **Desktop app**
   a aplikace přepnutá na **In production**. V režimu Testing přihlášení vyprší po 7 dnech.
3. Client ID a secret do Nastavení → YouTube, pak **Přihlásit k YouTube** účtem kanálu.
   Refresh token se uloží do `config.json` a cestuje se složkou na jiné notebooky.

**Průběh:**
- **Start:** aplikace najde nebo založí znovupoužitelný stream (adresu a klíč si vezme z API) a založí
  přenos. Přenos se spustí sám, jakmile přijde video (`enableAutoStart`), a sám **nekončí**
  (`enableAutoStop = false`). Pak přenos se streamem propojí. ID přenosu uloží do `data\prenos.json`.
- **Stop / restart aplikace / výpadek:** přenos zůstává. Další start pokračuje **ve stejném videu na stejné
  adrese**.
- **Ukončit přenos utkání** (tlačítko): zastaví vysílání a přenos na YouTube uzavře. Další start založí nový.
- **Hlídání každých 15 s:** stav přenosu a zdraví streamu včetně problémů hlášených YouTubem se ukazují v UI.
  - Když video chodí ~45 s a přenos stále není živě (zaseknutí „Preparing stream“), aplikace ho na živé
    přepne sama.
  - Když někdo přenos ukončí ve Studiu, aplikace založí nový.
- V UI je odkaz na přenos s tlačítkem Kopírovat a trvalý odkaz kanálu `…/channel/<id>/live`. Ten funguje
  jen u veřejných přenosů.
- Název přenosu je šablona (`{datum}`, `{cas}`). V kroku 3 přibudou týmy, např. `{domaci} – {hoste}`.

## 6b. Ikona v oznamovací oblasti (požadavek 2026-11-21)

- Aplikace nemá okno konzole (sestavení `-ldflags -H=windowsgui`), ale ikonu kuželky vedle hodin.
  Barvy: šedá = nevysílá se, červená = vysílá se, oranžová = obnovuji spojení / náhradní obraz.
- Levé kliknutí otevře ovládání. Pravé kliknutí ukáže nabídku Otevřít ovládání / Ukončit. Při běžícím
  vysílání se aplikace před ukončením zeptá.
- Druhé spuštění exe jen otevře ovládání už běžící aplikace (port v `data\port.txt`).
- Chyby při startu (chybí FFmpeg, obsazený port) se ukážou v dialogovém okně.

## 7. Utkání a bodování

### Model (1. KLM, parametrizovaný)

- 2 týmy po 6 hráčích, 4 dráhy, 120 hodů na hráče = 4 úseky po 30 hodech.
- 6 soubojů (dvojice domácí – host), hrají se ve 3 částech po 2 soubojích (1+2, 3+4, 5+6).
- Úsek (jedna dráha): vyšší součet = 1 setový bod (SB), remíza = 0,5 : 0,5.
- Souboj: víc SB = 1 bod. Při rovnosti SB rozhodne víc kuželek, při rovnosti kuželek 0,5 : 0,5.
- Celkový součet kuželek týmu: vyšší = 2 body, remíza 1 : 1.
- Celkem 8 bodů (6 + 2).

Parametry v `config.json`:

```json
"pravidla": {
  "hracuNaTym": 6, "drah": 4, "soubojuNaCast": 2,
  "sbVyhra": 1, "sbRemiza": 0.5,
  "bodSouboj": 1, "remizaSoubojeRozhodujeKuzelky": true,
  "bodyZaCelkem": 2
}
```

Celý výpočet je v jednom souboru (`scoring.go`) a pokrývají ho jednotkové testy včetně remíz.

### Soubor utkání (`data\zapasy\2026-11-21_Domaci_Hoste.json`)

```json
{
  "datum": "2026-11-21", "soutez": "1. KLM", "kolo": 9, "misto": "…",
  "domaci": { "nazev": "…", "zkratka": "…", "hraci": ["…", "…"] },
  "hoste":  { "nazev": "…", "zkratka": "…", "hraci": ["…", "…"] },
  "souboje": [
    { "domaci": "Novák", "hoste": "Dvořák",
      "useky": [ {"d": 152, "h": 140}, {"d": 148, "h": null}, {"d": null, "h": null}, {"d": null, "h": null} ],
      "prubezne": {"d": 71, "h": 66} }
  ],
  "aktualniCast": 1,
  "stavGrafiky": "utkani"
}
```

- `null` = úsek ještě není dohraný. `prubezne` je volitelný mezistav rozehraného úseku a do celkového
  součtu se počítá jen dočasně.
- Body se ze souboru nečtou, vždy se počítají z kuželek.
- Ukládá se po každé změně atomicky (dočasný soubor + přejmenování). Pád aplikace tak neztratí data.
- Historie změn v paměti dovoluje krok zpět.

## 8. Webové rozhraní

Server poslouchá jen na `127.0.0.1`, takže se Windows firewall nic neptá. Proti útokům z jiných webů
(DNS rebinding, CSRF) server kontroluje hlavičky `Host` a `Origin`. Živé změny posílá přes SSE a víc
otevřených záložek se samo synchronizuje.

| Záložka | Obsah |
|---|---|
| **Vysílání** | Velké tlačítko Start/Stop, stav, statistiky, náhled výstupu, posledních 20 řádků logu, tlačítko „Skrýt/zobrazit grafiku“ |
| **Zápis** | Aktuální část: 2 souboje × 2 hráči × 4 úseky + průběžný stav. Ovládá se klávesnicí (Tab/Enter), číslo se kontroluje (0–270, upozornění nad 200). Body se dopočítávají živě. Tlačítka Zpět a Další část. Každou dřívější hodnotu jde kdykoli opravit |
| **Utkání** | Nové utkání: týmy, 6+6 hráčů, pořadí dvojic. Soupisky jde uložit a znovu načíst. Domácí tým je předvyplněný. Seznam a načtení uložených utkání |
| **Nastavení** | Vše z kap. 5, testy kamery a kodérů. Podstránky Kamera, YouTube, Výstup, **Obraz** (výřez), **Grafika** (rozmístění), Pravidla, Aplikace |

### Editor výřezu (Nastavení → Obraz)

- Ukáže aktuální celý snímek z kamery (jeden snímek přes ffmpeg, tlačítko Obnovit).
- Přes něj je obdélník výřezu. Myší se táhne a mění velikost za rohy, volitelně se zámkem na 16:9.
  Hodnoty x/y/šířka/výška jdou zadat i číselně.
- Vedle je náhled výsledku po převodu na 16:9 (oříznout / pruhy / roztáhnout).
- Tlačítka Použít (restart vysílání) a Obnovit celý snímek.

### Editor rozmístění grafiky (Nastavení → Grafika)

- Na pozadí je náhled výstupního obrazu (už po výřezu), přes něj jsou bloky grafiky.
- Každý blok jde myší přesunout, změnit mu velikost a skrýt ho. Bloky se přichytávají k okrajům a ke středu.
  Hodnoty jsou vidět i číselně (x, y, měřítko).
- Změna se projeví ve vysílání **hned**, bez restartu.
- Tlačítka „Výchozí rozmístění“ a „Uložit jako předvolbu“. Předvolby se pojmenují (např. „kuželna Hradec“)
  a uloží do `config.json`. Hodí se, když se kamera přesune nebo jiný záběr potřebuje jiné rozložení.
- Ochranná zóna: volitelně se dá označit obdélník, který nesmí být zakrytý (např. kuželky na konci drah).
  Editor varuje, když do něj blok zasahuje.

Přepínač stavu grafiky: `Utkání` (běžná lišta) / `Přestávka` (lišta + text) / `Výsledek` (velká tabulka
všech 6 soubojů na konci) / `Skryto`.

**Úplné vypnutí grafiky:**
- Hlavní vypínač „Grafika zapnuta / vypnuta“ je na záložkách Vysílání i Zápis a má klávesovou zkratku.
- Vypnutí vysílání nepřeruší, platí hned a vysílá se čistý obraz z kamery. Zápis skóre přitom běží dál
  a po zapnutí se ukáže aktuální stav.
- V `config.json` je volba `grafikaPriStartu` (zapnuto / vypnuto), tedy jestli vysílání začne s grafikou
  nebo bez ní. Aplikace jde použít i úplně bez zápisu, jen jako přenos kamery na YouTube.
- Technicky: vypnutá grafika = zcela průhledný snímek. Restart ffmpeg kvůli tomu potřeba není.

## 9. Grafika (návrh rozvržení, výstup 1280×720)

```
┌──────────────────────────────────────────────────────────────────────────┐
│                                                                          │
│                         (obraz drah z kamery)                            │
│                                                                          │
├──────────────────────────────────────────────────────────────────────────┤
│ DOMÁCÍ  3 : 1  HOSTÉ      kuželky 1 712 : 1 698 (+14)         část 2/3   │
│ Novák      152 148  71 │ 371  2 SB  :  0 SB  346 │  66 140 140   Dvořák  │
│ Svoboda    139 155  60 │ 354  1 SB  :  1 SB  349 │  64 141 144   Černý   │
└──────────────────────────────────────────────────────────────────────────┘
```

Grafika se skládá z **nezávislých bloků**. Každý má vlastní polohu, měřítko a viditelnost (viz editor
v kap. 8):

| Blok | Obsah | Výchozí poloha |
|---|---|---|
| `hlavicka` | týmy, stav bodů, součet kuželek a rozdíl, část | dole uprostřed, nad souboji |
| `souboje` | 2 právě hrané souboje (jména, úseky, součty, SB) | dole uprostřed |
| `zprava` | volný text (Přestávka, Konec části…) | nahoře uprostřed |
| `loga` | loga klubů | vedle hlavičky |
| `vysledek` | velká tabulka 6 soubojů (stav grafiky „Výsledek“) | uprostřed obrazu |

- Poloha se ukládá relativně (kotva + x/y v % obrazu), takže platí pro 720p i 1080p.
- Rozvržení výše je jen výchozí. Konečnou polohu určí záběr kamery a uživatel ji kdykoli změní za běhu.
- Velké číslice, aby byly čitelné i na mobilu diváka. Průběžný (nedohraný) úsek bude odlišen barvou.
- Vykreslení je v Go (bez prohlížeče), takže na každém notebooku vypadá grafika stejně.
- Volitelně loga klubů (PNG ve složce `data\loga\`).

## 10. Rizika a jejich řešení

| Riziko | Řešení |
|---|---|
| YouTube po delším výpadku (> ~1 min) ukončí vysílání a nové visí na „Preparing stream“ (ověřeno testem) | Oddělení kamery od vysílání (kap. 4), tlačítko Restartovat vysílání. Později případně YouTube API pro kontrolu stavu vysílání |
| FFmpeg vypisuje URL kamery i s heslem (ověřeno testem) | Každý řádek z ffmpegu prochází maskováním (`Config.Mask`), pokryto testem |
| Grafika z roury se rozejde s obrazem nebo ffmpeg zablokuje | Ověřit prototypem jako první krok, záložní řešení v kap. 6 |
| Výpadky a artefakty RTSP u Reolinku | TCP, aktuální firmware kamery, restart s prodlevou, hlídání postupu |
| RTSP je v kameře vypnuté | Návod v UI, test kamery ukáže srozumitelnou chybu |
| Slabý notebook (rychlost < 1.0×) | Hardwarový kodér, 720p, hwaccel dekódování, nižší rozlišení v kameře. UI upozorní, když rychlost klesá |
| SmartScreen / antivir u nepodepsaného exe | Návod („Další informace → Přesto spustit“, odblokovat zip). Podpis kódu stojí peníze, zatím ne |
| Notebook usne / vypne displej | `SetThreadExecutionState` po dobu vysílání |
| Tajné údaje v `config.json` | Maskování v UI a logu, upozornění. Složku nesdílet veřejně |
| Upload sdílený s wifi pro diváky | Výchozí 4 Mb/s (720p). Statistiky v UI ukážou problém |
| Změna výřezu během přenosu přeruší vysílání na ~3–5 s | Výřez nastavit před začátkem. Později lze přidat změnu za běhu přes příkazy filtru `crop` (`zmq`) |
| Silný výřez = nižší ostrost (digitální zoom) | Editor ukáže výsledné rozlišení výřezu a varuje, když klesne pod výstupní rozlišení |
| Chyba v pravidlech | Parametry + testy, výsledky ověřit s rozhodčím na prvních utkáních |
| Licence FFmpeg (GPL) | `ffmpeg.exe` je samostatný program vedle aplikace. Přiložit LICENSE a odkaz na zdrojový kód |

## 11. Postup prací

**Stav (2026-11-21):**
- Krok 0 hotový.
- Krok 1 hotový a ověřený na YouTube: řetězec rozdělený na dva procesy, restart kamery vysílání nepřeruší.
- Krok 2 hotový:
  - webové UI se záložkami Vysílání a Nastavení,
  - Start/Stop, ruční restarty, vypínač grafiky (klávesa G),
  - náhled vysílaného obrazu, log v UI,
  - test kamery a kodérů,
  - ukládání nastavení za běhu s potvrzením dopadu.
  - Místo SSE se stav dotazuje 1× za sekundu (pro lokální stránku stačí).

- YouTube přes API (kap. 6a) a ikona v oznamovací oblasti (kap. 6b) hotové a ověřené uživatelem.
- Krok 3 hotový:
  - záložka Utkání: týmy, zkratky do grafiky, 6+6 hráčů, uložené soupisky, seznam uložených utkání,
  - záložka Zápis: části, dráhy, průběžný stav, Enter skáče po drahách, krok zpět Ctrl+Z,
    režimy grafiky (lišta / lišta + zpráva / tabulka), náhled grafiky,
  - výpočet bodů s testy, pravidla v Nastavení,
  - název přenosu z utkání (`{utkani}`),
  - grafika: lišta (hlavička + 2 souboje), zpráva, výsledková tabulka.
  - Poloha a měřítko lišty a zprávy už jsou v `config.json` (`grafika.rozlozeni`). Editor myší přijde v kroku 4.

- Krok 4 hotový (záložka **Obraz a grafika**, místo podstránek Nastavení):
  - výřez myší se zámkem 16:9, otočení o 180° a zrcadlení, režimy oříznout / pruhy / roztáhnout,
  - náhled výsledku a upozornění na digitální zoom. Použití výřezu restartuje jen příjem z kamery,
  - rozmístění lišty a zprávy tažením myší, velikost kolečkem nebo posuvníkem, neprůhlednost,
    přichycení ke středu, výchozí rozmístění, předvolby. Ve vysílání se projeví hned.
  - Ochranná zóna (kap. 8) zatím není, doplní se podle potřeby.
- Zjištěno: zvuk z kamery P320 je digitální ticho (−91 dB) už přímo na RTSP. Mikrofon je v kameře
  vypnutý, takže YouTube hlásí „audio bitrate 0“. Nejde o chybu aplikace.

- Záloha na Google Disk a tabulka výsledků (rozhodnuto 2026-11-21):
  - stejné přihlášení jako YouTube, navíc oprávnění `drive.file` (jen soubory aplikace),
  - složka „KuzelkyStream“: `zapas_*.json`, `tym_*.json`, `nastaveni-grafiky.json`,
  - nahrává se 10 s po změně, při výpadku internetu opakovaně, při ukončení aplikace naposledy,
  - při startu a po přihlášení proběhne porovnání: chybějící nebo novější utkání a soupisky se stáhnou
    (nový notebook). Nastavení grafiky se obnoví jen tlačítkem,
  - tabulka „Kuželky – výsledky“ s listy Utkání a Hráči, řádky se aktualizují podle klíče,
  - v Google Cloud musí být zapnuté Google Drive API a Google Sheets API,
  - ověřeno testem proti falešnému Google Disku a Tabulkám, se skutečným účtem zatím ne.
- Distribuce (varianta A): `build.ps1` → `dist\KuzelkyStream-<verze>.zip`
  (exe + `ffmpeg\ffmpeg.exe` + licence + `NAVOD.txt`).
  - `ffprobe` už není potřeba.
  - Verze je v exe a v záhlaví UI.
  - Pro menší zip použít FFmpeg „essentials“.
  - Ikona přímo v exe zatím chybí: potřebuje nástroj go-winres a jeho spuštění zatím nebylo schválené.

**Otevřený bod:** při zakolísání uploadu (v testu ~7 s) aplikace zahazuje snímky. Řešení je fronta
odeslaných dat (muxer `fifo`). Nejdřív je ale nutné umět poznat mrtvé spojení, protože s frontou ffmpeg
dál hlásí postup i bez odesílání (např. `rw_timeout` nebo sledování odeslaných bajtů). Ověřit samostatným
testem s odpojením sítě.

0. **Ruční ověření (uživatel, bez kódu):** zapnout RTSP v kameře, nastavit pevnou IP, zjistit nabízená
   rozlišení. Povolit živé vysílání na YouTube kanálu. Neveřejný testovací přenos přes portable OBS z typického
   notebooku.
1. **Prototyp řetězce:** Go spustí ffmpeg (kamera → grafika z roury → YouTube) se zkušební grafikou, která
   se každou sekundu mění. Měří se zátěž, zpoždění a obnova po odpojení kamery i internetu.
2. **Kostra aplikace:** nastavení, webové UI (Vysílání, Nastavení), hlídání procesu, logy, detekce kodérů,
   test kamery.
3. **Utkání:** model, výpočet bodů + testy, záložky Utkání a Zápis, ukládání.
4. **Grafika:** vykreslování, ladění vzhledu na skutečném záběru.
5. **Zátěžové testy:** 3 h vysílání v kuse, vytažení kabelu kamery i internetu, 2–3 různé noteboky.
   Pak sestavení zipu.

Později (mimo první verzi): pojmenované a naplánované přenosy přes YouTube API (OAuth), ikona v oznamovací
oblasti místo okna konzole, napojení na automaty (snímání čítačů), export výsledků.

## Doplněno 2026-11-21: mazání a webkamera

- **Mazání utkání a týmů** (záložka Utkání, tlačítka Smazat). Smaže se lokální soubor a do `data\smazane.json`
  se zapíše záznam o smazání. Cloudová synchronizace pak přesune soubor na Disku do koše, smaže řádky utkání
  z tabulky výsledků (oba listy) a nahraje `smazane.json` na Disk. Ostatní notebooky při synchronizaci smažou
  svou kopii, místo aby ji nahrály zpět. Položka změněná po čase smazání (např. tým, který znovu hraje) se
  bere jako nová a záznam o smazání se zruší. Nové utkání nedostane jméno souboru smazaného utkání.
- **Webkamera jako zdroj obrazu** (Nastavení → Kamera → Zdroj obrazu). FFmpeg čte zařízení přes DirectShow
  (`-f dshow -i video=…:audio=…`), mikrofon je volitelný (bez něj jde ticho). Seznam zařízení a rozlišení
  aplikace zjistí z ffmpeg (`-list_devices`, `-list_options`). Webkameru smí mít otevřenou jen jeden program,
  takže během vysílání nejde test kamery ani snímek pro výřez (aplikace to hlásí). Výpadek / odpojení USB řeší
  stejný supervizor jako u RTSP (nový pokus každé 3 s).
