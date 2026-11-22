# Kuželky Stream

Přenosná aplikace pro Windows na živé přenosy kuželkářských utkání z kuželny na YouTube.
Bere obraz z IP kamery (RTSP), vkládá do něj grafiku se skóre zapisovaným ručně během utkání
a vysílá na YouTube kanál oddílu.

- **Bez instalace:** složka s `KuzelkyStream.exe` a `ffmpeg\ffmpeg.exe`. Ovládání běží v prohlížeči
  na `http://127.0.0.1:8080` a vedle hodin je ikona.
- **Spolehlivé vysílání:** kamera a YouTube jsou oddělené procesy. Výpadek kamery vysílání nepřeruší,
  divák mezitím vidí náhradní obraz se skóre.
- **YouTube přes API:** aplikace sama založí přenos, hlídá ho a přenos má pevnou adresu po celé utkání.
- **Zápis skóre:** utkání podle pravidel 1. KLM (nastavitelná), body, setové body a součty
  se počítají samy.
- **Obraz a grafika:** výřez z kamery, převod na 16:9 a rozmístění grafiky myší, vše za běhu.
- **Záloha:** utkání a soupisky na Google Disk, výsledky do Google Tabulky.

- **Výsledky z ČKA:** utkání se dá založit z odkazu na vysledky.kuzelky.cz a výsledky se pak načítají samy.
- **Více notebooků:** nastavení (kamera, YouTube, grafika, pravidla) se sdílí přes Google Disk oddílu –
  nový notebook potřebuje jen jedno přihlášení.
- **Automatické aktualizace:** při každém spuštění se aplikace sama aktualizuje z GitHub Releases.

**Stažení:** [KuzelkyStream.zip – nejnovější verze](https://github.com/karbobrak/kuzelky-stream/releases/latest/download/KuzelkyStream.zip)

Návod pro obsluhu je přímo v aplikaci (záložka **Nápověda**), krátký start v [docs/NAVOD.txt](docs/NAVOD.txt).
Podrobná specifikace: [SPECIFIKACE.md](SPECIFIKACE.md).

**Vydání nové verze** (správce): commitnout, pak `.uild.ps1 -Version X.Y.Z -Publish -Notes "co je nového"`.
Potřebuje [GitHub CLI](https://cli.github.com/) přihlášené k účtu repozitáře (`gh auth login`).
Aplikace v kuželnách si novou verzi stáhnou při příštím spuštění.
Stránky pro Google OAuth: [popis aplikace](https://karbobrak.github.io/kuzelky-stream/),
[zásady ochrany soukromí](https://karbobrak.github.io/kuzelky-stream/soukromi.html).

## Sestavení

Potřebujete Go 1.27+ a FFmpeg pro Windows (doporučený build „essentials“ z
[gyan.dev](https://www.gyan.dev/ffmpeg/builds/)).

```powershell
.\build.ps1 -Version 1.0.0 -FFmpeg C:\cesta\k\ffmpeg.exe
```

Skript spustí testy a vytvoří `dist\KuzelkyStream-1.0.0.zip`. Při vývoji stačí `go run ./cmd/kuzelkystream`.

## Struktura

| Složka | Obsah |
|---|---|
| `cmd/kuzelkystream` | spuštění aplikace, ikona v oznamovací oblasti |
| `internal/stream` | FFmpeg: příjem z kamery, vysílání, hlídání procesů, filtry obrazu |
| `internal/frame` | snímky YUV a vkládání grafiky |
| `internal/overlay` | vykreslení grafiky se skóre |
| `internal/match` | utkání, výpočet bodů, ukládání |
| `internal/google` | YouTube, Google Disk a Tabulky (REST + OAuth) |
| `internal/app` | logika aplikace, záloha do cloudu |
| `internal/web` | webové rozhraní a API |

`config.json` (hesla a přihlášení), `data/` a `logs/` vznikají vedle exe a do repozitáře nepatří.
